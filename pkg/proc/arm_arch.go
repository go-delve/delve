package proc

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"strings"

	"github.com/go-delve/delve/pkg/dwarf/frame"
	"github.com/go-delve/delve/pkg/dwarf/op"
	"golang.org/x/arch/arm/armasm"
)

const (
	armDwarfIPRegNum uint64 = 12
	armDwarfSPRegNum uint64 = 13
	armDwarfLRRegNum uint64 = 14
	armDwarfBPRegNum uint64 = 11
)

// bkpt #0
var armBreakInstruction = []byte{0x70, 0x0, 0x20, 0xe1}

// ARMArch returns an initialized ARM
// struct.
func ARMArch(goos string) *Arch {
	return &Arch{
		Name:                             "arm",
		ptrSize:                          4,
		maxInstructionLength:             4,
		breakpointInstruction:            armBreakInstruction,
		breakInstrMovesPC:                false,
		derefTLS:                         false,
		prologues:                        prologuesARM,
		fixFrameUnwindContext:            armFixFrameUnwindContext,
		switchStack:                      armSwitchStack,
		regSize:                          armRegSize,
		RegistersToDwarfRegisters:        armRegistersToDwarfRegisters,
		addrAndStackRegsToDwarfRegisters: armAddrAndStackRegsToDwarfRegisters,
		DwarfRegisterToString:            armDwarfRegisterToString,
		inhibitStepInto:                  func(*BinaryInfo, uint64) bool { return false },
		asmDecode:                        armAsmDecode,
	}
}

