package proc

import (
	"debug/elf"
	"debug/gosym"
	"debug/macho"
	"fmt"
)

func readPcLnTableElf(exe *elf.File, path string) (*gosym.Table, error) {
	// Default section label is .gopclntab
	sectionLabel := ".gopclntab"

	section := exe.Section(sectionLabel)
	if section == nil {
		// binary may be built with -pie
		sectionLabel = ".data.rel.ro.gopclntab"
		section = exe.Section(sectionLabel)
		if section == nil {
			return nil, fmt.Errorf("could not read section .gopclntab")
		}
	}
	tableData, err := section.Data()
	if err != nil {
		return nil, fmt.Errorf("found section but could not read .gopclntab")
	}

	addr := exe.Section(".text").Addr
	lineTable := gosym.NewLineTable(tableData, addr)
	symTable, err := gosym.NewTable([]byte{}, lineTable)
	if err != nil {
		return nil, fmt.Errorf("could not create symbol table from  %s ", path)
	}
	return symTable, nil
}

func readPcLnTableMacho(exe *macho.File, path string) (*gosym.Table, error) {
	// Default section label is __gopclntab
	sectionLabel := "__gopclntab"

	section := exe.Section(sectionLabel)
	if section == nil {
		return nil, fmt.Errorf("could not read section __gopclntab")
	}
	tableData, err := section.Data()
	if err != nil {
		return nil, fmt.Errorf("found section but could not read __gopclntab")
	}

	addr := exe.Section("__text").Addr
	lineTable := gosym.NewLineTable(tableData, addr)
	symTable, err := gosym.NewTable([]byte{}, lineTable)
	if err != nil {
		return nil, fmt.Errorf("could not create symbol table from  %s ", path)
	}
	return symTable, nil
}
