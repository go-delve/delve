package proc

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"strings"

	"github.com/go-delve/delve/pkg/dwarf/frame"
	"github.com/go-delve/delve/pkg/dwarf/op"
	"golang.org/x/arch/arm64/arm64asm"
)

const (
	arm64DwarfIPRegNum uint64 = 32
	arm64DwarfSPRegNum uint64 = 31
	arm64DwarfLRRegNum uint64 = 30
	arm64DwarfBPRegNum uint64 = 29
)

var arm64BreakInstruction = []byte{0x0, 0x0, 0x20, 0xd4}

// ARM64Arch returns an initialized ARM64
// struct.
func ARM64Arch(goos string) *Arch {
	return &Arch{
		Name:                             "arm64",
		ptrSize:                          8,
		maxInstructionLength:             4,
		breakpointInstruction:            arm64BreakInstruction,
		breakInstrMovesPC:                false,
		derefTLS:                         false,
		prologues:                        prologuesARM64,
		fixFrameUnwindContext:            arm64FixFrameUnwindContext,
		switchStack:                      arm64SwitchStack,
		regSize:                          arm64RegSize,
		RegistersToDwarfRegisters:        arm64RegistersToDwarfRegisters,
		addrAndStackRegsToDwarfRegisters: arm64AddrAndStackRegsToDwarfRegisters,
		DwarfRegisterToString:            arm64DwarfRegisterToString,
		inhibitStepInto:                  func(*BinaryInfo, uint64) bool { return false },
		asmDecode:                        arm64AsmDecode,
		usesLR:                           true,
	}
}

