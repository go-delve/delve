package line

import (
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/davecheney/profile"
)

func grabDebugLineSection(p string, t *testing.T) []byte {
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

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
	p, err := filepath.Abs("../../_fixtures/testnextprog")
	if err != nil {
		t.Fatal(err)
	}

	err = exec.Command("go", "build", "-gcflags=-N -l", "-o", p, p+".go").Run()
	if err != nil {
		t.Fatal("Could not compile test file", p, err)
	}
	defer os.Remove(p)
	data := grabDebugLineSection(p, t)
	dbl := Parse(data)
	prologue := dbl.Prologue

	if prologue.Version != uint16(2) {
		t.Fatal("Version not parsed correctly", prologue.Version)
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

	if !strings.Contains(dbl.FileNames[0].Name, "/dbg/_fixtures/testnextprog.go") {
		t.Fatal("First entry not parsed correctly")
	}
}

func BenchmarkLineParser(b *testing.B) {
	defer profile.Start(profile.MemProfile).Stop()
	p, err := filepath.Abs("../../_fixtures/testnextprog")
	if err != nil {
		b.Fatal(err)
	}

	data := grabDebugLineSection(p, nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Parse(data)
	}
}