func armFixFrameUnwindContext(fctxt *frame.FrameContext, pc uint64, bi *BinaryInfo) *frame.FrameContext {
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
			RetAddrReg: armDwarfIPRegNum,
			Regs: map[uint64]frame.DWRule{
				armDwarfIPRegNum: frame.DWRule{
					Rule:   frame.RuleOffset,
					Offset: int64(-a.PtrSize()),
				},
				armDwarfBPRegNum: frame.DWRule{
					Rule:   frame.RuleOffset,
					Offset: int64(-2 * a.PtrSize()),
				},
				armDwarfSPRegNum: frame.DWRule{
					Rule:   frame.RuleValOffset,
					Offset: 0,
				},
			},
			CFA: frame.DWRule{
				Rule:   frame.RuleCFA,
				Reg:    armDwarfBPRegNum,
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
	if fctxt.Regs[armDwarfBPRegNum].Rule == frame.RuleUndefined {
		fctxt.Regs[armDwarfBPRegNum] = frame.DWRule{
			Rule:   frame.RuleFramePointer,
			Reg:    armDwarfBPRegNum,
			Offset: 0,
		}
	}
	if fctxt.Regs[armDwarfLRRegNum].Rule == frame.RuleUndefined {
		fctxt.Regs[armDwarfLRRegNum] = frame.DWRule{
			Rule:   frame.RuleFramePointer,
			Reg:    armDwarfLRRegNum,
			Offset: 0,
		}
	}

	return fctxt
}

const armCgocallSPOffsetSaveSlot = 0x8
const armPrevG0schedSPOffsetSaveSlot = 0x10
const armSpAlign = 16

func armSwitchStack(it *stackIterator, callFrameRegs *op.DwarfRegisters) bool {
	if it.frame.Current.Fn != nil {
		switch it.frame.Current.Fn.Name {
		case "runtime.asmcgocall", "runtime.cgocallback_gofunc", "runtime.sigpanic":
			//do nothing
		case "runtime.goexit", "runtime.rt0_go", "runtime.mcall":
			// Look for "top of stack" functions.
			it.atend = true
			return true
		case "crosscall2":
			//The offsets get from runtime/cgo/asm_arm.s:10
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
		off, _ := readIntRaw(it.mem, uintptr(callFrameRegs.SP()+armCgocallSPOffsetSaveSlot), int64(it.bi.Arch.PtrSize()))
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
		// runtime.cgocallback_gofunc in $GOROOT/src/runtime/asm_arm.s
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

		it.g0_sched_sp, _ = readUintRaw(it.mem, uintptr(callFrameRegs.SP()+armPrevG0schedSPOffsetSaveSlot), int64(it.bi.Arch.PtrSize()))
		it.systemstack = true
		return false
	}

	return false
}

func armRegSize(regnum uint64) int {
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
var armDwarfToHardware = map[int]armasm.Reg{
	0:  armasm.R0,
	1:  armasm.R1,
	2:  armasm.R2,
	3:  armasm.R3,
	4:  armasm.R4,
	5:  armasm.R5,
	6:  armasm.R6,
	7:  armasm.R7,
	8:  armasm.R8,
	9:  armasm.R9,
	10: armasm.R10,
	11: armasm.R11,
	12: armasm.R12,
	13: armasm.R13,
	14: armasm.R14,
	15: armasm.R15,

	64: armasm.S0,
	65: armasm.S1,
	66: armasm.S2,
	67: armasm.S3,
	68: armasm.S4,
	69: armasm.S5,
	70: armasm.S6,
	71: armasm.S7,
	72: armasm.S8,
	73: armasm.S9,
	74: armasm.S10,
	75: armasm.S11,
	76: armasm.S12,
	77: armasm.S13,
	78: armasm.S14,
	79: armasm.S15,
	80: armasm.S16,
	81: armasm.S17,
	82: armasm.S18,
	83: armasm.S19,
	84: armasm.S20,
	85: armasm.S21,
	86: armasm.S22,
	87: armasm.S23,
	88: armasm.S24,
	89: armasm.S25,
	90: armasm.S26,
	91: armasm.S27,
	92: armasm.S28,
	93: armasm.S29,
	94: armasm.S30,
	95: armasm.S31,
}

var armNameToDwarf = func() map[string]int {
	r := make(map[string]int)
	for i := 0; i <= 30; i++ {
		r[fmt.Sprintf("s%d", i)] = i
	}
	r["pc"] = int(armDwarfIPRegNum)
	r["lr"] = int(armDwarfLRRegNum)
	r["sp"] = int(armDwarfSPRegNum)
	for i := 0; i <= 31; i++ {
		r[fmt.Sprintf("v%d", i)] = i + 64
	}
	return r
}()

func maxarmDwarfRegister() int {
	max := int(armDwarfIPRegNum)
	for i := range armDwarfToHardware {
		if i > max {
			max = i
		}
	}
	return max
}

func armRegistersToDwarfRegisters(staticBase uint64, regs Registers) op.DwarfRegisters {
	dregs := make([]*op.DwarfRegister, maxarmDwarfRegister()+1)

	dregs[armDwarfIPRegNum] = op.DwarfRegisterFromUint64(regs.PC())
	dregs[armDwarfSPRegNum] = op.DwarfRegisterFromUint64(regs.SP())
	dregs[armDwarfBPRegNum] = op.DwarfRegisterFromUint64(regs.BP())
	if lr, err := regs.Get(int(armasm.LR)); err != nil {
		dregs[armDwarfLRRegNum] = op.DwarfRegisterFromUint64(lr)
	}

	for dwarfReg, asmReg := range armDwarfToHardware {
		v, err := regs.Get(int(asmReg))
		if err == nil {
			dregs[dwarfReg] = op.DwarfRegisterFromUint64(v)
		}
	}

	dr := op.NewDwarfRegisters(staticBase, dregs, binary.LittleEndian, armDwarfIPRegNum, armDwarfSPRegNum, armDwarfBPRegNum, armDwarfLRRegNum)
	dr.SetLoadMoreCallback(loadMoreDwarfRegistersFromSliceFunc(dr, regs, armNameToDwarf))
	return *dr
}

func armAddrAndStackRegsToDwarfRegisters(staticBase, pc, sp, bp, lr uint64) op.DwarfRegisters {
	dregs := make([]*op.DwarfRegister, armDwarfIPRegNum+1)
	dregs[armDwarfIPRegNum] = op.DwarfRegisterFromUint64(pc)
	dregs[armDwarfSPRegNum] = op.DwarfRegisterFromUint64(sp)
	dregs[armDwarfBPRegNum] = op.DwarfRegisterFromUint64(bp)
	dregs[armDwarfLRRegNum] = op.DwarfRegisterFromUint64(lr)

	return *op.NewDwarfRegisters(staticBase, dregs, binary.LittleEndian, armDwarfIPRegNum, armDwarfSPRegNum, armDwarfBPRegNum, armDwarfLRRegNum)
}

func armDwarfRegisterToString(i int, reg *op.DwarfRegister) (name string, floatingPoint bool, repr string) {
	// see armDwarfToHardware table for explanation
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
