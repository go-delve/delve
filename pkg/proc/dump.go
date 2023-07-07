package proc

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"
	"sync"

	"github.com/go-delve/delve/pkg/elfwriter"
	"github.com/go-delve/delve/pkg/version"
)

var (
	ErrMemoryMapNotSupported = errors.New("MemoryMap not supported")
)

// DumpState represents the current state of a core dump in progress.
type DumpState struct {
	Mutex sync.Mutex

	Dumping  bool
	AllDone  bool
	Canceled bool
	DoneChan chan struct{}

	ThreadsDone, ThreadsTotal int
	MemDone, MemTotal         uint64

	Err error
}

// DumpFlags is used to configure (*Target).Dump
type DumpFlags uint16

const (
	DumpPlatformIndependent DumpFlags = 1 << iota // always use platform-independent notes format
)

// MemoryMapEntry represent a memory mapping in the target process.
type MemoryMapEntry struct {
	Addr uint64
	Size uint64

	Read, Write, Exec bool

	Filename string
	Offset   uint64
}

func (state *DumpState) setErr(err error) {
	if err == nil {
		return
	}
	state.Mutex.Lock()
	if state.Err == nil {
		state.Err = err
	}
	state.Mutex.Unlock()
}

func (state *DumpState) setThreadsTotal(n int) {
	state.Mutex.Lock()
	state.ThreadsTotal = n
	state.ThreadsDone = 0
	state.Mutex.Unlock()
}

func (state *DumpState) threadDone() {
	state.Mutex.Lock()
	state.ThreadsDone++
	state.Mutex.Unlock()
}

func (state *DumpState) setMemTotal(n uint64) {
	state.Mutex.Lock()
	state.MemTotal = n
	state.Mutex.Unlock()
}

func (state *DumpState) memDone(delta uint64) {
	state.Mutex.Lock()
	state.MemDone += delta
	state.Mutex.Unlock()
}

func (state *DumpState) isCanceled() bool {
	state.Mutex.Lock()
	defer state.Mutex.Unlock()
	return state.Canceled
}

