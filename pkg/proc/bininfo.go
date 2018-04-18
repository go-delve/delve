package proc

import (
	"bytes"
	"debug/dwarf"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"encoding/binary"
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
	"github.com/derekparker/delve/pkg/dwarf/op"
	"github.com/derekparker/delve/pkg/dwarf/reader"
	"github.com/derekparker/delve/pkg/goversion"
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
	loclist       loclistReader
	compileUnits  []*compileUnit
	types         map[string]dwarf.Offset
	packageVars   []packageVar // packageVars is a list of all global/package variables in debug_info, sorted by address
	gStructOffset uint64

	// Functions is a list of all DW_TAG_subprogram entries in debug_info, sorted by entry point
	Functions []Function
	// Sources is a list of all source files found in debug_line.
	Sources []string
	// LookupFunc maps function names to a description of the function.
	LookupFunc map[string]*Function

	typeCache map[dwarf.Offset]godwarf.Type

	loadModuleDataOnce sync.Once
	moduleData         []moduleData
	nameOfRuntimeType  map[uintptr]nameOfRuntimeTypeEntry

	// runtimeTypeToDIE maps between the offset of a runtime._type in
	// runtime.moduledata.types and the offset of the DIE in debug_info. This
	// map is filled by using the extended attribute godwarf.AttrGoRuntimeType
	// which was added in go 1.11.
	runtimeTypeToDIE map[uint64]runtimeTypeDIE

	// consts[off] lists all the constants with the type defined at offset off.
	consts constantsMap

	loadErrMu sync.Mutex
	loadErr   error

	dwarfReader *dwarf.Reader
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
	optimized     bool   // this compile unit is optimized
	producer      string // producer attribute
}

type partialUnitConstant struct {
	name  string
	typ   dwarf.Offset
	value int64
}

type partialUnit struct {
	entry       *dwarf.Entry
	types       map[string]dwarf.Offset
	variables   []packageVar
	constants   []partialUnitConstant
	functions   []Function
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
	return packageName(fn.Name)
}

func packageName(name string) string {
	pathend := strings.LastIndex(name, "/")
	if pathend < 0 {
		pathend = 0
	}

	if i := strings.Index(name[pathend:], "."); i != -1 {
		return name[:pathend+i]
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

// Optimized returns true if the function was optimized by the compiler.
func (fn *Function) Optimized() bool {
	return fn.cu.optimized
}

type constantsMap map[dwarf.Offset]*constantType

type constantType struct {
	initialized bool
	values      []constantValue
}

type constantValue struct {
	name      string
	fullName  string
	value     int64
	singleBit bool
}

// packageVar represents a package-level variable (or a C global variable).
// If a global variable does not have an address (for example it's stored in
// a register, or non-contiguously) addr will be 0.
type packageVar struct {
	name   string
	offset dwarf.Offset
	addr   uint64
}

type loclistReader struct {
	data  []byte
	cur   int
	ptrSz int
}

func (rdr *loclistReader) Seek(off int) {
	rdr.cur = off
}

func (rdr *loclistReader) read(sz int) []byte {
	r := rdr.data[rdr.cur : rdr.cur+sz]
	rdr.cur += sz
	return r
}

func (rdr *loclistReader) oneAddr() uint64 {
	switch rdr.ptrSz {
	case 4:
		addr := binary.LittleEndian.Uint32(rdr.read(rdr.ptrSz))
		if addr == ^uint32(0) {
			return ^uint64(0)
		}
		return uint64(addr)
	case 8:
		addr := uint64(binary.LittleEndian.Uint64(rdr.read(rdr.ptrSz)))
		return addr
	default:
		panic("bad address size")
	}
}

func (rdr *loclistReader) Next(e *loclistEntry) bool {
	e.lowpc = rdr.oneAddr()
	e.highpc = rdr.oneAddr()

	if e.lowpc == 0 && e.highpc == 0 {
		return false
	}

	if e.BaseAddressSelection() {
		e.instr = nil
		return true
	}

	instrlen := binary.LittleEndian.Uint16(rdr.read(2))
	e.instr = rdr.read(int(instrlen))
	return true
}

type loclistEntry struct {
	lowpc, highpc uint64
	instr         []byte
}

type runtimeTypeDIE struct {
	offset dwarf.Offset
	kind   int64
}

func (e *loclistEntry) BaseAddressSelection() bool {
	return e.lowpc == ^uint64(0)
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
			if fn != nil {
				return
			}
		}
	}
	err = fmt.Errorf("could not find %s:%d", filename, lineno)
	return
}

