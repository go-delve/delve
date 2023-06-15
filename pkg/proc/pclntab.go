package proc

import (
	"bytes"
	"debug/buildinfo"
	"debug/elf"
	"debug/gosym"
	"encoding/binary"
	"fmt"
	"strings"
)

// From go/src/debug/gosym/pclntab.go
const (
	go12magic  = 0xfffffffb
	go116magic = 0xfffffffa
	go118magic = 0xfffffff0
	go120magic = 0xfffffff1
)

// Select the magic number based on the Go version
func magicNumber(goVersion string) []byte {
	bs := make([]byte, 4)
	var magic uint32
	if strings.Compare(goVersion, "go1.20") >= 0 {
		magic = go120magic
	} else if strings.Compare(goVersion, "go1.18") >= 0 {
		magic = go118magic
	} else if strings.Compare(goVersion, "go1.16") >= 0 {
		magic = go116magic
	} else {
		magic = go12magic
	}
	binary.LittleEndian.PutUint32(bs, magic)
	return bs
}

func readPcLnTableElf(exe *elf.File, path string) (*gosym.Table, error) {
	// Default section label is .gopclntab
	sectionLabel := ".gopclntab"

	section := exe.Section(sectionLabel)
	if section == nil {
		// binary may be built with -pie
		sectionLabel = ".data.rel.ro"
		section = exe.Section(sectionLabel)
		if section == nil {
			return nil, fmt.Errorf("could not read section .gopclntab")
		}
	}
	tableData, err := section.Data()
	if err != nil {
		return nil, fmt.Errorf("found section but could not read .gopclntab")
	}

	bi, err := buildinfo.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Find .gopclntab by magic number even if there is no section label
	magic := magicNumber(bi.GoVersion)
	pclntabIndex := bytes.Index(tableData, magic)
	if pclntabIndex >= 0 {
		tableData = tableData[pclntabIndex:]
	}
	addr := exe.Section(".text").Addr
	lineTable := gosym.NewLineTable(tableData, addr)
	symTable, err := gosym.NewTable([]byte{}, lineTable)
	if err != nil {
		return nil, fmt.Errorf("could not create symbol table from  %s ", path)
	}
	return symTable, nil
}
