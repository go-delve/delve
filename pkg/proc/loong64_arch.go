package proc

import (
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/go-delve/delve/pkg/dwarf/frame"
	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/regnum"
)

// Break Instruction : 0x002a0000
var loong64BreakInstruction = []byte{0x00, 0x00, 0x2a, 0x00}

// LOONG64Arch returns an initialized LOONG64 struct.
func LOONG64Arch(goos string) *Arch {
	return &Arch{
		Name:                             "loong64",
		ptrSize:                          8,
		maxInstructionLength:             4,
		breakpointInstruction:            loong64BreakInstruction,
		breakInstrMovesPC:                false,
		derefTLS:                         false,
		prologues:                        nil,
		fixFrameUnwindContext:            loong64FixFrameUnwindContext,
		switchStack:                      loong64SwitchStack,
		regSize:                          loong64RegSize,
		RegistersToDwarfRegisters:        loong64RegistersToDwarfRegisters,
		addrAndStackRegsToDwarfRegisters: loong64AddrAndStackRegsToDwarfRegisters,
		DwarfRegisterToString:            loong64DwarfRegisterToString,
		inhibitStepInto:                  func(*BinaryInfo, uint64) bool { return false },
		asmDecode:                        loong64AsmDecode,
		usesLR:                           true,
		PCRegNum:                         regnum.LOONG64_PC,
		SPRegNum:                         regnum.LOONG64_SP,
		ContextRegNum:                    regnum.LOONG64_R0 + 29,
		asmRegisters:                     loong64AsmRegisters,
		RegisterNameToDwarf:              nameToDwarfFunc(regnum.LOONG64NameToDwarf),
		RegnumToString:                   regnum.LOONG64ToName,
		debugCallMinStackSize:            280,
		maxRegArgBytes:                   16*8 + 16*8, // 16 int argument registers plus 16 float argument registers
	}
}

func loong64FixFrameUnwindContext(fctxt *frame.FrameContext, pc uint64, bi *BinaryInfo) *frame.FrameContext {
	a := bi.Arch
	if fctxt == nil {
		// When there's no frame descriptor entry use BP (the frame pointer) instead
		// - return register is [bp + a.PtrSize()] (i.e. [cfa-a.PtrSize()])
		// - cfa is bp + a.PtrSize()*2
		// - bp is [bp] (i.e. [cfa-a.PtrSize()*2])
		// - sp is cfa
		return &frame.FrameContext{
			RetAddrReg: regnum.LOONG64_LR,
			Regs: map[uint64]frame.DWRule{
				regnum.LOONG64_LR: {
					Rule:   frame.RuleOffset,
					Offset: int64(-a.PtrSize()),
				},

				regnum.LOONG64_FP: {
					Rule:   frame.RuleOffset,
					Offset: int64(-2 * a.PtrSize()),
				},

				regnum.LOONG64_SP: {
					Rule:   frame.RuleValOffset,
					Offset: 0,
				},
			},

			CFA: frame.DWRule{
				Rule:   frame.RuleCFA,
				Reg:    regnum.LOONG64_FP,
				Offset: int64(2 * a.PtrSize()),
			},
		}
	}

	if fctxt.Regs[regnum.LOONG64_LR].Rule == frame.RuleUndefined {
		fctxt.Regs[regnum.LOONG64_LR] = frame.DWRule{
			Rule:   frame.RuleRegister,
			Reg:    regnum.LOONG64_LR,
			Offset: 0,
		}
	}

	return fctxt
}

const loong64cgocallSPOffsetSaveSlot = 0x8

