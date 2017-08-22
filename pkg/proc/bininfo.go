package proc

import (
	"debug/dwarf"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
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
	compileUnits  []*compileUnit
	types         map[string]dwarf.Offset
	packageVars   map[string]dwarf.Offset
	gStructOffset uint64

	// Functions is a list of all DW_TAG_subprogram entries in debug_info.
	Functions []Function
	// Sources is a list of all source files found in debug_line.
	Sources []string
	// LookupFunc maps function names to a description of the function.
	LookupFunc map[string]*Function

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

const dwarfGoLanguage = 22 // DW_LANG_Go (from DWARF v5, section 7.12, page 231)

type compileUnit struct {
	entry         *dwarf.Entry        // debug_info entry describing this compile unit
	isgo          bool                // true if this is the go compile unit
	Name          string              // univocal name for non-go compile units
	lineInfo      *line.DebugLineInfo // debug_line segment associated with this compile unit
	LowPC, HighPC uint64
}

// Function describes a function in the target program.
type Function struct {
	Name       string
	Entry, End uint64 // same as DW_AT_lowpc and DW_AT_highpc
	offset     dwarf.Offset
	cu         *compileUnit
}

// PackageName returns the package part of the symbol name,
// or the empty string if there is none.
// Borrowed from $GOROOT/debug/gosym/symtab.go
func (fn *Function) PackageName() string {
	pathend := strings.LastIndex(fn.Name, "/")
	if pathend < 0 {
		pathend = 0
	}

	if i := strings.Index(fn.Name[pathend:], "."); i != -1 {
		return fn.Name[:pathend+i]
	}
	return ""
}

// ReceiverName returns the receiver type name of this symbol,
// or the empty string if there is none.
// Borrowed from $GOROOT/debug/gosym/symtab.go
func (fn *Function) ReceiverName() string {
	pathend := strings.LastIndex(fn.Name, "/")
	if pathend < 0 {
		pathend = 0
	}
	l := strings.Index(fn.Name[pathend:], ".")
	r := strings.LastIndex(fn.Name[pathend:], ".")
	if l == -1 || r == -1 || l == r {
		return ""
	}
	return fn.Name[pathend+l+1 : pathend+r]
}

// BaseName returns the symbol name without the package or receiver name.
// Borrowed from $GOROOT/debug/gosym/symtab.go
func (fn *Function) BaseName() string {
	if i := strings.LastIndex(fn.Name, "."); i != -1 {
		return fn.Name[i+1:]
	}
	return fn.Name
}

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

// Types returns list of types present in the debugged program.
func (bi *BinaryInfo) Types() ([]string, error) {
	types := make([]string, 0, len(bi.types))
	for k := range bi.types {
		types = append(types, k)
	}
	return types, nil
}

// PCToLine converts an instruction address to a file/line/function.
func (bi *BinaryInfo) PCToLine(pc uint64) (string, int, *Function) {
	fn := bi.PCToFunc(pc)
	if fn == nil {
		return "", 0, nil
	}
	f, ln := fn.cu.lineInfo.PCToLine(fn.Entry, pc)
	return f, ln, fn
}

// LineToPC converts a file:line into a memory address.
func (bi *BinaryInfo) LineToPC(filename string, lineno int) (pc uint64, fn *Function, err error) {
	for _, cu := range bi.compileUnits {
		if cu.lineInfo.Lookup[filename] != nil {
			pc = cu.lineInfo.LineToPC(filename, lineno)
			fn = bi.PCToFunc(pc)
			if fn == nil {
				err = fmt.Errorf("no code at %s:%d", filename, lineno)
			}
			return
		}
	}
	err = fmt.Errorf("could not find %s:%d", filename, lineno)
	return
}

// PCToFunc returns the function containing the given PC address
func (bi *BinaryInfo) PCToFunc(pc uint64) *Function {
	i := sort.Search(len(bi.Functions), func(i int) bool {
		fn := bi.Functions[i]
		return pc <= fn.Entry || (fn.Entry <= pc && pc < fn.End)
	})
	if i != len(bi.Functions) {
		fn := &bi.Functions[i]
		if fn.Entry <= pc && pc < fn.End {
			return fn
		}
	}
	return nil
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

type nilCloser struct{}

func (c *nilCloser) Close() error { return nil }

// New creates a new BinaryInfo object using the specified data. Use LoadBinary instead.
func (bi *BinaryInfo) LoadFromData(dwdata *dwarf.Data, debugFrameBytes []byte, debugLineBytes []byte) {
	bi.closer = (*nilCloser)(nil)
	bi.dwarf = dwdata

	if debugFrameBytes != nil {
		bi.frameEntries = frame.Parse(debugFrameBytes, frame.DwarfEndian(debugFrameBytes))
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go bi.loadDebugInfoMaps(debugLineBytes, &wg)
	wg.Wait()
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

	debugLineBytes, err := getDebugLineInfoElf(elfFile)
	if err != nil {
		return err
	}

	wg.Add(3)
	go bi.parseDebugFrameElf(elfFile, wg)
	go bi.loadDebugInfoMaps(debugLineBytes, wg)
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

func getDebugLineInfoElf(exe *elf.File) ([]byte, error) {
	if sec := exe.Section(".debug_line"); sec != nil {
		debugLine, err := exe.Section(".debug_line").Data()
		if err != nil {
			return nil, fmt.Errorf("could not get .debug_line section: %v", err)
		}
		return debugLine, nil
	}
	return nil, errors.New("could not find .debug_line section in binary")
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

	debugLineBytes, err := getDebugLineInfoPE(peFile)
	if err != nil {
		return err
	}

	wg.Add(2)
	go bi.parseDebugFramePE(peFile, wg)
	go bi.loadDebugInfoMaps(debugLineBytes, wg)

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

func getDebugLineInfoPE(exe *pe.File) ([]byte, error) {
	if sec := exe.Section(".debug_line"); sec != nil {
		debugLine, err := sec.Data()
		if err != nil && uint32(len(debugLine)) < sec.Size {
			return nil, fmt.Errorf("could not get .debug_line section: %v", err)
		}
		if 0 < sec.VirtualSize && sec.VirtualSize < sec.Size {
			debugLine = debugLine[:sec.VirtualSize]
		}
		return debugLine, nil
	}
	return nil, errors.New("could not find .debug_line section in binary")
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

	debugLineBytes, err := getDebugLineInfoMacho(exe)
	if err != nil {
		return err
	}

	wg.Add(2)
	go bi.parseDebugFrameMacho(exe, wg)
	go bi.loadDebugInfoMaps(debugLineBytes, wg)
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

func getDebugLineInfoMacho(exe *macho.File) ([]byte, error) {
	if sec := exe.Section("__debug_line"); sec != nil {
		debugLine, err := exe.Section("__debug_line").Data()
		if err != nil {
			return nil, fmt.Errorf("could not get __debug_line section: %v", err)
		}
		return debugLine, nil
	}
	return nil, errors.New("could not find __debug_line section in binary")
}
