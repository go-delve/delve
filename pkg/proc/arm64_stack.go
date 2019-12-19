package proc

import (
	"debug/dwarf"
	"errors"
	"fmt"
	"strings"

	"github.com/go-delve/delve/pkg/dwarf/frame"
	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/reader"
)

// arm64cgocallSPOffsetSaveSlot is the offset from systemstack.SP where
// (goroutine.SP - StackHi) is saved in runtime.asmcgocall after the stack
// switch happens.
const arm64cgocallSPOffsetSaveSlot = 0x8
const prevG0schedSPOffsetSaveSlot = 0x10
const spAlign = 16

// arm64_stack holds information required to stackIterator and walk the program stack.
type arm64Stack struct {
	pc    uint64
	top   bool
	atend bool
	frame Stackframe
	bi    *BinaryInfo
	mem   MemoryReadWriter
	err   error

	stackhi        uint64
	systemstack    bool
	stackBarrierPC uint64
	stkbar         []savedLR

	// regs is the register set for the current frame
	regs op.DwarfRegisters

	g           *G     // the goroutine being stacktraced, nil if we are stacktracing a goroutine-less thread
	g0_sched_sp uint64 // value of g0.sched.sp (see comments around its use)

	opts StacktraceOptions
}

// Next points the iterator to the next stack frame.
func (it *arm64Stack) Next() bool {
	if it.err != nil || it.atend {
		return false
	}
	callFrameRegs, ret, retaddr := it.advanceRegs()
	it.frame = it.newStackframe(ret, retaddr)

	if it.stkbar != nil && it.frame.Ret == it.stackBarrierPC && it.frame.addrret == it.stkbar[0].ptr {
		// Skip stack barrier frames
		it.frame.Ret = it.stkbar[0].val
		it.stkbar = it.stkbar[1:]
	}

	if it.opts&StacktraceSimple == 0 {
		if it.switchStack() {
			return true
		}
	}

	if it.frame.Ret <= 0 {
		it.atend = true
		return true
	}

	it.top = false
	it.pc = it.frame.Ret
	it.regs = callFrameRegs
	return true
}

// switchStack will use the current frame to determine if it's time to
// switch between the system stack and the goroutine stack or vice versa.
// Sets it.atend when the top of the stack is reached.
func (it *arm64Stack) switchSp() bool {
	_, _, fn := it.bi.PCToLine(it.pc)
	if fn == nil {
		return false
	}
	switch fn.Name {
	case "runtime.asmcgocall":
		if !it.systemstack {
			return false
		}

		// This function is called by a goroutine to execute a C function and
		// switches from the goroutine stack to the system stack.
		// Since we are unwinding the stack from callee to caller we have  switch
		// from the system stack to the goroutine stack.
		off, _ := readIntRaw(it.mem, uintptr(it.regs.SP()+arm64cgocallSPOffsetSaveSlot), int64(it.bi.Arch.PtrSize()))
		oldsp := it.regs.SP()
		newsp := uint64(int64(it.stackhi) - off)

		// runtime.asmcgocall can also be called from inside the system stack,
		// in that case no stack switch actually happens
		if newsp == oldsp {
			return false
		}
		it.systemstack = false
		it.regs.Reg(it.regs.SPRegNum).Uint64Val = uint64(int64(newsp))
		return true

	case "runtime.cgocallback_gofunc":
		// For a detailed description of how this works read the long comment at
		// the start of $GOROOT/src/runtime/cgocall.go and the source code of
		// runtime.cgocallback_gofunc in $GOROOT/src/runtime/asm_arm64.s
		//
		// When a C functions calls back into go it will eventually call into
		// runtime.cgocallback_gofunc which is the function that does the stack
		// switch from the system stack back into the goroutine stack
		// Since we are going backwards on the stack here we see the transition
		// as goroutine stack -> system stack.
		if it.systemstack {
			return false
		}

		if it.g0_sched_sp <= 0 {
			return false
		}
		// entering the system stack
		it.regs.Reg(it.regs.SPRegNum).Uint64Val = it.g0_sched_sp
		// reads the previous value of g0.sched.sp that runtime.cgocallback_gofunc saved on the stack

		it.g0_sched_sp, _ = readUintRaw(it.mem, uintptr(it.regs.SP()+prevG0schedSPOffsetSaveSlot), int64(it.bi.Arch.PtrSize()))
		it.systemstack = true
		return true
	default:
		return false
	}
}

