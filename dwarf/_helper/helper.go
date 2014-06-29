package dwarfhelper

import (
	"debug/elf"
	"debug/gosym"
	"os"
	"testing"
)

func GosymData(testfile string, t *testing.T) *gosym.Table {
	f, err := os.Open(testfile)
	if err != nil {
		t.Fatal(err)
	}

	e, err := elf.NewFile(f)
	if err != nil {
		t.Fatal(err)
	}

	return parseGoSym(t, e)
}

func parseGoSym(t *testing.T, exe *elf.File) *gosym.Table {
	symdat, err := exe.Section(".gosymtab").Data()
	if err != nil {
		t.Fatal(err)
	}

	pclndat, err := exe.Section(".gopclntab").Data()
	if err != nil {
		t.Fatal(err)
	}

	pcln := gosym.NewLineTable(pclndat, exe.Section(".text").Addr)
	tab, err := gosym.NewTable(symdat, pcln)
	if err != nil {
		t.Fatal(err)
	}

	return tab
}
