package proc

import (
	"debug/dwarf"
	"errors"
	"fmt"

	"github.com/derekparker/delve/pkg/dwarf/frame"
	"github.com/derekparker/delve/pkg/dwarf/op"
)

// This code is partly adaped from runtime.gentraceback in
// $GOROOT/src/runtime/traceback.go

const runtimeStackBarrier = "runtime.stackBarrier"

// NoReturnAddr is returned when return address
// could not be found during stack trace.
type NoReturnAddr struct {
	Fn string
}

func (nra NoReturnAddr) Error() string {
	return fmt.Sprintf("could not find return address for %s", nra.Fn)
}

// Stackframe represents a frame in a system stack.
type Stackframe struct {
	// Address the function above this one on the call stack will return to.
	Current Location
	// Address of the call instruction for the function above on the call stack.
	Call Location
	// Frame registers.
	Regs op.DwarfRegisters
	// High address of the stack.
	StackHi uint64
	// Return address for this stack frame (as read from the stack frame itself).
	Ret uint64
	// Address to the memory location containing the return address
	addrret uint64
	// Err is set if an error occoured during stacktrace
	Err error
}

// ThreadStacktrace returns the stack trace for thread.
// Note the locations in the array are return addresses not call addresses.
func ThreadStacktrace(thread Thread, depth int) ([]Stackframe, error) {
	regs, err := thread.Registers(true)
	if err != nil {
		return nil, err
	}
	it := newStackIterator(thread.BinInfo(), thread, thread.BinInfo().Arch.RegistersToDwarfRegisters(regs), 0, nil, -1)
	return it.stacktrace(depth)
}

func (g *G) stackIterator() (*stackIterator, error) {
	stkbar, err := g.stkbar()
	if err != nil {
		return nil, err
	}
	if g.Thread != nil {
		regs, err := g.Thread.Registers(true)
		if err != nil {
			return nil, err
		}
		return newStackIterator(g.variable.bi, g.Thread, g.variable.bi.Arch.RegistersToDwarfRegisters(regs), g.stackhi, stkbar, g.stkbarPos), nil
	}
	return newStackIterator(g.variable.bi, g.variable.mem, g.variable.bi.Arch.GoroutineToDwarfRegisters(g), g.stackhi, stkbar, g.stkbarPos), nil
}

// Stacktrace returns the stack trace for a goroutine.
// Note the locations in the array are return addresses not call addresses.
func (g *G) Stacktrace(depth int) ([]Stackframe, error) {
	it, err := g.stackIterator()
	if err != nil {
		return nil, err
	}
	return it.stacktrace(depth)
}

// NullAddrError is an error for a null address.
type NullAddrError struct{}

func (n NullAddrError) Error() string {
	return "NULL address"
}

// stackIterator holds information
// required to iterate and walk the program
// stack.
type stackIterator struct {
	pc    uint64
	top   bool
	atend bool
	frame Stackframe
	bi    *BinaryInfo
	mem   MemoryReadWriter
	err   error

	stackhi        uint64
	stackBarrierPC uint64
	stkbar         []savedLR

	// regs is the register set for the current frame
	regs op.DwarfRegisters
}

type savedLR struct {
	ptr uint64
	val uint64
}

func newStackIterator(bi *BinaryInfo, mem MemoryReadWriter, regs op.DwarfRegisters, stackhi uint64, stkbar []savedLR, stkbarPos int) *stackIterator {
	stackBarrierFunc := bi.LookupFunc[runtimeStackBarrier] // stack barriers were removed in Go 1.9
	var stackBarrierPC uint64
	if stackBarrierFunc != nil && stkbar != nil {
		stackBarrierPC = stackBarrierFunc.Entry
		fn := bi.PCToFunc(regs.PC())
		if fn != nil && fn.Name == runtimeStackBarrier {
			// We caught the goroutine as it's executing the stack barrier, we must
			// determine whether or not g.stackPos has already been incremented or not.
			if len(stkbar) > 0 && stkbar[stkbarPos].ptr < regs.SP() {
				// runtime.stackBarrier has not incremented stkbarPos.
			} else if stkbarPos > 0 && stkbar[stkbarPos-1].ptr < regs.SP() {
				// runtime.stackBarrier has incremented stkbarPos.
				stkbarPos--
			} else {
				return &stackIterator{err: fmt.Errorf("failed to unwind through stackBarrier at SP %x", regs.SP())}
			}
		}
		stkbar = stkbar[stkbarPos:]
	}
	return &stackIterator{pc: regs.PC(), regs: regs, top: true, bi: bi, mem: mem, err: nil, atend: false, stackhi: stackhi, stackBarrierPC: stackBarrierPC, stkbar: stkbar}
}