// switchStack will use the current frame to determine if it's time to
// switch between the system stack and the goroutine stack or vice versa.
// Sets it.atend when the top of the stack is reached.
func (it *arm64Stack) switchStack() bool {
	if it.frame.Current.Fn == nil {
		return false
	}
	switch it.frame.Current.Fn.Name {
	case "runtime.asmcgocall":
		return false
	case "runtime.cgocallback_gofunc":
		return false
	case "runtime.goexit", "runtime.rt0_go", "runtime.mcall":
		// Look for "top of stack" functions.
		it.atend = true
		return true
	case "runtime.sigpanic":
		return false
	case "crosscall2":
		newsp, _ := readUintRaw(it.mem, uintptr(it.regs.SP()+8*24), int64(it.bi.Arch.PtrSize()))
		newbp, _ := readUintRaw(it.mem, uintptr(it.regs.SP()+8*14), int64(it.bi.Arch.PtrSize()))
		newlr, _ := readUintRaw(it.mem, uintptr(it.regs.SP()+8*15), int64(it.bi.Arch.PtrSize()))
		if it.regs.Reg(it.regs.BPRegNum) != nil {
			it.regs.Reg(it.regs.BPRegNum).Uint64Val = uint64(newbp)
		} else {
			reg, _ := it.readRegisterAt(it.regs.BPRegNum, it.regs.SP()+8*14)
			it.regs.AddReg(it.regs.BPRegNum, reg)
		}
		it.regs.Reg(it.regs.LRRegNum).Uint64Val = uint64(newlr)
		it.regs.Reg(it.regs.SPRegNum).Uint64Val = uint64(newsp)
		it.pc = newlr
		return true
	default:
		if it.systemstack && it.top && it.g != nil && strings.HasPrefix(it.frame.Current.Fn.Name, "runtime.") && it.frame.Current.Fn.Name != "runtime.fatalthrow" {
			// The runtime switches to the system stack in multiple places.
			// This usually happens through a call to runtime.systemstack but there
			// are functions that switch to the system stack manually (for example
			// runtime.morestack).
			// Since we are only interested in printing the system stack for cgo
			// calls we switch directly to the goroutine stack if we detect that the
			// function at the top of the stack is a runtime function.
			it.switchToGoroutineStack()
			return true
		}

		return false
	}
}

func (it *arm64Stack) switchToGoroutineStack() {
	it.systemstack = false
	it.top = false
	it.pc = it.g.PC
	it.regs.Reg(it.regs.SPRegNum).Uint64Val = it.g.SP
	it.regs.Reg(it.regs.BPRegNum).Uint64Val = it.g.BP
	it.regs.Reg(it.regs.LRRegNum).Uint64Val = it.g.LR
}

// Frame returns the frame the iterator is pointing at.
func (it *arm64Stack) Frame() Stackframe {
	it.frame.Bottom = it.atend
	return it.frame
}

// Err returns the error encountered during stack iteration.
func (it *arm64Stack) Err() error {
	return it.err
}

// frameBase calculates the frame base pseudo-register for DWARF for fn and
// the current frame.
func (it *arm64Stack) frameBase(fn *Function) int64 {
	rdr := fn.cu.image.dwarfReader
	rdr.Seek(fn.offset)
	e, err := rdr.Next()
	if err != nil {
		return 0
	}
	fb, _, _, _ := it.bi.Location(e, dwarf.AttrFrameBase, it.pc, it.regs)
	return fb
}

