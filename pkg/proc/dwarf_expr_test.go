// Tests for loading variables that have complex location expressions. They
// are only produced for optimized code (for both Go and C) therefore we can
// not get the compiler to produce them reliably enough for tests.

package proc_test

import (
	"bytes"
	"debug/dwarf"
	"encoding/binary"
	"fmt"
	"go/constant"
	"testing"
	"unsafe"

	"github.com/go-delve/delve/pkg/dwarf/dwarfbuilder"
	"github.com/go-delve/delve/pkg/dwarf/godwarf"
	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/linutil"
)

func ptrSizeByRuntimeArch() int {
	return int(unsafe.Sizeof(uintptr(0)))
}

func fakeCFA() uint64 {
	ptrSize := ptrSizeByRuntimeArch()
	if ptrSize == 8 {
		return 0xc420051d00
	}
	if ptrSize == 4 {
		return 0xc4251d00
	}
	panic(fmt.Errorf("not support ptr size %d", ptrSize))
}

func fakeBinaryInfo(t *testing.T, dwb *dwarfbuilder.Builder) (*proc.BinaryInfo, *dwarf.Data) {
	abbrev, aranges, frame, info, line, pubnames, ranges, str, loc, err := dwb.Build()
	assertNoError(err, t, "dwarfbuilder.Build")
	dwdata, err := dwarf.New(abbrev, aranges, frame, info, line, pubnames, ranges, str)
	assertNoError(err, t, "creating dwarf")

	bi := proc.NewBinaryInfo("linux", "amd64")
	bi.LoadImageFromData(dwdata, frame, line, loc)

	return bi, dwdata
}

// fakeMemory implements proc.MemoryReadWriter by reading from a byte slice.
// Byte 0 of "data"  is at address "base".
type fakeMemory struct {
	base uint64
	data []byte
}

func newFakeMemory(base uint64, contents ...interface{}) *fakeMemory {
	mem := &fakeMemory{base: base}
	var buf bytes.Buffer
	for _, x := range contents {
		binary.Write(&buf, binary.LittleEndian, x)
	}
	mem.data = buf.Bytes()
	return mem
}

func (mem *fakeMemory) ReadMemory(data []byte, addr uint64) (int, error) {
	if uint64(addr) < mem.base {
		return 0, fmt.Errorf("read out of bounds %d %#x", len(data), addr)
	}
	start := uint64(addr) - mem.base
	end := uint64(len(data)) + start
	if end > uint64(len(mem.data)) {
		panic(fmt.Errorf("read out of bounds %d %#x", len(data), addr))
	}
	copy(data, mem.data[start:end])
	return len(data), nil
}

func (mem *fakeMemory) WriteMemory(uint64, []byte) (int, error) {
	return 0, fmt.Errorf("not implemented")
}

func uintExprCheck(t *testing.T, scope *proc.EvalScope, expr string, tgt uint64) {
	thevar, err := scope.EvalExpression(expr, normalLoadConfig)
	assertNoError(err, t, fmt.Sprintf("EvalExpression(%s)", expr))
	if thevar.Unreadable != nil {
		t.Errorf("variable %q unreadable: %v", expr, thevar.Unreadable)
	} else {
		if v, _ := constant.Uint64Val(thevar.Value); v != tgt {
			t.Errorf("expected value %x got %x for %q", tgt, v, expr)
		}
	}
}

func dwarfExprCheck(t *testing.T, mem proc.MemoryReadWriter, regs op.DwarfRegisters, bi *proc.BinaryInfo, testCases map[string]uint16, fn *proc.Function) *proc.EvalScope {
	scope := &proc.EvalScope{Location: proc.Location{PC: 0x40100, Fn: fn}, Regs: regs, Mem: mem, BinInfo: bi}
	for name, value := range testCases {
		uintExprCheck(t, scope, name, uint64(value))
	}

	return scope
}

func dwarfRegisters(bi *proc.BinaryInfo, regs *linutil.AMD64Registers) op.DwarfRegisters {
	a := proc.AMD64Arch("linux")
	so := bi.PCToImage(regs.PC())
	dwarfRegs := a.RegistersToDwarfRegisters(so.StaticBase, regs)
	dwarfRegs.CFA = int64(fakeCFA())
	dwarfRegs.FrameBase = int64(fakeCFA())
	return dwarfRegs
}