func arm64FixFrameUnwindContext(fctxt *frame.FrameContext, pc uint64, bi *BinaryInfo) *frame.FrameContext {
	a := bi.Arch
	if a.sigreturnfn == nil {
		a.sigreturnfn = bi.LookupFunc["runtime.sigreturn"]
	}

	if fctxt == nil || (a.sigreturnfn != nil && pc >= a.sigreturnfn.Entry && pc < a.sigreturnfn.End) {
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
			RetAddrReg: arm64DwarfIPRegNum,
			Regs: map[uint64]frame.DWRule{
				arm64DwarfIPRegNum: frame.DWRule{
					Rule:   frame.RuleOffset,
					Offset: int64(-a.PtrSize()),
				},
				arm64DwarfBPRegNum: frame.DWRule{
					Rule:   frame.RuleOffset,
					Offset: int64(-2 * a.PtrSize()),
				},
				arm64DwarfSPRegNum: frame.DWRule{
					Rule:   frame.RuleValOffset,
					Offset: 0,
				},
			},
			CFA: frame.DWRule{
				Rule:   frame.RuleCFA,
				Reg:    arm64DwarfBPRegNum,
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
	if fctxt.Regs[arm64DwarfBPRegNum].Rule == frame.RuleUndefined {
		fctxt.Regs[arm64DwarfBPRegNum] = frame.DWRule{
			Rule:   frame.RuleFramePointer,
			Reg:    arm64DwarfBPRegNum,
			Offset: 0,
		}
	}
	if fctxt.Regs[arm64DwarfLRRegNum].Rule == frame.RuleUndefined {
		fctxt.Regs[arm64DwarfLRRegNum] = frame.DWRule{
			Rule:   frame.RuleFramePointer,
			Reg:    arm64DwarfLRRegNum,
			Offset: 0,
		}
	}

	return fctxt
}

const arm64cgocallSPOffsetSaveSlot = 0x8
const prevG0schedSPOffsetSaveSlot = 0x10
const spAlign = 16

func arm64SwitchStack(it *stackIterator, callFrameRegs *op.DwarfRegisters) bool {
	if it.frame.Current.Fn != nil {
		switch it.frame.Current.Fn.Name {
		case "runtime.asmcgocall", "runtime.cgocallback_gofunc", "runtime.sigpanic", "runtime.cgocallback":
			//do nothing
		case "runtime.goexit", "runtime.rt0_go", "runtime.mcall":
			// Look for "top of stack" functions.
			it.atend = true
			return true
		case "crosscall2":
			//The offsets get from runtime/cgo/asm_arm64.s:10
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
		off, _ := readIntRaw(it.mem, uint64(callFrameRegs.SP()+arm64cgocallSPOffsetSaveSlot), int64(it.bi.Arch.PtrSize()))
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

		it.loadG0SchedSP()
		if it.g0_sched_sp <= 0 {
			return false
		}
		// entering the system stack
		callFrameRegs.Reg(callFrameRegs.SPRegNum).Uint64Val = it.g0_sched_sp
		// reads the previous value of g0.sched.sp that runtime.cgocallback_gofunc saved on the stack

		it.g0_sched_sp, _ = readUintRaw(it.mem, uint64(callFrameRegs.SP()+prevG0schedSPOffsetSaveSlot), int64(it.bi.Arch.PtrSize()))
		it.systemstack = true
		return false
	}

	return false
}

func arm64RegSize(regnum uint64) int {
	// fp registers
	if regnum >= 64 && regnum <= 95 {
		return 16
	}

	return 8 // general registers
}

// The mapping between hardware registers and DWARF registers is specified
// in the DWARF for the ARM® Architecture page 7,
// Table 1
// http://infocenter.arm.com/help/topic/com.arm.doc.ihi0040b/IHI0040B_aadwarf.pdf
var arm64DwarfToHardware = map[int]arm64asm.Reg{
	0:  arm64asm.X0,
	1:  arm64asm.X1,
	2:  arm64asm.X2,
	3:  arm64asm.X3,
	4:  arm64asm.X4,
	5:  arm64asm.X5,
	6:  arm64asm.X6,
	7:  arm64asm.X7,
	8:  arm64asm.X8,
	9:  arm64asm.X9,
	10: arm64asm.X10,
	11: arm64asm.X11,
	12: arm64asm.X12,
	13: arm64asm.X13,
	14: arm64asm.X14,
	15: arm64asm.X15,
	16: arm64asm.X16,
	17: arm64asm.X17,
	18: arm64asm.X18,
	19: arm64asm.X19,
	20: arm64asm.X20,
	21: arm64asm.X21,
	22: arm64asm.X22,
	23: arm64asm.X23,
	24: arm64asm.X24,
	25: arm64asm.X25,
	26: arm64asm.X26,
	27: arm64asm.X27,
	28: arm64asm.X28,
	29: arm64asm.X29,
	30: arm64asm.X30,
	31: arm64asm.SP,

	64: arm64asm.V0,
	65: arm64asm.V1,
	66: arm64asm.V2,
	67: arm64asm.V3,
	68: arm64asm.V4,
	69: arm64asm.V5,
	70: arm64asm.V6,
	71: arm64asm.V7,
	72: arm64asm.V8,
	73: arm64asm.V9,
	74: arm64asm.V10,
	75: arm64asm.V11,
	76: arm64asm.V12,
	77: arm64asm.V13,
	78: arm64asm.V14,
	79: arm64asm.V15,
	80: arm64asm.V16,
	81: arm64asm.V17,
	82: arm64asm.V18,
	83: arm64asm.V19,
	84: arm64asm.V20,
	85: arm64asm.V21,
	86: arm64asm.V22,
	87: arm64asm.V23,
	88: arm64asm.V24,
	89: arm64asm.V25,
	90: arm64asm.V26,
	91: arm64asm.V27,
	92: arm64asm.V28,
	93: arm64asm.V29,
	94: arm64asm.V30,
	95: arm64asm.V31,
}

var arm64NameToDwarf = func() map[string]int {
	r := make(map[string]int)
	for i := 0; i <= 30; i++ {
		r[fmt.Sprintf("x%d", i)] = i
	}
	r["pc"] = int(arm64DwarfIPRegNum)
	r["lr"] = int(arm64DwarfLRRegNum)
	r["sp"] = 31
	for i := 0; i <= 31; i++ {
		r[fmt.Sprintf("v%d", i)] = i + 64
	}
	return r
}()

func maxArm64DwarfRegister() int {
	max := int(arm64DwarfIPRegNum)
	for i := range arm64DwarfToHardware {
		if i > max {
			max = i
		}
	}
	return max
}

func arm64RegistersToDwarfRegisters(staticBase uint64, regs Registers) op.DwarfRegisters {
	dregs := make([]*op.DwarfRegister, maxArm64DwarfRegister()+1)

	dregs[arm64DwarfIPRegNum] = op.DwarfRegisterFromUint64(regs.PC())
	dregs[arm64DwarfSPRegNum] = op.DwarfRegisterFromUint64(regs.SP())
	dregs[arm64DwarfBPRegNum] = op.DwarfRegisterFromUint64(regs.BP())
	if lr, err := regs.Get(int(arm64asm.X30)); err != nil {
		dregs[arm64DwarfLRRegNum] = op.DwarfRegisterFromUint64(lr)
	}

	for dwarfReg, asmReg := range arm64DwarfToHardware {
		v, err := regs.Get(int(asmReg))
		if err == nil {
			dregs[dwarfReg] = op.DwarfRegisterFromUint64(v)
		}
	}

	dr := op.NewDwarfRegisters(staticBase, dregs, binary.LittleEndian, arm64DwarfIPRegNum, arm64DwarfSPRegNum, arm64DwarfBPRegNum, arm64DwarfLRRegNum)
	dr.SetLoadMoreCallback(loadMoreDwarfRegistersFromSliceFunc(dr, regs, arm64NameToDwarf))
	return *dr
}

func arm64AddrAndStackRegsToDwarfRegisters(staticBase, pc, sp, bp, lr uint64) op.DwarfRegisters {
	dregs := make([]*op.DwarfRegister, arm64DwarfIPRegNum+1)
	dregs[arm64DwarfIPRegNum] = op.DwarfRegisterFromUint64(pc)
	dregs[arm64DwarfSPRegNum] = op.DwarfRegisterFromUint64(sp)
	dregs[arm64DwarfBPRegNum] = op.DwarfRegisterFromUint64(bp)
	dregs[arm64DwarfLRRegNum] = op.DwarfRegisterFromUint64(lr)

	return *op.NewDwarfRegisters(staticBase, dregs, binary.LittleEndian, arm64DwarfIPRegNum, arm64DwarfSPRegNum, arm64DwarfBPRegNum, arm64DwarfLRRegNum)
}

func arm64DwarfRegisterToString(i int, reg *op.DwarfRegister) (name string, floatingPoint bool, repr string) {
	// see arm64DwarfToHardware table for explanation
	switch {
	case i <= 30:
		name = fmt.Sprintf("X%d", i)
	case i == 31:
		name = "SP"
	case i == 32:
		name = "PC"
	case i >= 64 && i <= 95:
		name = fmt.Sprintf("V%d", i-64)
	default:
		name = fmt.Sprintf("unknown%d", i)
	}

	if reg.Bytes != nil && name[0] == 'V' {
		buf := bytes.NewReader(reg.Bytes)

		var out bytes.Buffer
		var vi [16]uint8
		for i := range vi {
			binary.Read(buf, binary.LittleEndian, &vi[i])
		}

		fmt.Fprintf(&out, "0x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x", vi[15], vi[14], vi[13], vi[12], vi[11], vi[10], vi[9], vi[8], vi[7], vi[6], vi[5], vi[4], vi[3], vi[2], vi[1], vi[0])

		fmt.Fprintf(&out, "\tv2_int={ %02x%02x%02x%02x%02x%02x%02x%02x %02x%02x%02x%02x%02x%02x%02x%02x }", vi[7], vi[6], vi[5], vi[4], vi[3], vi[2], vi[1], vi[0], vi[15], vi[14], vi[13], vi[12], vi[11], vi[10], vi[9], vi[8])

		fmt.Fprintf(&out, "\tv4_int={ %02x%02x%02x%02x %02x%02x%02x%02x %02x%02x%02x%02x %02x%02x%02x%02x }", vi[3], vi[2], vi[1], vi[0], vi[7], vi[6], vi[5], vi[4], vi[11], vi[10], vi[9], vi[8], vi[15], vi[14], vi[13], vi[12])

		fmt.Fprintf(&out, "\tv8_int={ %02x%02x %02x%02x %02x%02x %02x%02x %02x%02x %02x%02x %02x%02x %02x%02x }", vi[1], vi[0], vi[3], vi[2], vi[5], vi[4], vi[7], vi[6], vi[9], vi[8], vi[11], vi[10], vi[13], vi[12], vi[15], vi[14])

		fmt.Fprintf(&out, "\tv16_int={ %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x }", vi[0], vi[1], vi[2], vi[3], vi[4], vi[5], vi[6], vi[7], vi[8], vi[9], vi[10], vi[11], vi[12], vi[13], vi[14], vi[15])

		buf.Seek(0, io.SeekStart)
		var v2 [2]float64
		for i := range v2 {
			binary.Read(buf, binary.LittleEndian, &v2[i])
		}
		fmt.Fprintf(&out, "\tv2_float={ %g %g }", v2[0], v2[1])

		buf.Seek(0, io.SeekStart)
		var v4 [4]float32
		for i := range v4 {
			binary.Read(buf, binary.LittleEndian, &v4[i])
		}
		fmt.Fprintf(&out, "\tv4_float={ %g %g %g %g }", v4[0], v4[1], v4[2], v4[3])

		return name, true, out.String()
	} else if reg.Bytes == nil || (reg.Bytes != nil && len(reg.Bytes) < 16) {
		return name, false, fmt.Sprintf("%#016x", reg.Uint64Val)
	}
	return name, false, fmt.Sprintf("%#x", reg.Bytes)
}
