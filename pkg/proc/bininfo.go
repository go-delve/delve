package proc

import (
	"debug/gosym"
	"debug/pe"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/derekparker/delve/pkg/dwarf/frame"
	"github.com/derekparker/delve/pkg/dwarf/line"
	"github.com/derekparker/delve/pkg/dwarf/reader"

	"golang.org/x/debug/dwarf"
	"golang.org/x/debug/elf"
	"golang.org/x/debug/macho"
)

type BinaryInfo struct {
	lastModified time.Time // Time the executable of this process was last modified

	goos   string
	closer io.Closer

	// Maps package names to package paths, needed to lookup types inside DWARF info
	packageMap map[string]string

	arch         Arch
	dwarf        *dwarf.Data
	frameEntries frame.FrameDescriptionEntries
	lineInfo     line.DebugLines
	goSymTable   *gosym.Table
	types        map[string]dwarf.Offset
	functions    []functionDebugInfo

	loadModuleDataOnce sync.Once
	moduleData         []moduleData
	nameOfRuntimeType  map[uintptr]nameOfRuntimeTypeEntry
}

var UnsupportedLinuxArchErr = errors.New("unsupported architecture - only linux/amd64 is supported")
var UnsupportedWindowsArchErr = errors.New("unsupported architecture of windows/386 - only windows/amd64 is supported")
var UnsupportedDarwinArchErr = errors.New("unsupported architecture - only darwin/amd64 is supported")

func NewBinaryInfo(goos, goarch string) BinaryInfo {
	r := BinaryInfo{goos: goos, nameOfRuntimeType: make(map[uintptr]nameOfRuntimeTypeEntry)}

	// TODO: find better way to determine proc arch (perhaps use executable file info)
	switch goarch {
	case "amd64":
		r.arch = AMD64Arch(goos)
	}

	return r
}

func (bininfo *BinaryInfo) LoadBinaryInfo(path string, wg *sync.WaitGroup) error {
	fi, err := os.Stat(path)
	if err == nil {
		bininfo.lastModified = fi.ModTime()
	}

	switch bininfo.goos {
	case "linux":
		return bininfo.LoadBinaryInfoElf(path, wg)
	case "windows":
		return bininfo.LoadBinaryInfoPE(path, wg)
	case "darwin":
		return bininfo.LoadBinaryInfoMacho(path, wg)
	}
	return errors.New("unsupported operating system")
}

func (bi *BinaryInfo) LastModified() time.Time {
	return bi.lastModified
}

// DwarfReader returns a reader for the dwarf data
func (bi *BinaryInfo) DwarfReader() *reader.Reader {
	return reader.New(bi.dwarf)
}

// Sources returns list of source files that comprise the debugged binary.
func (bi *BinaryInfo) Sources() map[string]*gosym.Obj {
	return bi.goSymTable.Files
}

// Funcs returns list of functions present in the debugged program.
func (bi *BinaryInfo) Funcs() []gosym.Func {
	return bi.goSymTable.Funcs
}

// Types returns list of types present in the debugged program.
func (bi *BinaryInfo) Types() ([]string, error) {
	types := make([]string, 0, len(bi.types))
	for k := range bi.types {
		types = append(types, k)
	}
	return types, nil
}

// PCToLine converts an instruction address to a file/line/function.
func (bi *BinaryInfo) PCToLine(pc uint64) (string, int, *gosym.Func) {
	return bi.goSymTable.PCToLine(pc)
}

// LineToPC converts a file:line into a memory address.
func (bi *BinaryInfo) LineToPC(filename string, lineno int) (pc uint64, fn *gosym.Func, err error) {
	return bi.goSymTable.LineToPC(filename, lineno)
}

func (bi *BinaryInfo) Close() error {
	return bi.closer.Close()
}

// ELF ///////////////////////////////////////////////////////////////

func (bi *BinaryInfo) LoadBinaryInfoElf(path string, wg *sync.WaitGroup) error {
	exe, err := os.OpenFile(path, 0, os.ModePerm)
	if err != nil {
		return err
	}
	bi.closer = exe
	elfFile, err := elf.NewFile(exe)
	if err != nil {
		return err
	}
	if elfFile.Machine != elf.EM_X86_64 {
		return UnsupportedLinuxArchErr
	}
	bi.dwarf, err = elfFile.DWARF()
	if err != nil {
		return err
	}

	wg.Add(4)
	go bi.parseDebugFrameElf(elfFile, wg)
	go bi.obtainGoSymbolsElf(elfFile, wg)
	go bi.parseDebugLineInfoElf(elfFile, wg)
	go bi.loadDebugInfoMaps(wg)
	return nil
}

