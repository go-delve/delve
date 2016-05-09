package dwarf

import (
	"debug/gosym"
	"errors"
	"fmt"

	"golang.org/x/debug/elf"

	"github.com/derekparker/delve/pkg/dwarf/frame"
	"github.com/derekparker/delve/pkg/dwarf/line"
)

func newExecutable(path string) (*elf.File, error) {
	return elf.Open(path)
}

func parseFrame(exe *elf.File) (frame.FrameDescriptionEntries, error) {
	debugFrameSec := exe.Section(".debug_frame")
	debugInfoSec := exe.Section(".debug_info")

	if debugFrameSec != nil && debugInfoSec != nil {
		debugFrame, err := exe.Section(".debug_frame").Data()
		if err != nil {
			return nil, fmt.Errorf("dwarf: could not get .debug_frame section: %v", err)
		}
		dat, err := debugInfoSec.Data()
		if err != nil {
			return nil, fmt.Errorf("dwarf: could not get .debug_frame section: %v", err)
		}
		return frame.Parse(debugFrame, frame.DwarfEndian(dat)), nil
	}
	return nil, errors.New("dwarf: could not get .debug_frame section")
}

func parseLine(exe *elf.File) (line.DebugLines, error) {
	if sec := exe.Section(".debug_line"); sec != nil {
		debugLine, err := exe.Section(".debug_line").Data()
		if err != nil {
			return nil, fmt.Errorf("dwarf: could not get .debug_line section: %v", err)
		}
		return line.Parse(debugLine), nil
	}
	return nil, errors.New("dwarf: could not get .debug_line section")
}

func parseGoSymbols(exe *elf.File) (*gosym.Table, error) {
	var (
		symdat  []byte
		pclndat []byte
		err     error
	)

	if sec := exe.Section(".gosymtab"); sec != nil {
		symdat, err = sec.Data()
		if err != nil {
			return nil, fmt.Errorf("dwarf: could not get .gosymtab section: %v", err)
		}
	}

	if sec := exe.Section(".gopclntab"); sec != nil {
		pclndat, err = sec.Data()
		if err != nil {
			return nil, fmt.Errorf("dwarf: could not get .gopclntab section: %v", err)
		}
	}

	pcln := gosym.NewLineTable(pclndat, exe.Section(".text").Addr)
	tab, err := gosym.NewTable(symdat, pcln)
	if err != nil {
		return nil, fmt.Errorf("dwarf: could not get initialize line table: %b", err)
	}

	return tab, nil
}
