package proc

import (
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/go-delve/delve/pkg/dwarf/frame"
	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/regnum"
)

// ebreak instruction: 0x00100073
var riscv64BreakInstruction = []byte{0x73, 0x00, 0x10, 0x00}

// c.ebreak instruction: 0x9002
var riscv64CompressedBreakInstruction = []byte{0x02, 0x90}

// RISCV64Arch returns an initialized RISCV64 struct.
func RISCV64Arch(goos string) *Arch {
	return &Arch{
		Name:                             "riscv64",
		ptrSize:                          8,
		maxInstructionLength:             4,
		breakpointInstruction:            riscv64CompressedBreakInstruction,
		altBreakpointInstruction:         riscv64BreakInstruction,
		breakInstrMovesPC:                false,
		derefTLS:                         false,
		prologues:                        nil,
		fixFrameUnwindContext:            riscv64FixFrameUnwindContext,
		switchStack:                      riscv64SwitchStack,
		regSize:                          riscv64RegSize,
		RegistersToDwarfRegisters:        riscv64RegistersToDwarfRegisters,
		addrAndStackRegsToDwarfRegisters: riscv64AddrAndStackRegsToDwarfRegisters,
		DwarfRegisterToString:            riscv64DwarfRegisterToString,
		inhibitStepInto:                  func(*BinaryInfo, uint64) bool { return false },
		asmDecode:                        riscv64AsmDecode,
		usesLR:                           true,
		PCRegNum:                         regnum.RISCV64_PC,
		SPRegNum:                         regnum.RISCV64_SP,
		asmRegisters:                     riscv64AsmRegisters,
		RegisterNameToDwarf:              nameToDwarfFunc(regnum.RISCV64NameToDwarf),
		RegnumToString:                   regnum.RISCV64ToName,
		debugCallMinStackSize:            288,         // TODO
		maxRegArgBytes:                   16*8 + 16*8, // 16 int argument registers plus 16 float argument registers
	}
}

func riscv64FixFrameUnwindContext(fctxt *frame.FrameContext, pc uint64, bi *BinaryInfo) *frame.FrameContext {
	a := bi.Arch

	if a.sigreturnfn == nil {
		a.sigreturnfn = bi.lookupOneFunc("runtime.sigreturn")
	}

	if (fctxt == nil) || ((a.sigreturnfn != nil) && (pc >= a.sigreturnfn.Entry) && (pc < a.sigreturnfn.End)) {
		// When there's no frame descriptor entry use BP (the frame pointer) instead
		// - return register is [bp + a.PtrSize()] (i.e. [cfa-a.PtrSize()])
		// - cfa is bp + a.PtrSize()*2
		// - bp is [bp] (i.e. [cfa-a.PtrSize()*2])
		// - sp is cfa

		// When the signal handler runs it will move the execution to the signal
		// handling stack (installed using the sigaltstack system call).
		// This isn't a proper stack switch: the pointer to g in TLS will still
		// refer to whatever g was executing on that thread before the signal was
		// received.
		// Since go did not execute a stack switch the previous value of sp, pc
		// and bp is not saved inside g.sched, as it normally would.
		// The only way to recover is to either read sp/pc from the signal context
		// parameter (the ucontext_t* parameter) or to unconditionally follow the
		// frame pointer when we get to runtime.sigreturn (which is what we do
		// here).

		return &frame.FrameContext{
			RetAddrReg: regnum.RISCV64_PC,
			Regs: map[uint64]frame.DWRule{
				regnum.RISCV64_PC: {
					Rule:   frame.RuleOffset,
					Offset: int64(-a.PtrSize()),
				},

				regnum.RISCV64_FP: {
					Rule:   frame.RuleOffset,
					Offset: int64(-2 * a.PtrSize()),
				},

				regnum.RISCV64_SP: {
					Rule:   frame.RuleValOffset,
					Offset: 0,
				},
			},

			CFA: frame.DWRule{
				Rule:   frame.RuleCFA,
				Reg:    regnum.RISCV64_FP,
				Offset: int64(2 * a.PtrSize()),
			},
		}
	}

	if a.crosscall2fn == nil {
		a.crosscall2fn = bi.lookupOneFunc("crosscall2")
	}

	if a.crosscall2fn != nil && pc >= a.crosscall2fn.Entry && pc < a.crosscall2fn.End {
		rule := fctxt.CFA

		if rule.Offset == crosscall2SPOffsetBad {
			rule.Offset += crosscall2SPOffset
		}
		fctxt.CFA = rule
	}

	// We assume that FP is the frame pointer and we want to keep it updated,
	// so that we can use it to unwind the stack even when we encounter frames
	// without descriptor entries.
	// If there isn't a rule already we emit one.
	if fctxt.Regs[regnum.RISCV64_FP].Rule == frame.RuleUndefined {
		fctxt.Regs[regnum.RISCV64_FP] = frame.DWRule{
			Rule:   frame.RuleFramePointer,
			Reg:    regnum.RISCV64_FP,
			Offset: 0,
		}
	}

	if fctxt.Regs[regnum.RISCV64_LR].Rule == frame.RuleUndefined {
		fctxt.Regs[regnum.RISCV64_LR] = frame.DWRule{
			Rule:   frame.RuleRegister,
			Reg:    regnum.RISCV64_LR,
			Offset: 0,
		}
	}

	return fctxt
}