func loong64SwitchStack(it *stackIterator, callFrameRegs *op.DwarfRegisters) bool {
	if it.frame.Current.Fn == nil {
		if it.systemstack && it.g != nil && it.top {
			if err := it.switchToGoroutineStack(); err != nil {
				it.err = err
				return false
			}
			return true
		}
		return false
	}
	switch it.frame.Current.Fn.Name {
	case "runtime.goexit", "runtime.rt0_go":
		// Look for "top of stack" functions.
		it.atend = true
		return true

	case "runtime.mcall":
		if it.systemstack && it.g != nil {
			it.switchToGoroutineStack()
			return true
		}
		it.atend = true
		return true

	case "runtime.asmcgocall":
		if it.top || !it.systemstack {
			return false
		}
		// This function is called by a goroutine to execute a C function and
		// switches from the goroutine stack to the system stack.
		// Since we are unwinding the stack from callee to caller we have to switch
		// from the system stack to the goroutine stack.
		oldsp := it.regs.SP()
		off, _ := readIntRaw(it.mem, oldsp+loong64cgocallSPOffsetSaveSlot, int64(it.bi.Arch.PtrSize()))
		newsp := uint64(int64(it.stackhi) - off)

		// The runtime.asmcgocall prologue contains: addi.d $sp, $sp, -8,
		// hence we require newsp + 8 at this point
		it.regs.Reg(it.regs.SPRegNum).Uint64Val = newsp + 8

		// runtime.asmcgocall can also be called from inside the system stack,
		// in that case no stack switch actually happens
		if it.regs.SP() == oldsp {
			return false
		}

		it.top = false
		it.systemstack = false
		// The return value is stored in the LR register which is saved at -8(SP).
		addrret := uint64(int64(it.regs.SP()) - int64(it.bi.Arch.PtrSize()))
		it.frame.Ret, _ = readUintRaw(it.mem, addrret, int64(it.bi.Arch.PtrSize()))
		it.pc = it.frame.Ret
		return true

	case "runtime.cgocallback_gofunc", "runtime.cgocallback":
		// For a detailed description of how this works read the long comment at
		// the start of $GOROOT/src/runtime/cgocall.go and the source code of
		// runtime.cgocallback_gofunc in $GOROOT/src/runtime/asm_loong64.s
		//
		// When a C functions calls back into go it will eventually call into
		// runtime.cgocallback_gofunc which is the function that does the stack
		// switch from the system stack back into the goroutine stack
		// Since we are going backwards on the stack here we see the transition
		// as goroutine stack -> system stack.
		if it.top || it.systemstack {
			return false
		}

		it.loadG0SchedSP()
		if it.g0_sched_sp <= 0 {
			return false
		}
		// entering the system stack
		it.regs.Reg(it.regs.SPRegNum).Uint64Val = it.g0_sched_sp
		// reads the previous value of g0.sched.sp that runtime.cgocallback_gofunc saved on the stack
		it.g0_sched_sp, _ = readUintRaw(it.mem, it.regs.SP()+loong64cgocallSPOffsetSaveSlot, int64(it.bi.Arch.PtrSize()))
		it.top = false
		callFrameRegs, ret, retaddr := it.advanceRegs()
		frameOnSystemStack := it.newStackframe(ret, retaddr)
		it.pc = frameOnSystemStack.Ret
		it.regs = callFrameRegs
		it.systemstack = true
		return true

	case "crosscall2":
		// The offsets get from runtime/cgo/asm_loong64.s:25
		newsp, _ := readUintRaw(it.mem, it.regs.SP()+8*23, int64(it.bi.Arch.PtrSize()))
		newbp, _ := readUintRaw(it.mem, it.regs.SP()+8*4, int64(it.bi.Arch.PtrSize()))
		newlr, _ := readUintRaw(it.mem, it.regs.SP()+8*22, int64(it.bi.Arch.PtrSize()))
		if it.regs.Reg(it.regs.BPRegNum) != nil {
			it.regs.Reg(it.regs.BPRegNum).Uint64Val = newbp
		} else {
			reg, _ := it.readRegisterAt(it.regs.BPRegNum, it.regs.SP()+8*4)
			it.regs.AddReg(it.regs.BPRegNum, reg)
		}
		it.regs.Reg(it.regs.LRRegNum).Uint64Val = newlr
		it.regs.Reg(it.regs.SPRegNum).Uint64Val = newsp
		it.pc = newlr
		return true

	case "runtime.mstart":
		// Calls to runtime.systemstack will switch to the systemstack then:
		// 1. alter the goroutine stack so that it looks like systemstack_switch
		//    was called
		// 2. alter the system stack so that it looks like the bottom-most frame
		//    belongs to runtime.mstart
		// If we find a runtime.mstart frame on the system stack of a goroutine
		// parked on runtime.systemstack_switch we assume runtime.systemstack was
		// called and continue tracing from the parked position.

		if it.top || !it.systemstack || it.g == nil {
			return false
		}
		if fn := it.bi.PCToFunc(it.g.PC); fn == nil || fn.Name != "runtime.systemstack_switch" {
			return false
		}

		it.switchToGoroutineStack()
		return true

	case "runtime.newstack", "runtime.systemstack":
		if it.systemstack && it.g != nil {
			it.switchToGoroutineStack()
			return true
		}
		return false
	}

	return false
}

func loong64RegSize(regnum uint64) int {
	// All CPU registers are 64bit
	return 8
}

func loong64RegistersToDwarfRegisters(staticBase uint64, regs Registers) *op.DwarfRegisters {
	dregs := initDwarfRegistersFromSlice(int(regnum.LOONG64MaxRegNum()), regs, regnum.LOONG64NameToDwarf)
	dr := op.NewDwarfRegisters(staticBase, dregs, binary.LittleEndian, regnum.LOONG64_PC, regnum.LOONG64_SP, regnum.LOONG64_FP, regnum.LOONG64_LR)
	dr.SetLoadMoreCallback(loadMoreDwarfRegistersFromSliceFunc(dr, regs, regnum.LOONG64NameToDwarf))
	return dr
}

func loong64AddrAndStackRegsToDwarfRegisters(staticBase, pc, sp, bp, lr uint64) op.DwarfRegisters {
	dregs := make([]*op.DwarfRegister, int(regnum.LOONG64MaxRegNum()))
	dregs[regnum.LOONG64_PC] = op.DwarfRegisterFromUint64(pc)
	dregs[regnum.LOONG64_SP] = op.DwarfRegisterFromUint64(sp)
	dregs[regnum.LOONG64_FP] = op.DwarfRegisterFromUint64(bp)
	dregs[regnum.LOONG64_LR] = op.DwarfRegisterFromUint64(lr)

	return *op.NewDwarfRegisters(staticBase, dregs, binary.LittleEndian, regnum.LOONG64_PC, regnum.LOONG64_SP, regnum.LOONG64_FP, regnum.LOONG64_LR)
}

func loong64DwarfRegisterToString(i int, reg *op.DwarfRegister) (name string, floatingPoint bool, repr string) {
	name = regnum.LOONG64ToName(uint64(i))

	if reg == nil {
		return name, false, ""
	}

	if strings.HasPrefix(name, "FCC") {
		return name, true, fmt.Sprintf("%#x", reg.Uint64Val)
	} else if strings.HasPrefix(name, "F") {
		return name, true, fmt.Sprintf("%#016x", reg.Uint64Val)
	} else {
		return name, false, fmt.Sprintf("%#016x", reg.Uint64Val)
	}
}
