package dwarfhelper

import (
	"debug/elf"
	"debug/gosym"
	"os"
	"path/filepath"
	"testing"
)

func GosymData(testfile string, t testing.TB) *gosym.Table {
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

func GrabDebugFrameSection(fp string, t testing.TB) []byte {
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

	data, err := ef.Section(".debug_frame").Data()
	if err != nil {
		t.Fatal(err)
	}

	return data
}

func parseGoSym(t testing.TB, exe *elf.File) *gosym.Table {
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
