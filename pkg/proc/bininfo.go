package proc

import (
	"bytes"
	"debug/dwarf"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-delve/delve/pkg/dwarf/frame"
	"github.com/go-delve/delve/pkg/dwarf/godwarf"
	"github.com/go-delve/delve/pkg/dwarf/line"
	"github.com/go-delve/delve/pkg/dwarf/loclist"
	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/reader"
	"github.com/go-delve/delve/pkg/dwarf/util"
	"github.com/go-delve/delve/pkg/goversion"
	"github.com/go-delve/delve/pkg/logflags"
	"github.com/hashicorp/golang-lru/simplelru"
	"github.com/sirupsen/logrus"
)

const (
	dwarfGoLanguage    = 22   // DW_LANG_Go (from DWARF v5, section 7.12, page 231)
	dwarfAttrAddrBase  = 0x74 // debug/dwarf.AttrAddrBase in Go 1.14, defined here for compatibility with Go < 1.14
	dwarfTreeCacheSize = 512  // size of the dwarfTree cache of each image
)

// BinaryInfo holds information on the binaries being executed (this
// includes both the executable and also any loaded libraries).
type BinaryInfo struct {
	// Architecture of this binary.
	Arch *Arch

	// GOOS operating system this binary is executing on.
	GOOS string

	debugInfoDirectories []string

	// Functions is a list of all DW_TAG_subprogram entries in debug_info, sorted by entry point
	Functions []Function
	// Sources is a list of all source files found in debug_line.
	Sources []string
	// LookupFunc maps function names to a description of the function.
	LookupFunc map[string]*Function

	// SymNames maps addr to a description *elf.Symbol of this addr.
	SymNames map[uint64]*elf.Symbol

	// Images is a list of loaded shared libraries (also known as
	// shared objects on linux or DLLs on windows).
	Images []*Image

	ElfDynamicSection ElfDynamicSection

	lastModified time.Time // Time the executable of this process was last modified

	// PackageMap maps package names to package paths, needed to lookup types inside DWARF info.
	// On Go1.12 this mapping is determined by using the last element of a package path, for example:
	//   github.com/go-delve/delve
	// will map to 'delve' because it ends in '/delve'.
	// Starting with Go1.13 debug_info will contain a special attribute
	// (godwarf.AttrGoPackageName) containing the canonical package name for
	// each package.
	// If multiple packages have the same name the map entry will have more
	// than one item in the slice.
	PackageMap map[string][]string

	frameEntries frame.FrameDescriptionEntries

	types       map[string]dwarfRef
	packageVars []packageVar // packageVars is a list of all global/package variables in debug_info, sorted by address

	gStructOffset uint64

	// nameOfRuntimeType maps an address of a runtime._type struct to its
	// decoded name. Used with versions of Go <= 1.10 to figure out the DIE of
	// the concrete type of interfaces.
	nameOfRuntimeType map[uint64]nameOfRuntimeTypeEntry

	// consts[off] lists all the constants with the type defined at offset off.
	consts constantsMap

	// inlinedCallLines maps a file:line pair, corresponding to the header line
	// of a function to a list of PC addresses where an inlined call to that
	// function starts.
	inlinedCallLines map[fileLine][]uint64

	// dwrapUnwrapCache caches unwrapping of defer wrapper functions (dwrap)
	dwrapUnwrapCache map[uint64]*Function

	// Go 1.17 register ABI is enabled.
	regabi bool

	logger *logrus.Entry
}

var (
	// ErrCouldNotDetermineRelocation is an error returned when Delve could not determine the base address of a
	// position independent executable.
	ErrCouldNotDetermineRelocation = errors.New("could not determine the base address of a PIE")

	// ErrNoDebugInfoFound is returned when Delve cannot open the debug_info
	// section or find an external debug info file.
	ErrNoDebugInfoFound = errors.New("could not open debug info")
)

var (
	supportedLinuxArch = map[elf.Machine]bool{
		elf.EM_X86_64:  true,
		elf.EM_AARCH64: true,
		elf.EM_386:     true,
	}

	supportedWindowsArch = map[_PEMachine]bool{
		_IMAGE_FILE_MACHINE_AMD64: true,
	}

	supportedDarwinArch = map[macho.Cpu]bool{
		macho.CpuAmd64: true,
		macho.CpuArm64: true,
	}
)

// ErrFunctionNotFound is returned when failing to find the
// function named 'FuncName' within the binary.
type ErrFunctionNotFound struct {
	FuncName string
}

func (err *ErrFunctionNotFound) Error() string {
	return fmt.Sprintf("could not find function %s\n", err.FuncName)
}

// FindFileLocation returns the PC for a given file:line.
// Assumes that `file` is normalized to lower case and '/' on Windows.
func FindFileLocation(p Process, filename string, lineno int) ([]uint64, error) {
	// A single file:line can appear in multiple concrete functions, because of
	// generics instantiation as well as multiple inlined calls into other
	// concrete functions.

	// 1. Find all instructions assigned in debug_line to filename:lineno.

	bi := p.BinInfo()

	fileFound := false
	pcs := []line.PCStmt{}
	for _, image := range bi.Images {
		for _, cu := range image.compileUnits {
			if cu.lineInfo == nil || cu.lineInfo.Lookup[filename] == nil {
				continue
			}

			fileFound = true
			pcs = append(pcs, cu.lineInfo.LineToPCs(filename, lineno)...)
		}
	}

	if len(pcs) == 0 {
		// Check if the line contained a call to a function that was inlined, in
		// that case it's possible for the line itself to not appear in debug_line
		// at all, but it will still be in debug_info as the call site for an
		// inlined subroutine entry.
		for _, pc := range bi.inlinedCallLines[fileLine{filename, lineno}] {
			pcs = append(pcs, line.PCStmt{PC: pc, Stmt: true})
		}
	}

	if len(pcs) == 0 {
		return nil, &ErrCouldNotFindLine{fileFound, filename, lineno}
	}

	// 2. assign all occurences of filename:lineno to their containing function

	pcByFunc := map[*Function][]line.PCStmt{}
	sort.Slice(pcs, func(i, j int) bool { return pcs[i].PC < pcs[j].PC })
	var fn *Function
	for _, pcstmt := range pcs {
		if fn == nil || (pcstmt.PC < fn.Entry) || (pcstmt.PC >= fn.End) {
			fn = p.BinInfo().PCToFunc(pcstmt.PC)
		}
		if fn != nil {
			pcByFunc[fn] = append(pcByFunc[fn], pcstmt)
		}
	}

	selectedPCs := []uint64{}

	for fn, pcs := range pcByFunc {

		// 3. for each concrete function split instruction between the inlined functions it contains

		if strings.Contains(fn.Name, "·dwrap·") || fn.trampoline {
			// skip autogenerated functions
			continue
		}

		dwtree, err := fn.cu.image.getDwarfTree(fn.offset)
		if err != nil {
			return nil, fmt.Errorf("loading DWARF for %s@%#x: %v", fn.Name, fn.offset, err)
		}
		inlrngs := allInlineCallRanges(dwtree)

		// findInlRng returns the DWARF offset of the inlined call containing pc.
		// If multiple nested inlined calls contain pc the deepest one is returned
		// (since allInlineCallRanges returns inlined call by decreasing depth
		// this is the first matching entry of the slice).
		findInlRng := func(pc uint64) dwarf.Offset {
			for _, inlrng := range inlrngs {
				if inlrng.rng[0] <= pc && pc < inlrng.rng[1] {
					return inlrng.off
				}
			}
			return fn.offset
		}

		pcsByOff := map[dwarf.Offset][]line.PCStmt{}

		for _, pc := range pcs {
			off := findInlRng(pc.PC)
			pcsByOff[off] = append(pcsByOff[off], pc)
		}

		// 4. pick the first instruction with stmt set for each inlined call as
		//    well as the main body of the concrete function. If nothing has
		//    is_stmt set pick the first instruction instead.

		for off, pcs := range pcsByOff {
			sort.Slice(pcs, func(i, j int) bool { return pcs[i].PC < pcs[j].PC })

			var selectedPC uint64
			for _, pc := range pcs {
				if pc.Stmt {
					selectedPC = pc.PC
					break
				}
			}

			if selectedPC == 0 && len(pcs) > 0 {
				selectedPC = pcs[0].PC
			}

			if selectedPC == 0 {
				continue
			}

			// 5. if we picked the entry point of the function, skip it

			if off == fn.offset && fn.Entry == selectedPC {
				selectedPC, _ = FirstPCAfterPrologue(p, fn, true)
			}

			selectedPCs = append(selectedPCs, selectedPC)
		}
	}

	return selectedPCs, nil
}

// inlRnage is the range of an inlined call
type inlRange struct {
	off   dwarf.Offset
	depth uint32
	rng   [2]uint64
}

// allInlineCallRanges returns all inlined calls contained inside 'tree' in
// reverse nesting order (i.e. the most nested calls are returned first).
// Note that a single inlined call might not have a continuous range of
// addresses and therefore appear multiple times in the returned slice.
func allInlineCallRanges(tree *godwarf.Tree) []inlRange {
	r := []inlRange{}

	var visit func(*godwarf.Tree, uint32)
	visit = func(n *godwarf.Tree, depth uint32) {
		if n.Tag == dwarf.TagInlinedSubroutine {
			for _, rng := range n.Ranges {
				r = append(r, inlRange{off: n.Offset, depth: depth, rng: rng})
			}
		}
		for _, child := range n.Children {
			visit(child, depth+1)
		}
	}
	visit(tree, 0)

	sort.SliceStable(r, func(i, j int) bool { return r[i].depth > r[j].depth })
	return r
}

