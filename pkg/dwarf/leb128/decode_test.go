package leb128

import (
	"bytes"
	"testing"
)

func TestDecodeUnsigned(t *testing.T) {
	leb128 := bytes.NewBuffer([]byte{0xE5, 0x8E, 0x26})

	n, c := DecodeUnsigned(leb128)
	if n != 624485 {
		t.Fatal("Number was not decoded properly, got: ", n, c)
	}

	if c != 3 {
		t.Fatal("Count not returned correctly")
	}
}

func TestDecodeSigned(t *testing.T) {
	sleb128 := bytes.NewBuffer([]byte{0x9b, 0xf1, 0x59})

	n, c := DecodeSigned(sleb128)
	if n != -624485 {
		t.Fatal("Number was not decoded properly, got: ", n, c)
	}
}