func (it *arm64Stack) newStackframe(ret, retaddr uint64) Stackframe {
	f, l, fn := it.bi.PCToLine(it.pc)
	if fn == nil {
		f = "?"
		l = -1
	} else {
		it.regs.FrameBase = it.frameBase(fn)
	}
	r := Stackframe{Current: Location{PC: it.pc, File: f, Line: l, Fn: fn}, Regs: it.regs, Ret: ret, addrret: retaddr, stackHi: it.stackhi, SystemStack: it.systemstack, lastpc: it.pc}
	if !it.top {
		fnname := ""
		if r.Current.Fn != nil {
			fnname = r.Current.Fn.Name
		}
		switch fnname {
		case "runtime.mstart", "runtime.systemstack_switch":
			// these frames are inserted by runtime.systemstack and there is no CALL
			// instruction to look for at pc - 1
			r.Call = r.Current
		default:
			if r.Current.Fn != nil && it.pc == r.Current.Fn.Entry {
				// if the return address is the entry point of the function that
				// contains it then this is some kind of fake return frame (for example
				// runtime.sigreturn) that didn't actually call the current frame,
				// attempting to get the location of the CALL instruction would just
				// obfuscate what's going on, since there is no CALL instruction.
				r.Call = r.Current
			} else {
				r.lastpc = it.pc - 1
				r.Call.File, r.Call.Line, r.Call.Fn = it.bi.PCToLine(it.pc - 1)
				if r.Call.Fn == nil {
					r.Call.File = "?"
					r.Call.Line = -1
				}
				r.Call.PC = r.Current.PC
			}
		}
	} else {
		r.Call = r.Current
	}
	return r
}