// FindFunctionLocation finds address of a function's line
// If lineOffset is passed FindFunctionLocation will return the address of that line
func FindFunctionLocation(p Process, funcName string, lineOffset int) ([]uint64, error) {
	bi := p.BinInfo()
	origfn := bi.LookupFunc[funcName]
	if origfn == nil {
		return nil, &ErrFunctionNotFound{funcName}
	}

	if lineOffset > 0 {
		filename, lineno := origfn.cu.lineInfo.PCToLine(origfn.Entry, origfn.Entry)
		return FindFileLocation(p, filename, lineno+lineOffset)
	}

	r := make([]uint64, 0, len(origfn.InlinedCalls)+1)
	if origfn.Entry > 0 {
		// add concrete implementation of the function
		pc, err := FirstPCAfterPrologue(p, origfn, false)
		if err != nil {
			return nil, err
		}
		r = append(r, pc)
	}
	// add inlined calls to the function
	for _, call := range origfn.InlinedCalls {
		r = append(r, call.LowPC)
	}
	if len(r) == 0 {
		return nil, &ErrFunctionNotFound{funcName}
	}
	return r, nil
}

// FirstPCAfterPrologue returns the address of the first
// instruction after the prologue for function fn.
// If sameline is set FirstPCAfterPrologue will always return an
// address associated with the same line as fn.Entry.
func FirstPCAfterPrologue(p Process, fn *Function, sameline bool) (uint64, error) {
	pc, _, line, ok := fn.cu.lineInfo.PrologueEndPC(fn.Entry, fn.End)
	if ok {
		if !sameline {
			return pc, nil
		}
		_, entryLine := fn.cu.lineInfo.PCToLine(fn.Entry, fn.Entry)
		if entryLine == line {
			return pc, nil
		}
	}

	pc, err := firstPCAfterPrologueDisassembly(p, fn, sameline)
	if err != nil {
		return fn.Entry, err
	}

	if pc == fn.Entry {
		// Look for the first instruction with the stmt flag set, so that setting a
		// breakpoint with file:line and with the function name always result on
		// the same instruction being selected.
		if pc2, _, _, ok := fn.cu.lineInfo.FirstStmtForLine(fn.Entry, fn.End); ok {
			return pc2, nil
		}
	}

	return pc, nil
}

// cpuArch is a stringer interface representing CPU architectures.
type cpuArch interface {
	String() string
}

// ErrUnsupportedArch is returned when attempting to debug a binary compiled for an unsupported architecture.
type ErrUnsupportedArch struct {
	os      string
	cpuArch cpuArch
}

func (e *ErrUnsupportedArch) Error() string {
	var supportArchs []cpuArch
	switch e.os {
	case "linux":
		for linuxArch := range supportedLinuxArch {
			supportArchs = append(supportArchs, linuxArch)
		}
	case "windows":
		for windowArch := range supportedWindowsArch {
			supportArchs = append(supportArchs, windowArch)
		}
	case "darwin":
		for darwinArch := range supportedDarwinArch {
			supportArchs = append(supportArchs, darwinArch)
		}
	}

	errStr := "unsupported architecture of " + e.os + "/" + e.cpuArch.String()
	errStr += " - only"
	for _, arch := range supportArchs {
		errStr += " " + e.os + "/" + arch.String() + " "
	}
	if len(supportArchs) == 1 {
		errStr += "is supported"
	} else {
		errStr += "are supported"
	}

	return errStr
}

type compileUnit struct {
	name    string // univocal name for non-go compile units
	Version uint8  // DWARF version of this compile unit
	lowPC   uint64
	ranges  [][2]uint64

	entry     *dwarf.Entry        // debug_info entry describing this compile unit
	isgo      bool                // true if this is the go compile unit
	lineInfo  *line.DebugLineInfo // debug_line segment associated with this compile unit
	optimized bool                // this compile unit is optimized
	producer  string              // producer attribute

	offset dwarf.Offset // offset of the entry describing the compile unit

	image *Image // parent image of this compilation unit.
}

type fileLine struct {
	file string
	line int
}

// dwarfRef is a reference to a Debug Info Entry inside a shared object.
type dwarfRef struct {
	imageIndex int
	offset     dwarf.Offset
}

// InlinedCall represents a concrete inlined call to a function.
type InlinedCall struct {
	cu            *compileUnit
	LowPC, HighPC uint64 // Address range of the generated inlined instructions
}

// Function describes a function in the target program.
type Function struct {
	Name       string
	Entry, End uint64 // same as DW_AT_lowpc and DW_AT_highpc
	offset     dwarf.Offset
	cu         *compileUnit

	trampoline bool // DW_AT_trampoline attribute set to true

	// InlinedCalls lists all inlined calls to this function
	InlinedCalls []InlinedCall
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

// PrologueEndPC returns the PC just after the function prologue
func (fn *Function) PrologueEndPC() uint64 {
	pc, _, _, ok := fn.cu.lineInfo.PrologueEndPC(fn.Entry, fn.End)
	if !ok {
		return fn.Entry
	}
	return pc
}

// From $GOROOT/src/runtime/traceback.go:597
// exportedRuntime reports whether the function is an exported runtime function.
// It is only for runtime functions, so ASCII A-Z is fine.
func (fn *Function) exportedRuntime() bool {
	name := fn.Name
	const n = len("runtime.")
	return len(name) > n && name[:n] == "runtime." && 'A' <= name[n] && name[n] <= 'Z'
}

// unexportedRuntime reports whether the function is a private runtime function.
func (fn *Function) privateRuntime() bool {
	name := fn.Name
	const n = len("runtime.")
	return len(name) > n && name[:n] == "runtime." && !('A' <= name[n] && name[n] <= 'Z')
}

type constantsMap map[dwarfRef]*constantType

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
	cu     *compileUnit
	offset dwarf.Offset
	addr   uint64
}

type buildIDHeader struct {
	Namesz uint32
	Descsz uint32
	Type   uint32
}

// ElfDynamicSection describes the .dynamic section of an ELF executable.
type ElfDynamicSection struct {
	Addr uint64 // relocated address of where the .dynamic section is mapped in memory
	Size uint64 // size of the .dynamic section of the executable
}

// NewBinaryInfo returns an initialized but unloaded BinaryInfo struct.
func NewBinaryInfo(goos, goarch string) *BinaryInfo {
	r := &BinaryInfo{GOOS: goos, nameOfRuntimeType: make(map[uint64]nameOfRuntimeTypeEntry), logger: logflags.DebuggerLogger()}

	// TODO: find better way to determine proc arch (perhaps use executable file info).
	switch goarch {
	case "386":
		r.Arch = I386Arch(goos)
	case "amd64":
		r.Arch = AMD64Arch(goos)
	case "arm64":
		r.Arch = ARM64Arch(goos)
	}
	return r
}

// LoadBinaryInfo will load and store the information from the binary at 'path'.
func (bi *BinaryInfo) LoadBinaryInfo(path string, entryPoint uint64, debugInfoDirs []string) error {
	fi, err := os.Stat(path)
	if err == nil {
		bi.lastModified = fi.ModTime()
	}

	bi.debugInfoDirectories = debugInfoDirs

	return bi.AddImage(path, entryPoint)
}

func loadBinaryInfo(bi *BinaryInfo, image *Image, path string, entryPoint uint64) error {
	var wg sync.WaitGroup
	defer wg.Wait()

	switch bi.GOOS {
	case "linux", "freebsd":
		return loadBinaryInfoElf(bi, image, path, entryPoint, &wg)
	case "windows":
		return loadBinaryInfoPE(bi, image, path, entryPoint, &wg)
	case "darwin":
		return loadBinaryInfoMacho(bi, image, path, entryPoint, &wg)
	}
	return errors.New("unsupported operating system")
}

// GStructOffset returns the offset of the G
// struct in thread local storage.
func (bi *BinaryInfo) GStructOffset() uint64 {
	return bi.gStructOffset
}

// LastModified returns the last modified time of the binary.
func (bi *BinaryInfo) LastModified() time.Time {
	return bi.lastModified
}

