package core

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/debug/elf"

	"golang.org/x/arch/x86/x86asm"

	"github.com/derekparker/delve/pkg/proc"
)

// Copied from golang.org/x/sys/unix.PtraceRegs since it's not available on
// all systems.
type LinuxCoreRegisters struct {
	R15      uint64
	R14      uint64
	R13      uint64
	R12      uint64
	Rbp      uint64
	Rbx      uint64
	R11      uint64
	R10      uint64
	R9       uint64
	R8       uint64
	Rax      uint64
	Rcx      uint64
	Rdx      uint64
	Rsi      uint64
	Rdi      uint64
	Orig_rax uint64
	Rip      uint64
	Cs       uint64
	Eflags   uint64
	Rsp      uint64
	Ss       uint64
	Fs_base  uint64
	Gs_base  uint64
	Ds       uint64
	Es       uint64
	Fs       uint64
	Gs       uint64
}

// Copied from golang.org/x/sys/unix.Timeval since it's not available on all
// systems.
type LinuxCoreTimeval struct {
	Sec  int64
	Usec int64
}

const NT_FILE elf.NType = 0x46494c45 // "FILE".

func (r *LinuxCoreRegisters) PC() uint64 {
	return r.Rip
}

func (r *LinuxCoreRegisters) SP() uint64 {
	return r.Rsp
}

func (r *LinuxCoreRegisters) BP() uint64 {
	return r.Rbp
}

func (r *LinuxCoreRegisters) CX() uint64 {
	return r.Rcx
}

func (r *LinuxCoreRegisters) TLS() uint64 {
	return r.Fs_base
}

func (r *LinuxCoreRegisters) GAddr() (uint64, bool) {
	return 0, false
}