func (it *arm64Stack) Stacktrace(depth int) ([]Stackframe, error) {
	if depth < 0 {
		return nil, errors.New("negative maximum stack depth")
	}
	if it.opts&StacktraceG != 0 && it.g != nil {
		it.switchToGoroutineStack()
		it.top = true
	}
	frames := make([]Stackframe, 0, depth+1)
	for it.Next() {
		frames = it.appendInlineCalls(frames, it.Frame())
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

func (it *arm64Stack) appendInlineCalls(frames []Stackframe, frame Stackframe) []Stackframe {
	if frame.Call.Fn == nil {
		return append(frames, frame)
	}
	if frame.Call.Fn.cu.lineInfo == nil {
		return append(frames, frame)
	}

	callpc := frame.Call.PC
	if len(frames) > 0 {
		callpc--
	}

	image := frame.Call.Fn.cu.image

	irdr := reader.InlineStack(image.dwarf, frame.Call.Fn.offset, reader.ToRelAddr(callpc, image.StaticBase))
	for irdr.Next() {
		entry, offset := reader.LoadAbstractOrigin(irdr.Entry(), image.dwarfReader)

		fnname, okname := entry.Val(dwarf.AttrName).(string)
		fileidx, okfileidx := entry.Val(dwarf.AttrCallFile).(int64)
		line, okline := entry.Val(dwarf.AttrCallLine).(int64)

		if !okname || !okfileidx || !okline {
			break
		}
		if fileidx-1 < 0 || fileidx-1 >= int64(len(frame.Current.Fn.cu.lineInfo.FileNames)) {
			break
		}

		inlfn := &Function{Name: fnname, Entry: frame.Call.Fn.Entry, End: frame.Call.Fn.End, offset: offset, cu: frame.Call.Fn.cu}
		frames = append(frames, Stackframe{
			Current: frame.Current,
			Call: Location{
				frame.Call.PC,
				frame.Call.File,
				frame.Call.Line,
				inlfn,
			},
			Regs:        frame.Regs,
			stackHi:     frame.stackHi,
			Ret:         frame.Ret,
			addrret:     frame.addrret,
			Err:         frame.Err,
			SystemStack: frame.SystemStack,
			Inlined:     true,
			lastpc:      frame.lastpc,
		})

		frame.Call.File = frame.Current.Fn.cu.lineInfo.FileNames[fileidx-1].Path
		frame.Call.Line = int(line)
	}

	return append(frames, frame)
}

// advanceRegs calculates it.callFrameRegs using it.regs and the frame
// descriptor entry for the current stack frame.
// it.regs.CallFrameCFA is updated.
func (it *arm64Stack) advanceRegs() (callFrameRegs op.DwarfRegisters, ret uint64, retaddr uint64) {
	fde, err := it.bi.frameEntries.FDEForPC(it.pc)
	var framectx *frame.FrameContext
	if _, nofde := err.(*frame.ErrNoFDEForPC); nofde {
		framectx = it.bi.Arch.FixFrameUnwindContext(nil, it.pc, it.bi)
	} else {
		framectx = it.bi.Arch.FixFrameUnwindContext(fde.EstablishFrame(it.pc), it.pc, it.bi)
	}
	_ = it.switchSp()
	cfareg, err := it.executeFrameRegRule(0, framectx.CFA, 0)
	if cfareg == nil {
		it.err = fmt.Errorf("CFA becomes undefined at PC %#x", it.pc)
		return op.DwarfRegisters{}, 0, 0
	}
	it.regs.CFA = int64(cfareg.Uint64Val)

	callFrameRegs = op.DwarfRegisters{ByteOrder: it.regs.ByteOrder, PCRegNum: it.regs.PCRegNum, SPRegNum: it.regs.SPRegNum, BPRegNum: it.regs.BPRegNum, LRRegNum: it.regs.LRRegNum}

	// According to the standard the compiler should be responsible for emitting
	// rules for the RSP register so that it can then be used to calculate CFA,
	// however neither Go nor GCC do this.
	// In the following line we copy GDB's behaviour by assuming this is
	// implicit.
	// See also the comment in dwarf2_frame_default_init in
	// $GDB_SOURCE/dwarf2-frame.c
	callFrameRegs.AddReg(uint64(arm64DwarfSPRegNum), cfareg)

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

	_, _, fn := it.bi.PCToLine(it.pc)
	if fn != nil && fn.Name == "runtime.sigpanic" {
		lr, _ := readUintRaw(it.mem, uintptr(cfareg.Uint64Val), int64(it.bi.Arch.PtrSize()))
		if callFrameRegs.Regs[30] != nil {
			callFrameRegs.Regs[30].Uint64Val = uint64(int64(lr))
		} else {
			lrreg := op.DwarfRegisterFromUint64(uint64(int64(lr)))
			callFrameRegs.AddReg(30, lrreg)
		}
		origsp := uint64(cfareg.Uint64Val + spAlign)
		callFrameRegs.Regs[callFrameRegs.SPRegNum].Uint64Val = uint64(origsp)
	}

	if ret == 0 && it.regs.Regs[30] != nil {
		ret = it.regs.Regs[30].Uint64Val
	}
	return callFrameRegs, ret, retaddr
}

func (it *arm64Stack) executeFrameRegRule(regnum uint64, rule frame.DWRule, cfa int64) (*op.DwarfRegister, error) {
	switch rule.Rule {
	default:
		fallthrough
	case frame.RuleUndefined:
		return nil, nil
	case frame.RuleSameVal:
		if it.regs.Reg(regnum) == nil {
			return nil, nil
		}
		reg := *it.regs.Reg(regnum)
		return &reg, nil
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
	case frame.RuleFramePointer:
		curReg := it.regs.Reg(rule.Reg)
		if curReg == nil {
			return nil, nil
		}
		if curReg.Uint64Val <= uint64(cfa) {
			return it.readRegisterAt(regnum, curReg.Uint64Val)
		} else {
			newReg := *curReg
			return &newReg, nil
		}
	}
}

func (it *arm64Stack) readRegisterAt(regnum uint64, addr uint64) (*op.DwarfRegister, error) {
	buf := make([]byte, it.bi.Arch.RegSize(regnum))
	_, err := it.mem.ReadMemory(buf, uintptr(addr))
	if err != nil {
		return nil, err
	}
	return op.DwarfRegisterFromBytes(buf), nil
}