// Dump writes a core dump to out. State is updated as the core dump is written.
func (t *Target) Dump(out elfwriter.WriteCloserSeeker, flags DumpFlags, state *DumpState) {
	defer func() {
		state.Mutex.Lock()
		if ierr := recover(); ierr != nil {
			state.Err = newInternalError(ierr, 2)
		}
		err := out.Close()
		if state.Err == nil && err != nil {
			state.Err = fmt.Errorf("error writing output file: %v", err)
		}
		state.Dumping = false
		state.Mutex.Unlock()
		if state.DoneChan != nil {
			close(state.DoneChan)
		}
	}()

	bi := t.BinInfo()

	var fhdr elf.FileHeader
	fhdr.Class = elf.ELFCLASS64
	fhdr.Data = elf.ELFDATA2LSB
	fhdr.Version = elf.EV_CURRENT

	switch bi.GOOS {
	case "linux":
		fhdr.OSABI = elf.ELFOSABI_LINUX
	case "freebsd":
		fhdr.OSABI = elf.ELFOSABI_FREEBSD
	default:
		// There is no OSABI value for windows or macOS because nobody generates ELF core dumps on those systems.
		fhdr.OSABI = 0xff
	}

	fhdr.Type = elf.ET_CORE

	switch bi.Arch.Name {
	case "amd64":
		fhdr.Machine = elf.EM_X86_64
	case "386":
		fhdr.Machine = elf.EM_386
	case "arm64":
		fhdr.Machine = elf.EM_AARCH64
	case "ppc64le":
		fhdr.Machine = elf.EM_PPC64
	default:
		panic("not implemented")
	}

	fhdr.Entry = 0

	w := elfwriter.New(out, &fhdr)

	notes := []elfwriter.Note{}

	entryPoint, err := t.EntryPoint()
	if err != nil {
		state.setErr(err)
		return
	}

	notes = append(notes, elfwriter.Note{
		Type: elfwriter.DelveHeaderNoteType,
		Name: "Delve Header",
		Data: []byte(fmt.Sprintf("%s/%s\n%s\n%s%d\n%s%#x\n", bi.GOOS, bi.Arch.Name, version.DelveVersion.String(), elfwriter.DelveHeaderTargetPidPrefix, t.pid, elfwriter.DelveHeaderEntryPointPrefix, entryPoint)),
	})

	threads := t.ThreadList()
	state.setThreadsTotal(len(threads))

	var threadsDone bool

	if flags&DumpPlatformIndependent == 0 {
		threadsDone, notes, err = t.proc.DumpProcessNotes(notes, state.threadDone)
		if err != nil {
			state.setErr(err)
			return
		}
	}

	if !threadsDone {
		for _, th := range threads {
			if w.Err != nil {
				state.setErr(fmt.Errorf("error writing to output file: %v", w.Err))
				return
			}
			if state.isCanceled() {
				return
			}
			notes = t.dumpThreadNotes(notes, state, th)
			state.threadDone()
		}
	}

	memmap, err := t.proc.MemoryMap()
	if err != nil {
		state.setErr(err)
		return
	}

	memmapFilter := make([]MemoryMapEntry, 0, len(memmap))
	memtot := uint64(0)
	for i := range memmap {
		mme := &memmap[i]
		if t.shouldDumpMemory(mme) {
			memmapFilter = append(memmapFilter, *mme)
			memtot += mme.Size
		}
	}

	state.setMemTotal(memtot)

	for i := range memmapFilter {
		mme := &memmapFilter[i]
		if w.Err != nil {
			state.setErr(fmt.Errorf("error writing to output file: %v", w.Err))
			return
		}
		if state.isCanceled() {
			return
		}
		t.dumpMemory(state, w, mme)
	}

	notesProg := w.WriteNotes(notes)
	w.Progs = append(w.Progs, notesProg)
	w.WriteProgramHeaders()
	if w.Err != nil {
		state.setErr(fmt.Errorf("error writing to output file: %v", w.Err))
	}
	state.Mutex.Lock()
	state.AllDone = true
	state.Mutex.Unlock()
}

// dumpThreadNotes appends notes describing a thread (thread id and its
// registers) using a platform-independent format.
func (t *Target) dumpThreadNotes(notes []elfwriter.Note, state *DumpState, th Thread) []elfwriter.Note {
	// If the backend doesn't provide a way to dump a thread we use a custom format for the note:
	// - thread_id (8 bytes)
	// - pc value (8 bytes)
	// - sp value (8 bytes)
	// - bp value (8 bytes)
	// - tls value (8 bytes)
	// - has_gaddr (1 byte)
	// - gaddr value (8 bytes)
	// - num_registers (4 bytes)
	// Followed by a list of num_register, each as follows:
	// - register_name_len (2 bytes)
	// - register_name (register_name_len bytes)
	// - register_data_len (2 bytes)
	// - register_data (register_data_len bytes)

	buf := new(bytes.Buffer)
	_ = binary.Write(buf, binary.LittleEndian, uint64(th.ThreadID()))

	regs, err := th.Registers()
	if err != nil {
		state.setErr(err)
		return notes
	}

	for _, specialReg := range []uint64{regs.PC(), regs.SP(), regs.BP(), regs.TLS()} {
		binary.Write(buf, binary.LittleEndian, specialReg)
	}

	gaddr, hasGaddr := regs.GAddr()
	binary.Write(buf, binary.LittleEndian, hasGaddr)
	binary.Write(buf, binary.LittleEndian, gaddr)

	regsv, err := regs.Slice(true)
	if err != nil {
		state.setErr(err)
		return notes
	}

	binary.Write(buf, binary.LittleEndian, uint32(len(regsv)))

	for _, reg := range regsv {
		binary.Write(buf, binary.LittleEndian, uint16(len(reg.Name)))
		buf.Write([]byte(reg.Name))
		if reg.Reg.Bytes != nil {
			binary.Write(buf, binary.LittleEndian, uint16(len(reg.Reg.Bytes)))
			buf.Write(reg.Reg.Bytes)
		} else {
			binary.Write(buf, binary.LittleEndian, uint16(8))
			binary.Write(buf, binary.LittleEndian, reg.Reg.Uint64Val)
		}
	}

	return append(notes, elfwriter.Note{
		Type: elfwriter.DelveThreadNodeType,
		Name: "",
		Data: buf.Bytes(),
	})
}

