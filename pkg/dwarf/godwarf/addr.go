package godwarf

import (
	"bytes"
	"encoding/binary"
	"errors"

	"github.com/go-delve/delve/pkg/dwarf"
)

// DebugAddrSection represents the debug_addr section of DWARFv5.
// See DWARFv5 section 7.27 page 241 and following.
type DebugAddrSection struct {
	byteOrder binary.ByteOrder
	ptrSz     int
	data      []byte
}

// ParseAddr parses the header of a debug_addr section.
func ParseAddr(data []byte) *DebugAddrSection {
	if len(data) == 0 {
		return nil
	}
	r := &DebugAddrSection{data: data}
	_, dwarf64, _, byteOrder := dwarf.ReadDwarfLengthVersion(data)
	r.byteOrder = byteOrder
	data = data[6:]
	if dwarf64 {
		data = data[8:]
	}

	addrSz := data[0]
	segSelSz := data[1]
	r.ptrSz = int(addrSz + segSelSz)

	return r
}

// GetSubsection returns the subsection of debug_addr starting at addrBase
func (addr *DebugAddrSection) GetSubsection(addrBase uint64) *DebugAddr {
	if addr == nil {
		return nil
	}
	return &DebugAddr{DebugAddrSection: addr, addrBase: addrBase}
}

// DebugAddr represents a subsection of the debug_addr section with a specific base address
type DebugAddr struct {
	*DebugAddrSection
	addrBase uint64
}

// Get returns the address at index idx starting from addrBase.
func (addr *DebugAddr) Get(idx uint64) (uint64, error) {
	if addr == nil || addr.DebugAddrSection == nil {
		return 0, errors.New("debug_addr section not present")
	}
	off := idx*uint64(addr.ptrSz) + addr.addrBase
	return dwarf.ReadUintRaw(bytes.NewReader(addr.data[off:]), addr.byteOrder, addr.ptrSz)
}