func (bi *BinaryInfo) parseDebugFrameElf(exe *elf.File, wg *sync.WaitGroup) {
	defer wg.Done()

	debugFrameSec := exe.Section(".debug_frame")
	debugInfoSec := exe.Section(".debug_info")

	if debugFrameSec != nil && debugInfoSec != nil {
		debugFrame, err := exe.Section(".debug_frame").Data()
		if err != nil {
			fmt.Println("could not get .debug_frame section", err)
			os.Exit(1)
		}
		dat, err := debugInfoSec.Data()
		if err != nil {
			fmt.Println("could not get .debug_info section", err)
			os.Exit(1)
		}
		bi.frameEntries = frame.Parse(debugFrame, frame.DwarfEndian(dat))
	} else {
		fmt.Println("could not find .debug_frame section in binary")
		os.Exit(1)
	}
}

func (bi *BinaryInfo) obtainGoSymbolsElf(exe *elf.File, wg *sync.WaitGroup) {
	defer wg.Done()

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

	bi.goSymTable = tab
}

func (bi *BinaryInfo) parseDebugLineInfoElf(exe *elf.File, wg *sync.WaitGroup) {
	defer wg.Done()

	if sec := exe.Section(".debug_line"); sec != nil {
		debugLine, err := exe.Section(".debug_line").Data()
		if err != nil {
			fmt.Println("could not get .debug_line section", err)
			os.Exit(1)
		}
		bi.lineInfo = line.Parse(debugLine)
	} else {
		fmt.Println("could not find .debug_line section in binary")
		os.Exit(1)
	}
}

// PE ////////////////////////////////////////////////////////////////

func (bi *BinaryInfo) LoadBinaryInfoPE(path string, wg *sync.WaitGroup) error {
	peFile, closer, err := openExecutablePathPE(path)
	if err != nil {
		return err
	}
	bi.closer = closer
	if peFile.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
		return UnsupportedWindowsArchErr
	}
	bi.dwarf, err = dwarfFromPE(peFile)
	if err != nil {
		return err
	}

	wg.Add(4)
	go bi.parseDebugFramePE(peFile, wg)
	go bi.obtainGoSymbolsPE(peFile, wg)
	go bi.parseDebugLineInfoPE(peFile, wg)
	go bi.loadDebugInfoMaps(wg)
	return nil
}

func openExecutablePathPE(path string) (*pe.File, io.Closer, error) {
	f, err := os.OpenFile(path, 0, os.ModePerm)
	if err != nil {
		return nil, nil, err
	}
	peFile, err := pe.NewFile(f)
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return peFile, f, nil
}

