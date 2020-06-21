package loclist

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/go-delve/delve/pkg/dwarf/util"
)

func TestLoclist5(t *testing.T) {
	buf := new(bytes.Buffer)

	p32 := func(n uint32) { binary.Write(buf, binary.LittleEndian, n) }
	p16 := func(n uint16) { binary.Write(buf, binary.LittleEndian, n) }
	p8 := func(n uint8) { binary.Write(buf, binary.LittleEndian, n) }
	uleb := func(n uint64) { util.EncodeULEB128(buf, n) }

	p32(0x0) // length (use 0 because it is ignored)
	p16(0x5) // version
	p8(4)    // address size
	p8(0)    // segment selector size
	p32(0)   // offset_entry_count

	off := buf.Len()

	// (offset) start_base+0x10200 .. start_base+0x10300: 0x2
	p8(_DW_LLE_offset_pair)
	uleb(0x10200)
	uleb(0x10300)
	uleb(4)
	p32(2)

	// base address -> 0x02000000
	p8(_DW_LLE_base_address)
	p32(0x02000000)

	// (offset) 0x02010400 .. 0x02010500: 3
	p8(_DW_LLE_offset_pair)
	uleb(0x10400)
	uleb(0x10500)
	uleb(4)
	p32(3)

	// (offset) 0x02010600 .. 0x02010600: 4
	p8(_DW_LLE_offset_pair)
	uleb(0x10600)
	uleb(0x10600)
	uleb(4)
	p32(4)

	// (offset) 0x02010800 .. 0x02010900: 5
	p8(_DW_LLE_offset_pair)
	uleb(0x10800)
	uleb(0x10900)
	uleb(4)
	p32(5)

	// (start end) 0x2010a00 .. 0x2010b00: 6
	p8(_DW_LLE_start_end)
	p32(0x2010a00)
	p32(0x2010b00)
	uleb(4)
	p32(6)

	// (start length) 0x2010c00 .. 0x2010d00: 7
	p8(_DW_LLE_start_length)
	p32(0x2010c00)
	uleb(0x100)
	uleb(4)
	p32(7)

	// (offset) 0x02000000 .. 0x02000001: 8
	p8(_DW_LLE_offset_pair)
	uleb(0)
	uleb(1)
	uleb(4)
	p32(8)

	// default location 10
	p8(_DW_LLE_default_location)
	uleb(4)
	p32(10)

	// loclist end
	p8(_DW_LLE_end_of_list)

	type testCase struct {
		pc  uint64
		tgt Entry
	}

	testCases := []testCase{
		{0x01000000, Entry{0x01000000, 0x01000001, []byte{10, 0, 0, 0}}}, // default entry
		{0x01010200, Entry{0x01010200, 0x01010300, []byte{2, 0, 0, 0}}},  // offset pair entry
		{0x01010210, Entry{0x01010200, 0x01010300, []byte{2, 0, 0, 0}}},  // offset pair entry
		{0x01010300, Entry{0x01010300, 0x01010301, []byte{10, 0, 0, 0}}}, // default entry (one past offset pair entry)
		{0x02010400, Entry{0x02010400, 0x02010500, []byte{3, 0, 0, 0}}},  // offset pair entry, after base address selection
		{0x02010600, Entry{0x02010600, 0x02010601, []byte{10, 0, 0, 0}}}, // default entry (beginning of empty offset pair entry)
		{0x02010800, Entry{0x02010800, 0x02010900, []byte{5, 0, 0, 0}}},  // offset pair entry after empty offset pair
		{0x02010a00, Entry{0x02010a00, 0x02010b00, []byte{6, 0, 0, 0}}},  // start end entry
		{0x02010c00, Entry{0x02010c00, 0x02010d00, []byte{7, 0, 0, 0}}},  // start length entry
		{0x02000000, Entry{0x02000000, 0x02000001, []byte{8, 0, 0, 0}}},  // out of order offset pair entry
		{0x02000001, Entry{0x02000001, 0x02000002, []byte{10, 0, 0, 0}}}, // default entry (after out of order offset pair)
	}

	ll := NewDwarf5Reader(buf.Bytes())

	for _, tc := range testCases {
		e, err := ll.Find(off, 0x0, 0x01000000, tc.pc, nil)
		if err != nil {
			t.Errorf("error returned for %#x: %v", tc.pc, err)
			continue
		}
		if e.LowPC != tc.tgt.LowPC || e.HighPC != tc.tgt.HighPC || !bytes.Equal(e.Instr, tc.tgt.Instr) {
			t.Errorf("output mismatch for %#x,\nexpected %#v,\ngot     %#v", tc.pc, tc.tgt, e)
		}
	}
}
