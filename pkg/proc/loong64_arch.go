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

func loong64SwitchStack(it *stackIterator, callFrameRegs *op.DwarfRegisters) bool {
	if it.frame.Current.Fn == nil {
		if it.systemstack && it.g != nil && it.top {
			it.switchToGoroutineStack()
			return true
		}
		return false
	}
	switch it.frame.Current.Fn.Name {
	case "runtime.goexit", "runtime.rt0_go", "runtime.mcall":
		// Look for "top of stack" functions.
		it.atend = true
		return true

	default:
		if it.systemstack && it.top && it.g != nil && strings.HasPrefix(it.frame.Current.Fn.Name, "runtime.") && it.frame.Current.Fn.Name != "runtime.throw" && it.frame.Current.Fn.Name != "runtime.fatalthrow" {
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
