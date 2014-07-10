package line

import (
	"debug/elf"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func grabDebugLineSection(fp string, t *testing.T) []byte {
	p, err := filepath.Abs(fp)
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

	data, err := ef.Section(".debug_line").Data()
	if err != nil {
		t.Fatal(err)
	}

	return data
}

func TestDebugLinePrologueParser(t *testing.T) {
	// Test against known good values, from readelf --debug-dump=rawline _fixtures/testnextprog
	var (
		data     = grabDebugLineSection("../../_fixtures/testnextprog", t)
		dbl      = Parse(data)
		prologue = dbl.Prologue
	)

	if prologue.Length != uint32(59807) {
		t.Fatal("Length was not parsed correctly", prologue.Length)
	}

	if prologue.Version != uint16(2) {
		t.Fatal("Version not parsed correctly", prologue.Version)
	}

	if prologue.PrologueLength != uint32(5224) {
		t.Fatal("Prologue Length not parsed correctly", prologue.PrologueLength)
	}

	if prologue.MinInstrLength != uint8(1) {
		t.Fatal("Minimun Instruction Length not parsed correctly", prologue.MinInstrLength)
	}

	if prologue.InitialIsStmt != uint8(1) {
		t.Fatal("Initial value of 'is_stmt' not parsed correctly", prologue.InitialIsStmt)
	}

	if prologue.LineBase != int8(-1) {
		t.Fatal("Line base not parsed correctly", prologue.LineBase)
	}

	if prologue.LineRange != uint8(4) {
		t.Fatal("Line Range not parsed correctly", prologue.LineRange)
	}

	if prologue.OpcodeBase != uint8(10) {
		t.Fatal("Opcode Base not parsed correctly", prologue.OpcodeBase)
	}

	lengths := []uint8{0, 1, 1, 1, 1, 0, 0, 0, 1}
	for i, l := range prologue.StdOpLengths {
		if l != lengths[i] {
			t.Fatal("Length not parsed correctly", l)
		}
	}

	if len(dbl.IncludeDirs) != 0 {
		t.Fatal("Include dirs not parsed correctly")
	}

	if len(dbl.FileNames) != 123 {
		t.Fatal("Filenames not parsed correctly", len(dbl.FileNames))
	}

	if !strings.Contains(dbl.FileNames[0].Name, "/dbg/_fixtures/testnextprog.go") {
		t.Fatal("First entry not parsed correctly")
	}
}