func (r *LinuxCoreRegisters) Get(n int) (uint64, error) {
	reg := x86asm.Reg(n)
	const (
		mask8  = 0x000f
		mask16 = 0x00ff
		mask32 = 0xffff
	)

	switch reg {
	// 8-bit
	case x86asm.AL:
		return r.Rax & mask8, nil
	case x86asm.CL:
		return r.Rcx & mask8, nil
	case x86asm.DL:
		return r.Rdx & mask8, nil
	case x86asm.BL:
		return r.Rbx & mask8, nil
	case x86asm.AH:
		return (r.Rax >> 8) & mask8, nil
	case x86asm.CH:
		return (r.Rcx >> 8) & mask8, nil
	case x86asm.DH:
		return (r.Rdx >> 8) & mask8, nil
	case x86asm.BH:
		return (r.Rbx >> 8) & mask8, nil
	case x86asm.SPB:
		return r.Rsp & mask8, nil
	case x86asm.BPB:
		return r.Rbp & mask8, nil
	case x86asm.SIB:
		return r.Rsi & mask8, nil
	case x86asm.DIB:
		return r.Rdi & mask8, nil
	case x86asm.R8B:
		return r.R8 & mask8, nil
	case x86asm.R9B:
		return r.R9 & mask8, nil
	case x86asm.R10B:
		return r.R10 & mask8, nil
	case x86asm.R11B:
		return r.R11 & mask8, nil
	case x86asm.R12B:
		return r.R12 & mask8, nil
	case x86asm.R13B:
		return r.R13 & mask8, nil
	case x86asm.R14B:
		return r.R14 & mask8, nil
	case x86asm.R15B:
		return r.R15 & mask8, nil

	// 16-bit
	case x86asm.AX:
		return r.Rax & mask16, nil
	case x86asm.CX:
		return r.Rcx & mask16, nil
	case x86asm.DX:
		return r.Rdx & mask16, nil
	case x86asm.BX:
		return r.Rbx & mask16, nil
	case x86asm.SP:
		return r.Rsp & mask16, nil
	case x86asm.BP:
		return r.Rbp & mask16, nil
	case x86asm.SI:
		return r.Rsi & mask16, nil
	case x86asm.DI:
		return r.Rdi & mask16, nil
	case x86asm.R8W:
		return r.R8 & mask16, nil
	case x86asm.R9W:
		return r.R9 & mask16, nil
	case x86asm.R10W:
		return r.R10 & mask16, nil
	case x86asm.R11W:
		return r.R11 & mask16, nil
	case x86asm.R12W:
		return r.R12 & mask16, nil
	case x86asm.R13W:
		return r.R13 & mask16, nil
	case x86asm.R14W:
		return r.R14 & mask16, nil
	case x86asm.R15W:
		return r.R15 & mask16, nil

	// 32-bit
	case x86asm.EAX:
		return r.Rax & mask32, nil
	case x86asm.ECX:
		return r.Rcx & mask32, nil
	case x86asm.EDX:
		return r.Rdx & mask32, nil
	case x86asm.EBX:
		return r.Rbx & mask32, nil
	case x86asm.ESP:
		return r.Rsp & mask32, nil
	case x86asm.EBP:
		return r.Rbp & mask32, nil
	case x86asm.ESI:
		return r.Rsi & mask32, nil
	case x86asm.EDI:
		return r.Rdi & mask32, nil
	case x86asm.R8L:
		return r.R8 & mask32, nil
	case x86asm.R9L:
		return r.R9 & mask32, nil
	case x86asm.R10L:
		return r.R10 & mask32, nil
	case x86asm.R11L:
		return r.R11 & mask32, nil
	case x86asm.R12L:
		return r.R12 & mask32, nil
	case x86asm.R13L:
		return r.R13 & mask32, nil
	case x86asm.R14L:
		return r.R14 & mask32, nil
	case x86asm.R15L:
		return r.R15 & mask32, nil

	// 64-bit
	case x86asm.RAX:
		return r.Rax, nil
	case x86asm.RCX:
		return r.Rcx, nil
	case x86asm.RDX:
		return r.Rdx, nil
	case x86asm.RBX:
		return r.Rbx, nil
	case x86asm.RSP:
		return r.Rsp, nil
	case x86asm.RBP:
		return r.Rbp, nil
	case x86asm.RSI:
		return r.Rsi, nil
	case x86asm.RDI:
		return r.Rdi, nil
	case x86asm.R8:
		return r.R8, nil
	case x86asm.R9:
		return r.R9, nil
	case x86asm.R10:
		return r.R10, nil
	case x86asm.R11:
		return r.R11, nil
	case x86asm.R12:
		return r.R12, nil
	case x86asm.R13:
		return r.R13, nil
	case x86asm.R14:
		return r.R14, nil
	case x86asm.R15:
		return r.R15, nil
	}

	return 0, proc.UnknownRegisterError
}

func (r *LinuxCoreRegisters) SetPC(proc.IThread, uint64) error {
	return errors.New("not supported")
}

func (r *LinuxCoreRegisters) Slice() []proc.Register {
	var regs = []struct {
		k string
		v uint64
	}{
		{"Rip", r.Rip},
		{"Rsp", r.Rsp},
		{"Rax", r.Rax},
		{"Rbx", r.Rbx},
		{"Rcx", r.Rcx},
		{"Rdx", r.Rdx},
		{"Rdi", r.Rdi},
		{"Rsi", r.Rsi},
		{"Rbp", r.Rbp},
		{"R8", r.R8},
		{"R9", r.R9},
		{"R10", r.R10},
		{"R11", r.R11},
		{"R12", r.R12},
		{"R13", r.R13},
		{"R14", r.R14},
		{"R15", r.R15},
		{"Orig_rax", r.Orig_rax},
		{"Cs", r.Cs},
		{"Eflags", r.Eflags},
		{"Ss", r.Ss},
		{"Fs_base", r.Fs_base},
		{"Gs_base", r.Gs_base},
		{"Ds", r.Ds},
		{"Es", r.Es},
		{"Fs", r.Fs},
		{"Gs", r.Gs},
	}
	out := make([]proc.Register, 0, len(regs))
	for _, reg := range regs {
		if reg.k == "Eflags" {
			out = proc.AppendEflagReg(out, reg.k, reg.v)
		} else {
			out = proc.AppendQwordReg(out, reg.k, reg.v)
		}
	}
	return out
}