// DwarfReader returns a reader for the dwarf data
func (so *Image) DwarfReader() *reader.Reader {
	return reader.New(so.dwarf)
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

type ErrCouldNotFindLine struct {
	fileFound bool
	filename  string
	lineno    int
}

func (err *ErrCouldNotFindLine) Error() string {
	if err.fileFound {
		return fmt.Sprintf("could not find statement at %s:%d, please use a line with a statement", err.filename, err.lineno)
	}
	return fmt.Sprintf("could not find file %s", err.filename)
}

// AllPCsForFileLines returns a map providing all PC addresses for filename and each line in linenos
func (bi *BinaryInfo) AllPCsForFileLines(filename string, linenos []int) map[int][]uint64 {
	r := make(map[int][]uint64)
	for _, line := range linenos {
		r[line] = make([]uint64, 0, 1)
	}
	for _, image := range bi.Images {
		for _, cu := range image.compileUnits {
			if cu.lineInfo != nil && cu.lineInfo.Lookup[filename] != nil {
				cu.lineInfo.AllPCsForFileLines(filename, r)
			}
		}
	}
	return r
}

// PCToFunc returns the concrete function containing the given PC address.
// If the PC address belongs to an inlined call it will return the containing function.
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

// PCToImage returns the image containing the given PC address.
func (bi *BinaryInfo) PCToImage(pc uint64) *Image {
	fn := bi.PCToFunc(pc)
	return bi.funcToImage(fn)
}

// Image represents a loaded library file (shared object on linux, DLL on windows).
type Image struct {
	Path       string
	StaticBase uint64
	addr       uint64

	index int // index of this object in BinaryInfo.SharedObjects

	closer         io.Closer
	sepDebugCloser io.Closer

	dwarf        *dwarf.Data
	dwarfReader  *dwarf.Reader
	loclist2     *loclist.Dwarf2Reader
	loclist5     *loclist.Dwarf5Reader
	debugAddr    *godwarf.DebugAddrSection
	debugLineStr []byte

	typeCache map[dwarf.Offset]godwarf.Type

	compileUnits []*compileUnit // compileUnits is sorted by increasing DWARF offset

	dwarfTreeCache      *simplelru.LRU
	runtimeMallocgcTree *godwarf.Tree // patched version of runtime.mallocgc's DIE

	// runtimeTypeToDIE maps between the offset of a runtime._type in
	// runtime.moduledata.types and the offset of the DIE in debug_info. This
	// map is filled by using the extended attribute godwarf.AttrGoRuntimeType
	// which was added in go 1.11.
	runtimeTypeToDIE map[uint64]runtimeTypeDIE

	loadErrMu sync.Mutex
	loadErr   error
}

func (image *Image) registerRuntimeTypeToDIE(entry *dwarf.Entry, ardr *reader.Reader) {
	if off, ok := entry.Val(godwarf.AttrGoRuntimeType).(uint64); ok {
		if _, ok := image.runtimeTypeToDIE[off]; !ok {
			image.runtimeTypeToDIE[off] = runtimeTypeDIE{entry.Offset, -1}
		}
	}
}

// AddImage adds the specified image to bi, loading data asynchronously.
// Addr is the relocated entry point for the executable and staticBase (i.e.
// the relocation offset) for all other images.
// The first image added must be the executable file.
func (bi *BinaryInfo) AddImage(path string, addr uint64) error {
	// Check if the image is already present.
	if len(bi.Images) > 0 && !strings.HasPrefix(path, "/") {
		return nil
	}
	for _, image := range bi.Images {
		if image.Path == path && image.addr == addr {
			return nil
		}
	}

	// Actually add the image.
	image := &Image{Path: path, addr: addr, typeCache: make(map[dwarf.Offset]godwarf.Type)}
	image.dwarfTreeCache, _ = simplelru.NewLRU(dwarfTreeCacheSize, nil)

	// add Image regardless of error so that we don't attempt to re-add it every time we stop
	image.index = len(bi.Images)
	bi.Images = append(bi.Images, image)
	err := loadBinaryInfo(bi, image, path, addr)
	if err != nil {
		bi.Images[len(bi.Images)-1].loadErr = err
	}
	bi.macOSDebugFrameBugWorkaround()
	return err
}

// moduleDataToImage finds the image corresponding to the given module data object.
func (bi *BinaryInfo) moduleDataToImage(md *moduleData) *Image {
	fn := bi.PCToFunc(uint64(md.text))
	if fn != nil {
		return bi.funcToImage(fn)
	}
	// Try searching for the image with the closest address preceding md.text
	var so *Image
	for i := range bi.Images {
		if int64(bi.Images[i].StaticBase) > int64(md.text) {
			continue
		}
		if so == nil || int64(bi.Images[i].StaticBase) > int64(so.StaticBase) {
			so = bi.Images[i]
		}
	}
	return so
}

// imageToModuleData finds the module data in mds corresponding to the given image.
func (bi *BinaryInfo) imageToModuleData(image *Image, mds []moduleData) *moduleData {
	for _, md := range mds {
		im2 := bi.moduleDataToImage(&md)
		if im2 != nil && im2.index == image.index {
			return &md
		}
	}
	return nil
}

// typeToImage returns the image containing the give type.
func (bi *BinaryInfo) typeToImage(typ godwarf.Type) *Image {
	return bi.Images[typ.Common().Index]
}

var errBinaryInfoClose = errors.New("multiple errors closing executable files")

// Close closes all internal readers.
func (bi *BinaryInfo) Close() error {
	var errs []error
	for _, image := range bi.Images {
		if err := image.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	switch len(errs) {
	case 0:
		return nil
	case 1:
		return errs[0]
	default:
		return errBinaryInfoClose
	}
}

func (image *Image) Close() error {
	var err1, err2 error
	if image.sepDebugCloser != nil {
		err := image.sepDebugCloser.Close()
		if err != nil {
			err1 = fmt.Errorf("closing shared object %q (split dwarf): %v", image.Path, err)
		}
	}
	if image.closer != nil {
		err := image.closer.Close()
		if err != nil {
			err2 = fmt.Errorf("closing shared object %q: %v", image.Path, err)
		}
	}
	if err1 != nil && err2 != nil {
		return errBinaryInfoClose
	}
	if err1 != nil {
		return err1
	}
	return err2
}

func (image *Image) setLoadError(fmtstr string, args ...interface{}) {
	image.loadErrMu.Lock()
	image.loadErr = fmt.Errorf(fmtstr, args...)
	image.loadErrMu.Unlock()
}

// LoadError returns any error incurred while loading this image.
func (image *Image) LoadError() error {
	return image.loadErr
}

func (image *Image) getDwarfTree(off dwarf.Offset) (*godwarf.Tree, error) {
	if image.runtimeMallocgcTree != nil && off == image.runtimeMallocgcTree.Offset {
		return image.runtimeMallocgcTree, nil
	}
	if r, ok := image.dwarfTreeCache.Get(off); ok {
		return r.(*godwarf.Tree), nil
	}
	r, err := godwarf.LoadTree(off, image.dwarf, image.StaticBase)
	if err != nil {
		return nil, err
	}
	image.dwarfTreeCache.Add(off, r)
	return r, nil
}

type nilCloser struct{}

func (c *nilCloser) Close() error { return nil }

// LoadImageFromData creates a new Image, using the specified data, and adds it to bi.
// This is used for debugging BinaryInfo, you should use LoadBinary instead.
func (bi *BinaryInfo) LoadImageFromData(dwdata *dwarf.Data, debugFrameBytes, debugLineBytes, debugLocBytes []byte) {
	image := &Image{}
	image.closer = (*nilCloser)(nil)
	image.sepDebugCloser = (*nilCloser)(nil)
	image.dwarf = dwdata
	image.typeCache = make(map[dwarf.Offset]godwarf.Type)
	image.dwarfTreeCache, _ = simplelru.NewLRU(dwarfTreeCacheSize, nil)

	if debugFrameBytes != nil {
		bi.frameEntries, _ = frame.Parse(debugFrameBytes, frame.DwarfEndian(debugFrameBytes), 0, bi.Arch.PtrSize(), 0)
	}

	image.loclist2 = loclist.NewDwarf2Reader(debugLocBytes, bi.Arch.PtrSize())

	bi.loadDebugInfoMaps(image, nil, debugLineBytes, nil, nil)

	bi.Images = append(bi.Images, image)
}

func (bi *BinaryInfo) locationExpr(entry godwarf.Entry, attr dwarf.Attr, pc uint64) ([]byte, *locationExpr, error) {
	//TODO(aarzilli): handle DW_FORM_loclistx attribute form new in DWARFv5
	a := entry.Val(attr)
	if a == nil {
		return nil, nil, fmt.Errorf("no location attribute %s", attr)
	}
	if instr, ok := a.([]byte); ok {
		return instr, &locationExpr{isBlock: true, instr: instr}, nil
	}
	off, ok := a.(int64)
	if !ok {
		return nil, nil, fmt.Errorf("could not interpret location attribute %s", attr)
	}
	instr := bi.loclistEntry(off, pc)
	if instr == nil {
		return nil, nil, fmt.Errorf("could not find loclist entry at %#x for address %#x", off, pc)
	}
	return instr, &locationExpr{pc: pc, off: off, instr: instr}, nil
}

type locationExpr struct {
	isBlock   bool
	isEscaped bool
	off       int64
	pc        uint64
	instr     []byte
}

func (le *locationExpr) String() string {
	if le == nil {
		return ""
	}
	var descr bytes.Buffer

	if le.isBlock {
		fmt.Fprintf(&descr, "[block] ")
		op.PrettyPrint(&descr, le.instr)
	} else {
		fmt.Fprintf(&descr, "[%#x:%#x] ", le.off, le.pc)
		op.PrettyPrint(&descr, le.instr)
	}

	if le.isEscaped {
		fmt.Fprintf(&descr, " (escaped)")
	}
	return descr.String()
}

// LocationCovers returns the list of PC addresses that is covered by the
// location attribute 'attr' of entry 'entry'.
func (bi *BinaryInfo) LocationCovers(entry *dwarf.Entry, attr dwarf.Attr) ([][2]uint64, error) {
	a := entry.Val(attr)
	if a == nil {
		return nil, fmt.Errorf("attribute %s not found", attr)
	}
	if _, isblock := a.([]byte); isblock {
		return [][2]uint64{[2]uint64{0, ^uint64(0)}}, nil
	}

	off, ok := a.(int64)
	if !ok {
		return nil, fmt.Errorf("attribute %s of unsupported type %T", attr, a)
	}
	cu := bi.Images[0].findCompileUnitForOffset(entry.Offset)
	if cu == nil {
		return nil, errors.New("could not find compile unit")
	}
	if cu.Version >= 5 && cu.image.loclist5 != nil {
		return nil, errors.New("LocationCovers does not support DWARFv5")
	}

	image := cu.image
	base := cu.lowPC
	if image == nil || image.loclist2.Empty() {
		return nil, errors.New("malformed executable")
	}

	r := [][2]uint64{}
	var e loclist.Entry
	image.loclist2.Seek(int(off))
	for image.loclist2.Next(&e) {
		if e.BaseAddressSelection() {
			base = e.HighPC
			continue
		}
		r = append(r, [2]uint64{e.LowPC + base, e.HighPC + base})
	}
	return r, nil
}

// Location returns the location described by attribute attr of entry.
// This will either be an int64 address or a slice of Pieces for locations
// that don't correspond to a single memory address (registers, composite
// locations).
func (bi *BinaryInfo) Location(entry godwarf.Entry, attr dwarf.Attr, pc uint64, regs op.DwarfRegisters, mem MemoryReadWriter) (int64, []op.Piece, *locationExpr, error) {
	instr, descr, err := bi.locationExpr(entry, attr, pc)
	if err != nil {
		return 0, nil, nil, err
	}
	readMemory := op.ReadMemoryFunc(nil)
	if mem != nil {
		readMemory = mem.ReadMemory
	}
	addr, pieces, err := op.ExecuteStackProgram(regs, instr, bi.Arch.PtrSize(), readMemory)
	return addr, pieces, descr, err
}

// loclistEntry returns the loclist entry in the loclist starting at off,
// for address pc.
func (bi *BinaryInfo) loclistEntry(off int64, pc uint64) []byte {
	var base uint64
	image := bi.Images[0]
	cu := bi.findCompileUnit(pc)
	if cu != nil {
		base = cu.lowPC
		image = cu.image
	}
	if image == nil {
		return nil
	}

	var loclist loclist.Reader = image.loclist2
	var debugAddr *godwarf.DebugAddr
	if cu != nil && cu.Version >= 5 && image.loclist5 != nil {
		loclist = image.loclist5
		if addrBase, ok := cu.entry.Val(dwarfAttrAddrBase).(int64); ok {
			debugAddr = image.debugAddr.GetSubsection(uint64(addrBase))
		}
	}

	if loclist.Empty() {
		return nil
	}

	e, err := loclist.Find(int(off), image.StaticBase, base, pc, debugAddr)
	if err != nil {
		bi.logger.Errorf("error reading loclist section: %v", err)
		return nil
	}
	if e != nil {
		return e.Instr
	}

	return nil
}

// findCompileUnit returns the compile unit containing address pc.
func (bi *BinaryInfo) findCompileUnit(pc uint64) *compileUnit {
	for _, image := range bi.Images {
		for _, cu := range image.compileUnits {
			for _, rng := range cu.ranges {
				if pc >= rng[0] && pc < rng[1] {
					return cu
				}
			}
		}
	}
	return nil
}

func (bi *Image) findCompileUnitForOffset(off dwarf.Offset) *compileUnit {
	i := sort.Search(len(bi.compileUnits), func(i int) bool {
		return bi.compileUnits[i].offset >= off
	})
	if i > 0 {
		i--
	}
	return bi.compileUnits[i]
}

// Producer returns the value of DW_AT_producer.
func (bi *BinaryInfo) Producer() string {
	for _, cu := range bi.Images[0].compileUnits {
		if cu.isgo && cu.producer != "" {
			return cu.producer
		}
	}
	return ""
}

// Type returns the Dwarf type entry at `offset`.
func (image *Image) Type(offset dwarf.Offset) (godwarf.Type, error) {
	return godwarf.ReadType(image.dwarf, image.index, offset, image.typeCache)
}

// funcToImage returns the Image containing function fn, or the
// executable file as a fallback.
func (bi *BinaryInfo) funcToImage(fn *Function) *Image {
	if fn == nil {
		return bi.Images[0]
	}
	return fn.cu.image
}

// parseDebugFrameGeneral parses a debug_frame and a eh_frame section.
// At least one of the two must be present and parsed correctly, if
// debug_frame is present it must be parsable correctly.
func (bi *BinaryInfo) parseDebugFrameGeneral(image *Image, debugFrameBytes []byte, debugFrameName string, debugFrameErr error, ehFrameBytes []byte, ehFrameAddr uint64, ehFrameName string, byteOrder binary.ByteOrder) {
	if debugFrameBytes == nil && ehFrameBytes == nil {
		image.setLoadError("could not get %s section: %v", debugFrameName, debugFrameErr)
		return
	}

	if debugFrameBytes != nil {
		fe, err := frame.Parse(debugFrameBytes, byteOrder, image.StaticBase, bi.Arch.PtrSize(), 0)
		if err != nil {
			image.setLoadError("could not parse %s section: %v", debugFrameName, err)
			return
		}
		bi.frameEntries = bi.frameEntries.Append(fe)
	}

	if ehFrameBytes != nil && ehFrameAddr > 0 {
		fe, err := frame.Parse(ehFrameBytes, byteOrder, image.StaticBase, bi.Arch.PtrSize(), ehFrameAddr)
		if err != nil {
			if debugFrameBytes == nil {
				image.setLoadError("could not parse %s section: %v", ehFrameName, err)
				return
			}
			bi.logger.Warnf("could not parse %s section: %v", ehFrameName, err)
			return
		}
		bi.frameEntries = bi.frameEntries.Append(fe)
	}
}

// ELF ///////////////////////////////////////////////////////////////

// ErrNoBuildIDNote is used in openSeparateDebugInfo to signal there's no
// build-id note on the binary, so LoadBinaryInfoElf will return
// the error message coming from elfFile.DWARF() instead.
type ErrNoBuildIDNote struct{}

func (e *ErrNoBuildIDNote) Error() string {
	return "can't find build-id note on binary"
}

// openSeparateDebugInfo searches for a file containing the separate
// debug info for the binary using the "build ID" method as described
// in GDB's documentation [1], and if found returns two handles, one
// for the bare file, and another for its corresponding elf.File.
// [1] https://sourceware.org/gdb/onlinedocs/gdb/Separate-Debug-Files.html
//
// Alternatively, if the debug file cannot be found be the build-id, Delve
// will look in directories specified by the debug-info-directories config value.
func (bi *BinaryInfo) openSeparateDebugInfo(image *Image, exe *elf.File, debugInfoDirectories []string) (*os.File, *elf.File, error) {
	var debugFilePath string
	desc1, desc2, _ := parseBuildID(exe)
	for _, dir := range debugInfoDirectories {
		var potentialDebugFilePath string
		if strings.Contains(dir, "build-id") {
			potentialDebugFilePath = fmt.Sprintf("%s/%s/%s.debug", dir, desc1, desc2)
		} else if strings.HasPrefix(image.Path, "/proc") {
			path, err := filepath.EvalSymlinks(image.Path)
			if err == nil {
				potentialDebugFilePath = fmt.Sprintf("%s/%s.debug", dir, filepath.Base(path))
			}
		} else {
			potentialDebugFilePath = fmt.Sprintf("%s/%s.debug", dir, filepath.Base(image.Path))
		}
		_, err := os.Stat(potentialDebugFilePath)
		if err == nil {
			debugFilePath = potentialDebugFilePath
			break
		}
	}
	// We cannot find the debug information locally on the system. Try and see if we're on a system that
	// has debuginfod so that we can use that in order to find any relevant debug information.
	if debugFilePath == "" {
		const debuginfodFind = "debuginfod-find"
		if _, err := exec.LookPath(debuginfodFind); err == nil {
			cmd := exec.Command(debuginfodFind, "debuginfo", desc1+desc2)
			out, err := cmd.CombinedOutput()
			if err != nil {
				return nil, nil, ErrNoDebugInfoFound
			}
			debugFilePath = string(out)
		} else {
			return nil, nil, ErrNoDebugInfoFound
		}
	}
	sepFile, err := os.OpenFile(debugFilePath, 0, os.ModePerm)
	if err != nil {
		return nil, nil, errors.New("can't open separate debug file: " + err.Error())
	}

	elfFile, err := elf.NewFile(sepFile)
	if err != nil {
		sepFile.Close()
		return nil, nil, fmt.Errorf("can't open separate debug file %q: %v", debugFilePath, err.Error())
	}

	if !supportedLinuxArch[elfFile.Machine] {
		sepFile.Close()
		return nil, nil, fmt.Errorf("can't open separate debug file %q: %v", debugFilePath, &ErrUnsupportedArch{os: "linux", cpuArch: elfFile.Machine})
	}

	return sepFile, elfFile, nil
}

func parseBuildID(exe *elf.File) (string, string, error) {
	buildid := exe.Section(".note.gnu.build-id")
	if buildid == nil {
		return "", "", &ErrNoBuildIDNote{}
	}

	br := buildid.Open()
	bh := new(buildIDHeader)
	if err := binary.Read(br, binary.LittleEndian, bh); err != nil {
		return "", "", errors.New("can't read build-id header: " + err.Error())
	}

	name := make([]byte, bh.Namesz)
	if err := binary.Read(br, binary.LittleEndian, name); err != nil {
		return "", "", errors.New("can't read build-id name: " + err.Error())
	}

	if strings.TrimSpace(string(name)) != "GNU\x00" {
		return "", "", errors.New("invalid build-id signature")
	}

	descBinary := make([]byte, bh.Descsz)
	if err := binary.Read(br, binary.LittleEndian, descBinary); err != nil {
		return "", "", errors.New("can't read build-id desc: " + err.Error())
	}
	desc := hex.EncodeToString(descBinary)
	return desc[:2], desc[2:], nil
}

// loadBinaryInfoElf specifically loads information from an ELF binary.
func loadBinaryInfoElf(bi *BinaryInfo, image *Image, path string, addr uint64, wg *sync.WaitGroup) error {
	exe, err := os.OpenFile(path, 0, os.ModePerm)
	if err != nil {
		return err
	}
	image.closer = exe
	elfFile, err := elf.NewFile(exe)
	if err != nil {
		return err
	}
	if !supportedLinuxArch[elfFile.Machine] {
		return &ErrUnsupportedArch{os: "linux", cpuArch: elfFile.Machine}
	}

	if image.index == 0 {
		// adding executable file:
		// - addr is entryPoint therefore staticBase needs to be calculated by
		//   subtracting the entry point specified in the executable file from addr.
		// - memory address of the .dynamic section needs to be recorded in
		//   BinaryInfo so that we can find loaded libraries.
		if addr != 0 {
			image.StaticBase = addr - elfFile.Entry
		} else if elfFile.Type == elf.ET_DYN {
			return ErrCouldNotDetermineRelocation
		}
		if dynsec := elfFile.Section(".dynamic"); dynsec != nil {
			bi.ElfDynamicSection.Addr = dynsec.Addr + image.StaticBase
			bi.ElfDynamicSection.Size = dynsec.Size
		}
	} else {
		image.StaticBase = addr
	}

	dwarfFile := elfFile

	var debugInfoBytes []byte
	image.dwarf, err = elfFile.DWARF()
	if err != nil {
		var sepFile *os.File
		var serr error
		sepFile, dwarfFile, serr = bi.openSeparateDebugInfo(image, elfFile, bi.debugInfoDirectories)
		if serr != nil {
			return serr
		}
		image.sepDebugCloser = sepFile
		image.dwarf, err = dwarfFile.DWARF()
		if err != nil {
			return err
		}
	}

	debugInfoBytes, err = godwarf.GetDebugSectionElf(dwarfFile, "info")
	if err != nil {
		return err
	}

	image.dwarfReader = image.dwarf.Reader()

	debugLineBytes, err := godwarf.GetDebugSectionElf(dwarfFile, "line")
	if err != nil {
		return err
	}
	debugLocBytes, _ := godwarf.GetDebugSectionElf(dwarfFile, "loc")
	image.loclist2 = loclist.NewDwarf2Reader(debugLocBytes, bi.Arch.PtrSize())
	debugLoclistBytes, _ := godwarf.GetDebugSectionElf(dwarfFile, "loclists")
	image.loclist5 = loclist.NewDwarf5Reader(debugLoclistBytes)
	debugAddrBytes, _ := godwarf.GetDebugSectionElf(dwarfFile, "addr")
	image.debugAddr = godwarf.ParseAddr(debugAddrBytes)
	debugLineStrBytes, _ := godwarf.GetDebugSectionElf(dwarfFile, "line_str")
	image.debugLineStr = debugLineStrBytes

	wg.Add(3)
	go bi.parseDebugFrameElf(image, dwarfFile, debugInfoBytes, wg)
	go bi.loadDebugInfoMaps(image, debugInfoBytes, debugLineBytes, wg, nil)
	go bi.loadSymbolName(image, elfFile, wg)
	if image.index == 0 {
		// determine g struct offset only when loading the executable file
		wg.Add(1)
		go bi.setGStructOffsetElf(image, dwarfFile, wg)
	}
	return nil
}

//  _STT_FUNC is a code object, see /usr/include/elf.h for a full definition.
const _STT_FUNC = 2

func (bi *BinaryInfo) loadSymbolName(image *Image, file *elf.File, wg *sync.WaitGroup) {
	defer wg.Done()
	if bi.SymNames == nil {
		bi.SymNames = make(map[uint64]*elf.Symbol)
	}
	symSecs, _ := file.Symbols()
	for _, symSec := range symSecs {
		if symSec.Info == _STT_FUNC { // TODO(chainhelen), need to parse others types.
			s := symSec
			bi.SymNames[symSec.Value+image.StaticBase] = &s
		}
	}
}

func (bi *BinaryInfo) parseDebugFrameElf(image *Image, exe *elf.File, debugInfoBytes []byte, wg *sync.WaitGroup) {
	defer wg.Done()

	debugFrameData, debugFrameErr := godwarf.GetDebugSectionElf(exe, "frame")
	ehFrameSection := exe.Section(".eh_frame")
	var ehFrameData []byte
	var ehFrameAddr uint64
	if ehFrameSection != nil {
		ehFrameAddr = ehFrameSection.Addr
		ehFrameData, _ = ehFrameSection.Data()
	}

	bi.parseDebugFrameGeneral(image, debugFrameData, ".debug_frame", debugFrameErr, ehFrameData, ehFrameAddr, ".eh_frame", frame.DwarfEndian(debugInfoBytes))
}

func (bi *BinaryInfo) setGStructOffsetElf(image *Image, exe *elf.File, wg *sync.WaitGroup) {
	defer wg.Done()

	// This is a bit arcane. Essentially:
	// - If the program is pure Go, it can do whatever it wants, and puts the G
	//   pointer at %fs-8 on 64 bit.
	// - %Gs is the index of private storage in GDT on 32 bit, and puts the G
	//   pointer at -4(tls).
	// - Otherwise, Go asks the external linker to place the G pointer by
	//   emitting runtime.tlsg, a TLS symbol, which is relocated to the chosen
	//   offset in libc's TLS block.
	// - On ARM64 (but really, any architecture other than i386 and 86x64) the
	//   offset is calculate using runtime.tls_g and the formula is different.

	var tls *elf.Prog
	for _, prog := range exe.Progs {
		if prog.Type == elf.PT_TLS {
			tls = prog
			break
		}
	}

	switch exe.Machine {
	case elf.EM_X86_64, elf.EM_386:
		tlsg := getSymbol(image, exe, "runtime.tlsg")
		if tlsg == nil || tls == nil {
			bi.gStructOffset = ^uint64(bi.Arch.PtrSize()) + 1 //-ptrSize
			return
		}

		// According to https://reviews.llvm.org/D61824, linkers must pad the actual
		// size of the TLS segment to ensure that (tlsoffset%align) == (vaddr%align).
		// This formula, copied from the lld code, matches that.
		// https://github.com/llvm-mirror/lld/blob/9aef969544981d76bea8e4d1961d3a6980980ef9/ELF/InputSection.cpp#L643
		memsz := tls.Memsz + (-tls.Vaddr-tls.Memsz)&(tls.Align-1)

		// The TLS register points to the end of the TLS block, which is
		// tls.Memsz long. runtime.tlsg is an offset from the beginning of that block.
		bi.gStructOffset = ^(memsz) + 1 + tlsg.Value // -tls.Memsz + tlsg.Value

	case elf.EM_AARCH64:
		tlsg := getSymbol(image, exe, "runtime.tls_g")
		if tlsg == nil || tls == nil {
			bi.gStructOffset = 2 * uint64(bi.Arch.PtrSize())
			return
		}

		bi.gStructOffset = tlsg.Value + uint64(bi.Arch.PtrSize()*2) + ((tls.Vaddr - uint64(bi.Arch.PtrSize()*2)) & (tls.Align - 1))

	default:
		// we should never get here
		panic("architecture not supported")
	}
}

func getSymbol(image *Image, exe *elf.File, name string) *elf.Symbol {
	symbols, err := exe.Symbols()
	if err != nil {
		image.setLoadError("could not parse ELF symbols: %v", err)
		return nil
	}

	for _, symbol := range symbols {
		if symbol.Name == name {
			s := symbol
			return &s
		}
	}
	return nil
}

// PE ////////////////////////////////////////////////////////////////

const _IMAGE_DLLCHARACTERISTICS_DYNAMIC_BASE = 0x0040

// loadBinaryInfoPE specifically loads information from a PE binary.
func loadBinaryInfoPE(bi *BinaryInfo, image *Image, path string, entryPoint uint64, wg *sync.WaitGroup) error {
	peFile, closer, err := openExecutablePathPE(path)
	if err != nil {
		return err
	}
	image.closer = closer
	cpuArch := _PEMachine(peFile.Machine)
	if !supportedWindowsArch[cpuArch] {
		return &ErrUnsupportedArch{os: "windows", cpuArch: cpuArch}
	}
	image.dwarf, err = peFile.DWARF()
	if err != nil {
		return err
	}
	debugInfoBytes, err := godwarf.GetDebugSectionPE(peFile, "info")
	if err != nil {
		return err
	}

	//TODO(aarzilli): actually test this when Go supports PIE buildmode on Windows.
	opth := peFile.OptionalHeader.(*pe.OptionalHeader64)
	if entryPoint != 0 {
		image.StaticBase = entryPoint - opth.ImageBase
	} else {
		if opth.DllCharacteristics&_IMAGE_DLLCHARACTERISTICS_DYNAMIC_BASE != 0 {
			return ErrCouldNotDetermineRelocation
		}
	}

	image.dwarfReader = image.dwarf.Reader()

	debugLineBytes, err := godwarf.GetDebugSectionPE(peFile, "line")
	if err != nil {
		return err
	}
	debugLocBytes, _ := godwarf.GetDebugSectionPE(peFile, "loc")
	image.loclist2 = loclist.NewDwarf2Reader(debugLocBytes, bi.Arch.PtrSize())
	debugLoclistBytes, _ := godwarf.GetDebugSectionPE(peFile, "loclists")
	image.loclist5 = loclist.NewDwarf5Reader(debugLoclistBytes)
	debugAddrBytes, _ := godwarf.GetDebugSectionPE(peFile, "addr")
	image.debugAddr = godwarf.ParseAddr(debugAddrBytes)

	wg.Add(2)
	go bi.parseDebugFramePE(image, peFile, debugInfoBytes, wg)
	go bi.loadDebugInfoMaps(image, debugInfoBytes, debugLineBytes, wg, nil)

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

func (bi *BinaryInfo) parseDebugFramePE(image *Image, exe *pe.File, debugInfoBytes []byte, wg *sync.WaitGroup) {
	defer wg.Done()

	debugFrameBytes, err := godwarf.GetDebugSectionPE(exe, "frame")
	bi.parseDebugFrameGeneral(image, debugFrameBytes, ".debug_frame", err, nil, 0, "", frame.DwarfEndian(debugInfoBytes))
}

// MACH-O ////////////////////////////////////////////////////////////

// loadBinaryInfoMacho specifically loads information from a Mach-O binary.
func loadBinaryInfoMacho(bi *BinaryInfo, image *Image, path string, entryPoint uint64, wg *sync.WaitGroup) error {
	exe, err := macho.Open(path)

	if err != nil {
		return err
	}

	if entryPoint != 0 {
		// This is a little bit hacky. We use the entryPoint variable, but it
		// actually holds the address of the mach-o header. We can use this
		// to calculate the offset to the non-aslr location of the mach-o header
		// (which is 0x100000000)
		image.StaticBase = entryPoint - 0x100000000
	}

	image.closer = exe
	if !supportedDarwinArch[exe.Cpu] {
		return &ErrUnsupportedArch{os: "darwin", cpuArch: exe.Cpu}
	}
	image.dwarf, err = exe.DWARF()
	if err != nil {
		return err
	}
	debugInfoBytes, err := godwarf.GetDebugSectionMacho(exe, "info")
	if err != nil {
		return err
	}

	image.dwarfReader = image.dwarf.Reader()

	debugLineBytes, err := godwarf.GetDebugSectionMacho(exe, "line")
	if err != nil {
		return err
	}
	debugLocBytes, _ := godwarf.GetDebugSectionMacho(exe, "loc")
	image.loclist2 = loclist.NewDwarf2Reader(debugLocBytes, bi.Arch.PtrSize())
	debugLoclistBytes, _ := godwarf.GetDebugSectionMacho(exe, "loclists")
	image.loclist5 = loclist.NewDwarf5Reader(debugLoclistBytes)
	debugAddrBytes, _ := godwarf.GetDebugSectionMacho(exe, "addr")
	image.debugAddr = godwarf.ParseAddr(debugAddrBytes)

	wg.Add(2)
	go bi.parseDebugFrameMacho(image, exe, debugInfoBytes, wg)
	go bi.loadDebugInfoMaps(image, debugInfoBytes, debugLineBytes, wg, bi.setGStructOffsetMacho)
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

func (bi *BinaryInfo) parseDebugFrameMacho(image *Image, exe *macho.File, debugInfoBytes []byte, wg *sync.WaitGroup) {
	defer wg.Done()

	debugFrameBytes, debugFrameErr := godwarf.GetDebugSectionMacho(exe, "frame")
	ehFrameSection := exe.Section("__eh_frame")
	var ehFrameBytes []byte
	var ehFrameAddr uint64
	if ehFrameSection != nil {
		ehFrameAddr = ehFrameSection.Addr
		ehFrameBytes, _ = ehFrameSection.Data()
	}

	bi.parseDebugFrameGeneral(image, debugFrameBytes, "__debug_frame", debugFrameErr, ehFrameBytes, ehFrameAddr, "__eh_frame", frame.DwarfEndian(debugInfoBytes))
}

// macOSDebugFrameBugWorkaround applies a workaround for:
//  https://github.com/golang/go/issues/25841
// It finds the Go function with the lowest entry point and the first
// debug_frame FDE, calculates the difference between the start of the
// function and the start of the FDE and sums it to all debug_frame FDEs.
// A number of additional checks are performed to make sure we don't ruin
// executables unaffected by this bug.
func (bi *BinaryInfo) macOSDebugFrameBugWorkaround() {
	//TODO: log extensively because of bugs in the field
	if bi.GOOS != "darwin" || bi.Arch.Name != "arm64" {
		return
	}
	if len(bi.Images) > 1 {
		// Only do this for the first executable, but it might work for plugins as
		// well if we had a way to distinguish where entries in bi.frameEntries
		// come from
		return
	}
	exe, ok := bi.Images[0].closer.(*macho.File)
	if !ok {
		return
	}
	if exe.Flags&macho.FlagPIE == 0 {
		bi.logger.Infof("debug_frame workaround not needed: not a PIE (%#x)", exe.Flags)
		return
	}

	// Find first Go function (first = lowest entry point)
	var fn *Function
	for i := range bi.Functions {
		if bi.Functions[i].cu.isgo && bi.Functions[i].Entry > 0 {
			fn = &bi.Functions[i]
			break
		}
	}
	if fn == nil {
		bi.logger.Warn("debug_frame workaround not applied: could not find a Go function")
		return
	}

	if fde, _ := bi.frameEntries.FDEForPC(fn.Entry); fde != nil {
		// Function is covered, no need to apply workaround
		bi.logger.Warnf("debug_frame workaround not applied: function %s (at %#x) covered by %#x-%#x", fn.Name, fn.Entry, fde.Begin(), fde.End())
		return
	}

	// Find lowest FDE in debug_frame
	var fde *frame.FrameDescriptionEntry
	for i := range bi.frameEntries {
		if bi.frameEntries[i].CIE.CIE_id == ^uint32(0) {
			fde = bi.frameEntries[i]
			break
		}
	}

	if fde == nil {
		bi.logger.Warnf("debug_frame workaround not applied because there are no debug_frame entries (%d)", len(bi.frameEntries))
		return
	}

	fnsize := fn.End - fn.Entry

	if fde.End()-fde.Begin() != fnsize || fde.Begin() > fn.Entry {
		bi.logger.Warnf("debug_frame workaround not applied: function %s (at %#x-%#x) has a different size than the first FDE (%#x-%#x) (or the FDE starts after the function)", fn.Name, fn.Entry, fn.End, fde.Begin(), fde.End())
		return
	}

	delta := fn.Entry - fde.Begin()

	bi.logger.Infof("applying debug_frame workaround +%#x: function %s (at %#x-%#x) and FDE %#x-%#x", delta, fn.Name, fn.Entry, fn.End, fde.Begin(), fde.End())

	for i := range bi.frameEntries {
		if bi.frameEntries[i].CIE.CIE_id == ^uint32(0) {
			bi.frameEntries[i].Translate(delta)
		}
	}
}

// Do not call this function directly it isn't able to deal correctly with package paths
func (bi *BinaryInfo) findType(name string) (godwarf.Type, error) {
	ref, found := bi.types[name]
	if !found {
		return nil, reader.ErrTypeNotFound
	}
	image := bi.Images[ref.imageIndex]
	return godwarf.ReadType(image.dwarf, ref.imageIndex, ref.offset, image.typeCache)
}

func (bi *BinaryInfo) findTypeExpr(expr ast.Expr) (godwarf.Type, error) {
	if lit, islit := expr.(*ast.BasicLit); islit && lit.Kind == token.STRING {
		// Allow users to specify type names verbatim as quoted
		// string. Useful as a catch-all workaround for cases where we don't
		// parse/serialize types correctly or can not resolve package paths.
		typn, _ := strconv.Unquote(lit.Value)

		// Check if the type in question is an array type, in which case we try to
		// fake it.
		if len(typn) > 0 && typn[0] == '[' {
			closedBrace := strings.Index(typn, "]")
			if closedBrace > 1 {
				n, err := strconv.Atoi(typn[1:closedBrace])
				if err == nil {
					return bi.findArrayType(n, typn[closedBrace+1:])
				}
			}
		}
		return bi.findType(typn)
	}
	bi.expandPackagesInType(expr)
	if snode, ok := expr.(*ast.StarExpr); ok {
		// Pointer types only appear in the dwarf informations when
		// a pointer to the type is used in the target program, here
		// we create a pointer type on the fly so that the user can
		// specify a pointer to any variable used in the target program
		ptyp, err := bi.findTypeExpr(snode.X)
		if err != nil {
			return nil, err
		}
		return pointerTo(ptyp, bi.Arch), nil
	}
	if anode, ok := expr.(*ast.ArrayType); ok {
		// Array types (for example [N]byte) are only present in DWARF if they are
		// used by the program, but it's convenient to make all of them available
		// to the user for two reasons:
		// 1. to allow reading arbitrary memory byte-by-byte (by casting an
		//    address to an array of bytes).
		// 2. to read the contents of a channel's buffer (we create fake array
		//    types for them)

		alen, litlen := anode.Len.(*ast.BasicLit)
		if litlen && alen.Kind == token.INT {
			n, _ := strconv.Atoi(alen.Value)
			return bi.findArrayType(n, exprToString(anode.Elt))
		}
	}
	return bi.findType(exprToString(expr))
}

func (bi *BinaryInfo) findArrayType(n int, etyp string) (godwarf.Type, error) {
	switch etyp {
	case "byte", "uint8":
		etyp = "uint8"
		fallthrough
	default:
		btyp, err := bi.findType(etyp)
		if err != nil {
			return nil, err
		}
		return fakeArrayType(uint64(n), btyp), nil
	}
}

func complexType(typename string) bool {
	for _, ch := range typename {
		switch ch {
		case '*', '[', '<', '{', '(', ' ':
			return true
		}
	}
	return false
}

func (bi *BinaryInfo) registerTypeToPackageMap(entry *dwarf.Entry) {
	if entry.Tag != dwarf.TagTypedef && entry.Tag != dwarf.TagBaseType && entry.Tag != dwarf.TagClassType && entry.Tag != dwarf.TagStructType {
		return
	}

	typename, ok := entry.Val(dwarf.AttrName).(string)
	if !ok || complexType(typename) {
		return
	}

	dot := strings.LastIndex(typename, ".")
	if dot < 0 {
		return
	}
	path := typename[:dot]
	slash := strings.LastIndex(path, "/")
	if slash < 0 || slash+1 >= len(path) {
		return
	}
	name := path[slash+1:]
	bi.PackageMap[name] = []string{path}
}

func (bi *BinaryInfo) loadDebugInfoMaps(image *Image, debugInfoBytes, debugLineBytes []byte, wg *sync.WaitGroup, cont func()) {
	if wg != nil {
		defer wg.Done()
	}

	if bi.types == nil {
		bi.types = make(map[string]dwarfRef)
	}
	if bi.consts == nil {
		bi.consts = make(map[dwarfRef]*constantType)
	}
	if bi.PackageMap == nil {
		bi.PackageMap = make(map[string][]string)
	}
	if bi.inlinedCallLines == nil {
		bi.inlinedCallLines = make(map[fileLine][]uint64)
	}
	if bi.dwrapUnwrapCache == nil {
		bi.dwrapUnwrapCache = make(map[uint64]*Function)
	}

	image.runtimeTypeToDIE = make(map[uint64]runtimeTypeDIE)

	ctxt := newLoadDebugInfoMapsContext(bi, image, util.ReadUnitVersions(debugInfoBytes))

	reader := image.DwarfReader()

	for entry, err := reader.Next(); entry != nil; entry, err = reader.Next() {
		if err != nil {
			image.setLoadError("error reading debug_info: %v", err)
			break
		}
		switch entry.Tag {
		case dwarf.TagCompileUnit:
			cu := &compileUnit{}
			cu.image = image
			cu.entry = entry
			cu.offset = entry.Offset
			cu.Version = ctxt.offsetToVersion[cu.offset]
			if lang, _ := entry.Val(dwarf.AttrLanguage).(int64); lang == dwarfGoLanguage {
				cu.isgo = true
			}
			cu.name, _ = entry.Val(dwarf.AttrName).(string)
			compdir, _ := entry.Val(dwarf.AttrCompDir).(string)
			if compdir != "" {
				cu.name = filepath.Join(compdir, cu.name)
			}
			cu.ranges, _ = image.dwarf.Ranges(entry)
			for i := range cu.ranges {
				cu.ranges[i][0] += image.StaticBase
				cu.ranges[i][1] += image.StaticBase
			}
			if len(cu.ranges) >= 1 {
				cu.lowPC = cu.ranges[0][0]
			}
			lineInfoOffset, hasLineInfo := entry.Val(dwarf.AttrStmtList).(int64)
			if hasLineInfo && lineInfoOffset >= 0 && lineInfoOffset < int64(len(debugLineBytes)) {
				var logfn func(string, ...interface{})
				if logflags.DebugLineErrors() {
					logger := logrus.New().WithFields(logrus.Fields{"layer": "dwarf-line"})
					logger.Logger.Level = logrus.DebugLevel
					logfn = func(fmt string, args ...interface{}) {
						logger.Printf(fmt, args)
					}
				}
				cu.lineInfo = line.Parse(compdir, bytes.NewBuffer(debugLineBytes[lineInfoOffset:]), image.debugLineStr, logfn, image.StaticBase, bi.GOOS == "windows", bi.Arch.PtrSize())
			}
			cu.producer, _ = entry.Val(dwarf.AttrProducer).(string)
			if cu.isgo && cu.producer != "" {
				semicolon := strings.Index(cu.producer, ";")
				if semicolon < 0 {
					cu.optimized = goversion.ProducerAfterOrEqual(cu.producer, 1, 10)
				} else {
					cu.optimized = !strings.Contains(cu.producer[semicolon:], "-N") || !strings.Contains(cu.producer[semicolon:], "-l")
					const regabi = " regabi"
					if i := strings.Index(cu.producer[semicolon:], regabi); i > 0 {
						i += semicolon
						if i+len(regabi) >= len(cu.producer) || cu.producer[i+len(regabi)] == ' ' {
							bi.regabi = true
						}
					}
					cu.producer = cu.producer[:semicolon]
				}
			}
			gopkg, _ := entry.Val(godwarf.AttrGoPackageName).(string)
			if cu.isgo && gopkg != "" {
				bi.PackageMap[gopkg] = append(bi.PackageMap[gopkg], escapePackagePath(strings.Replace(cu.name, "\\", "/", -1)))
			}
			image.compileUnits = append(image.compileUnits, cu)
			if entry.Children {
				bi.loadDebugInfoMapsCompileUnit(ctxt, image, reader, cu)
			}

		case dwarf.TagPartialUnit:
			reader.SkipChildren()

		default:
			// ignore unknown tags
			reader.SkipChildren()
		}
	}

	sort.Sort(compileUnitsByOffset(image.compileUnits))
	sort.Sort(functionsDebugInfoByEntry(bi.Functions))
	sort.Sort(packageVarsByAddr(bi.packageVars))

	bi.LookupFunc = make(map[string]*Function)
	for i := range bi.Functions {
		bi.LookupFunc[bi.Functions[i].Name] = &bi.Functions[i]
	}

	for _, cu := range image.compileUnits {
		if cu.lineInfo != nil {
			for _, fileEntry := range cu.lineInfo.FileNames {
				bi.Sources = append(bi.Sources, fileEntry.Path)
			}
		}
	}
	sort.Strings(bi.Sources)
	bi.Sources = uniq(bi.Sources)

	if bi.regabi {
		// prepare patch for runtime.mallocgc's DIE
		fn := bi.LookupFunc["runtime.mallocgc"]
		if fn != nil && fn.cu.image == image {
			tree, err := image.getDwarfTree(fn.offset)
			if err == nil {
				tree.Children, err = regabiMallocgcWorkaround(bi)
				if err != nil {
					bi.logger.Errorf("could not patch runtime.mallogc: %v", err)
				} else {
					image.runtimeMallocgcTree = tree
				}
			}
		}
	}

	if cont != nil {
		cont()
	}
}

// loadDebugInfoMapsCompileUnit loads entry from a single compile unit.
func (bi *BinaryInfo) loadDebugInfoMapsCompileUnit(ctxt *loadDebugInfoMapsContext, image *Image, reader *reader.Reader, cu *compileUnit) {
	hasAttrGoPkgName := goversion.ProducerAfterOrEqual(cu.producer, 1, 13)

	depth := 0

	for entry, err := reader.Next(); entry != nil; entry, err = reader.Next() {
		if err != nil {
			image.setLoadError("error reading debug_info: %v", err)
			return
		}
		switch entry.Tag {
		case 0:
			if depth == 0 {
				return
			} else {
				depth--
			}
		case dwarf.TagImportedUnit:
			bi.loadDebugInfoMapsImportedUnit(entry, ctxt, image, cu)
			reader.SkipChildren()

		case dwarf.TagArrayType, dwarf.TagBaseType, dwarf.TagClassType, dwarf.TagStructType, dwarf.TagUnionType, dwarf.TagConstType, dwarf.TagVolatileType, dwarf.TagRestrictType, dwarf.TagEnumerationType, dwarf.TagPointerType, dwarf.TagSubroutineType, dwarf.TagTypedef, dwarf.TagUnspecifiedType:
			if name, ok := entry.Val(dwarf.AttrName).(string); ok {
				if !cu.isgo {
					name = "C." + name
				}
				if _, exists := bi.types[name]; !exists {
					bi.types[name] = dwarfRef{image.index, entry.Offset}
				}
			}
			if cu != nil && cu.isgo && !hasAttrGoPkgName {
				bi.registerTypeToPackageMap(entry)
			}
			image.registerRuntimeTypeToDIE(entry, ctxt.ardr)
			reader.SkipChildren()

		case dwarf.TagVariable:
			if n, ok := entry.Val(dwarf.AttrName).(string); ok {
				var addr uint64
				if loc, ok := entry.Val(dwarf.AttrLocation).([]byte); ok {
					if len(loc) == bi.Arch.PtrSize()+1 && op.Opcode(loc[0]) == op.DW_OP_addr {
						addr, _ = util.ReadUintRaw(bytes.NewReader(loc[1:]), binary.LittleEndian, bi.Arch.PtrSize())
					}
				}
				if !cu.isgo {
					n = "C." + n
				}
				if _, known := ctxt.knownPackageVars[n]; !known {
					bi.packageVars = append(bi.packageVars, packageVar{n, cu, entry.Offset, addr + image.StaticBase})
				}
			}
			reader.SkipChildren()

		case dwarf.TagConstant:
			name, okName := entry.Val(dwarf.AttrName).(string)
			typ, okType := entry.Val(dwarf.AttrType).(dwarf.Offset)
			val, okVal := entry.Val(dwarf.AttrConstValue).(int64)
			if okName && okType && okVal {
				if !cu.isgo {
					name = "C." + name
				}
				ct := bi.consts[dwarfRef{image.index, typ}]
				if ct == nil {
					ct = &constantType{}
					bi.consts[dwarfRef{image.index, typ}] = ct
				}
				ct.values = append(ct.values, constantValue{name: name, fullName: name, value: val})
			}
			reader.SkipChildren()

		case dwarf.TagSubprogram:
			inlined := false
			if inval, ok := entry.Val(dwarf.AttrInline).(int64); ok {
				inlined = inval == 1
			}

			if inlined {
				bi.addAbstractSubprogram(entry, ctxt, reader, image, cu)
			} else {
				originOffset, hasAbstractOrigin := entry.Val(dwarf.AttrAbstractOrigin).(dwarf.Offset)
				if hasAbstractOrigin {
					bi.addConcreteInlinedSubprogram(entry, originOffset, ctxt, reader, cu)
				} else {
					bi.addConcreteSubprogram(entry, ctxt, reader, cu)
				}
			}

		default:
			if entry.Children {
				depth++
			}
		}
	}
}

// loadDebugInfoMapsImportedUnit loads entries into cu from the partial unit
// referenced in a DW_TAG_imported_unit entry.
func (bi *BinaryInfo) loadDebugInfoMapsImportedUnit(entry *dwarf.Entry, ctxt *loadDebugInfoMapsContext, image *Image, cu *compileUnit) {
	off, ok := entry.Val(dwarf.AttrImport).(dwarf.Offset)
	if !ok {
		return
	}
	reader := image.DwarfReader()
	reader.Seek(off)
	imentry, err := reader.Next()
	if err != nil {
		return
	}
	if imentry.Tag != dwarf.TagPartialUnit {
		return
	}
	bi.loadDebugInfoMapsCompileUnit(ctxt, image, reader, cu)
}

// addAbstractSubprogram adds the abstract entry for an inlined function.
func (bi *BinaryInfo) addAbstractSubprogram(entry *dwarf.Entry, ctxt *loadDebugInfoMapsContext, reader *reader.Reader, image *Image, cu *compileUnit) {
	name, ok := subprogramEntryName(entry, cu)
	if !ok {
		bi.logger.Warnf("reading debug_info: abstract subprogram without name at %#x", entry.Offset)
		// In some cases clang produces abstract subprograms that do not have a
		// name, but we should process them anyway.
	}

	if entry.Children {
		bi.loadDebugInfoMapsInlinedCalls(ctxt, reader, cu)
	}

	originIdx := ctxt.lookupAbstractOrigin(bi, entry.Offset)
	fn := &bi.Functions[originIdx]
	fn.Name = name
	fn.offset = entry.Offset
	fn.cu = cu
}

// addConcreteInlinedSubprogram adds the concrete entry of a subprogram that was also inlined.
func (bi *BinaryInfo) addConcreteInlinedSubprogram(entry *dwarf.Entry, originOffset dwarf.Offset, ctxt *loadDebugInfoMapsContext, reader *reader.Reader, cu *compileUnit) {
	lowpc, highpc, ok := subprogramEntryRange(entry, cu.image)
	if !ok {
		bi.logger.Warnf("reading debug_info: concrete inlined subprogram without address range at %#x", entry.Offset)
		if entry.Children {
			reader.SkipChildren()
		}
		return
	}

	originIdx := ctxt.lookupAbstractOrigin(bi, originOffset)
	fn := &bi.Functions[originIdx]
	fn.offset = entry.Offset
	fn.Entry = lowpc
	fn.End = highpc
	fn.cu = cu

	if entry.Children {
		bi.loadDebugInfoMapsInlinedCalls(ctxt, reader, cu)
	}
}

// addConcreteSubprogram adds a concrete subprogram (a normal subprogram
// that doesn't have abstract or inlined entries)
func (bi *BinaryInfo) addConcreteSubprogram(entry *dwarf.Entry, ctxt *loadDebugInfoMapsContext, reader *reader.Reader, cu *compileUnit) {
	lowpc, highpc, ok := subprogramEntryRange(entry, cu.image)
	if !ok {
		bi.logger.Warnf("reading debug_info: concrete subprogram without address range at %#x", entry.Offset)
		// When clang inlines a function, in some cases, it produces a concrete
		// subprogram without address range and then inlined calls that reference
		// it, instead of producing an abstract subprogram.
		// It is unclear if this behavior is standard.
	}

	name, ok := subprogramEntryName(entry, cu)
	if !ok {
		bi.logger.Warnf("reading debug_info: concrete subprogram without name at %#x", entry.Offset)
	}

	trampoline, _ := entry.Val(dwarf.AttrTrampoline).(bool)

	originIdx := ctxt.lookupAbstractOrigin(bi, entry.Offset)
	fn := &bi.Functions[originIdx]

	fn.Name = name
	fn.Entry = lowpc
	fn.End = highpc
	fn.offset = entry.Offset
	fn.cu = cu
	fn.trampoline = trampoline

	if entry.Children {
		bi.loadDebugInfoMapsInlinedCalls(ctxt, reader, cu)
	}
}

func subprogramEntryName(entry *dwarf.Entry, cu *compileUnit) (string, bool) {
	name, ok := entry.Val(dwarf.AttrName).(string)
	if !ok {
		return "", false
	}
	if !cu.isgo {
		name = "C." + name
	}
	return name, true
}

func subprogramEntryRange(entry *dwarf.Entry, image *Image) (lowpc, highpc uint64, ok bool) {
	ok = false
	if ranges, _ := image.dwarf.Ranges(entry); len(ranges) >= 1 {
		ok = true
		lowpc = ranges[0][0] + image.StaticBase
		highpc = ranges[0][1] + image.StaticBase
	}
	return lowpc, highpc, ok
}

func (bi *BinaryInfo) loadDebugInfoMapsInlinedCalls(ctxt *loadDebugInfoMapsContext, reader *reader.Reader, cu *compileUnit) {
	for {
		entry, err := reader.Next()
		if err != nil {
			cu.image.setLoadError("error reading debug_info: %v", err)
			return
		}
		switch entry.Tag {
		case 0:
			return
		case dwarf.TagInlinedSubroutine:
			originOffset, ok := entry.Val(dwarf.AttrAbstractOrigin).(dwarf.Offset)
			if !ok {
				bi.logger.Warnf("reading debug_info: inlined call without origin offset at %#x", entry.Offset)
				reader.SkipChildren()
				continue
			}

			lowpc, highpc, ok := subprogramEntryRange(entry, cu.image)
			if !ok {
				bi.logger.Warnf("reading debug_info: inlined call without address range at %#x", entry.Offset)
				reader.SkipChildren()
				continue
			}

			callfileidx, ok1 := entry.Val(dwarf.AttrCallFile).(int64)
			callline, ok2 := entry.Val(dwarf.AttrCallLine).(int64)
			if !ok1 || !ok2 {
				bi.logger.Warnf("reading debug_info: inlined call without CallFile/CallLine at %#x", entry.Offset)
				reader.SkipChildren()
				continue
			}
			callfile, cferr := cu.filePath(int(callfileidx), entry)
			if cferr != nil {
				bi.logger.Warnf("%v", cferr)
				reader.SkipChildren()
				continue
			}

			originIdx := ctxt.lookupAbstractOrigin(bi, originOffset)
			fn := &bi.Functions[originIdx]

			fn.InlinedCalls = append(fn.InlinedCalls, InlinedCall{
				cu:     cu,
				LowPC:  lowpc,
				HighPC: highpc,
			})

			if fn.cu == nil {
				fn.cu = cu
			}

			fl := fileLine{callfile, int(callline)}
			bi.inlinedCallLines[fl] = append(bi.inlinedCallLines[fl], lowpc)
		}
		reader.SkipChildren()
	}
}

func uniq(s []string) []string {
	if len(s) <= 0 {
		return s
	}
	src, dst := 1, 1
	for src < len(s) {
		if s[src] != s[dst-1] {
			s[dst] = s[src]
			dst++
		}
		src++
	}
	return s[:dst]
}

func (bi *BinaryInfo) expandPackagesInType(expr ast.Expr) {
	switch e := expr.(type) {
	case *ast.ArrayType:
		bi.expandPackagesInType(e.Elt)
	case *ast.ChanType:
		bi.expandPackagesInType(e.Value)
	case *ast.FuncType:
		for i := range e.Params.List {
			bi.expandPackagesInType(e.Params.List[i].Type)
		}
		if e.Results != nil {
			for i := range e.Results.List {
				bi.expandPackagesInType(e.Results.List[i].Type)
			}
		}
	case *ast.MapType:
		bi.expandPackagesInType(e.Key)
		bi.expandPackagesInType(e.Value)
	case *ast.ParenExpr:
		bi.expandPackagesInType(e.X)
	case *ast.SelectorExpr:
		switch x := e.X.(type) {
		case *ast.Ident:
			if len(bi.PackageMap[x.Name]) > 0 {
				// There's no particular reason to expect the first entry to be the
				// correct one if the package name is ambiguous, but trying all possible
				// expansions of all types mentioned in the expression is complicated
				// and, besides type assertions, users can always specify the type they
				// want exactly, using a string.
				x.Name = bi.PackageMap[x.Name][0]
			}
		default:
			bi.expandPackagesInType(e.X)
		}
	case *ast.StarExpr:
		bi.expandPackagesInType(e.X)
	default:
		// nothing to do
	}
}

// escapePackagePath returns pkg with '.' replaced with '%2e' (in all
// elements of the path except the first one) like Go does in variable and
// type names.
func escapePackagePath(pkg string) string {
	slash := strings.Index(pkg, "/")
	if slash < 0 {
		slash = 0
	}
	return pkg[:slash] + strings.Replace(pkg[slash:], ".", "%2e", -1)
}

// Looks up symbol (either functions or global variables) at address addr.
// Used by disassembly formatter.
func (bi *BinaryInfo) symLookup(addr uint64) (string, uint64) {
	fn := bi.PCToFunc(addr)
	if fn != nil {
		if fn.Entry == addr {
			// only report the function name if it's the exact address because it's
			// easier to read the absolute address than function_name+offset.
			return fn.Name, fn.Entry
		}
		return "", 0
	}
	if sym, ok := bi.SymNames[addr]; ok {
		return sym.Name, addr
	}
	i := sort.Search(len(bi.packageVars), func(i int) bool {
		return bi.packageVars[i].addr >= addr
	})
	if i >= len(bi.packageVars) {
		return "", 0
	}
	if bi.packageVars[i].addr > addr {
		// report previous variable + offset if i-th variable starts after addr
		i--
	}
	if i >= 0 && bi.packageVars[i].addr != 0 {
		return bi.packageVars[i].name, bi.packageVars[i].addr
	}
	return "", 0
}

type PackageBuildInfo struct {
	ImportPath    string
	DirectoryPath string
	Files         map[string]struct{}
}

// ListPackagesBuildInfo returns the list of packages used by the program along with
// the directory where each package was compiled and optionally the list of
// files constituting the package.
func (bi *BinaryInfo) ListPackagesBuildInfo(includeFiles bool) []*PackageBuildInfo {
	m := make(map[string]*PackageBuildInfo)
	for _, cu := range bi.Images[0].compileUnits {
		if cu.image != bi.Images[0] || !cu.isgo || cu.lineInfo == nil {
			//TODO(aarzilli): what's the correct thing to do for plugins?
			continue
		}

		ip := strings.Replace(cu.name, "\\", "/", -1)
		if _, ok := m[ip]; !ok {
			path := cu.lineInfo.FirstFile()
			if ext := filepath.Ext(path); ext != ".go" && ext != ".s" {
				continue
			}
			dp := filepath.Dir(path)
			m[ip] = &PackageBuildInfo{
				ImportPath:    ip,
				DirectoryPath: dp,
				Files:         make(map[string]struct{}),
			}
		}

		if includeFiles {
			pbi := m[ip]

			for _, file := range cu.lineInfo.FileNames {
				pbi.Files[file.Path] = struct{}{}
			}
		}
	}

	r := make([]*PackageBuildInfo, 0, len(m))
	for _, pbi := range m {
		r = append(r, pbi)
	}

	sort.Slice(r, func(i, j int) bool { return r[i].ImportPath < r[j].ImportPath })
	return r
}

// cuFilePath takes a compilation unit "cu" and a file index reference
// "fileidx" and returns the corresponding file name entry from the
// DWARF line table associated with the unit; "entry" is the offset of
// the attribute where the file reference originated, for logging
// purposes. Return value is the file string and an error value; error
// will be non-nil if the file could not be recovered, perhaps due to
// malformed DWARF.
func (cu *compileUnit) filePath(fileidx int, entry *dwarf.Entry) (string, error) {
	if cu.lineInfo == nil {
		return "", fmt.Errorf("reading debug_info: file reference within a compilation unit without debug_line section at %#x", entry.Offset)
	}
	// File numbering is slightly different before and after DWARF 5;
	// account for this here. See section 6.2.4 of the DWARF 5 spec.
	if cu.Version < 5 {
		fileidx--
	}
	if fileidx < 0 || fileidx >= len(cu.lineInfo.FileNames) {
		return "", fmt.Errorf("reading debug_info: file index (%d) out of range in compile unit file table at %#x", fileidx, entry.Offset)
	}
	return cu.lineInfo.FileNames[fileidx].Path, nil
}
