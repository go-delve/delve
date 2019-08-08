package proc

import (
	"encoding/binary"

	"github.com/go-delve/delve/pkg/dwarf/frame"
	"github.com/go-delve/delve/pkg/dwarf/op"
	"golang.org/x/arch/x86/x86asm"
)

// Arch defines an interface for representing a
// CPU architecture.
type Arch interface {
	PtrSize() int
	BreakpointInstruction() []byte
	BreakpointSize() int
	DerefTLS() bool
	FixFrameUnwindContext(*frame.FrameContext, uint64, *BinaryInfo) *frame.FrameContext
	RegSize(uint64) int
	RegistersToDwarfRegisters(uint64, Registers) op.DwarfRegisters
	GoroutineToDwarfRegisters(*G) op.DwarfRegisters
}

// AMD64 represents the AMD64 CPU architecture.
type AMD64 struct {
	gStructOffset uint64
	goos          string

	// crosscall2fn is the DIE of crosscall2, a function used by the go runtime
	// to call C functions. This function in go 1.9 (and previous versions) had
	// a bad frame descriptor which needs to be fixed to generate good stack
	// traces.
	crosscall2fn *Function

	// sigreturnfn is the DIE of runtime.sigreturn, the return trampoline for
	// the signal handler. See comment in FixFrameUnwindContext for a
	// description of why this is needed.
	sigreturnfn *Function
}

const (
	amd64DwarfIPRegNum uint64 = 16
	amd64DwarfSPRegNum uint64 = 7
	amd64DwarfBPRegNum uint64 = 6
)

var amd64BreakInstruction = []byte{0xCC}

// AMD64Arch returns an initialized AMD64
// struct.
func AMD64Arch(goos string) *AMD64 {
	return &AMD64{
		goos: goos,
	}
}

// PtrSize returns the size of a pointer
// on this architecture.
func (a *AMD64) PtrSize() int {
	return 8
}

// BreakpointInstruction returns the Breakpoint
// instruction for this architecture.
func (a *AMD64) BreakpointInstruction() []byte {
	return amd64BreakInstruction
}

// BreakpointSize returns the size of the
// breakpoint instruction on this architecture.
func (a *AMD64) BreakpointSize() int {
	return len(amd64BreakInstruction)
}

// DerefTLS returns true if the value of regs.TLS()+GStructOffset() is a
// pointer to the G struct
func (a *AMD64) DerefTLS() bool {
	return a.goos == "windows"
}

const (
	crosscall2SPOffsetBad        = 0x8
	crosscall2SPOffsetWindows    = 0x118
	crosscall2SPOffsetNonWindows = 0x58
)

