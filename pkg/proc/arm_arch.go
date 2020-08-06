package proc

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"strings"

	"github.com/go-delve/delve/pkg/dwarf/frame"
	"github.com/go-delve/delve/pkg/dwarf/op"
)

const (
	armDwarfPCRegNum uint64 = 15
	armDwarfSPRegNum uint64 = 13
	armDwarfLRRegNum uint64 = 14
	armDwarfBPRegNum uint64 = 11
)

// Undefined instruction
var armBreakInstruction = []byte{0xf0, 0x01, 0xf0, 0xe7}

// ARMArch returns an initialized ARM
// struct.
func ARMArch(goos string) *Arch {
	return &Arch{
		Name:                             "arm",
		ptrSize:                          4,
		maxInstructionLength:             4,
		breakpointInstruction:            armBreakInstruction,
		breakInstrMovesPC:                true,
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
			RetAddrReg: armDwarfPCRegNum,
			Regs: map[uint64]frame.DWRule{
				armDwarfPCRegNum: frame.DWRule{
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

const armCgocallSPOffsetSaveSlot = 0x4
const armPrevG0schedSPOffsetSaveSlot = 0x8

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
			newsp, _ := readUintRaw(it.mem, uintptr(it.regs.SP()+8*9+4*14), int64(it.bi.Arch.PtrSize()))
			newbp, _ := readUintRaw(it.mem, uintptr(it.regs.SP()+4*11), int64(it.bi.Arch.PtrSize()))
			newlr, _ := readUintRaw(it.mem, uintptr(it.regs.SP()+4*13), int64(it.bi.Arch.PtrSize()))
			if it.regs.Reg(it.regs.BPRegNum) != nil {
				it.regs.Reg(it.regs.BPRegNum).Uint64Val = uint64(newbp)
			} else {
				reg, _ := it.readRegisterAt(it.regs.BPRegNum, it.regs.SP()+4*11)
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
		return 8
	}

	return 4 // general registers
}

// The mapping between hardware registers and DWARF registers is specified
// in the DWARF for the ARM® Architecture page 7,
// Table 1
// http://infocenter.arm.com/help/topic/com.arm.doc.ihi0040b/IHI0040B_aadwarf.pdf
var armDwarfToName = map[int]string{
	0:  "R0",
	1:  "R1",
	2:  "R2",
	3:  "R3",
	4:  "R4",
	5:  "R5",
	6:  "R6",
	7:  "R7",
	8:  "R8",
	9:  "R9",
	10: "R10",
	11: "BP", // R11
	12: "R12",
	13: "SP", // R13
	14: "LR", // R14
	15: "PC", // R15
	// No CPSR and ORIG_R0 in DWARF for the ARM architecture's Note.

	64: "S0",
	65: "S1",
	66: "S2",
	67: "S3",
	68: "S4",
	69: "S5",
	70: "S6",
	71: "S7",
	72: "S8",
	73: "S9",
	74: "S10",
	75: "S11",
	76: "S12",
	77: "S13",
	78: "S14",
	79: "S15",
	80: "S16",
	81: "S17",
	82: "S18",
	83: "S19",
	84: "S20",
	85: "S21",
	86: "S22",
	87: "S23",
	88: "S24",
	89: "S25",
	90: "S26",
	91: "S27",
	92: "S28",
	93: "S29",
	94: "S30",
	95: "S31",
}

var armNameToDwarf = func() map[string]int {
	r := make(map[string]int)
	for regNum, regName := range armDwarfToName {
		r[strings.ToLower(regName)] = regNum
	}
	// Alias the original name to that register, do we really need it?
	r["r11"] = int(armDwarfBPRegNum)
	r["r15"] = int(armDwarfPCRegNum)
	r["r14"] = int(armDwarfLRRegNum)
	r["r13"] = int(armDwarfSPRegNum)

	return r
}()

func maxArmDwarfRegister() int {
	max := int(armDwarfPCRegNum)
	for i := range armDwarfToName {
		if i > max {
			max = i
		}
	}
	return max
}

func armRegistersToDwarfRegisters(staticBase uint64, regs Registers) op.DwarfRegisters {
	dregs := initDwarfRegistersFromSlice(maxArmDwarfRegister(), regs, armNameToDwarf)
	dr := op.NewDwarfRegisters(staticBase, dregs, binary.LittleEndian, armDwarfPCRegNum, armDwarfSPRegNum, armDwarfBPRegNum, armDwarfLRRegNum)
	dr.SetLoadMoreCallback(loadMoreDwarfRegistersFromSliceFunc(dr, regs, armNameToDwarf))
	return *dr
}

func armAddrAndStackRegsToDwarfRegisters(staticBase, pc, sp, bp, lr uint64) op.DwarfRegisters {
	dregs := make([]*op.DwarfRegister, armDwarfPCRegNum+1)
	dregs[armDwarfPCRegNum] = op.DwarfRegisterFromUint64(pc)
	dregs[armDwarfSPRegNum] = op.DwarfRegisterFromUint64(sp)
	dregs[armDwarfBPRegNum] = op.DwarfRegisterFromUint64(bp)
	dregs[armDwarfLRRegNum] = op.DwarfRegisterFromUint64(lr)

	return *op.NewDwarfRegisters(staticBase, dregs, binary.LittleEndian, armDwarfPCRegNum, armDwarfSPRegNum, armDwarfBPRegNum, armDwarfLRRegNum)
}

func formatVPFReg(vfp []byte) string {
	buf := bytes.NewReader(vfp)

	var out bytes.Buffer
	var vi [8]uint8
	for i := range vi {
		binary.Read(buf, binary.LittleEndian, &vi[i])
	}

	fmt.Fprintf(&out, "0x%02x%02x%02x%02x%02x%02x%02x%02x", vi[7], vi[6], vi[5], vi[4], vi[3], vi[2], vi[1], vi[0])

	fmt.Fprintf(&out, "\tv1_int={ %02x%02x%02x%02x%02x%02x%02x%02x }", vi[7], vi[6], vi[5], vi[4], vi[3], vi[2], vi[1], vi[0])

	fmt.Fprintf(&out, "\tv2_int={ %02x%02x%02x%02x %02x%02x%02x%02x }", vi[3], vi[2], vi[1], vi[0], vi[7], vi[6], vi[5], vi[4])

	fmt.Fprintf(&out, "\tv4_int={ %02x%02x %02x%02x %02x%02x %02x%02x }", vi[1], vi[0], vi[3], vi[2], vi[5], vi[4], vi[7], vi[6])

	fmt.Fprintf(&out, "\tv8_int={ %02x %02x %02x %02x %02x %02x %02x %02x }", vi[0], vi[1], vi[2], vi[3], vi[4], vi[5], vi[6], vi[7])

	buf.Seek(0, io.SeekStart)
	var v1 float64
	binary.Read(buf, binary.LittleEndian, &v1)
	fmt.Fprintf(&out, "\tv1_float={ %g }", v1)

	buf.Seek(0, io.SeekStart)
	var v2 [2]float32
	for i := range v2 {
		binary.Read(buf, binary.LittleEndian, &v2[i])
	}
	fmt.Fprintf(&out, "\tv2_float={ %g %g }", v2[0], v2[1])

	return out.String()
}

func armDwarfRegisterToString(i int, reg *op.DwarfRegister) (name string, floatingPoint bool, repr string) {
	// see armDwarfToName table for explanation
	name, ok := armDwarfToName[i]
	if !ok {
		name = fmt.Sprintf("unknown%d", i)
	}

	if reg.Bytes != nil && name[0] == 'S' {
		return name, true, formatVPFReg(reg.Bytes)
	} else if reg.Bytes == nil {
		// Those register is 32 bit.
		return name, false, fmt.Sprintf("%#04x", reg.Uint64Val)
	} else {
		return name, false, fmt.Sprintf("%#x", reg.Bytes)
	}
}
