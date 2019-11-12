package loclist

import (
	"encoding/binary"
)

// Reader parses and presents DWARF loclist information.
type Reader struct {
	data  []byte
	cur   int
	ptrSz int
}

// New returns an initialized loclist Reader.
func New(data []byte, ptrSz int) *Reader {
	return &Reader{data: data, ptrSz: ptrSz}
}

// Empty returns true if this reader has no data.
func (rdr *Reader) Empty() bool {
	return rdr.data == nil
}

// Seek moves the data pointer to the specified offset.
func (rdr *Reader) Seek(off int) {
	rdr.cur = off
}

// Next advances the reader to the next loclist entry, returning
// the entry and true if successful, or nil, false if not.
func (rdr *Reader) Next(e *Entry) bool {
	e.LowPC = rdr.oneAddr()
	e.HighPC = rdr.oneAddr()

	if e.LowPC == 0 && e.HighPC == 0 {
		return false
	}

	if e.BaseAddressSelection() {
		e.Instr = nil
		return true
	}

	instrlen := binary.LittleEndian.Uint16(rdr.read(2))
	e.Instr = rdr.read(int(instrlen))
	return true
}

func (rdr *Reader) read(sz int) []byte {
	r := rdr.data[rdr.cur : rdr.cur+sz]
	rdr.cur += sz
	return r
}

func (rdr *Reader) oneAddr() uint64 {
	switch rdr.ptrSz {
	case 4:
		addr := binary.LittleEndian.Uint32(rdr.read(rdr.ptrSz))
		if addr == ^uint32(0) {
			return ^uint64(0)
		}
		return uint64(addr)
	case 8:
		addr := uint64(binary.LittleEndian.Uint64(rdr.read(rdr.ptrSz)))
		return addr
	default:
		panic("bad address size")
	}
}

// Entry represents a single entry in the loclist section.
type Entry struct {
	LowPC, HighPC uint64
	Instr         []byte
}

// BaseAddressSelection returns true if entry.highpc should
// be used as the base address for subsequent entries.
func (e *Entry) BaseAddressSelection() bool {
	return e.LowPC == ^uint64(0)
}