// readCore reads a core file from corePath corresponding to the executable at
// exePath. For details on the Linux ELF core format, see:
// http://www.gabriel.urdhr.fr/2015/05/29/core-file/,
// http://uhlo.blogspot.fr/2012/05/brief-look-into-core-dumps.html,
// elf_core_dump in http://lxr.free-electrons.com/source/fs/binfmt_elf.c,
// and, if absolutely desperate, readelf.c from the binutils source.
func readCore(corePath, exePath string) (*Core, error) {
	core, err := elf.Open(corePath)
	if err != nil {
		return nil, err
	}
	exe, err := os.Open(exePath)
	if err != nil {
		return nil, err
	}

	if core.Type != elf.ET_CORE {
		return nil, fmt.Errorf("%v is not a core file", core)
	}

	notes, err := readNotes(core)
	if err != nil {
		return nil, err
	}
	memory := buildMemory(core, exe, notes)

	threads := map[int]*LinuxPrStatus{}
	pid := 0
	for _, note := range notes {
		switch note.Type {
		case elf.NT_PRSTATUS:
			t := note.Desc.(*LinuxPrStatus)
			threads[int(t.Pid)] = t
		case elf.NT_PRPSINFO:
			pid = int(note.Desc.(*LinuxPrPsInfo).Pid)
		}
	}
	return &Core{
		MemoryReader: memory,
		Threads:      threads,
		Pid:          pid,
	}, nil
}

type Core struct {
	proc.MemoryReader
	Threads map[int]*LinuxPrStatus
	Pid     int
}

// Note is a note from the PT_NOTE prog.
// Relevant types:
// - NT_FILE: File mapping information, e.g. program text mappings. Desc is a LinuxNTFile.
// - NT_PRPSINFO: Information about a process, including PID and signal. Desc is a LinuxPrPsInfo.
// - NT_PRSTATUS: Information about a thread, including base registers, state, etc. Desc is a LinuxPrStatus.
// - NT_FPREGSET (Not implemented): x87 floating point registers.
// - NT_X86_XSTATE (Not implemented): Other registers, including AVX and such.
type Note struct {
	Type elf.NType
	Name string
	Desc interface{} // Decoded Desc from the
}

// readNotes reads all the notes from the notes prog in core.
func readNotes(core *elf.File) ([]*Note, error) {
	var notesProg *elf.Prog
	for _, prog := range core.Progs {
		if prog.Type == elf.PT_NOTE {
			notesProg = prog
			break
		}
	}

	r := notesProg.Open()
	notes := []*Note{}
	for {
		note, err := readNote(r)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		notes = append(notes, note)
	}

	return notes, nil
}

// readNote reads a single note from r, decoding the descriptor if possible.
func readNote(r io.ReadSeeker) (*Note, error) {
	// Notes are laid out as described in the SysV ABI:
	// http://www.sco.com/developers/gabi/latest/ch5.pheader.html#note_section
	note := &Note{}
	hdr := &ELFNotesHdr{}

	err := binary.Read(r, binary.LittleEndian, hdr)
	if err != nil {
		return nil, err // don't wrap so readNotes sees EOF.
	}
	note.Type = elf.NType(hdr.Type)

	name := make([]byte, hdr.Namesz)
	if _, err := r.Read(name); err != nil {
		return nil, fmt.Errorf("reading name: %v", err)
	}
	note.Name = string(name)
	if err := skipPadding(r, 4); err != nil {
		return nil, fmt.Errorf("aligning after name: %v", err)
	}
	desc := make([]byte, hdr.Descsz)
	if _, err := r.Read(desc); err != nil {
		return nil, fmt.Errorf("reading desc: %v", err)
	}
	descReader := bytes.NewReader(desc)
	switch note.Type {
	case elf.NT_PRSTATUS:
		note.Desc = &LinuxPrStatus{}
		if err := binary.Read(descReader, binary.LittleEndian, note.Desc); err != nil {
			return nil, fmt.Errorf("reading NT_PRSTATUS: %v", err)
		}
	case elf.NT_PRPSINFO:
		note.Desc = &LinuxPrPsInfo{}
		if err := binary.Read(descReader, binary.LittleEndian, note.Desc); err != nil {
			return nil, fmt.Errorf("reading NT_PRPSINFO: %v", err)
		}
	case NT_FILE:
		// No good documentation reference, but the structure is
		// simply a header, including entry count, followed by that
		// many entries, and then the file name of each entry,
		// null-delimited. Not reading the names here.
		data := &LinuxNTFile{}
		if err := binary.Read(descReader, binary.LittleEndian, &data.LinuxNTFileHdr); err != nil {
			return nil, fmt.Errorf("reading NT_FILE header: %v", err)
		}
		for i := 0; i < int(data.Count); i++ {
			entry := &LinuxNTFileEntry{}
			if err := binary.Read(descReader, binary.LittleEndian, entry); err != nil {
				return nil, fmt.Errorf("reading NT_PRPSINFO entry %v: %v", i, err)
			}
			data.entries = append(data.entries, entry)
		}
		note.Desc = data
	}
	if err := skipPadding(r, 4); err != nil {
		return nil, fmt.Errorf("aligning after desc: %v", err)
	}
	return note, nil
}