func (t *Target) dumpMemory(state *DumpState, w *elfwriter.Writer, mme *MemoryMapEntry) {
	var flags elf.ProgFlag
	if mme.Read {
		flags |= elf.PF_R
	}
	if mme.Write {
		flags |= elf.PF_W
	}
	if mme.Exec {
		flags |= elf.PF_X
	}

	w.Progs = append(w.Progs, &elf.ProgHeader{
		Type:   elf.PT_LOAD,
		Flags:  flags,
		Off:    uint64(w.Here()),
		Vaddr:  mme.Addr,
		Paddr:  0,
		Filesz: mme.Size,
		Memsz:  mme.Size,
		Align:  0,
	})

	buf := make([]byte, 1024*1024)
	addr := mme.Addr
	sz := mme.Size
	mem := t.Memory()

	for sz > 0 {
		if w.Err != nil {
			state.setErr(fmt.Errorf("error writing to output file: %v", w.Err))
			return
		}
		if state.isCanceled() {
			return
		}
		chunk := buf
		if uint64(len(chunk)) > sz {
			chunk = chunk[:sz]
		}
		n, err := mem.ReadMemory(chunk, addr)
		for i := n; i < len(chunk); i++ {
			chunk[i] = 0
		}
		// Errors and short reads are ignored, the most likely reason is that
		// (*ProcessInternal).MemoryMap gave us a bad mapping that can't be read
		// and the behavior that's maximally useful to the user is to generate an
		// incomplete dump.
		w.Write(chunk)
		addr += uint64(len(chunk))
		sz -= uint64(len(chunk))
		if err == nil {
			state.memDone(uint64(len(chunk)))
		}
	}
}

func (t *Target) shouldDumpMemory(mme *MemoryMapEntry) bool {
	if !mme.Read {
		return false
	}
	exeimg := t.BinInfo().Images[0]
	if mme.Write || mme.Filename == "" || mme.Filename != exeimg.Path {
		return true
	}
	isgo := false
	for _, cu := range exeimg.compileUnits {
		if cu.isgo {
			isgo = true
			break
		}
	}
	if !isgo {
		return true
	}

	exe, err := elf.Open(exeimg.Path)
	if err != nil {
		return true
	}

	if exe.Type != elf.ET_EXEC {
		return true
	}

	for _, prog := range exe.Progs {
		if prog.Type == elf.PT_LOAD && (prog.Flags&elf.PF_W == 0) && (prog.Flags&elf.PF_R != 0) && (prog.Vaddr == mme.Addr) && (prog.Memsz == mme.Size) && (prog.Off == mme.Offset) {
			return false
		}
	}
	return true
}

type internalError struct {
	Err   interface{}
	Stack []internalErrorFrame
}

type internalErrorFrame struct {
	Pc   uintptr
	Func string
	File string
	Line int
}

func newInternalError(ierr interface{}, skip int) *internalError {
	r := &internalError{ierr, nil}
	for i := skip; ; i++ {
		pc, file, line, ok := runtime.Caller(i)
		if !ok {
			break
		}
		fname := "<unknown>"
		fn := runtime.FuncForPC(pc)
		if fn != nil {
			fname = fn.Name()
		}
		r.Stack = append(r.Stack, internalErrorFrame{pc, fname, file, line})
	}
	return r
}

func (err *internalError) Error() string {
	var out bytes.Buffer
	fmt.Fprintf(&out, "Internal debugger error: %v\n", err.Err)
	for _, frame := range err.Stack {
		fmt.Fprintf(&out, "%s (%#x)\n\t%s:%d\n", frame.Func, frame.Pc, frame.File, frame.Line)
	}
	return out.String()
}
