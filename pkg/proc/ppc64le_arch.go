package proc

import (
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/go-delve/delve/pkg/dwarf/frame"

	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/regnum"
)

// This is the unconditional trap, the same mnemonic that both clang and gcc use
// It's documented in Section C.6 Trap Mnemonics in the Power ISA Book 3
var ppc64leBreakInstruction = []byte{0x08, 0x00, 0xe0, 0x7f}

func PPC64LEArch(goos string) *Arch {
	return &Arch{
		Name:                             "ppc64le",
		ptrSize:                          8,
		maxInstructionLength:             4,
		breakpointInstruction:            ppc64leBreakInstruction,
		breakInstrMovesPC:                false,
		derefTLS:                         false, // Chapter 3.7 of the ELF V2 ABI Specification
		prologues:                        prologuesPPC64LE,
		fixFrameUnwindContext:            ppc64leFixFrameUnwindContext,
		switchStack:                      ppc64leSwitchStack,
		regSize:                          ppc64leRegSize,
		RegistersToDwarfRegisters:        ppc64leRegistersToDwarfRegisters,
		addrAndStackRegsToDwarfRegisters: ppc64leAddrAndStackRegsToDwarfRegisters,
		DwarfRegisterToString:            ppc64leDwarfRegisterToString,
		inhibitStepInto:                  func(*BinaryInfo, uint64) bool { return false },
		asmDecode:                        ppc64leAsmDecode,
		usesLR:                           true,
		PCRegNum:                         regnum.PPC64LE_PC,
		SPRegNum:                         regnum.PPC64LE_SP,
		ContextRegNum:                    regnum.PPC64LE_R0 + 11,
		LRRegNum:                         regnum.PPC64LE_LR,
		asmRegisters:                     ppc64leAsmRegisters,
		RegisterNameToDwarf:              nameToDwarfFunc(regnum.PPC64LENameToDwarf),
		debugCallMinStackSize:            320,
		maxRegArgBytes:                   13*8 + 13*8,
		argumentRegs:                     []int{regnum.PPC64LE_R0 + 3, regnum.PPC64LE_R0 + 4, regnum.PPC64LE_R0 + 5},
	}
}

func ppc64leFixFrameUnwindContext(fctxt *frame.FrameContext, pc uint64, bi *BinaryInfo) *frame.FrameContext {
	a := bi.Arch
	if a.sigreturnfn == nil {
		a.sigreturnfn = bi.lookupOneFunc("runtime.sigreturn")
	}
	if fctxt == nil || (a.sigreturnfn != nil && pc >= a.sigreturnfn.Entry && pc < a.sigreturnfn.End) {
		return &frame.FrameContext{
			RetAddrReg: regnum.PPC64LE_LR,
			Regs: map[uint64]frame.DWRule{
				regnum.PPC64LE_PC: {
					Rule:   frame.RuleOffset,
					Offset: int64(-a.PtrSize()),
				},
				regnum.PPC64LE_LR: {
					Rule:   frame.RuleOffset,
					Offset: int64(-2 * a.PtrSize()),
				},
				regnum.PPC64LE_SP: {
					Rule:   frame.RuleValOffset,
					Offset: 0,
				},
			},
			CFA: frame.DWRule{
				Rule:   frame.RuleCFA,
				Reg:    regnum.PPC64LE_SP,
				Offset: int64(2 * a.PtrSize()),
			},
		}
	}
	if a.crosscall2fn == nil {
		// This is used to fix issues with the c calling frames
		a.crosscall2fn = bi.lookupOneFunc("crosscall2")
	}

	// Checks if we marked the function as a crosscall and if we are currently in it
	if a.crosscall2fn != nil && pc >= a.crosscall2fn.Entry && pc < a.crosscall2fn.End {
		rule := fctxt.CFA
		if rule.Offset == crosscall2SPOffsetBad {
			// Linux support only
			rule.Offset += crosscall2SPOffsetLinuxPPC64LE
		}
		fctxt.CFA = rule
	}
	if fctxt.Regs[regnum.PPC64LE_LR].Rule == frame.RuleUndefined {
		fctxt.Regs[regnum.PPC64LE_LR] = frame.DWRule{
			Rule:   frame.RuleFramePointer,
			Reg:    regnum.PPC64LE_LR,
			Offset: 0,
		}
	}
	return fctxt
}

const ppc64cgocallSPOffsetSaveSlot = 32
const ppc64prevG0schedSPOffsetSaveSlot = 40

