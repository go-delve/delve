package linutil

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

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
	return 0, fmt.Errorf("not supprted ptr size %d", ptrSize)
}

// dynamicSearchDebug searches for the DT_DEBUG entry in the .dynamic section
func dynamicSearchDebug(p proc.Process) (uint64, error) {
	bi := p.BinInfo()
	mem := p.CurrentThread()

	dynbuf := make([]byte, bi.ElfDynamicSection.Size)
	_, err := mem.ReadMemory(dynbuf, uintptr(bi.ElfDynamicSection.Addr))
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
	_, err := p.CurrentThread().ReadMemory(ptrbuf, uintptr(addr))
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
	mem := p.CurrentThread()
	buf := make([]byte, 1)
	r := []byte{}
	for {
		if len(r) > maxLibraryPathLength {
			return "", fmt.Errorf("error reading libraries: string too long (%d)", len(r))
		}
		_, err := mem.ReadMemory(buf, uintptr(addr))
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

// ElfUpdateSharedObjects reads the list of dynamic libraries loaded by the
// dynamic linker from the .dynamic section and uses it to update p.BinInfo().
// See the SysV ABI for a description of how the .dynamic section works:
// http://www.sco.com/developers/gabi/latest/contents.html
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
		bi.AddImage(lm.name, lm.addr)
		libs = append(libs, lm.name)
		r_map = lm.next
	}

	return nil
}
