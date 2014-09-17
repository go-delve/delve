package line

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
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
		data = grabDebugLineSection(p, t)
		dbl  = Parse(data)
	)

	loc := dbl.NextLocation(testfile+".go", 20)

	if loc.File != testfile+".go" {
		t.Fatal("File not returned correctly", loc.File)
	}

	if loc.Line != 22 {
		t.Fatal("Line not returned correctly", loc.Line)
	}
}
