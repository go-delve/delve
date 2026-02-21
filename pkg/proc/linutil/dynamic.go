package linutil

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/go-delve/delve/pkg/proc"
)

const (
	maxNumLibraries      = 1000000 // maximum number of loaded libraries, to avoid loading forever on corrupted memory
	maxLibraryPathLength = 1000000 // maximum length for the path of a library, to avoid loading forever on corrupted memory
)

var ErrTooManyLibraries = errors.New("number of loaded libraries exceeds maximum")

const (
	_DT_NULL  = 0  // DT_NULL as defined by SysV ABI specification
	_DT_DEBUG = 21 // DT_DEBUG as defined by SysV ABI specification
)

// readUintRaw reads an integer of ptrSize bytes, with the specified byte order, from reader.
func readUintRaw(reader io.Reader, order binary.ByteOrder, ptrSize int) (uint64, error) {
	switch ptrSize {
	case 4:
		var n uint32
		if err := binary.Read(reader, order, &n); err != nil {
			return 0, err
		}
		return uint64(n), nil
	case 8:
		var n uint64
		if err := binary.Read(reader, order, &n); err != nil {
			return 0, err
		}
		return n, nil
	}
	return 0, fmt.Errorf("not supported ptr size %d", ptrSize)
}

// dynamicSearchDebug searches for the DT_DEBUG entry in the .dynamic section
func dynamicSearchDebug(p proc.Process) (uint64, error) {
	bi := p.BinInfo()
	mem := p.Memory()

	dynbuf := make([]byte, bi.ElfDynamicSection.Size)
	_, err := mem.ReadMemory(dynbuf, bi.ElfDynamicSection.Addr)
	if err != nil {
		return 0, err
	}

	rd := bytes.NewReader(dynbuf)

	for {
		var tag, val uint64
		if tag, err = readUintRaw(rd, binary.LittleEndian, p.BinInfo().Arch.PtrSize()); err != nil {
			return 0, err
		}
		if val, err = readUintRaw(rd, binary.LittleEndian, p.BinInfo().Arch.PtrSize()); err != nil {
			return 0, err
		}
		switch tag {
		case _DT_NULL:
			return 0, nil
		case _DT_DEBUG:
			return val, nil
		}
	}
}

func readPtr(p proc.Process, addr uint64) (uint64, error) {
	ptrbuf := make([]byte, p.BinInfo().Arch.PtrSize())
	_, err := p.Memory().ReadMemory(ptrbuf, addr)
	if err != nil {
		return 0, err
	}
	return readUintRaw(bytes.NewReader(ptrbuf), binary.LittleEndian, p.BinInfo().Arch.PtrSize())
}

type linkMap struct {
	addr       uint64
	name       string
	ld         uint64
	next, prev uint64
}

func readLinkMapNode(p proc.Process, r_map uint64) (*linkMap, error) {
	bi := p.BinInfo()

	var lm linkMap
	var ptrs [5]uint64
	for i := range ptrs {
		var err error
		ptrs[i], err = readPtr(p, r_map+uint64(bi.Arch.PtrSize()*i))
		if err != nil {
			return nil, err
		}
	}
	lm.addr = ptrs[0]
	var err error
	lm.name, err = readCString(p, ptrs[1])
	if err != nil {
		return nil, err
	}
	lm.ld = ptrs[2]
	lm.next = ptrs[3]
	lm.prev = ptrs[4]
	return &lm, nil
}

func readCString(p proc.Process, addr uint64) (string, error) {
	if addr == 0 {
		return "", nil
	}
	mem := p.Memory()
	buf := make([]byte, 1)
	r := []byte{}
	for {
		if len(r) > maxLibraryPathLength {
			return "", fmt.Errorf("error reading libraries: string too long (%d)", len(r))
		}
		_, err := mem.ReadMemory(buf, addr)
		if err != nil {
			return "", err
		}
		if buf[0] == 0 {
			break
		}
		r = append(r, buf[0])
		addr++
	}
	return string(r), nil
}

