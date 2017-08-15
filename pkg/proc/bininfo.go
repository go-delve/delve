package proc

import (
	"debug/dwarf"
	"debug/elf"
	"debug/gosym"
	"debug/macho"
	"debug/pe"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/derekparker/delve/pkg/dwarf/frame"
	"github.com/derekparker/delve/pkg/dwarf/godwarf"
	"github.com/derekparker/delve/pkg/dwarf/line"
	"github.com/derekparker/delve/pkg/dwarf/reader"
)

type BinaryInfo struct {
	lastModified time.Time // Time the executable of this process was last modified

	GOOS   string
	closer io.Closer

	// Maps package names to package paths, needed to lookup types inside DWARF info
	packageMap map[string]string

	Arch          Arch
	dwarf         *dwarf.Data
	frameEntries  frame.FrameDescriptionEntries
	lineInfo      line.DebugLines
	goSymTable    *gosym.Table
	types         map[string]dwarf.Offset
	packageVars   map[string]dwarf.Offset
	functions     []functionDebugInfo
	gStructOffset uint64

	typeCache map[dwarf.Offset]godwarf.Type

	loadModuleDataOnce sync.Once
	moduleData         []moduleData
	nameOfRuntimeType  map[uintptr]nameOfRuntimeTypeEntry

	loadErrMu sync.Mutex
	loadErr   error
}

var UnsupportedLinuxArchErr = errors.New("unsupported architecture - only linux/amd64 is supported")
var UnsupportedWindowsArchErr = errors.New("unsupported architecture of windows/386 - only windows/amd64 is supported")
var UnsupportedDarwinArchErr = errors.New("unsupported architecture - only darwin/amd64 is supported")

func NewBinaryInfo(goos, goarch string) BinaryInfo {
	r := BinaryInfo{GOOS: goos, nameOfRuntimeType: make(map[uintptr]nameOfRuntimeTypeEntry), typeCache: make(map[dwarf.Offset]godwarf.Type)}

	// TODO: find better way to determine proc arch (perhaps use executable file info)
	switch goarch {
	case "amd64":
		r.Arch = AMD64Arch(goos)
	}

	return r
}

func (bininfo *BinaryInfo) LoadBinaryInfo(path string, wg *sync.WaitGroup) error {
	fi, err := os.Stat(path)
	if err == nil {
		bininfo.lastModified = fi.ModTime()
	}

	switch bininfo.GOOS {
	case "linux":
		return bininfo.LoadBinaryInfoElf(path, wg)
	case "windows":
		return bininfo.LoadBinaryInfoPE(path, wg)
	case "darwin":
		return bininfo.LoadBinaryInfoMacho(path, wg)
	}
	return errors.New("unsupported operating system")
}