// FixFrameUnwindContext adds default architecture rules to fctxt or returns
// the default frame unwind context if fctxt is nil.
func (a *AMD64) FixFrameUnwindContext(fctxt *frame.FrameContext, pc uint64, bi *BinaryInfo) *frame.FrameContext {
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
			RetAddrReg: amd64DwarfIPRegNum,
			Regs: map[uint64]frame.DWRule{
				amd64DwarfIPRegNum: frame.DWRule{
					Rule:   frame.RuleOffset,
					Offset: int64(-a.PtrSize()),
				},
				amd64DwarfBPRegNum: frame.DWRule{
					Rule:   frame.RuleOffset,
					Offset: int64(-2 * a.PtrSize()),
				},
				amd64DwarfSPRegNum: frame.DWRule{
					Rule:   frame.RuleValOffset,
					Offset: 0,
				},
			},
			CFA: frame.DWRule{
				Rule:   frame.RuleCFA,
				Reg:    amd64DwarfBPRegNum,
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
			switch a.goos {
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
	if fctxt.Regs[amd64DwarfBPRegNum].Rule == frame.RuleUndefined {
		fctxt.Regs[amd64DwarfBPRegNum] = frame.DWRule{
			Rule:   frame.RuleFramePointer,
			Reg:    amd64DwarfBPRegNum,
			Offset: 0,
		}
	}

	return fctxt
}

// RegSize returns the size (in bytes) of register regnum.
// The mapping between hardware registers and DWARF registers is specified
// in the System V ABI AMD64 Architecture Processor Supplement page 57,
// figure 3.36
// https://www.uclibc.org/docs/psABI-x86_64.pdf
func (a *AMD64) RegSize(regnum uint64) int {
	// XMM registers
	if regnum > amd64DwarfIPRegNum && regnum <= 32 {
		return 16
	}
	// x87 registers
	if regnum >= 33 && regnum <= 40 {
		return 10
	}
	return 8
}

// The mapping between hardware registers and DWARF registers is specified
// in the System V ABI AMD64 Architecture Processor Supplement page 57,
// figure 3.36
// https://www.uclibc.org/docs/psABI-x86_64.pdf

var asm64DwarfToHardware = map[int]x86asm.Reg{
	0:  x86asm.RAX,
	1:  x86asm.RDX,
	2:  x86asm.RCX,
	3:  x86asm.RBX,
	4:  x86asm.RSI,
	5:  x86asm.RDI,
	8:  x86asm.R8,
	9:  x86asm.R9,
	10: x86asm.R10,
	11: x86asm.R11,
	12: x86asm.R12,
	13: x86asm.R13,
	14: x86asm.R14,
	15: x86asm.R15,
}

var amd64DwarfToName = map[int]string{
	17: "XMM0",
	18: "XMM1",
	19: "XMM2",
	20: "XMM3",
	21: "XMM4",
	22: "XMM5",
	23: "XMM6",
	24: "XMM7",
	25: "XMM8",
	26: "XMM9",
	27: "XMM10",
	28: "XMM11",
	29: "XMM12",
	30: "XMM13",
	31: "XMM14",
	32: "XMM15",
	33: "ST(0)",
	34: "ST(1)",
	35: "ST(2)",
	36: "ST(3)",
	37: "ST(4)",
	38: "ST(5)",
	39: "ST(6)",
	40: "ST(7)",
	49: "Eflags",
	50: "Es",
	51: "Cs",
	52: "Ss",
	53: "Ds",
	54: "Fs",
	55: "Gs",
	58: "Fs_base",
	59: "Gs_base",
	64: "MXCSR",
	65: "CW",
	66: "SW",
}

func maxAmd64DwarfRegister() int {
	max := int(amd64DwarfIPRegNum)
	for i := range asm64DwarfToHardware {
		if i > max {
			max = i
		}
	}
	for i := range amd64DwarfToName {
		if i > max {
			max = i
		}
	}
	return max
}

// RegistersToDwarfRegisters converts hardware registers to the format used
// by the DWARF expression interpreter.
func (a *AMD64) RegistersToDwarfRegisters(staticBase uint64, regs Registers) op.DwarfRegisters {
	dregs := make([]*op.DwarfRegister, maxAmd64DwarfRegister()+1)

	dregs[amd64DwarfIPRegNum] = op.DwarfRegisterFromUint64(regs.PC())
	dregs[amd64DwarfSPRegNum] = op.DwarfRegisterFromUint64(regs.SP())
	dregs[amd64DwarfBPRegNum] = op.DwarfRegisterFromUint64(regs.BP())

	for dwarfReg, asmReg := range asm64DwarfToHardware {
		v, err := regs.Get(int(asmReg))
		if err == nil {
			dregs[dwarfReg] = op.DwarfRegisterFromUint64(v)
		}
	}

	for _, reg := range regs.Slice(true) {
		for dwarfReg, regName := range amd64DwarfToName {
			if regName == reg.Name {
				dregs[dwarfReg] = op.DwarfRegisterFromBytes(reg.Bytes)
			}
		}
	}

	return op.DwarfRegisters{
		StaticBase: staticBase,
		Regs:       dregs,
		ByteOrder:  binary.LittleEndian,
		PCRegNum:   amd64DwarfIPRegNum,
		SPRegNum:   amd64DwarfSPRegNum,
		BPRegNum:   amd64DwarfBPRegNum,
	}
}

// GoroutineToDwarfRegisters extract the saved DWARF registers from a parked
// goroutine in the format used by the DWARF expression interpreter.
func (a *AMD64) GoroutineToDwarfRegisters(g *G) op.DwarfRegisters {
	dregs := make([]*op.DwarfRegister, amd64DwarfIPRegNum+1)
	dregs[amd64DwarfIPRegNum] = op.DwarfRegisterFromUint64(g.PC)
	dregs[amd64DwarfSPRegNum] = op.DwarfRegisterFromUint64(g.SP)
	dregs[amd64DwarfBPRegNum] = op.DwarfRegisterFromUint64(g.BP)

	so := g.variable.bi.PCToImage(g.PC)

	return op.DwarfRegisters{StaticBase: so.StaticBase, Regs: dregs, ByteOrder: binary.LittleEndian, PCRegNum: amd64DwarfIPRegNum, SPRegNum: amd64DwarfSPRegNum, BPRegNum: amd64DwarfBPRegNum}
}
