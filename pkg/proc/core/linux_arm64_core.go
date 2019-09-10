package core

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/linutil"
)

// Copied from golang.org/x/sys/unix.Timeval since it's not available on all
// systems.
type LinuxCoreTimeval_arm64 struct {
	Sec  int64
	Usec int64
}

// NT_FILE is file mapping information, e.g. program text mappings. Desc is a LinuxNTFile.
const NT_FILE_arm64 elf.NType = 0x46494c45 // "FILE".

// NT_X86_XSTATE is other registers, including AVX and such.
const NT_X86_XSTATE_arm64 elf.NType = 0x202 // Note type for notes containing X86 XSAVE area.

// NT_AUXV is the note type for notes containing a copy of the Auxv array
const NT_AUXV_arm64 elf.NType = 0x6

const elfErrorBadMagicNumber_arm64 = "bad magic number"

// readLinuxARM64Core reads a core file from corePath corresponding to the executable at
// exePath. For details on the Linux ELF core format, see:
// http://www.gabriel.urdhr.fr/2015/05/29/core-file/,
// http://uhlo.blogspot.fr/2012/05/brief-look-into-core-dumps.html,
// elf_core_dump in http://lxr.free-electrons.com/source/fs/binfmt_elf.c,
// and, if absolutely desperate, readelf.c from the binutils source.
func readLinuxARM64Core(corePath, exePath string) (*Process, error) {
	coreFile, err := elf.Open(corePath)
	if err != nil {
		if _, isfmterr := err.(*elf.FormatError); isfmterr && (strings.Contains(err.Error(), elfErrorBadMagicNumber_arm64) || strings.Contains(err.Error(), " at offset 0x0: too short")) {
			// Go >=1.11 and <1.11 produce different errors when reading a non-elf file.
			return nil, ErrUnrecognizedFormat
		}
		return nil, err
	}
	exe, err := os.Open(exePath)
	if err != nil {
		return nil, err
	}
	exeELF, err := elf.NewFile(exe)
	if err != nil {
		return nil, err
	}

	if coreFile.Type != elf.ET_CORE {
		return nil, fmt.Errorf("%v is not a core file", coreFile)
	}
	if exeELF.Type != elf.ET_EXEC && exeELF.Type != elf.ET_DYN {
		return nil, fmt.Errorf("%v is not an exe file", exeELF)
	}

	notes, err := readNotes_arm64(coreFile)
	if err != nil {
		return nil, err
	}
	memory := buildMemory_arm64(coreFile, exeELF, exe, notes)
	entryPoint := findEntryPoint_arm64(notes)

	p := &Process{
		mem:         memory,
		Threads:     map[int]*Thread{},
		entryPoint:  entryPoint,
		bi:          proc.NewBinaryInfo("linux", "arm64"),
		breakpoints: proc.NewBreakpointMap(),
	}

	var lastThread *linuxARM64Thread
	for _, note := range notes {
		switch note.Type {
		case elf.NT_PRSTATUS:
			t := note.Desc.(*LinuxPrStatus_arm64)
			lastThread = &linuxARM64Thread{linutil.ARM64Registers{Regs: &t.Reg}, t}
			p.Threads[int(t.Pid)] = &Thread{lastThread, p, proc.CommonThread{}}
			if p.currentThread == nil {
				p.currentThread = p.Threads[int(t.Pid)]
			}
		case NT_X86_XSTATE_arm64:
			if lastThread != nil {
				lastThread.regs.Fpregs = note.Desc.(*linutil.ARM64Xstate).Decode()
			}
		case elf.NT_PRPSINFO:
			p.pid = int(note.Desc.(*LinuxPrPsInfo_arm64).Pid)
		}
	}
	return p, nil
}

type linuxARM64Thread struct {
	regs linutil.ARM64Registers
	t    *LinuxPrStatus_arm64
}

func (t *linuxARM64Thread) registers(floatingPoint bool) (proc.Registers, error) {
	var r linutil.ARM64Registers
	r.Regs = t.regs.Regs
	if floatingPoint {
		r.Fpregs = t.regs.Fpregs
	}
	return &r, nil
}

func (t *linuxARM64Thread) pid() int {
	return int(t.t.Pid)
}

// Note is a note from the PT_NOTE prog.
// Relevant types:
// - NT_FILE: File mapping information, e.g. program text mappings. Desc is a LinuxNTFile.
// - NT_PRPSINFO: Information about a process, including PID and signal. Desc is a LinuxPrPsInfo.
// - NT_PRSTATUS: Information about a thread, including base registers, state, etc. Desc is a LinuxPrStatus.
// - NT_FPREGSET (Not implemented): x87 floating point registers.
// - NT_X86_XSTATE: Other registers, including AVX and such.
type Note_arm64 struct {
	Type elf.NType
	Name string
	Desc interface{} // Decoded Desc from the
}