// GStructOffset returns the offset of the G
// struct in thread local storage.
func (bi *BinaryInfo) GStructOffset() uint64 {
	return bi.gStructOffset
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

// PCToFunc returns the function containing the given PC address
func (bi *BinaryInfo) PCToFunc(pc uint64) *gosym.Func {
	return bi.goSymTable.PCToFunc(pc)
}

func (bi *BinaryInfo) Close() error {
	return bi.closer.Close()
}

func (bi *BinaryInfo) setLoadError(fmtstr string, args ...interface{}) {
	bi.loadErrMu.Lock()
	bi.loadErr = fmt.Errorf(fmtstr, args...)
	bi.loadErrMu.Unlock()
}

func (bi *BinaryInfo) LoadError() error {
	return bi.loadErr
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

	wg.Add(5)
	go bi.parseDebugFrameElf(elfFile, wg)
	go bi.obtainGoSymbolsElf(elfFile, wg)
	go bi.parseDebugLineInfoElf(elfFile, wg)
	go bi.loadDebugInfoMaps(wg)
	go bi.setGStructOffsetElf(elfFile, wg)
	return nil
}

func (bi *BinaryInfo) parseDebugFrameElf(exe *elf.File, wg *sync.WaitGroup) {
	defer wg.Done()

	debugFrameSec := exe.Section(".debug_frame")
	debugInfoSec := exe.Section(".debug_info")

	if debugFrameSec != nil && debugInfoSec != nil {
		debugFrame, err := exe.Section(".debug_frame").Data()
		if err != nil {
			bi.setLoadError("could not get .debug_frame section: %v", err)
			return
		}
		dat, err := debugInfoSec.Data()
		if err != nil {
			bi.setLoadError("could not get .debug_frame section: %v", err)
			return
		}
		bi.frameEntries = frame.Parse(debugFrame, frame.DwarfEndian(dat))
	} else {
		bi.setLoadError("could not find .debug_frame section in binary")
		return
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
			bi.setLoadError("could not get .gosymtab section: %v", err)
			return
		}
	}

	if sec := exe.Section(".gopclntab"); sec != nil {
		pclndat, err = sec.Data()
		if err != nil {
			bi.setLoadError("could not get .gopclntab section: %v", err)
			return
		}
	}

	pcln := gosym.NewLineTable(pclndat, exe.Section(".text").Addr)
	tab, err := gosym.NewTable(symdat, pcln)
	if err != nil {
		bi.setLoadError("could not get initialize line table: %v", err)
		return
	}

	bi.goSymTable = tab
}

func (bi *BinaryInfo) parseDebugLineInfoElf(exe *elf.File, wg *sync.WaitGroup) {
	defer wg.Done()

	if sec := exe.Section(".debug_line"); sec != nil {
		debugLine, err := exe.Section(".debug_line").Data()
		if err != nil {
			bi.setLoadError("could not get .debug_line section: %v", err)
			return
		}
		bi.lineInfo = line.Parse(debugLine)
	} else {
		bi.setLoadError("could not find .debug_line section in binary")
		return
	}
}

func (bi *BinaryInfo) setGStructOffsetElf(exe *elf.File, wg *sync.WaitGroup) {
	defer wg.Done()

	// This is a bit arcane. Essentially:
	// - If the program is pure Go, it can do whatever it wants, and puts the G
	//   pointer at %fs-8.
	// - Otherwise, Go asks the external linker to place the G pointer by
	//   emitting runtime.tlsg, a TLS symbol, which is relocated to the chosen
	//   offset in libc's TLS block.
	symbols, err := exe.Symbols()
	if err != nil {
		bi.setLoadError("could not parse ELF symbols: %v", err)
		return
	}
	var tlsg *elf.Symbol
	for _, symbol := range symbols {
		if symbol.Name == "runtime.tlsg" {
			s := symbol
			tlsg = &s
			break
		}
	}
	if tlsg == nil {
		bi.gStructOffset = ^uint64(8) + 1 // -8
		return
	}
	var tls *elf.Prog
	for _, prog := range exe.Progs {
		if prog.Type == elf.PT_TLS {
			tls = prog
			break
		}
	}
	// The TLS register points to the end of the TLS block, which is
	// tls.Memsz long. runtime.tlsg is an offset from the beginning of that block.
	bi.gStructOffset = ^(tls.Memsz) + 1 + tlsg.Value // -tls.Memsz + tlsg.Value
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
	bi.dwarf, err = peFile.DWARF()
	if err != nil {
		return err
	}

	wg.Add(4)
	go bi.parseDebugFramePE(peFile, wg)
	go bi.obtainGoSymbolsPE(peFile, wg)
	go bi.parseDebugLineInfoPE(peFile, wg)
	go bi.loadDebugInfoMaps(wg)

	// Use ArbitraryUserPointer (0x28) as pointer to pointer
	// to G struct per:
	// https://golang.org/src/runtime/cgo/gcc_windows_amd64.c

	bi.gStructOffset = 0x28
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

func (bi *BinaryInfo) parseDebugFramePE(exe *pe.File, wg *sync.WaitGroup) {
	defer wg.Done()

	debugFrameSec := exe.Section(".debug_frame")
	debugInfoSec := exe.Section(".debug_info")

	if debugFrameSec != nil && debugInfoSec != nil {
		debugFrame, err := debugFrameSec.Data()
		if err != nil && uint32(len(debugFrame)) < debugFrameSec.Size {
			bi.setLoadError("could not get .debug_frame section: %v", err)
			return
		}
		if 0 < debugFrameSec.VirtualSize && debugFrameSec.VirtualSize < debugFrameSec.Size {
			debugFrame = debugFrame[:debugFrameSec.VirtualSize]
		}
		dat, err := debugInfoSec.Data()
		if err != nil {
			bi.setLoadError("could not get .debug_info section: %v", err)
			return
		}
		bi.frameEntries = frame.Parse(debugFrame, frame.DwarfEndian(dat))
	} else {
		bi.setLoadError("could not find .debug_frame section in binary")
		return
	}
}

func (bi *BinaryInfo) obtainGoSymbolsPE(exe *pe.File, wg *sync.WaitGroup) {
	defer wg.Done()

	_, symdat, pclndat, err := pclnPE(exe)
	if err != nil {
		bi.setLoadError("could not get Go symbols: %v", err)
		return
	}

	pcln := gosym.NewLineTable(pclndat, uint64(exe.Section(".text").Offset))
	tab, err := gosym.NewTable(symdat, pcln)
	if err != nil {
		bi.setLoadError("could not get initialize line table: %v", err)
		return
	}

	bi.goSymTable = tab
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
			bi.setLoadError("could not get .debug_line section: %v", err)
			return
		}
		if 0 < sec.VirtualSize && sec.VirtualSize < sec.Size {
			debugLine = debugLine[:sec.VirtualSize]
		}
		bi.lineInfo = line.Parse(debugLine)
	} else {
		bi.setLoadError("could not find .debug_line section in binary")
		return
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
	bi.gStructOffset = 0x8a0
	return nil
}