// AllPCsForFileLine returns all PC addresses for the given filename:lineno.
func (bi *BinaryInfo) AllPCsForFileLine(filename string, lineno int) []uint64 {
	r := make([]uint64, 0, 1)
	for _, cu := range bi.compileUnits {
		if cu.lineInfo.Lookup[filename] != nil {
			r = append(r, cu.lineInfo.AllPCsForFileLine(filename, lineno)...)
		}
	}
	return r
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

// LoadFromData creates a new BinaryInfo object using the specified data.
// This is used for debugging BinaryInfo, you should use LoadBinary instead.
func (bi *BinaryInfo) LoadFromData(dwdata *dwarf.Data, debugFrameBytes, debugLineBytes, debugLocBytes []byte) {
	bi.closer = (*nilCloser)(nil)
	bi.dwarf = dwdata

	if debugFrameBytes != nil {
		bi.frameEntries = frame.Parse(debugFrameBytes, frame.DwarfEndian(debugFrameBytes))
	}

	bi.loclistInit(debugLocBytes)

	bi.loadDebugInfoMaps(debugLineBytes, nil, nil)
}

func (bi *BinaryInfo) loclistInit(data []byte) {
	bi.loclist.data = data
	bi.loclist.ptrSz = bi.Arch.PtrSize()
}

// Location returns the location described by attribute attr of entry.
// This will either be an int64 address or a slice of Pieces for locations
// that don't correspond to a single memory address (registers, composite
// locations).
func (bi *BinaryInfo) Location(entry reader.Entry, attr dwarf.Attr, pc uint64, regs op.DwarfRegisters) (int64, []op.Piece, string, error) {
	a := entry.Val(attr)
	if a == nil {
		return 0, nil, "", fmt.Errorf("no location attribute %s", attr)
	}
	if instr, ok := a.([]byte); ok {
		var descr bytes.Buffer
		fmt.Fprintf(&descr, "[block] ")
		op.PrettyPrint(&descr, instr)
		addr, pieces, err := op.ExecuteStackProgram(regs, instr)
		return addr, pieces, descr.String(), err
	}
	off, ok := a.(int64)
	if !ok {
		return 0, nil, "", fmt.Errorf("could not interpret location attribute %s", attr)
	}
	if bi.loclist.data == nil {
		return 0, nil, "", fmt.Errorf("could not find loclist entry at %#x for address %#x (no debug_loc section found)", off, pc)
	}
	instr := bi.loclistEntry(off, pc)
	if instr == nil {
		return 0, nil, "", fmt.Errorf("could not find loclist entry at %#x for address %#x", off, pc)
	}
	var descr bytes.Buffer
	fmt.Fprintf(&descr, "[%#x:%#x] ", off, pc)
	op.PrettyPrint(&descr, instr)
	addr, pieces, err := op.ExecuteStackProgram(regs, instr)
	return addr, pieces, descr.String(), err
}

// loclistEntry returns the loclist entry in the loclist starting at off,
// for address pc.
func (bi *BinaryInfo) loclistEntry(off int64, pc uint64) []byte {
	var base uint64
	if cu := bi.findCompileUnit(pc); cu != nil {
		base = cu.LowPC
	}

	bi.loclist.Seek(int(off))
	var e loclistEntry
	for bi.loclist.Next(&e) {
		if e.BaseAddressSelection() {
			base = e.highpc
			continue
		}
		if pc >= e.lowpc+base && pc < e.highpc+base {
			return e.instr
		}
	}

	return nil
}

// findCompileUnit returns the compile unit containing address pc.
func (bi *BinaryInfo) findCompileUnit(pc uint64) *compileUnit {
	for _, cu := range bi.compileUnits {
		if pc >= cu.LowPC && pc < cu.HighPC {
			return cu
		}
	}
	return nil
}

func (bi *BinaryInfo) Producer() string {
	for _, cu := range bi.compileUnits {
		if cu.isgo && cu.producer != "" {
			return cu.producer
		}
	}
	return ""
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

	bi.dwarfReader = bi.dwarf.Reader()

	debugLineBytes, err := getDebugLineInfoElf(elfFile)
	if err != nil {
		return err
	}
	bi.loclistInit(getDebugLocElf(elfFile))

	wg.Add(3)
	go bi.parseDebugFrameElf(elfFile, wg)
	go bi.loadDebugInfoMaps(debugLineBytes, wg, nil)
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

func getDebugLocElf(exe *elf.File) []byte {
	if sec := exe.Section(".debug_loc"); sec != nil {
		debugLoc, _ := exe.Section(".debug_loc").Data()
		return debugLoc
	}
	return nil
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

	bi.dwarfReader = bi.dwarf.Reader()

	debugLineBytes, err := getDebugLineInfoPE(peFile)
	if err != nil {
		return err
	}
	bi.loclistInit(getDebugLocPE(peFile))

	wg.Add(2)
	go bi.parseDebugFramePE(peFile, wg)
	go bi.loadDebugInfoMaps(debugLineBytes, wg, nil)

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

func getDebugLocPE(exe *pe.File) []byte {
	if sec := exe.Section(".debug_loc"); sec != nil {
		debugLoc, _ := sec.Data()
		return debugLoc
	}
	return nil
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

	bi.dwarfReader = bi.dwarf.Reader()

	debugLineBytes, err := getDebugLineInfoMacho(exe)
	if err != nil {
		return err
	}
	bi.loclistInit(getDebugLocMacho(exe))

	wg.Add(2)
	go bi.parseDebugFrameMacho(exe, wg)
	go bi.loadDebugInfoMaps(debugLineBytes, wg, bi.setGStructOffsetMacho)
	return nil
}

func (bi *BinaryInfo) setGStructOffsetMacho() {
	// In go1.11 it's 0x30, before 0x8a0, see:
	// https://github.com/golang/go/issues/23617
	// and go commit b3a854c733257c5249c3435ffcee194f8439676a
	producer := bi.Producer()
	if producer != "" && goversion.ProducerAfterOrEqual(producer, 1, 11) {
		bi.gStructOffset = 0x30
		return
	}
	bi.gStructOffset = 0x8a0
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

func getDebugLocMacho(exe *macho.File) []byte {
	if sec := exe.Section("__debug_loc"); sec != nil {
		debugLoc, _ := sec.Data()
		return debugLoc
	}
	return nil
}
