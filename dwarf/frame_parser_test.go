package frame

import (
	"bytes"
	"testing"
)

func TestDecodeULEB128(t *testing.T) {
	var leb128 = bytes.NewBuffer([]byte{0xE5, 0x8E, 0x26})

	n, c := decodeULEB128(leb128)
	if n != 624485 {
		t.Fatal("Number was not decoded properly, got: ", n, c)
	}

	if c != 3 {
		t.Fatal("Count not returned correctly")
	}
}

func TestDecodeSLEB128(t *testing.T) {
	sleb128 := bytes.NewBuffer([]byte{0x9b, 0xf1, 0x59})

	n, c := decodeSLEB128(sleb128)
	if n != -624485 {
		t.Fatal("Number was not decoded properly, got: ", n, c)
	}
}

func TestParseString(t *testing.T) {
	bstr := bytes.NewBuffer([]byte{'h', 'i', 0x0, 0xFF, 0xCC})
	str, _ := parseString(bstr)

	if str != "hi" {
		t.Fatal("String was not parsed correctly")
	}
}

func TestParse(t *testing.T) {
	var (
		data = grabDebugFrameSection("../_fixtures/testprog", t)
		fe   = Parse(data)[0]
		ce   = fe.CIE
	)

	if ce.Length != 16 {
		t.Error("Length was not parsed correctly, got ", ce.Length)
	}

	if ce.Version != 0x3 {
		t.Fatalf("Version was not parsed correctly expected %#v got %#v", 0x3, ce.Version)
	}

	if ce.Augmentation != "" {
		t.Fatal("Augmentation was not parsed correctly")
	}

	if ce.CodeAlignmentFactor != 0x1 {
		t.Fatal("Code Alignment Factor was not parsed correctly")
	}

	if ce.DataAlignmentFactor != -4 {
		t.Fatalf("Data Alignment Factor was not parsed correctly got %#v", ce.DataAlignmentFactor)
	}

	if fe.Length != 32 {
		t.Fatal("Length was not parsed correctly, got ", fe.Length)
	}

}
