package proc

import (
	"fmt"
	"strings"

	"github.com/go-delve/delve/pkg/dwarf/frame"
	"github.com/go-delve/delve/pkg/dwarf/op"
)

// Arch represents a CPU architecture.
type Arch struct {
	Name string // architecture name

	ptrSize                  int
	maxInstructionLength     int
	prologues                []opcodeSeq
	breakpointInstruction    []byte
	altBreakpointInstruction []byte
	breakInstrMovesPC        bool
	derefTLS                 bool
	usesLR                   bool // architecture uses a link register, also called RA on some architectures
	PCRegNum                 uint64
	SPRegNum                 uint64
	BPRegNum                 uint64
	ContextRegNum            uint64 // register used to pass a closure context when calling a function pointer
	LRRegNum                 uint64

	// asmDecode decodes the assembly instruction starting at mem[0:] into asmInst.
	// It assumes that the Loc and AtPC fields of asmInst have already been filled.
	asmDecode func(asmInst *AsmInstruction, mem []byte, regs *op.DwarfRegisters, memrw MemoryReadWriter, bi *BinaryInfo) error
	// fixFrameUnwindContext applies architecture specific rules for unwinding a stack frame
	// on the given arch.
	fixFrameUnwindContext func(*frame.FrameContext, uint64, *BinaryInfo) *frame.FrameContext
	// switchStack will use the current frame to determine if it's time to
	// switch between the system stack and the goroutine stack or vice versa.
	switchStack func(it *stackIterator, callFrameRegs *op.DwarfRegisters) bool
	// regSize returns the size (in bytes) of register regnum.
	regSize func(uint64) int
	// RegistersToDwarfRegisters maps hardware registers to DWARF registers.
	RegistersToDwarfRegisters func(uint64, Registers) *op.DwarfRegisters
	// addrAndStackRegsToDwarfRegisters returns DWARF registers from the passed in
	// PC, SP, and BP registers in the format used by the DWARF expression interpreter.
	addrAndStackRegsToDwarfRegisters func(uint64, uint64, uint64, uint64, uint64) op.DwarfRegisters
	// DwarfRegisterToString returns the name and value representation of the
	// given register, the register value can be nil in which case only the
	// register name will be returned.
	DwarfRegisterToString func(int, *op.DwarfRegister) (string, bool, string)
	// inhibitStepInto returns whether StepBreakpoint can be set at pc.
	inhibitStepInto     func(bi *BinaryInfo, pc uint64) bool
	RegisterNameToDwarf func(s string) (int, bool)
	RegnumToString      func(uint64) string
	// debugCallMinStackSize is the minimum stack size for call injection on this architecture.
	debugCallMinStackSize uint64
	// maxRegArgBytes is extra padding for ABI1 call injections, equivalent to
	// the maximum space occupied by register arguments.
	maxRegArgBytes int

	// asmRegisters maps assembly register numbers to dwarf registers.
	asmRegisters map[int]asmRegister

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

type asmRegister struct {
	dwarfNum uint64
	offset   uint
	mask     uint64
}

const (
	mask8  = 0x000000ff
	mask16 = 0x0000ffff
	mask32 = 0xffffffff
)

// PtrSize returns the size of a pointer for the architecture.
func (a *Arch) PtrSize() int {
	return a.ptrSize
}

// MaxInstructionLength is the maximum size in bytes of an instruction.
func (a *Arch) MaxInstructionLength() int {
	return a.maxInstructionLength
}

// BreakpointInstruction is the instruction that will trigger a breakpoint trap for
// the given architecture.
func (a *Arch) BreakpointInstruction() []byte {
	return a.breakpointInstruction
}

// AltBreakpointInstruction returns an alternate encoding for the breakpoint instruction.
func (a *Arch) AltBreakpointInstruction() []byte {
	return a.altBreakpointInstruction
}

// BreakInstrMovesPC is true if hitting the breakpoint instruction advances the
// instruction counter by the size of the breakpoint instruction.
func (a *Arch) BreakInstrMovesPC() bool {
	return a.breakInstrMovesPC
}

// BreakpointSize is the size of the breakpoint instruction for the given architecture.
func (a *Arch) BreakpointSize() int {
	return len(a.breakpointInstruction)
}

// DerefTLS is true if the G struct stored in the TLS section is a pointer
// and the address must be dereferenced to find to actual G struct.
func (a *Arch) DerefTLS() bool {
	return a.derefTLS
}

// getAsmRegister returns the value of the asm register asmreg using the asmRegisters table of arch.
// The interpretation of asmreg is architecture specific and defined by the disassembler.
// A mask value of 0 inside asmRegisters is equivalent to ^uint64(0).
func (arch *Arch) getAsmRegister(regs *op.DwarfRegisters, asmreg int) (uint64, error) {
	hwreg, ok := arch.asmRegisters[asmreg]
	if !ok {
		return 0, ErrUnknownRegister
	}
	reg := regs.Reg(hwreg.dwarfNum)
	if reg == nil {
		return 0, fmt.Errorf("register %#x not found", asmreg)
	}
	n := (reg.Uint64Val >> hwreg.offset)
	if hwreg.mask != 0 {
		n = n & hwreg.mask
	}
	return n, nil
}

func nameToDwarfFunc(n2d map[string]int) func(string) (int, bool) {
	return func(name string) (int, bool) {
		r, ok := n2d[strings.ToLower(name)]
		return r, ok
	}
}

// crosscall2 is defined in $GOROOT/src/runtime/cgo/asm_amd64.s.
const (
	crosscall2SPOffsetBad          = 0x8
	crosscall2SPOffsetWindowsAMD64 = 0x118
	crosscall2SPOffset             = 0x58
)