func (bi *BinaryInfo) parseDebugFrameMacho(exe *macho.File, wg *sync.WaitGroup) {
	defer wg.Done()

	debugFrameSec := exe.Section("__debug_frame")
	debugInfoSec := exe.Section("__debug_info")

	if debugFrameSec != nil && debugInfoSec != nil {
		debugFrame, err := exe.Section("__debug_frame").Data()
		if err != nil {
			bi.setLoadError("could not get __debug_frame section: %v", err)
			return
		}
		dat, err := debugInfoSec.Data()
		if err != nil {
			bi.setLoadError("could not get .debug_info section: %v", err)
			return
		}
		bi.frameEntries = frame.Parse(debugFrame, frame.DwarfEndian(dat))
	} else {
		bi.setLoadError("could not find __debug_frame section in binary")
		return
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
			bi.setLoadError("could not get .gosymtab section: %v", err)
			return
		}
	}

	if sec := exe.Section("__gopclntab"); sec != nil {
		pclndat, err = sec.Data()
		if err != nil {
			bi.setLoadError("could not get .gopclntab section: %v", err)
			return
		}
	}

	pcln := gosym.NewLineTable(pclndat, exe.Section("__text").Addr)
	tab, err := gosym.NewTable(symdat, pcln)
	if err != nil {
		bi.setLoadError("could not get initialize line table: %v", err)
		return
	}

	bi.goSymTable = tab
}

func (bi *BinaryInfo) parseDebugLineInfoMacho(exe *macho.File, wg *sync.WaitGroup) {
	defer wg.Done()

	if sec := exe.Section("__debug_line"); sec != nil {
		debugLine, err := exe.Section("__debug_line").Data()
		if err != nil {
			bi.setLoadError("could not get __debug_line section: %v", err)
			return
		}
		bi.lineInfo = line.Parse(debugLine)
	} else {
		bi.setLoadError("could not find __debug_line section in binary")
		return
	}
}