// Adapted from src/debug/pe/file.go: pe.(*File).DWARF()
func dwarfFromPE(f *pe.File) (*dwarf.Data, error) {
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

func (bi *BinaryInfo) parseDebugFramePE(exe *pe.File, wg *sync.WaitGroup) {
	defer wg.Done()

	debugFrameSec := exe.Section(".debug_frame")
	debugInfoSec := exe.Section(".debug_info")

	if debugFrameSec != nil && debugInfoSec != nil {
		debugFrame, err := debugFrameSec.Data()
		if err != nil && uint32(len(debugFrame)) < debugFrameSec.Size {
			fmt.Println("could not get .debug_frame section", err)
			os.Exit(1)
		}
		if 0 < debugFrameSec.VirtualSize && debugFrameSec.VirtualSize < debugFrameSec.Size {
			debugFrame = debugFrame[:debugFrameSec.VirtualSize]
		}
		dat, err := debugInfoSec.Data()
		if err != nil {
			fmt.Println("could not get .debug_info section", err)
			os.Exit(1)
		}
		bi.frameEntries = frame.Parse(debugFrame, frame.DwarfEndian(dat))
	} else {
		fmt.Println("could not find .debug_frame section in binary")
		os.Exit(1)
	}
}

func (dbp *BinaryInfo) obtainGoSymbolsPE(exe *pe.File, wg *sync.WaitGroup) {
	defer wg.Done()

	_, symdat, pclndat, err := pclnPE(exe)
	if err != nil {
		fmt.Println("could not get Go symbols", err)
		os.Exit(1)
	}

	pcln := gosym.NewLineTable(pclndat, uint64(exe.Section(".text").Offset))
	tab, err := gosym.NewTable(symdat, pcln)
	if err != nil {
		fmt.Println("could not get initialize line table", err)
		os.Exit(1)
	}

	dbp.goSymTable = tab
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

// Borrowed from https://golang.org/src/cmd/internal/objfile/pe.go
func pclnPE(exe *pe.File) (textStart uint64, symtab, pclntab []byte, err error) {
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

func (bi *BinaryInfo) parseDebugLineInfoPE(exe *pe.File, wg *sync.WaitGroup) {
	defer wg.Done()

	if sec := exe.Section(".debug_line"); sec != nil {
		debugLine, err := sec.Data()
		if err != nil && uint32(len(debugLine)) < sec.Size {
			fmt.Println("could not get .debug_line section", err)
			os.Exit(1)
		}
		if 0 < sec.VirtualSize && sec.VirtualSize < sec.Size {
			debugLine = debugLine[:sec.VirtualSize]
		}
		bi.lineInfo = line.Parse(debugLine)
	} else {
		fmt.Println("could not find .debug_line section in binary")
		os.Exit(1)
	}
}

// MACH-O ////////////////////////////////////////////////////////////

func (bi *BinaryInfo) LoadBinaryInfoMacho(path string, wg *sync.WaitGroup) error {
	exe, err := macho.Open(path)
	if err != nil {
		return err
	}
	bi.closer = exe
	if exe.Cpu != macho.CpuAmd64 {
		return UnsupportedDarwinArchErr
	}
	bi.dwarf, err = exe.DWARF()
	if err != nil {
		return err
	}

	wg.Add(4)
	go bi.parseDebugFrameMacho(exe, wg)
	go bi.obtainGoSymbolsMacho(exe, wg)
	go bi.parseDebugLineInfoMacho(exe, wg)
	go bi.loadDebugInfoMaps(wg)
	return nil
}

func (bi *BinaryInfo) parseDebugFrameMacho(exe *macho.File, wg *sync.WaitGroup) {
	defer wg.Done()

	debugFrameSec := exe.Section("__debug_frame")
	debugInfoSec := exe.Section("__debug_info")

	if debugFrameSec != nil && debugInfoSec != nil {
		debugFrame, err := exe.Section("__debug_frame").Data()
		if err != nil {
			fmt.Println("could not get __debug_frame section", err)
			os.Exit(1)
		}
		dat, err := debugInfoSec.Data()
		if err != nil {
			fmt.Println("could not get .debug_info section", err)
			os.Exit(1)
		}
		bi.frameEntries = frame.Parse(debugFrame, frame.DwarfEndian(dat))
	} else {
		fmt.Println("could not find __debug_frame section in binary")
		os.Exit(1)
	}
}

func (bi *BinaryInfo) obtainGoSymbolsMacho(exe *macho.File, wg *sync.WaitGroup) {
	defer wg.Done()

	var (
		symdat  []byte
		pclndat []byte
		err     error
	)

	if sec := exe.Section("__gosymtab"); sec != nil {
		symdat, err = sec.Data()
		if err != nil {
			fmt.Println("could not get .gosymtab section", err)
			os.Exit(1)
		}
	}

	if sec := exe.Section("__gopclntab"); sec != nil {
		pclndat, err = sec.Data()
		if err != nil {
			fmt.Println("could not get .gopclntab section", err)
			os.Exit(1)
		}
	}

	pcln := gosym.NewLineTable(pclndat, exe.Section("__text").Addr)
	tab, err := gosym.NewTable(symdat, pcln)
	if err != nil {
		fmt.Println("could not get initialize line table", err)
		os.Exit(1)
	}

	bi.goSymTable = tab
}

func (bi *BinaryInfo) parseDebugLineInfoMacho(exe *macho.File, wg *sync.WaitGroup) {
	defer wg.Done()

	if sec := exe.Section("__debug_line"); sec != nil {
		debugLine, err := exe.Section("__debug_line").Data()
		if err != nil {
			fmt.Println("could not get __debug_line section", err)
			os.Exit(1)
		}
		bi.lineInfo = line.Parse(debugLine)
	} else {
		fmt.Println("could not find __debug_line section in binary")
		os.Exit(1)
	}
}
