package loclist

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/go-delve/delve/pkg/dwarf/godwarf"
	"github.com/go-delve/delve/pkg/dwarf/util"
)

// Dwarf5Reader parses and presents DWARF loclist information for DWARF version 5 and later.
// See DWARFv5 section 7.29 page 243 and following.
type Dwarf5Reader struct {
	byteOrder binary.ByteOrder
	ptrSz     int
	data      []byte
}

func NewDwarf5Reader(data []byte) *Dwarf5Reader {
	if len(data) == 0 {
		return nil
	}
	r := &Dwarf5Reader{data: data}

	_, dwarf64, _, byteOrder := util.ReadDwarfLengthVersion(data)
	r.byteOrder = byteOrder

	data = data[6:]
	if dwarf64 {
		data = data[8:]
	}

	addrSz := data[0]
	segSelSz := data[1]
	r.ptrSz = int(addrSz + segSelSz)

	// Not read:
	// - offset_entry_count (4 bytes)
	// - offset table (offset_entry_count*4 or offset_entry_count*8 if dwarf64 is set)

	return r
}

func (rdr *Dwarf5Reader) Empty() bool {
	return rdr == nil
}

// Find returns the loclist entry for the specified PC address, inside the
// loclist stating at off. Base is the base address of the compile unit and
// staticBase is the static base at which the image is loaded.
func (rdr *Dwarf5Reader) Find(off int, staticBase, base, pc uint64, debugAddr *godwarf.DebugAddr) (*Entry, error) {
	it := &loclistsIterator{rdr: rdr, debugAddr: debugAddr, buf: bytes.NewBuffer(rdr.data), base: base, staticBase: staticBase}
	it.buf.Next(off)

	for it.next() {
		if !it.onRange {
			continue
		}
		if it.start <= pc && pc < it.end {
			return &Entry{it.start, it.end, it.instr}, nil
		}
	}

	if it.err != nil {
		return nil, it.err
	}

	if it.defaultInstr != nil {
		return &Entry{pc, pc + 1, it.defaultInstr}, nil
	}

	return nil, nil
}

type loclistsIterator struct {
	rdr        *Dwarf5Reader
	debugAddr  *godwarf.DebugAddr
	buf        *bytes.Buffer
	staticBase uint64
	base       uint64 // base for offsets in the list

	onRange      bool
	atEnd        bool
	start, end   uint64
	instr        []byte
	defaultInstr []byte
	err          error
}

const (
	_DW_LLE_end_of_list      uint8 = 0x0
	_DW_LLE_base_addressx    uint8 = 0x1
	_DW_LLE_startx_endx      uint8 = 0x2
	_DW_LLE_startx_length    uint8 = 0x3
	_DW_LLE_offset_pair      uint8 = 0x4
	_DW_LLE_default_location uint8 = 0x5
	_DW_LLE_base_address     uint8 = 0x6
	_DW_LLE_start_end        uint8 = 0x7
	_DW_LLE_start_length     uint8 = 0x8
)

func (it *loclistsIterator) next() bool {
	if it.err != nil || it.atEnd {
		return false
	}
	opcode, err := it.buf.ReadByte()
	if err != nil {
		it.err = err
		return false
	}
	switch opcode {
	case _DW_LLE_end_of_list:
		it.atEnd = true
		it.onRange = false
		return false

	case _DW_LLE_base_addressx:
		baseIdx, _ := util.DecodeULEB128(it.buf)
		if err != nil {
			it.err = err
			return false
		}
		it.base, it.err = it.debugAddr.Get(baseIdx)
		it.base += it.staticBase
		it.onRange = false

	case _DW_LLE_startx_endx:
		startIdx, _ := util.DecodeULEB128(it.buf)
		endIdx, _ := util.DecodeULEB128(it.buf)
		it.readInstr()

		it.start, it.err = it.debugAddr.Get(startIdx)
		if it.err == nil {
			it.end, it.err = it.debugAddr.Get(endIdx)
		}
		it.onRange = true

	case _DW_LLE_startx_length:
		startIdx, _ := util.DecodeULEB128(it.buf)
		length, _ := util.DecodeULEB128(it.buf)
		it.readInstr()

		it.start, it.err = it.debugAddr.Get(startIdx)
		it.end = it.start + length
		it.onRange = true

	case _DW_LLE_offset_pair:
		off1, _ := util.DecodeULEB128(it.buf)
		off2, _ := util.DecodeULEB128(it.buf)
		it.readInstr()

		it.start = it.base + off1
		it.end = it.base + off2
		it.onRange = true

	case _DW_LLE_default_location:
		it.readInstr()
		it.defaultInstr = it.instr
		it.onRange = false

	case _DW_LLE_base_address:
		it.base, it.err = util.ReadUintRaw(it.buf, it.rdr.byteOrder, it.rdr.ptrSz)
		it.base += it.staticBase
		it.onRange = false

	case _DW_LLE_start_end:
		it.start, it.err = util.ReadUintRaw(it.buf, it.rdr.byteOrder, it.rdr.ptrSz)
		it.end, it.err = util.ReadUintRaw(it.buf, it.rdr.byteOrder, it.rdr.ptrSz)
		it.readInstr()
		it.onRange = true

	case _DW_LLE_start_length:
		it.start, it.err = util.ReadUintRaw(it.buf, it.rdr.byteOrder, it.rdr.ptrSz)
		length, _ := util.DecodeULEB128(it.buf)
		it.readInstr()
		it.end = it.start + length
		it.onRange = true

	default:
		it.err = fmt.Errorf("unknown opcode %#x at %#x", opcode, len(it.rdr.data)-it.buf.Len())
		it.onRange = false
		it.atEnd = true
		return false
	}

	return true
}

func (it *loclistsIterator) readInstr() {
	length, _ := util.DecodeULEB128(it.buf)
	it.instr = it.buf.Next(int(length))
}
