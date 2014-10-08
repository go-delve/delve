package line

import (
	"debug/elf"
	"debug/gosym"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func parseGoSymTab(exe *elf.File, t *testing.T) *gosym.Table {
	var (
		symdat  []byte
		pclndat []byte
		err     error
	)

	if sec := exe.Section(".gosymtab"); sec != nil {
		symdat, err = sec.Data()
		if err != nil {
			fmt.Println("could not get .gosymtab section", err)
			os.Exit(1)
		}
	}

	if sec := exe.Section(".gopclntab"); sec != nil {
		pclndat, err = sec.Data()
		if err != nil {
			fmt.Println("could not get .gopclntab section", err)
			os.Exit(1)
		}
	}

	pcln := gosym.NewLineTable(pclndat, exe.Section(".text").Addr)
	tab, err := gosym.NewTable(symdat, pcln)
	if err != nil {
		fmt.Println("could not get initialize line table", err)
		os.Exit(1)
	}

	return tab
}

func TestNextLocAfterPC(t *testing.T) {
	testfile, _ := filepath.Abs("../../_fixtures/testnextprog")

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

	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	e, err := elf.NewFile(f)
	if err != nil {
		t.Fatal(err)
	}
	symtab := parseGoSymTab(e, t)
	pc, _, err := symtab.LineToPC(testfile+".go", 23)
	if err != nil {
		t.Fatal(err)
	}
	loc := dbl.NextLocation(pc, testfile+".go", 23)

	if loc.File != testfile+".go" {
		t.Fatal("File not returned correctly", loc.File)
	}

	if loc.Line != 24 {
		t.Fatal("Line not returned correctly", loc.Line)
	}
}
