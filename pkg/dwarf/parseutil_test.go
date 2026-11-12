package dwarf_test

import (
	"bytes"
	stddwarf "debug/dwarf"
	"reflect"
	"testing"

	"github.com/go-delve/delve/pkg/dwarf"
)

func TestReadUnitVersionsTruncatedInput(t *testing.T) {
	valid := []byte{7, 0, 0, 0, 4, 0, 0, 0, 0, 0, 8}
	for _, tail := range [][]byte{
		{1},
		{0xff, 0xff, 0xff, 0xff},
		{20, 0, 0, 0, 4, 0},
		{2, 0, 0, 0, 5, 0},
		{3, 0, 0, 0, 4, 0, 8},
	} {
		data := append(append([]byte(nil), valid...), tail...)
		got := dwarf.ReadUnitVersions(data)
		want := map[stddwarf.Offset]uint8{11: 4}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("truncated tail %x: got %v, want %v", tail, got, want)
		}
	}
}

func TestReadString(t *testing.T) {
	t.Parallel()
	bstr := bytes.NewBuffer([]byte{'h', 'i', 0x0, 0xFF, 0xCC})
	str, _ := dwarf.ReadString(bstr)

	if str != "hi" {
		t.Fatalf("String was not parsed correctly %#v", str)
	}
}
