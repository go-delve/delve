package dwarf_test

import (
	"bytes"
	"testing"

	"github.com/go-delve/delve/pkg/dwarf"
)

func TestReadString(t *testing.T) {
	bstr := bytes.NewBuffer([]byte{'h', 'i', 0x0, 0xFF, 0xCC})
	str, _ := dwarf.ReadString(bstr)

	if str != "hi" {
		t.Fatalf("String was not parsed correctly %#v", str)
	}
}