// ElfFindRBrk returns the address of the dynamic linker's r_brk notification
// function from the r_debug struct. The dynamic linker calls this function
// whenever shared objects are loaded or unloaded. Returns 0 if r_brk cannot
// be found.
func ElfFindRBrk(p proc.Process) (uint64, error) {
	bi := p.BinInfo()
	if bi.ElfDynamicSection.Addr == 0 {
		return 0, nil
	}
	debugAddr, err := dynamicSearchDebug(p)
	if err != nil {
		return 0, err
	}
	if debugAddr == 0 {
		return 0, nil
	}

	// r_brk is at offset 2*ptrSize in the r_debug struct:
	//   int r_version;           // offset 0
	//   struct link_map *r_map;  // offset ptrSize (aligned)
	//   ElfW(Addr) r_brk;        // offset 2*ptrSize
	rBrkOffset := uint64(2 * bi.Arch.PtrSize())
	return readPtr(p, debugAddr+rBrkOffset)
}

// ElfFindRBrkFromInterp finds the address of the dynamic linker's
// _dl_debug_state function by reading the ELF interpreter from disk.
// This is used at launch time when the dynamic linker hasn't initialized
// r_debug yet (DT_DEBUG is 0). The function reads the .interp section of
// the executable to find the interpreter path, then finds _dl_debug_state
// in the interpreter's dynamic symbol table.
// execPath is the path to the executable, and auxvBase is the base address
// of the interpreter in memory (AT_BASE from the auxiliary vector).
func ElfFindRBrkFromInterp(execPath string, auxvBase uint64) (uint64, error) {
	if auxvBase == 0 {
		return 0, nil
	}

	execFile, err := elf.Open(execPath)
	if err != nil {
		return 0, fmt.Errorf("could not open executable: %v", err)
	}
	defer execFile.Close()

	interpSec := execFile.Section(".interp")
	if interpSec == nil {
		return 0, nil
	}
	interpData, err := interpSec.Data()
	if err != nil {
		return 0, fmt.Errorf("could not read .interp section: %v", err)
	}
	interpPath := string(bytes.TrimRight(interpData, "\x00"))

	interpFile, err := elf.Open(interpPath)
	if err != nil {
		realPath, rerr := os.Readlink(interpPath)
		if rerr != nil {
			return 0, fmt.Errorf("could not open interpreter %s: %v", interpPath, err)
		}
		interpFile, err = elf.Open(realPath)
		if err != nil {
			return 0, fmt.Errorf("could not open interpreter %s: %v", realPath, err)
		}
	}
	defer interpFile.Close()

	dynsyms, err := interpFile.DynamicSymbols()
	if err != nil {
		return 0, fmt.Errorf("could not read dynamic symbols: %v", err)
	}
	for _, sym := range dynsyms {
		if sym.Name == "_dl_debug_state" {
			return auxvBase + sym.Value, nil
		}
	}

	return 0, nil
}

// ElfUpdateSharedObjects reads the list of dynamic libraries loaded by the
// dynamic linker from the .dynamic section and uses it to update p.BinInfo().
// See the SysV ABI for a description of how the .dynamic section works:
// https://www.sco.com/developers/gabi/latest/contents.html
func ElfUpdateSharedObjects(p proc.Process) error {
	bi := p.BinInfo()
	if bi.ElfDynamicSection.Addr == 0 {
		// no dynamic section, therefore nothing to do here
		return nil
	}
	debugAddr, err := dynamicSearchDebug(p)
	if err != nil {
		return err
	}
	if debugAddr == 0 {
		// no DT_DEBUG entry
		return nil
	}

	// Offsets of the fields of the r_debug and link_map structs,
	// see /usr/include/elf/link.h for a full description of those structs.
	debugMapOffset := uint64(p.BinInfo().Arch.PtrSize())

	r_map, err := readPtr(p, debugAddr+debugMapOffset)
	if err != nil {
		return err
	}

	libs := []string{}

	first := true

	for {
		if r_map == 0 {
			break
		}
		if len(libs) > maxNumLibraries {
			return ErrTooManyLibraries
		}
		lm, err := readLinkMapNode(p, r_map)
		if err != nil {
			return err
		}
		if !first || lm.addr != 0 {
			// First entry is the executable, we don't need to add it, and doing so
			// can cause duplicate entries due to base address mismatches.
			bi.AddImage(lm.name, lm.addr)
		}
		libs = append(libs, lm.name)
		first = false
		r_map = lm.next
	}

	return nil
}