// skipPadding moves r to the next multiple of pad.
func skipPadding(r io.ReadSeeker, pad int64) error {
	pos, err := r.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if pos%pad == 0 {
		return nil
	}
	if _, err := r.Seek(pad-(pos%pad), io.SeekCurrent); err != nil {
		return err
	}
	return nil
}

func buildMemory(core *elf.File, exe io.ReaderAt, notes []*Note) proc.MemoryReader {
	memory := &SplicedMemory{}

	// For now, assume all file mappings are to the exe.
	for _, note := range notes {
		if note.Type == NT_FILE {
			fileNote := note.Desc.(*LinuxNTFile)
			for _, entry := range fileNote.entries {
				r := &OffsetReaderAt{
					reader: exe,
					offset: uintptr(entry.Start - (entry.FileOfs * fileNote.PageSize)),
				}
				memory.Add(r, uintptr(entry.Start), uintptr(entry.End-entry.Start))
			}

		}
	}
	for _, prog := range core.Progs {
		if prog.Type == elf.PT_LOAD {
			if prog.Filesz == 0 {
				continue
			}
			r := &OffsetReaderAt{
				reader: prog.ReaderAt,
				offset: uintptr(prog.Vaddr),
			}
			memory.Add(r, uintptr(prog.Vaddr), uintptr(prog.Filesz))
		}
	}
	return memory
}

// Various structures from the ELF spec and the Linux kernel.
// AMD64 specific primarily because of unix.PtraceRegs, but also
// because some of the fields are word sized.
// See http://lxr.free-electrons.com/source/include/uapi/linux/elfcore.h
type LinuxPrPsInfo struct {
	State                uint8
	Sname                int8
	Zomb                 uint8
	Nice                 int8
	_                    [4]uint8
	Flag                 uint64
	Uid, Gid             uint32
	Pid, Ppid, Pgrp, Sid int32
	Fname                [16]uint8
	Args                 [80]uint8
}

type LinuxPrStatus struct {
	Siginfo                      LinuxSiginfo
	Cursig                       uint16
	_                            [2]uint8
	Sigpend                      uint64
	Sighold                      uint64
	Pid, Ppid, Pgrp, Sid         int32
	Utime, Stime, CUtime, CStime LinuxCoreTimeval
	Reg                          LinuxCoreRegisters
	Fpvalid                      int32
}

type LinuxSiginfo struct {
	Signo int32
	Code  int32
	Errno int32
}

type LinuxNTFile struct {
	LinuxNTFileHdr
	entries []*LinuxNTFileEntry
}

type LinuxNTFileHdr struct {
	Count    uint64
	PageSize uint64
}

type LinuxNTFileEntry struct {
	Start   uint64
	End     uint64
	FileOfs uint64
}

// ELF Notes header. Same size on 64 and 32-bit machines.
type ELFNotesHdr struct {
	Namesz uint32
	Descsz uint32
	Type   uint32
}