func TestDwarfExprRegisters(t *testing.T) {
	testCases := map[string]uint16{
		"a": 0x1234,
		"b": 0x4321,
		"c": 0x2143,
	}

	dwb := dwarfbuilder.New()

	uint16off := dwb.AddBaseType("uint16", dwarfbuilder.DW_ATE_unsigned, 2)

	dwb.AddSubprogram("main.main", 0x40100, 0x41000)
	dwb.Attr(dwarf.AttrFrameBase, dwarfbuilder.LocationBlock(op.DW_OP_call_frame_cfa))
	dwb.AddVariable("a", uint16off, dwarfbuilder.LocationBlock(op.DW_OP_reg0))
	dwb.AddVariable("b", uint16off, dwarfbuilder.LocationBlock(op.DW_OP_fbreg, int(8)))
	dwb.AddVariable("c", uint16off, dwarfbuilder.LocationBlock(op.DW_OP_regx, int(1)))
	dwb.TagClose()

	bi, _ := fakeBinaryInfo(t, dwb)

	mainfn := bi.LookupFunc["main.main"]
	mem := newFakeMemory(fakeCFA(), uint64(0), uint64(testCases["b"]))
	regs := linutil.AMD64Registers{Regs: &linutil.AMD64PtraceRegs{}}
	regs.Regs.Rax = uint64(testCases["a"])
	regs.Regs.Rdx = uint64(testCases["c"])

	dwarfExprCheck(t, mem, dwarfRegisters(bi, &regs), bi, testCases, mainfn)
}

func TestDwarfExprComposite(t *testing.T) {
	testCases := map[string]uint16{
		"pair.k": 0x8765,
		"pair.v": 0x5678,
		"n":      42,
	}

	const stringVal = "this is a string"

	dwb := dwarfbuilder.New()

	uint16off := dwb.AddBaseType("uint16", dwarfbuilder.DW_ATE_unsigned, 2)
	intoff := dwb.AddBaseType("int", dwarfbuilder.DW_ATE_signed, 8)

	byteoff := dwb.AddBaseType("uint8", dwarfbuilder.DW_ATE_unsigned, 1)

	byteptroff := dwb.AddPointerType("*uint8", byteoff)

	pairoff := dwb.AddStructType("main.pair", 4)
	dwb.Attr(godwarf.AttrGoKind, uint8(25))
	dwb.AddMember("k", uint16off, dwarfbuilder.LocationBlock(op.DW_OP_plus_uconst, uint(0)))
	dwb.AddMember("v", uint16off, dwarfbuilder.LocationBlock(op.DW_OP_plus_uconst, uint(2)))
	dwb.TagClose()

	stringoff := dwb.AddStructType("string", 16)
	dwb.Attr(godwarf.AttrGoKind, uint8(24))
	dwb.AddMember("str", byteptroff, dwarfbuilder.LocationBlock(op.DW_OP_plus_uconst, uint(0)))
	dwb.AddMember("len", intoff, dwarfbuilder.LocationBlock(op.DW_OP_plus_uconst, uint(8)))
	dwb.TagClose()

	dwb.AddSubprogram("main.main", 0x40100, 0x41000)
	dwb.AddVariable("pair", pairoff, dwarfbuilder.LocationBlock(
		op.DW_OP_reg2, op.DW_OP_piece, uint(2),
		op.DW_OP_call_frame_cfa, op.DW_OP_consts, int(16), op.DW_OP_plus, op.DW_OP_piece, uint(2)))
	dwb.AddVariable("s", stringoff, dwarfbuilder.LocationBlock(
		op.DW_OP_reg1, op.DW_OP_piece, uint(8),
		op.DW_OP_reg0, op.DW_OP_piece, uint(8)))
	dwb.AddVariable("n", intoff, dwarfbuilder.LocationBlock(op.DW_OP_reg3))
	dwb.TagClose()

	bi, _ := fakeBinaryInfo(t, dwb)

	mainfn := bi.LookupFunc["main.main"]

	mem := newFakeMemory(fakeCFA(), uint64(0), uint64(0), uint16(testCases["pair.v"]), []byte(stringVal))
	var regs linutil.AMD64Registers
	regs.Regs = &linutil.AMD64PtraceRegs{}
	regs.Regs.Rax = uint64(len(stringVal))
	regs.Regs.Rdx = fakeCFA() + 18
	regs.Regs.Rcx = uint64(testCases["pair.k"])
	regs.Regs.Rbx = uint64(testCases["n"])

	scope := dwarfExprCheck(t, mem, dwarfRegisters(bi, &regs), bi, testCases, mainfn)

	thevar, err := scope.EvalExpression("s", normalLoadConfig)
	assertNoError(err, t, fmt.Sprintf("EvalExpression(%s)", "s"))
	if thevar.Unreadable != nil {
		t.Errorf("variable \"s\" unreadable: %v", thevar.Unreadable)
	} else {
		if v := constant.StringVal(thevar.Value); v != stringVal {
			t.Errorf("expected value %q got %q", stringVal, v)
		}
	}
}

