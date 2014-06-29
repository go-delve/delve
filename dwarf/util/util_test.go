package util

import (
	"bytes"
	"testing"
)

func TestDecodeULEB128(t *testing.T) {
	var leb128 = bytes.NewBuffer([]byte{0xE5, 0x8E, 0x26})

	n, c := DecodeULEB128(leb128)
	if n != 624485 {
		t.Fatal("Number was not decoded properly, got: ", n, c)
	}

	if c != 3 {
		t.Fatal("Count not returned correctly")
	}
}

func TestDecodeSLEB128(t *testing.T) {
	sleb128 := bytes.NewBuffer([]byte{0x9b, 0xf1, 0x59})

	n, c := DecodeSLEB128(sleb128)
	if n != -624485 {
		t.Fatal("Number was not decoded properly, got: ", n, c)
	}
}

func TestParseString(t *testing.T) {
	bstr := bytes.NewBuffer([]byte{'h', 'i', 0x0, 0xFF, 0xCC})
	str, _ := ParseString(bstr)

	if str != "hi" {
		t.Fatal("String was not parsed correctly")
	}
}
