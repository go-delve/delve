package frame

import (
	"bytes"
	"debug/elf"
	"os"
	"path/filepath"
	"testing"
)

func grabDebugFrameSection(fp string, t *testing.T) []byte {
	p, err := filepath.Abs("../fixtures/testprog")
	if err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}

	ef, err := elf.NewFile(f)
	if err != nil {
		t.Fatal(err)
	}

	data, err := ef.Section(".debug_frame").Data()
	if err != nil {
		t.Fatal(err)
	}

	return data
}

func TestDecodeLEB128(t *testing.T) {
	var leb128 = bytes.NewBuffer([]byte{0xE5, 0x8E, 0x26})

	n, c := DecodeLEB128(leb128)
	if n != 624485 {
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
	data := grabDebugFrameSection("../fixtures/testprog", t)
	ce := Parse(data)[0]

	if ce.Length != 16 {
		t.Fatal("Length was not parsed correctly")
	}

	if ce.CIE_id != 0xffffffff {
		t.Fatal("CIE id was not parsed correctly")
	}

	if ce.Version != 0x3 {
		t.Fatalf("Version was not parsed correctly expected %#v got %#v, data was %#v", 0x3, ce.Version, data[0:40])
	}

	if ce.Augmentation != "" {
		t.Fatal("Augmentation was not parsed correctly")
	}

	if ce.CodeAlignmentFactor != 0x1 {
		t.Fatal("Code Alignment Factor was not parsed correctly")
	}

	if ce.DataAlignmentFactor != 0x7c {
		t.Fatal("Data Alignment Factor was not parsed correctly")
	}

	fe := ce.FrameDescriptorEntries[0]

	if fe.Length != 32 {
		t.Fatal("Length was not parsed correctly")
	}

	if fe.CIE_pointer != ce {
		t.Fatal("Frame entry does not point to parent CIE")
	}

	if fe.InitialLocation != 0x400c00 {
		t.Fatalf("Initial location not parsed correctly, got %#v", fe.InitialLocation)
	}

	if fe.AddressRange != 0x2b {
		t.Fatalf("Address range not parsed correctly %#v\n%#v", fe.AddressRange, data[15:48])
	}
}
