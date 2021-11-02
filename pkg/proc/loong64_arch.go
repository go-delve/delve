package proc

import (
	"encoding/binary"
	"fmt"
	"github.com/go-delve/delve/pkg/dwarf/frame"
	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/regnum"
	"strings"
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
		breakInstrMovesPC:                true,
		derefTLS:                         false,
		prologues:                        prologuesLOONG64,
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
	}
}

func loong64FixFrameUnwindContext(fctxt *frame.FrameContext, pc uint64, bi *BinaryInfo) *frame.FrameContext {
	a := bi.Arch

	if a.sigreturnfn == nil {
		a.sigreturnfn = bi.LookupFunc["runtime.sigreturn"]
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
			RetAddrReg: regnum.LOONG64_PC,
			Regs: map[uint64]frame.DWRule{
				regnum.LOONG64_PC: frame.DWRule{
					Rule:   frame.RuleOffset,
					Offset: int64(-a.PtrSize()),
				},

				regnum.LOONG64_FP: frame.DWRule{
					Rule:   frame.RuleOffset,
					Offset: int64(-2 * a.PtrSize()),
				},

				regnum.LOONG64_SP: frame.DWRule{
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

	if a.crosscall2fn == nil {
		a.crosscall2fn = bi.LookupFunc["crosscall2"]
	}

	if a.crosscall2fn != nil && pc >= a.crosscall2fn.Entry && pc < a.crosscall2fn.End {
		rule := fctxt.CFA

		if rule.Offset == crosscall2SPOffsetBad {
			switch bi.GOOS {
			case "windows":
				rule.Offset += crosscall2SPOffsetWindows
			default:
				rule.Offset += crosscall2SPOffsetNonWindows
			}
		}

		fctxt.CFA = rule
	}

	// We assume that RBP is the frame pointer and we want to keep it updated,
	// so that we can use it to unwind the stack even when we encounter frames
	// without descriptor entries.
	// If there isn't a rule already we emit one.
	if fctxt.Regs[regnum.LOONG64_FP].Rule == frame.RuleUndefined {
		fctxt.Regs[regnum.LOONG64_FP] = frame.DWRule{
			Rule:   frame.RuleFramePointer,
			Reg:    regnum.LOONG64_FP,
			Offset: 0,
		}
	}

	if fctxt.Regs[regnum.LOONG64_LR].Rule == frame.RuleUndefined {
		fctxt.Regs[regnum.LOONG64_LR] = frame.DWRule{
			Rule:   frame.RuleFramePointer,
			Reg:    regnum.LOONG64_LR,
			Offset: 0,
		}
	}

	return fctxt
}

const loong64cgocallSPOffsetSaveSlot = 0x8

func loong64SwitchStack(it *stackIterator, callFrameRegs *op.DwarfRegisters) bool {
	if it.frame.Current.Fn != nil {
		switch it.frame.Current.Fn.Name {
		case "runtime.asmcgocall", "runtime.cgocallback_gofunc", "runtime.sigpanic":
			//do nothing

		case "runtime.goexit", "runtime.rt0_go", "runtime.mcall":
			// Look for "top of stack" functions.
			it.atend = true
			return true

		case "crosscall2":
			//The offsets get from runtime/cgo/asm_loong64.s:10
			newsp, _ := readUintRaw(it.mem, uint64(it.regs.SP()+8*24), int64(it.bi.Arch.PtrSize()))
			newbp, _ := readUintRaw(it.mem, uint64(it.regs.SP()+8*14), int64(it.bi.Arch.PtrSize()))
			newlr, _ := readUintRaw(it.mem, uint64(it.regs.SP()+8*15), int64(it.bi.Arch.PtrSize()))

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
			if (it.systemstack && it.top && it.g != nil) &&
				(strings.HasPrefix(it.frame.Current.Fn.Name, "runtime.")) &&
				(it.frame.Current.Fn.Name != "runtime.fatalthrow") {
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
			uint64(callFrameRegs.SP()+loong64cgocallSPOffsetSaveSlot),
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

	case "runtime.cgocallback_gofunc":
		// For a detailed description of how this works read the long comment at
		// the start of $GOROOT/src/runtime/cgocall.go and the source code of
		// runtime.cgocallback_gofunc in $GOROOT/src/runtime/asm_loong64.s
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
		it.g0_sched_sp, _ = readUintRaw(it.mem,
			uint64(callFrameRegs.SP()+prevG0schedSPOffsetSaveSlot),
			int64(it.bi.Arch.PtrSize()))
		it.systemstack = true

		return false
	}

	return false
}

func loong64RegSize(regnum uint64) int {
	// All CPU registers are 64bit
	return 8
}

var loong64NameToDwarf = func() map[string]int {
	r := make(map[string]int)
	for i := 0; i <= 31; i++ {
		r[fmt.Sprintf("r%d", i)] = i
	}

	r["era"] = int(regnum.LOONG64_ERA)
	r["badv"] = int(regnum.LOONG64_BADV)

	return r
}()

func loong64RegistersToDwarfRegisters(staticBase uint64, regs Registers) *op.DwarfRegisters {
	dregs := initDwarfRegistersFromSlice(int(regnum.LOONG64MaxRegNum()),
		regs, regnum.LOONG64NameToDwarf)

	dr := op.NewDwarfRegisters(staticBase, dregs, binary.LittleEndian, regnum.LOONG64_PC,
		regnum.LOONG64_SP, regnum.LOONG64_FP, regnum.LOONG64_LR)

	dr.SetLoadMoreCallback(loadMoreDwarfRegistersFromSliceFunc(dr, regs, loong64NameToDwarf))

	return dr
}

func loong64AddrAndStackRegsToDwarfRegisters(staticBase, pc, sp, bp, lr uint64) op.DwarfRegisters {
	dregs := make([]*op.DwarfRegister, int(regnum.LOONG64MaxRegNum()))

	dregs[regnum.LOONG64_PC] = op.DwarfRegisterFromUint64(pc)
	dregs[regnum.LOONG64_SP] = op.DwarfRegisterFromUint64(sp)
	dregs[regnum.LOONG64_FP] = op.DwarfRegisterFromUint64(bp)
	dregs[regnum.LOONG64_LR] = op.DwarfRegisterFromUint64(lr)

	return *op.NewDwarfRegisters(staticBase, dregs, binary.LittleEndian, regnum.LOONG64_PC,
		regnum.LOONG64_SP, regnum.LOONG64_FP, regnum.LOONG64_LR)
}

func loong64DwarfRegisterToString(i int, reg *op.DwarfRegister) (name string, floatingPoint bool, repr string) {
	name = regnum.LOONG64ToName(uint64(i))

	if reg == nil {
		return name, false, ""
	}

	// Only support general-purpose registers
	floatingPoint = false
	repr = fmt.Sprintf("%#016x", reg.Uint64Val)

	return name, floatingPoint, repr
}
