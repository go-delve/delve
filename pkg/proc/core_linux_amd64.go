package proc

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"

	"golang.org/x/debug/elf"
	"golang.org/x/sys/unix"
)

const NT_FILE elf.NType = 0x46494c45 // "FILE".

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
	MemoryReader
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
		if err != nil {
			// Assume that any error is because we ran out of notes.
			break
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
		return nil, err
	}
	note.Type = elf.NType(hdr.Type)

	name := make([]byte, hdr.Namesz)
	if _, err := r.Read(name); err != nil {
		return nil, fmt.Errorf("reading name: %v", err)
	}
	note.Name = string(name)
	// Name is padded to a multiple of 4 bytes. Seek if necessary.
	if len(name)%4 != 0 {
		r.Seek(4-int64(len(name)%4), io.SeekCurrent)
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
		if err := binary.Read(descReader, binary.LittleEndian, data.LinuxNTFileHdr); err != nil {
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

	return note, nil
}

func buildMemory(core *elf.File, exe io.ReaderAt, notes []*Note) MemoryReader {
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
	Utime, Stime, CUtime, CStime unix.Timeval
	Reg                          unix.PtraceRegs
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
