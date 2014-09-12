package line

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/derekparker/dbg/dwarf/_helper"
)

var testfile string

func init() {
	testfile, _ = filepath.Abs("../../_fixtures/testnextprog")
}

func TestNextLocAfterPC(t *testing.T) {
	p, err := filepath.Abs("../../_fixtures/testnextprog")
	if err != nil {
		t.Fatal(err)
	}

	err = exec.Command("go", "build", "-gcflags=-N -l", "-o", p, p+".go").Run()
	if err != nil {
		t.Fatal("Could not compile test file", p, err)
	}
	defer os.Remove(p)

	var (
		data     = grabDebugLineSection(p, t)
		dbl      = Parse(data)
		gosym    = dwarfhelper.GosymData(testfile, t)
		pc, _, _ = gosym.LineToPC(testfile+".go", 20)
	)

	loc := dbl.NextLocAfterPC(pc)

	if loc.File != testfile+".go" {
		t.Fatal("File not returned correctly", loc.File)
	}

	if loc.Line != 22 {
		t.Fatal("Line not returned correctly", loc.Line)
	}
}