func ppc64leSwitchStack(it *stackIterator, callFrameRegs *op.DwarfRegisters) bool {
	if it.frame.Current.Fn == nil && it.systemstack && it.g != nil && it.top {
		it.switchToGoroutineStack()
		return true
	}
	if it.frame.Current.Fn != nil {
		switch it.frame.Current.Fn.Name {
		case "runtime.asmcgocall", "runtime.cgocallback_gofunc", "runtime.sigpanic", "runtime.cgocallback":
			//do nothing
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
		case "crosscall2":
			//The offsets get from runtime/cgo/asm_ppc64x.s:10
			newsp, _ := readUintRaw(it.mem, it.regs.SP()+8*24, int64(it.bi.Arch.PtrSize()))
			newbp, _ := readUintRaw(it.mem, it.regs.SP()+8*14, int64(it.bi.Arch.PtrSize()))
			newlr, _ := readUintRaw(it.mem, it.regs.SP()+16, int64(it.bi.Arch.PtrSize()))
			if it.regs.Reg(it.regs.BPRegNum) != nil {
				it.regs.Reg(it.regs.BPRegNum).Uint64Val = newbp
			} else {
				reg, _ := it.readRegisterAt(it.regs.BPRegNum, it.regs.SP()+8*14)
				it.regs.AddReg(it.regs.BPRegNum, reg)
			}
			it.regs.Reg(it.regs.LRRegNum).Uint64Val = newlr
			it.regs.Reg(it.regs.SPRegNum).Uint64Val = newsp
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
		}
	}
	fn := it.bi.PCToFunc(it.frame.Ret)
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
		// Since we are unwinding the stack from callee to caller we have to switch
		// from the system stack to the goroutine stack.
		off, _ := readIntRaw(it.mem,
			callFrameRegs.SP()+ppc64cgocallSPOffsetSaveSlot,
			int64(it.bi.Arch.PtrSize()))
		oldsp := callFrameRegs.SP()
		newsp := uint64(int64(it.stackhi) - off)

		// runtime.asmcgocall can also be called from inside the system stack,
		// in that case no stack switch actually happens
		if newsp == oldsp {
			return false
		}
		it.systemstack = false
		callFrameRegs.Reg(callFrameRegs.SPRegNum).Uint64Val = uint64(int64(newsp))
		return false

	case "runtime.cgocallback_gofunc", "runtime.cgocallback":
		// For a detailed description of how this works read the long comment at
		// the start of $GOROOT/src/runtime/cgocall.go and the source code of
		// runtime.cgocallback_gofunc in $GOROOT/src/runtime/asm_ppc64.s
		//
		// When a C functions calls back into go it will eventually call into
		// runtime.cgocallback_gofunc which is the function that does the stack
		// switch from the system stack back into the goroutine stack
		// Since we are going backwards on the stack here we see the transition
		// as goroutine stack -> system stack.
		if it.systemstack {
			return false
		}

		it.loadG0SchedSP()
		if it.g0_sched_sp <= 0 {
			return false
		}
		// entering the system stack
		callFrameRegs.Reg(callFrameRegs.SPRegNum).Uint64Val = it.g0_sched_sp
		// reads the previous value of g0.sched.sp that runtime.cgocallback_gofunc saved on the stack

		// TODO: is this save slot correct?
		it.g0_sched_sp, _ = readUintRaw(it.mem, callFrameRegs.SP()+ppc64prevG0schedSPOffsetSaveSlot, int64(it.bi.Arch.PtrSize()))
		it.systemstack = true
		return false
	}

	return false
}

// ppc64leRegSize returns the size (in bytes) of register regnum.
func ppc64leRegSize(regnum uint64) int {
	return 8 // each register is a 64-bit register
}

func ppc64leRegistersToDwarfRegisters(staticBase uint64, regs Registers) *op.DwarfRegisters {
	dregs := initDwarfRegistersFromSlice(int(regnum.PPC64LEMaxRegNum()), regs, regnum.PPC64LENameToDwarf)
	dr := op.NewDwarfRegisters(staticBase, dregs, binary.LittleEndian, regnum.PPC64LE_PC, regnum.PPC64LE_SP, regnum.PPC64LE_SP, regnum.PPC64LE_LR)
	dr.SetLoadMoreCallback(loadMoreDwarfRegistersFromSliceFunc(dr, regs, regnum.PPC64LENameToDwarf))
	return dr
}

func ppc64leAddrAndStackRegsToDwarfRegisters(staticBase, pc, sp, bp, lr uint64) op.DwarfRegisters {
	dregs := make([]*op.DwarfRegister, regnum.PPC64LE_LR+1)
	dregs[regnum.PPC64LE_PC] = op.DwarfRegisterFromUint64(pc)
	dregs[regnum.PPC64LE_SP] = op.DwarfRegisterFromUint64(sp)
	dregs[regnum.PPC64LE_LR] = op.DwarfRegisterFromUint64(lr)

	return *op.NewDwarfRegisters(staticBase, dregs, binary.LittleEndian, regnum.PPC64LE_PC, regnum.PPC64LE_SP, 0, regnum.PPC64LE_LR)
}

func ppc64leDwarfRegisterToString(i int, reg *op.DwarfRegister) (name string, floatingPoint bool, repr string) {
	name = regnum.PPC64LEToName(uint64(i))

	if reg == nil {
		return name, false, ""
	}

	if reg.Bytes == nil || (reg.Bytes != nil && len(reg.Bytes) < 16) {
		return name, false, fmt.Sprintf("%#016x", reg.Uint64Val)
	}
	return name, true, fmt.Sprintf("%#x", reg.Bytes)
}
