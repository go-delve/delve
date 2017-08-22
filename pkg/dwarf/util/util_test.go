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

func TestEncodeULEB128(t *testing.T) {
	tc := []uint64{0x00, 0x7f, 0x80, 0x8f, 0xffff, 0xfffffff7}
	for i := range tc {
		var buf bytes.Buffer
		EncodeULEB128(&buf, tc[i])
		enc := append([]byte{}, buf.Bytes()...)
		buf.Write([]byte{0x1, 0x2, 0x3})
		out, c := DecodeULEB128(&buf)
		t.Logf("input %x output %x encoded %x", tc[i], out, enc)
		if c != uint32(len(enc)) {
			t.Errorf("wrong encode")
		}
		if out != tc[i] {
			t.Errorf("wrong encode")
		}
	}
}

func TestEncodeSLEB128(t *testing.T) {
	tc := []int64{2, -2, 127, -127, 128, -128, 129, -129}
	for i := range tc {
		var buf bytes.Buffer
		EncodeSLEB128(&buf, tc[i])
		enc := append([]byte{}, buf.Bytes()...)
		buf.Write([]byte{0x1, 0x2, 0x3})
		out, c := DecodeSLEB128(&buf)
		t.Logf("input %x output %x encoded %x", tc[i], out, enc)
		if c != uint32(len(enc)) {
			t.Errorf("wrong encode")
		}
		if out != tc[i] {
			t.Errorf("wrong encode")
		}
	}
}

func TestParseString(t *testing.T) {
	bstr := bytes.NewBuffer([]byte{'h', 'i', 0x0, 0xFF, 0xCC})
	str, _ := ParseString(bstr)

	if str != "hi" {
		t.Fatalf("String was not parsed correctly %#v", str)
	}
}