const riscv64cgocallSPOffsetSaveSlot = 0x8
const riscv64prevG0schedSPOffsetSaveSlot = 0x10

func riscv64SwitchStack(it *stackIterator, callFrameRegs *op.DwarfRegisters) bool {
	if it.frame.Current.Fn == nil {
		if it.systemstack && it.g != nil && it.top {
			it.switchToGoroutineStack()
			return true
		}
		return false
	}
	switch it.frame.Current.Fn.Name {
	case "runtime.cgocallback_gofunc", "runtime.cgocallback", "runtime.asmcgocall", "crosscall2":
		// cgostacktrace is broken on riscv64, so do nothing here.

	case "runtime.goexit", "runtime.rt0_go", "runtime.mcall":
		// Look for "top of stack" functions.
		it.atend = true
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
		off, _ := readIntRaw(it.mem, uint64(callFrameRegs.SP()+riscv64cgocallSPOffsetSaveSlot), int64(it.bi.Arch.PtrSize()))
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

	case "runtime.cgocallback_gofunc":
		// For a detailed description of how this works read the long comment at
		// the start of $GOROOT/src/runtime/cgocall.go and the source code of
		// runtime.cgocallback_gofunc in $GOROOT/src/runtime/asm_riscv64.s
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
		it.g0_sched_sp, _ = readUintRaw(it.mem, uint64(callFrameRegs.SP()+riscv64prevG0schedSPOffsetSaveSlot), int64(it.bi.Arch.PtrSize()))
		it.systemstack = true
		return false
	}

	return false
}

func riscv64RegSize(regnum uint64) int {
	// All CPU registers are 64bit
	return 8
}

func riscv64RegistersToDwarfRegisters(staticBase uint64, regs Registers) *op.DwarfRegisters {
	dregs := initDwarfRegistersFromSlice(int(regnum.RISCV64MaxRegNum()), regs, regnum.RISCV64NameToDwarf)
	dr := op.NewDwarfRegisters(staticBase, dregs, binary.LittleEndian, regnum.RISCV64_PC, regnum.RISCV64_SP, regnum.RISCV64_FP, regnum.RISCV64_LR)
	dr.SetLoadMoreCallback(loadMoreDwarfRegistersFromSliceFunc(dr, regs, regnum.RISCV64NameToDwarf))
	return dr
}

func riscv64AddrAndStackRegsToDwarfRegisters(staticBase, pc, sp, bp, lr uint64) op.DwarfRegisters {
	dregs := make([]*op.DwarfRegister, int(regnum.RISCV64_PC+1))
	dregs[regnum.RISCV64_PC] = op.DwarfRegisterFromUint64(pc)
	dregs[regnum.RISCV64_SP] = op.DwarfRegisterFromUint64(sp)
	dregs[regnum.RISCV64_FP] = op.DwarfRegisterFromUint64(bp)
	dregs[regnum.RISCV64_LR] = op.DwarfRegisterFromUint64(lr)

	return *op.NewDwarfRegisters(staticBase, dregs, binary.LittleEndian, regnum.RISCV64_PC, regnum.RISCV64_SP, regnum.RISCV64_FP, regnum.RISCV64_LR)
}

func riscv64DwarfRegisterToString(i int, reg *op.DwarfRegister) (name string, floatingPoint bool, repr string) {
	name = regnum.RISCV64ToName(uint64(i))

	if reg == nil {
		return name, false, ""
	}

	if strings.HasPrefix(name, "F") {
		return name, true, fmt.Sprintf("%#016x", reg.Uint64Val)
	} else {
		return name, false, fmt.Sprintf("%#016x", reg.Uint64Val)
	}
}
