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
type linuxCoreTimeval struct {
	Sec  int64
	Usec int64
}

// NT_FILE is file mapping information, e.g. program text mappings. Desc is a LinuxNTFile.
const _NT_FILE elf.NType = 0x46494c45 // "FILE".

// NT_X86_XSTATE is other registers, including AVX and such.
const _NT_X86_XSTATE elf.NType = 0x202 // Note type for notes containing X86 XSAVE area.

// NT_AUXV is the note type for notes containing a copy of the Auxv array
const _NT_AUXV elf.NType = 0x6

// NT_FPREGSET is the note type for floating point registers.
const _NT_FPREGSET elf.NType = 0x2

// Fetch architecture using exeELF.Machine from core file
// Refer http://man7.org/linux/man-pages/man5/elf.5.html
const (
	_EM_ARM              = 40
	_EM_AARCH64          = 183
	_EM_X86_64           = 62
	_ARM_FP_HEADER_START = 512
)

const elfErrorBadMagicNumber = "bad magic number"

func linuxThreadsFromNotes(p *process, notes []*note, machineType elf.Machine) {
	var lastThreadAMD *linuxAMD64Thread
	var lastThreadARM *linuxARM64Thread
	for _, note := range notes {
		switch note.Type {
		case elf.NT_PRSTATUS:
			if machineType == _EM_X86_64 {
				t := note.Desc.(*linuxPrStatusAMD64)
				lastThreadAMD = &linuxAMD64Thread{linutil.AMD64Registers{Regs: &t.Reg}, t}
				p.Threads[int(t.Pid)] = &thread{lastThreadAMD, p, proc.CommonThread{}}
				if p.currentThread == nil {
					p.currentThread = p.Threads[int(t.Pid)]
				}
			} else if machineType == _EM_AARCH64 {
				t := note.Desc.(*linuxPrStatusARM64)
				lastThreadARM = &linuxARM64Thread{linutil.ARM64Registers{Regs: &t.Reg}, t}
				p.Threads[int(t.Pid)] = &thread{lastThreadARM, p, proc.CommonThread{}}
				if p.currentThread == nil {
					p.currentThread = p.Threads[int(t.Pid)]
				}
			}
		case _NT_FPREGSET:
			if machineType == _EM_AARCH64 {
				if lastThreadARM != nil {
					lastThreadARM.regs.Fpregs = note.Desc.(*linutil.ARM64PtraceFpRegs).Decode()
				}
			}
		case _NT_X86_XSTATE:
			if machineType == _EM_X86_64 {
				if lastThreadAMD != nil {
					lastThreadAMD.regs.Fpregs = note.Desc.(*linutil.AMD64Xstate).Decode()
				}
			}
		case elf.NT_PRPSINFO:
			p.pid = int(note.Desc.(*linuxPrPsInfo).Pid)
		}
	}
}