// Next points the iterator to the next stack frame.
func (it *stackIterator) Next() bool {
	if it.err != nil || it.atend {
		return false
	}
	callFrameRegs, ret, retaddr := it.advanceRegs()
	it.frame = it.newStackframe(ret, retaddr)

	if it.frame.Ret <= 0 {
		it.atend = true
		return true
	}

	if it.stkbar != nil && it.frame.Ret == it.stackBarrierPC && it.frame.addrret == it.stkbar[0].ptr {
		// Skip stack barrier frames
		it.frame.Ret = it.stkbar[0].val
		it.stkbar = it.stkbar[1:]
	}

	// Look for "top of stack" functions.
	if it.frame.Current.Fn != nil && (it.frame.Current.Fn.Name == "runtime.goexit" || it.frame.Current.Fn.Name == "runtime.rt0_go" || it.frame.Current.Fn.Name == "runtime.mcall") {
		it.atend = true
		return true
	}

	it.top = false
	it.pc = it.frame.Ret
	it.regs = callFrameRegs
	return true
}

// Frame returns the frame the iterator is pointing at.
func (it *stackIterator) Frame() Stackframe {
	return it.frame
}

// Err returns the error encountered during stack iteration.
func (it *stackIterator) Err() error {
	return it.err
}

// frameBase calculates the frame base pseudo-register for DWARF for fn and
// the current frame.
func (it *stackIterator) frameBase(fn *Function) int64 {
	rdr := it.bi.dwarf.Reader()
	rdr.Seek(fn.offset)
	e, err := rdr.Next()
	if err != nil {
		return 0
	}
	fb, _, _ := it.bi.Location(e, dwarf.AttrFrameBase, it.pc, it.regs)
	return fb
}

func (it *stackIterator) newStackframe(ret, retaddr uint64) Stackframe {
	if retaddr == 0 {
		it.err = NullAddrError{}
		return Stackframe{}
	}
	f, l, fn := it.bi.PCToLine(it.pc)
	if fn == nil {
		f = "?"
		l = -1
	} else {
		it.regs.FrameBase = it.frameBase(fn)
	}
	r := Stackframe{Current: Location{PC: it.pc, File: f, Line: l, Fn: fn}, Regs: it.regs, Ret: ret, addrret: retaddr, StackHi: it.stackhi}
	if !it.top {
		r.Call.File, r.Call.Line, r.Call.Fn = it.bi.PCToLine(it.pc - 1)
		if r.Call.Fn == nil {
			r.Call.File = "?"
			r.Call.Line = -1
		}
		r.Call.PC = r.Current.PC
	} else {
		r.Call = r.Current
	}
	return r
}

func (it *stackIterator) stacktrace(depth int) ([]Stackframe, error) {
	if depth < 0 {
		return nil, errors.New("negative maximum stack depth")
	}
	frames := make([]Stackframe, 0, depth+1)
	for it.Next() {
		frames = append(frames, it.Frame())
		if len(frames) >= depth+1 {
			break
		}
	}
	if err := it.Err(); err != nil {
		if len(frames) == 0 {
			return nil, err
		}
		frames = append(frames, Stackframe{Err: err})
	}
	return frames, nil
}