// readNotes reads all the notes from the notes prog in core.
func readNotes_arm64(core *elf.File) ([]*Note_arm64, error) {
	var notesProg *elf.Prog
	for _, prog := range core.Progs {
		if prog.Type == elf.PT_NOTE {
			notesProg = prog
			break
		}
	}

	r := notesProg.Open()
	notes := []*Note_arm64{}
	for {
		note, err := readNote_arm64(r)
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
func readNote_arm64(r io.ReadSeeker) (*Note_arm64, error) {
	// Notes are laid out as described in the SysV ABI:
	// http://www.sco.com/developers/gabi/latest/ch5.pheader.html#note_section
	note := &Note_arm64{}
	hdr := &ELFNotesHdr_arm64{}

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
	if err := skipPadding_arm64(r, 4); err != nil {
		return nil, fmt.Errorf("aligning after name: %v", err)
	}
	desc := make([]byte, hdr.Descsz)
	if _, err := r.Read(desc); err != nil {
		return nil, fmt.Errorf("reading desc: %v", err)
	}
	descReader := bytes.NewReader(desc)
	switch note.Type {
	case elf.NT_PRSTATUS:
		note.Desc = &LinuxPrStatus_arm64{}
		if err := binary.Read(descReader, binary.LittleEndian, note.Desc); err != nil {
			return nil, fmt.Errorf("reading NT_PRSTATUS: %v", err)
		}
	case elf.NT_PRPSINFO:
		note.Desc = &LinuxPrPsInfo_arm64{}
		if err := binary.Read(descReader, binary.LittleEndian, note.Desc); err != nil {
			return nil, fmt.Errorf("reading NT_PRPSINFO: %v", err)
		}
	case NT_FILE_arm64:
		// No good documentation reference, but the structure is
		// simply a header, including entry count, followed by that
		// many entries, and then the file name of each entry,
		// null-delimited. Not reading the names here.
		data := &LinuxNTFile_arm64{}
		if err := binary.Read(descReader, binary.LittleEndian, &data.LinuxNTFileHdr_arm64); err != nil {
			return nil, fmt.Errorf("reading NT_FILE header: %v", err)
		}
		for i := 0; i < int(data.Count); i++ {
			entry := &LinuxNTFileEntry_arm64{}
			if err := binary.Read(descReader, binary.LittleEndian, entry); err != nil {
				return nil, fmt.Errorf("reading NT_FILE entry %v: %v", i, err)
			}
			data.entries = append(data.entries, entry)
		}
		note.Desc = data
	case NT_X86_XSTATE_arm64:
		var fpregs linutil.ARM64Xstate
		if err := linutil.ARM64XstateRead(desc, true, &fpregs); err != nil {
			return nil, err
		}
		note.Desc = &fpregs
	case NT_AUXV_arm64:
		note.Desc = desc
	}
	if err := skipPadding_arm64(r, 4); err != nil {
		return nil, fmt.Errorf("aligning after desc: %v", err)
	}
	return note, nil
}

// skipPadding moves r to the next multiple of pad.
func skipPadding_arm64(r io.ReadSeeker, pad int64) error {
	pos, err := r.Seek(0, os.SEEK_CUR)
	if err != nil {
		return err
	}
	if pos%pad == 0 {
		return nil
	}
	if _, err := r.Seek(pad-(pos%pad), os.SEEK_CUR); err != nil {
		return err
	}
	return nil
}

func buildMemory_arm64(core, exeELF *elf.File, exe io.ReaderAt, notes []*Note_arm64) proc.MemoryReader {
	memory := &SplicedMemory{}

	// For now, assume all file mappings are to the exe.
	for _, note := range notes {
		if note.Type == NT_FILE_arm64 {
			fileNote := note.Desc.(*LinuxNTFile_arm64)
			for _, entry := range fileNote.entries {
				r := &OffsetReaderAt{
					reader: exe,
					offset: uintptr(entry.Start - (entry.FileOfs * fileNote.PageSize)),
				}
				memory.Add(r, uintptr(entry.Start), uintptr(entry.End-entry.Start))
			}

		}
	}

	// Load memory segments from exe and then from the core file,
	// allowing the corefile to overwrite previously loaded segments
	for _, elfFile := range []*elf.File{exeELF, core} {
		for _, prog := range elfFile.Progs {
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
	}
	return memory
}

func findEntryPoint_arm64(notes []*Note_arm64) uint64 {
	for _, note := range notes {
		if note.Type == NT_AUXV_arm64 {
			return linutil.EntryPointFromAuxvARM64(note.Desc.([]byte))
		}
	}
	return 0
}

// LinuxPrPsInfo has various structures from the ELF spec and the Linux kernel.
// ARM64 specific primarily because of unix.PtraceRegs, but also
// because some of the fields are word sized.
// See http://lxr.free-electrons.com/source/include/uapi/linux/elfcore.h
type LinuxPrPsInfo_arm64 struct {
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

// LinuxPrStatus is a copy of the prstatus kernel struct.
type LinuxPrStatus_arm64 struct {
	Siginfo                      LinuxSiginfo_arm64
	Cursig                       uint16
	_                            [2]uint8
	Sigpend                      uint64
	Sighold                      uint64
	Pid, Ppid, Pgrp, Sid         int32
	Utime, Stime, CUtime, CStime LinuxCoreTimeval_arm64
	Reg                          linutil.ARM64PtraceRegs
	Fpvalid                      int32
}

// LinuxSiginfo is a copy of the
// siginfo kernel struct.
type LinuxSiginfo_arm64 struct {
	Signo int32
	Code  int32
	Errno int32
}

// LinuxNTFile contains information on mapped files.
type LinuxNTFile_arm64 struct {
	LinuxNTFileHdr_arm64
	entries []*LinuxNTFileEntry_arm64
}

// LinuxNTFileHdr is a header struct for NTFile.
type LinuxNTFileHdr_arm64 struct {
	Count    uint64
	PageSize uint64
}

// LinuxNTFileEntry is an entry of an NT_FILE note.
type LinuxNTFileEntry_arm64 struct {
	Start   uint64
	End     uint64
	FileOfs uint64
}

// ELFNotesHdr is the ELF Notes header.
// Same size on 64 and 32-bit machines.
type ELFNotesHdr_arm64 struct {
	Namesz uint32
	Descsz uint32
	Type   uint32
}