// readLinuxCore reads a core file from corePath corresponding to the executable at
// exePath. For details on the Linux ELF core format, see:
// http://www.gabriel.urdhr.fr/2015/05/29/core-file/,
// http://uhlo.blogspot.fr/2012/05/brief-look-into-core-dumps.html,
// elf_core_dump in http://lxr.free-electrons.com/source/fs/binfmt_elf.c,
// and, if absolutely desperate, readelf.c from the binutils source.
func readLinuxCore(corePath, exePath string) (*process, error) {
	coreFile, err := elf.Open(corePath)
	if err != nil {
		if _, isfmterr := err.(*elf.FormatError); isfmterr && (strings.Contains(err.Error(), elfErrorBadMagicNumber) || strings.Contains(err.Error(), " at offset 0x0: too short")) {
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

	machineType := exeELF.Machine
	notes, err := readNotes(coreFile, machineType)
	if err != nil {
		return nil, err
	}
	memory := buildMemory(coreFile, exeELF, exe, notes)

	// TODO support 386
	var bi *proc.BinaryInfo
	switch machineType {
	case _EM_X86_64:
		bi = proc.NewBinaryInfo("linux", "amd64")
	case _EM_AARCH64:
		bi = proc.NewBinaryInfo("linux", "arm64")
	case _EM_ARM:
		bi = proc.NewBinaryInfo("linux", "arm")
	default:
		return nil, fmt.Errorf("unsupported machine type")
	}

	entryPoint := findEntryPoint(notes, bi.Arch.PtrSize())

	p := &process{
		mem:         memory,
		Threads:     map[int]*thread{},
		entryPoint:  entryPoint,
		bi:          bi,
		breakpoints: proc.NewBreakpointMap(),
	}

	linuxThreadsFromNotes(p, notes, machineType)
	return p, nil
}

type linuxAMD64Thread struct {
	regs linutil.AMD64Registers
	t    *linuxPrStatusAMD64
}

type linuxARM64Thread struct {
	regs linutil.ARM64Registers
	t    *linuxPrStatusARM64
}

func (t *linuxAMD64Thread) registers() (proc.Registers, error) {
	var r linutil.AMD64Registers
	r.Regs = t.regs.Regs
	r.Fpregs = t.regs.Fpregs
	return &r, nil
}

func (t *linuxARM64Thread) registers() (proc.Registers, error) {
	var r linutil.ARM64Registers
	r.Regs = t.regs.Regs
	r.Fpregs = t.regs.Fpregs
	return &r, nil
}

func (t *linuxAMD64Thread) pid() int {
	return int(t.t.Pid)
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
type note struct {
	Type elf.NType
	Name string
	Desc interface{} // Decoded Desc from the
}

// readNotes reads all the notes from the notes prog in core.
func readNotes(core *elf.File, machineType elf.Machine) ([]*note, error) {
	var notesProg *elf.Prog
	for _, prog := range core.Progs {
		if prog.Type == elf.PT_NOTE {
			notesProg = prog
			break
		}
	}

	r := notesProg.Open()
	notes := []*note{}
	for {
		note, err := readNote(r, machineType)
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
func readNote(r io.ReadSeeker, machineType elf.Machine) (*note, error) {
	// Notes are laid out as described in the SysV ABI:
	// http://www.sco.com/developers/gabi/latest/ch5.pheader.html#note_section
	note := &note{}
	hdr := &elfNotesHdr{}

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
		if machineType == _EM_X86_64 {
			note.Desc = &linuxPrStatusAMD64{}
		} else if machineType == _EM_AARCH64 {
			note.Desc = &linuxPrStatusARM64{}
		} else if machineType == _EM_ARM {
			note.Desc = &linuxPrStatusARM{}
		} else {
			return nil, fmt.Errorf("unsupported machine type")
		}
		if err := binary.Read(descReader, binary.LittleEndian, note.Desc); err != nil {
			return nil, fmt.Errorf("reading NT_PRSTATUS: %v", err)
		}
	case elf.NT_PRPSINFO:
		note.Desc = &linuxPrPsInfo{}
		if err := binary.Read(descReader, binary.LittleEndian, note.Desc); err != nil {
			return nil, fmt.Errorf("reading NT_PRPSINFO: %v", err)
		}
	case _NT_FILE:
		// No good documentation reference, but the structure is
		// simply a header, including entry count, followed by that
		// many entries, and then the file name of each entry,
		// null-delimited. Not reading the names here.
		data := &linuxNTFile{}
		if err := binary.Read(descReader, binary.LittleEndian, &data.linuxNTFileHdr); err != nil {
			return nil, fmt.Errorf("reading NT_FILE header: %v", err)
		}
		for i := 0; i < int(data.Count); i++ {
			entry := &linuxNTFileEntry{}
			if err := binary.Read(descReader, binary.LittleEndian, entry); err != nil {
				return nil, fmt.Errorf("reading NT_FILE entry %v: %v", i, err)
			}
			data.entries = append(data.entries, entry)
		}
		note.Desc = data
	case _NT_X86_XSTATE:
		if machineType == _EM_X86_64 {
			var fpregs linutil.AMD64Xstate
			if err := linutil.AMD64XstateRead(desc, true, &fpregs); err != nil {
				return nil, err
			}
			note.Desc = &fpregs
		}
	case _NT_AUXV:
		note.Desc = desc
	case _NT_FPREGSET:
		if machineType == _EM_AARCH64 {
			fpregs := &linutil.ARM64PtraceFpRegs{}
			rdr := bytes.NewReader(desc[:_ARM_FP_HEADER_START])
			if err := binary.Read(rdr, binary.LittleEndian, fpregs.Byte()); err != nil {
				return nil, err
			}
			note.Desc = fpregs
		} else if machineType == _EM_ARM {
			fpregs := &linutil.ARMPtraceFpRegs{}
			rdr := bytes.NewReader(desc[:_ARM_FP_HEADER_START])
			if err := binary.Read(rdr, binary.LittleEndian, fpregs.Byte()); err != nil {
				return nil, err
			}
			note.Desc = fpregs
		}
	}
	if err := skipPadding(r, 4); err != nil {
		return nil, fmt.Errorf("aligning after desc: %v", err)
	}
	return note, nil
}

// skipPadding moves r to the next multiple of pad.
func skipPadding(r io.ReadSeeker, pad int64) error {
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

func buildMemory(core, exeELF *elf.File, exe io.ReaderAt, notes []*note) proc.MemoryReader {
	memory := &splicedMemory{}

	// For now, assume all file mappings are to the exe.
	for _, note := range notes {
		if note.Type == _NT_FILE {
			fileNote := note.Desc.(*linuxNTFile)
			for _, entry := range fileNote.entries {
				r := &offsetReaderAt{
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
				r := &offsetReaderAt{
					reader: prog.ReaderAt,
					offset: uintptr(prog.Vaddr),
				}
				memory.Add(r, uintptr(prog.Vaddr), uintptr(prog.Filesz))
			}
		}
	}
	return memory
}

func findEntryPoint(notes []*note, ptrSize int) uint64 {
	for _, note := range notes {
		if note.Type == _NT_AUXV {
			return linutil.EntryPointFromAuxv(note.Desc.([]byte), ptrSize)
		}
	}
	return 0
}

// LinuxPrPsInfo has various structures from the ELF spec and the Linux kernel.
// AMD64 specific primarily because of unix.PtraceRegs, but also
// because some of the fields are word sized.
// See http://lxr.free-electrons.com/source/include/uapi/linux/elfcore.h
type linuxPrPsInfo struct {
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

// LinuxPrStatusAMD64 is a copy of the prstatus kernel struct.
type linuxPrStatusAMD64 struct {
	Siginfo                      linuxSiginfo
	Cursig                       uint16
	_                            [2]uint8
	Sigpend                      uint64
	Sighold                      uint64
	Pid, Ppid, Pgrp, Sid         int32
	Utime, Stime, CUtime, CStime linuxCoreTimeval
	Reg                          linutil.AMD64PtraceRegs
	Fpvalid                      int32
}

// LinuxPrStatusARM64 is a copy of the prstatus kernel struct.
type linuxPrStatusARM64 struct {
	Siginfo                      linuxSiginfo
	Cursig                       uint16
	_                            [2]uint8
	Sigpend                      uint64
	Sighold                      uint64
	Pid, Ppid, Pgrp, Sid         int32
	Utime, Stime, CUtime, CStime linuxCoreTimeval
	Reg                          linutil.ARM64PtraceRegs
	Fpvalid                      int32
}

// LinuxPrStatusARM is a copy of the prstatus kernel struct.
type linuxPrStatusARM struct {
	Siginfo                      linuxSiginfo
	Cursig                       uint16
	_                            [2]uint8
	Sigpend                      uint64
	Sighold                      uint64
	Pid, Ppid, Pgrp, Sid         int32
	Utime, Stime, CUtime, CStime linuxCoreTimeval
	Reg                          linutil.ARMPtraceRegs
	Fpvalid                      int32
}

// LinuxSiginfo is a copy of the
// siginfo kernel struct.
type linuxSiginfo struct {
	Signo int32
	Code  int32
	Errno int32
}

// LinuxNTFile contains information on mapped files.
type linuxNTFile struct {
	linuxNTFileHdr
	entries []*linuxNTFileEntry
}

// LinuxNTFileHdr is a header struct for NTFile.
type linuxNTFileHdr struct {
	Count    uint64
	PageSize uint64
}

// LinuxNTFileEntry is an entry of an NT_FILE note.
type linuxNTFileEntry struct {
	Start   uint64
	End     uint64
	FileOfs uint64
}

// elfNotesHdr is the ELF Notes header.
// Same size on 64 and 32-bit machines.
type elfNotesHdr struct {
	Namesz uint32
	Descsz uint32
	Type   uint32
}
