package util

import (
	"bytes"
	"testing"
)

func TestParseString(t *testing.T) {
	bstr := bytes.NewBuffer([]byte{'h', 'i', 0x0, 0xFF, 0xCC})
	str, _ := ParseString(bstr)

	if str != "hi" {
		t.Fatalf("String was not parsed correctly %#v", str)
	}
}
