// This file is part of GoRE.
//
// Copyright (C) 2019-2021 GoRE Authors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <http://www.gnu.org/licenses/>.

package gore

import (
	"debug/elf"
	"debug/gosym"
	"fmt"
	"os"
)

func openELF(fp string) (*elfFile, error) {
	osFile, err := os.Open(fp)
	if err != nil {
		return nil, fmt.Errorf("error when opening the ELF file: %w", err)
	}

	f, err := elf.NewFile(osFile)
	if err != nil {
		return nil, fmt.Errorf("error when parsing the ELF file: %w", err)
	}
	return &elfFile{file: f, osFile: osFile}, nil
}

type elfFile struct {
	file   *elf.File
	osFile *os.File
}

func (e *elfFile) getFile() *os.File {
	return e.osFile
}

func (e *elfFile) getPCLNTab() (*gosym.Table, error) {
	pclnSection := e.file.Section(".gopclntab")
	if pclnSection == nil {
		// No section found. Check if the PIE section exist instead.
		pclnSection = e.file.Section(".data.rel.ro.gopclntab")
	}
	if pclnSection == nil {
		return nil, fmt.Errorf("no gopclntab section found")
	}

	pclndat, err := pclnSection.Data()
	if err != nil {
		return nil, fmt.Errorf("could not get the data for the pclntab: %w", err)
	}

	pcln := gosym.NewLineTable(pclndat, e.file.Section(".text").Addr)
	return gosym.NewTable(make([]byte, 0), pcln)
}

func (e *elfFile) Close() error {
	err := e.file.Close()
	if err != nil {
		return err
	}
	return e.osFile.Close()
}

func (e *elfFile) getRData() ([]byte, error) {
	section := e.file.Section(".rodata")
	if section == nil {
		return nil, ErrSectionDoesNotExist
	}
	return section.Data()
}

func (e *elfFile) getCodeSection() ([]byte, error) {
	section := e.file.Section(".text")
	if section == nil {
		return nil, ErrSectionDoesNotExist
	}
	return section.Data()
}

func (e *elfFile) getPCLNTABData() (uint64, []byte, error) {
	start, data, err := e.getSectionData(".gopclntab")
	if err == ErrSectionDoesNotExist {
		// Try PIE location
		return e.getSectionData(".data.rel.ro.gopclntab")
	}
	return start, data, err
}

func (e *elfFile) moduledataSection() string {
	return ".noptrdata"
}

func (e *elfFile) getSectionDataFromOffset(off uint64) (uint64, []byte, error) {
	for _, section := range e.file.Sections {
		if section.Offset == 0 {
			// Only exist in memory
			continue
		}

		if section.Addr <= off && off < (section.Addr+section.Size) {
			data, err := section.Data()
			return section.Addr, data, err
		}
	}
	return 0, nil, ErrSectionDoesNotExist
}

func (e *elfFile) getSectionData(name string) (uint64, []byte, error) {
	section := e.file.Section(name)
	if section == nil {
		return 0, nil, ErrSectionDoesNotExist
	}
	data, err := section.Data()
	return section.Addr, data, err
}

func (e *elfFile) getFileInfo() *FileInfo {
	var wordSize int
	class := e.file.FileHeader.Class
	if class == elf.ELFCLASS32 {
		wordSize = intSize32
	}
	if class == elf.ELFCLASS64 {
		wordSize = intSize64
	}

	var arch string
	switch e.file.Machine {
	case elf.EM_386:
		arch = Arch386
	case elf.EM_MIPS:
		arch = ArchMIPS
	case elf.EM_X86_64:
		arch = ArchAMD64
	case elf.EM_ARM:
		arch = ArchARM
	}

	return &FileInfo{
		ByteOrder: e.file.FileHeader.ByteOrder,
		OS:        e.file.Machine.String(),
		WordSize:  wordSize,
		Arch:      arch,
	}
}

func (e *elfFile) getBuildID() (string, error) {
	_, data, err := e.getSectionData(".note.go.buildid")
	// If the note section does not exist, we just ignore the build id.
	if err == ErrSectionDoesNotExist {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("error when getting note section: %w", err)
	}
	return parseBuildIDFromElf(data, e.file.ByteOrder)
}
