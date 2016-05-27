package dwarf

import (
	"errors"
	"fmt"

	"debug/gosym"
	"debug/pe"

	"golang.org/x/debug/dwarf"

	"github.com/derekparker/delve/pkg/dwarf/frame"
	"github.com/derekparker/delve/pkg/dwarf/line"
)

func newExecutable(path string) (*pe.File, error) {
	return pe.Open(path)
}

func parseFrame(exe *pe.File) (frame.FrameDescriptionEntries, error) {
	debugFrameSec := exe.Section(".debug_frame")
	debugInfoSec := exe.Section(".debug_info")

	if debugFrameSec != nil && debugInfoSec != nil {
		debugFrame, err := debugFrameSec.Data()
		if err != nil && uint32(len(debugFrame)) < debugFrameSec.Size {
			return nil, fmt.Errorf("dwarf: could not get .debug_frame section: %v", err)
		}
		if 0 < debugFrameSec.VirtualSize && debugFrameSec.VirtualSize < debugFrameSec.Size {
			debugFrame = debugFrame[:debugFrameSec.VirtualSize]
		}
		dat, err := debugInfoSec.Data()
		if err != nil {
			return nil, fmt.Errorf("dwarf: could not get .debug_info section: %v", err)
		}
		return frame.Parse(debugFrame, frame.DwarfEndian(dat)), nil
	}
	return nil, errors.New("dwarf: could not find .debug_frame section in binary")
}

func parseLine(exe *pe.File) (line.DebugLines, error) {
	if sec := exe.Section(".debug_line"); sec != nil {
		debugLine, err := sec.Data()
		if err != nil && uint32(len(debugLine)) < sec.Size {
			return nil, fmt.Errorf("dwarf: could not get .debug_line section: %v", err)
		}
		if 0 < sec.VirtualSize && sec.VirtualSize < sec.Size {
			debugLine = debugLine[:sec.VirtualSize]
		}
		return line.Parse(debugLine), nil
	}
	return nil, errors.New("dwarf: could not find .debug_line section in binary")
}

func parseGoSymbols(exe *pe.File) (*gosym.Table, error) {
	_, symdat, pclndat, err := pcln(exe)
	if err != nil {
		return nil, fmt.Errorf("dwarf: could not get Go symbols: %v", err)
	}

	pcln := gosym.NewLineTable(pclndat, uint64(exe.Section(".text").Offset))
	tab, err := gosym.NewTable(symdat, pcln)
	if err != nil {
		return nil, fmt.Errorf("dwarf: could not get initialize line table: %v", err)
	}

	return tab, nil
}

// Borrowed from https://golang.org/src/cmd/internal/objfile/pe.go
func pcln(exe *pe.File) (textStart uint64, symtab, pclntab []byte, err error) {
	var imageBase uint64
	switch oh := exe.OptionalHeader.(type) {
	case *pe.OptionalHeader32:
		imageBase = uint64(oh.ImageBase)
	case *pe.OptionalHeader64:
		imageBase = oh.ImageBase
	default:
		return 0, nil, nil, fmt.Errorf("pe file format not recognized")
	}
	if sect := exe.Section(".text"); sect != nil {
		textStart = imageBase + uint64(sect.VirtualAddress)
	}
	if pclntab, err = loadPETable(exe, "runtime.pclntab", "runtime.epclntab"); err != nil {
		// We didn't find the symbols, so look for the names used in 1.3 and earlier.
		// TODO: Remove code looking for the old symbols when we no longer care about 1.3.
		var err2 error
		if pclntab, err2 = loadPETable(exe, "pclntab", "epclntab"); err2 != nil {
			return 0, nil, nil, err
		}
	}
	if symtab, err = loadPETable(exe, "runtime.symtab", "runtime.esymtab"); err != nil {
		// Same as above.
		var err2 error
		if symtab, err2 = loadPETable(exe, "symtab", "esymtab"); err2 != nil {
			return 0, nil, nil, err
		}
	}
	return textStart, symtab, pclntab, nil
}

// Adapted from src/debug/pe/file.go: pe.(*File).DWARF()
func parseDwarf(f *pe.File) (*dwarf.Data, error) {
	// There are many other DWARF sections, but these
	// are the ones the debug/dwarf package uses.
	// Don't bother loading others.
	var names = [...]string{"abbrev", "info", "line", "str"}
	var dat [len(names)][]byte
	for i, name := range names {
		name = ".debug_" + name
		s := f.Section(name)
		if s == nil {
			continue
		}
		b, err := s.Data()
		if err != nil && uint32(len(b)) < s.Size {
			return nil, err
		}
		if 0 < s.VirtualSize && s.VirtualSize < s.Size {
			b = b[:s.VirtualSize]
		}
		dat[i] = b
	}

	abbrev, info, line, str := dat[0], dat[1], dat[2], dat[3]
	return dwarf.New(abbrev, nil, nil, info, line, nil, nil, str)
}

// Borrowed from https://golang.org/src/cmd/internal/objfile/pe.go
func findPESymbol(f *pe.File, name string) (*pe.Symbol, error) {
	for _, s := range f.Symbols {
		if s.Name != name {
			continue
		}
		if s.SectionNumber <= 0 {
			return nil, fmt.Errorf("symbol %s: invalid section number %d", name, s.SectionNumber)
		}
		if len(f.Sections) < int(s.SectionNumber) {
			return nil, fmt.Errorf("symbol %s: section number %d is larger than max %d", name, s.SectionNumber, len(f.Sections))
		}
		return s, nil
	}
	return nil, fmt.Errorf("no %s symbol found", name)
}

// Borrowed from https://golang.org/src/cmd/internal/objfile/pe.go
func loadPETable(f *pe.File, sname, ename string) ([]byte, error) {
	ssym, err := findPESymbol(f, sname)
	if err != nil {
		return nil, err
	}
	esym, err := findPESymbol(f, ename)
	if err != nil {
		return nil, err
	}
	if ssym.SectionNumber != esym.SectionNumber {
		return nil, fmt.Errorf("%s and %s symbols must be in the same section", sname, ename)
	}
	sect := f.Sections[ssym.SectionNumber-1]
	data, err := sect.Data()
	if err != nil {
		return nil, err
	}
	return data[ssym.Value:esym.Value], nil
}