func TestDwarfExprLoclist(t *testing.T) {
	const before = 0x1234
	const after = 0x4321

	dwb := dwarfbuilder.New()

	uint16off := dwb.AddBaseType("uint16", dwarfbuilder.DW_ATE_unsigned, 2)

	dwb.AddSubprogram("main.main", 0x40100, 0x41000)
	dwb.AddVariable("a", uint16off, []dwarfbuilder.LocEntry{
		{Lowpc: 0x40100, Highpc: 0x40700, Loc: dwarfbuilder.LocationBlock(op.DW_OP_call_frame_cfa)},
		{Lowpc: 0x40700, Highpc: 0x41000, Loc: dwarfbuilder.LocationBlock(op.DW_OP_call_frame_cfa, op.DW_OP_consts, int(2), op.DW_OP_plus)},
	})
	dwb.TagClose()

	bi, _ := fakeBinaryInfo(t, dwb)

	mainfn := bi.LookupFunc["main.main"]

	mem := newFakeMemory(fakeCFA(), uint16(before), uint16(after))
	const PC = 0x40100
	regs := linutil.AMD64Registers{Regs: &linutil.AMD64PtraceRegs{Rip: PC}}

	scope := &proc.EvalScope{Location: proc.Location{PC: PC, Fn: mainfn}, Regs: dwarfRegisters(bi, &regs), Mem: mem, BinInfo: bi}

	uintExprCheck(t, scope, "a", before)
	scope.PC = 0x40800
	scope.Regs.Reg(scope.Regs.PCRegNum).Uint64Val = scope.PC
	uintExprCheck(t, scope, "a", after)
}

func TestIssue1419(t *testing.T) {
	// trying to read a slice variable with a location list that tries to read
	// from registers we don't have should not cause a panic.

	dwb := dwarfbuilder.New()

	uint64off := dwb.AddBaseType("uint64", dwarfbuilder.DW_ATE_unsigned, 8)
	intoff := dwb.AddBaseType("int", dwarfbuilder.DW_ATE_signed, 8)
	intptroff := dwb.AddPointerType("*int", intoff)

	sliceoff := dwb.AddStructType("[]int", 24)
	dwb.Attr(godwarf.AttrGoKind, uint8(23))
	dwb.AddMember("array", intptroff, dwarfbuilder.LocationBlock(op.DW_OP_plus_uconst, uint(0)))
	dwb.AddMember("len", uint64off, dwarfbuilder.LocationBlock(op.DW_OP_plus_uconst, uint(8)))
	dwb.AddMember("cap", uint64off, dwarfbuilder.LocationBlock(op.DW_OP_plus_uconst, uint(16)))
	dwb.TagClose()

	dwb.AddSubprogram("main.main", 0x40100, 0x41000)
	dwb.AddVariable("a", sliceoff, dwarfbuilder.LocationBlock(op.DW_OP_reg2, op.DW_OP_piece, uint(8), op.DW_OP_reg2, op.DW_OP_piece, uint(8), op.DW_OP_reg2, op.DW_OP_piece, uint(8)))
	dwb.TagClose()

	bi, _ := fakeBinaryInfo(t, dwb)

	mainfn := bi.LookupFunc["main.main"]

	mem := newFakeMemory(fakeCFA())

	scope := &proc.EvalScope{Location: proc.Location{PC: 0x40100, Fn: mainfn}, Regs: op.DwarfRegisters{}, Mem: mem, BinInfo: bi}

	va, err := scope.EvalExpression("a", normalLoadConfig)
	assertNoError(err, t, "EvalExpression(a)")
	t.Logf("%#x\n", va.Addr)
	t.Logf("%v", va)
	if va.Unreadable == nil {
		t.Fatalf("expected 'a' to be unreadable but it wasn't")
	}
	if va.Unreadable.Error() != "could not read 8 bytes from register 2 (size: 0)" {
		t.Fatalf("wrong unreadable reason for variable 'a': %v", va.Unreadable)
	}
}