// advanceRegs calculates it.callFrameRegs using it.regs and the frame
// descriptor entry for the current stack frame.
// it.regs.CallFrameCFA is updated.
func (it *stackIterator) advanceRegs() (callFrameRegs op.DwarfRegisters, ret uint64, retaddr uint64) {
	fde, err := it.bi.frameEntries.FDEForPC(it.pc)
	var framectx *frame.FrameContext
	if _, nofde := err.(*frame.NoFDEForPCError); nofde {
		framectx = it.bi.Arch.FixFrameUnwindContext(nil)
	} else {
		framectx = it.bi.Arch.FixFrameUnwindContext(fde.EstablishFrame(it.pc))
	}

	cfareg, err := it.executeFrameRegRule(0, framectx.CFA, 0)
	if cfareg == nil {
		it.err = fmt.Errorf("CFA becomes undefined at PC %#x", it.pc)
		return op.DwarfRegisters{}, 0, 0
	}
	it.regs.CFA = int64(cfareg.Uint64Val)

	callFrameRegs = op.DwarfRegisters{ByteOrder: it.regs.ByteOrder, PCRegNum: it.regs.PCRegNum, SPRegNum: it.regs.SPRegNum}

	// According to the standard the compiler should be responsible for emitting
	// rules for the RSP register so that it can then be used to calculate CFA,
	// however neither Go nor GCC do this.
	// In the following line we copy GDB's behaviour by assuming this is
	// implicit.
	// See also the comment in dwarf2_frame_default_init in
	// $GDB_SOURCE/dwarf2-frame.c
	callFrameRegs.AddReg(uint64(amd64DwarfSPRegNum), cfareg)

	for i, regRule := range framectx.Regs {
		reg, err := it.executeFrameRegRule(i, regRule, it.regs.CFA)
		callFrameRegs.AddReg(i, reg)
		if i == framectx.RetAddrReg {
			if reg == nil {
				if err == nil {
					err = fmt.Errorf("Undefined return address at %#x", it.pc)
				}
				it.err = err
			} else {
				ret = reg.Uint64Val
			}
			retaddr = uint64(it.regs.CFA + regRule.Offset)
		}
	}

	return callFrameRegs, ret, retaddr
}

func (it *stackIterator) executeFrameRegRule(regnum uint64, rule frame.DWRule, cfa int64) (*op.DwarfRegister, error) {
	switch rule.Rule {
	default:
		fallthrough
	case frame.RuleUndefined:
		return nil, nil
	case frame.RuleSameVal:
		return it.regs.Reg(regnum), nil
	case frame.RuleOffset:
		return it.readRegisterAt(regnum, uint64(cfa+rule.Offset))
	case frame.RuleValOffset:
		return op.DwarfRegisterFromUint64(uint64(cfa + rule.Offset)), nil
	case frame.RuleRegister:
		return it.regs.Reg(rule.Reg), nil
	case frame.RuleExpression:
		v, _, err := op.ExecuteStackProgram(it.regs, rule.Expression)
		if err != nil {
			return nil, err
		}
		return it.readRegisterAt(regnum, uint64(v))
	case frame.RuleValExpression:
		v, _, err := op.ExecuteStackProgram(it.regs, rule.Expression)
		if err != nil {
			return nil, err
		}
		return op.DwarfRegisterFromUint64(uint64(v)), nil
	case frame.RuleArchitectural:
		return nil, errors.New("architectural frame rules are unsupported")
	case frame.RuleCFA:
		if it.regs.Reg(rule.Reg) == nil {
			return nil, nil
		}
		return op.DwarfRegisterFromUint64(uint64(int64(it.regs.Uint64Val(rule.Reg)) + rule.Offset)), nil
	case frame.RuleRegOffset:
		if it.regs.Reg(rule.Reg) == nil {
			return nil, nil
		}
		return it.readRegisterAt(regnum, uint64(int64(it.regs.Uint64Val(rule.Reg))+rule.Offset))
	}
}

func (it *stackIterator) readRegisterAt(regnum uint64, addr uint64) (*op.DwarfRegister, error) {
	buf := make([]byte, it.bi.Arch.RegSize(regnum))
	_, err := it.mem.ReadMemory(buf, uintptr(addr))
	if err != nil {
		return nil, err
	}
	return op.DwarfRegisterFromBytes(buf), nil
}
