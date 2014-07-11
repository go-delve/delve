package line

import (
	"path/filepath"
	"testing"

	"github.com/derekparker/dbg/dwarf/_helper"
)

var testfile string

func init() {
	testfile, _ = filepath.Abs("../../_fixtures/testnextprog")
}

func TestNextLocAfterPC(t *testing.T) {
	var (
		data     = grabDebugLineSection("../../_fixtures/testnextprog", t)
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