func TestLocationCovers(t *testing.T) {
	const before = 0x1234
	const after = 0x4321

	dwb := dwarfbuilder.New()

	uint16off := dwb.AddBaseType("uint16", dwarfbuilder.DW_ATE_unsigned, 2)

	dwb.AddCompileUnit("main", 0x0)
	dwb.AddSubprogram("main.main", 0x40100, 0x41000)
	aOff := dwb.AddVariable("a", uint16off, []dwarfbuilder.LocEntry{
		{Lowpc: 0x40100, Highpc: 0x40700, Loc: dwarfbuilder.LocationBlock(op.DW_OP_call_frame_cfa)},
		{Lowpc: 0x40700, Highpc: 0x41000, Loc: dwarfbuilder.LocationBlock(op.DW_OP_call_frame_cfa, op.DW_OP_consts, int(2), op.DW_OP_plus)},
	})
	dwb.TagClose()
	dwb.TagClose()

	bi, dwdata := fakeBinaryInfo(t, dwb)

	dwrdr := dwdata.Reader()
	dwrdr.Seek(aOff)
	aEntry, err := dwrdr.Next()
	assertNoError(err, t, "reading 'a' entry")
	ranges, err := bi.LocationCovers(aEntry, dwarf.AttrLocation)
	assertNoError(err, t, "LocationCovers")
	t.Logf("%x", ranges)
	if fmt.Sprintf("%x", ranges) != "[[40100 40700] [40700 41000]]" {
		t.Error("wrong value returned by LocationCover")
	}

}

func TestIssue1636_InlineWithoutOrigin(t *testing.T) {
	// Gcc (specifically GNU C++11 6.3.0) will emit DW_TAG_inlined_subroutine
	// without a DW_AT_abstract_origin or a name. What is an inlined subroutine
	// without a reference to an abstract origin or even a name? Regardless,
	// Delve shouldn't crash.
	dwb := dwarfbuilder.New()
	dwb.AddCompileUnit("main", 0x0)
	dwb.AddSubprogram("main.main", 0x40100, 0x41000)
	dwb.TagOpen(dwarf.TagInlinedSubroutine, "")
	dwb.TagClose()
	dwb.TagClose()
	dwb.TagClose()
	fakeBinaryInfo(t, dwb)
}

func TestUnsupportedType(t *testing.T) {
	// Tests that reading an unsupported type does not cause an error
	dwb := dwarfbuilder.New()
	dwb.AddCompileUnit("main", 0x0)
	off := dwb.TagOpen(dwarf.TagReferenceType, "blah")
	dwb.TagClose()
	dwb.TagClose()
	_, dw := fakeBinaryInfo(t, dwb)
	_, err := godwarf.ReadType(dw, 0, off, make(map[dwarf.Offset]godwarf.Type))
	if err != nil {
		t.Errorf("unexpected error reading unsupported type: %#v", err)
	}
}

func TestNestedCompileUnts(t *testing.T) {
	// Tests that a compile unit with a nested entry that we don't care about
	// (such as a DW_TAG_namespace) is read fully.
	dwb := dwarfbuilder.New()
	dwb.AddCompileUnit("main", 0x0)
	dwb.TagOpen(dwarf.TagNamespace, "namespace")
	dwb.AddVariable("var1", 0x0, uint64(0x0))
	dwb.TagClose()
	dwb.AddVariable("var2", 0x0, uint64(0x0))
	dwb.TagClose()
	bi, _ := fakeBinaryInfo(t, dwb)
	if n := len(bi.PackageVars()); n != 2 {
		t.Errorf("expected 2 variables, got %d", n)
	}
}
