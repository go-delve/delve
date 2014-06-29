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

	f, l, _ := dbl.NextLocAfterPC(pc)

	if f != testfile+".go" {
		t.Fatal("File not returned correctly", f)
	}

	if l != 22 {
		t.Fatal("Line not returned correctly", l)
	}
}
