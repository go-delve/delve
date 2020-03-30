package proc

import (
	"github.com/go-delve/delve/pkg/dwarf/frame"
	"github.com/go-delve/delve/pkg/dwarf/op"
)

// Arch represents a CPU architecture.
type Arch struct {
	Name string // architecture name

	ptrSize               int
	maxInstructionLength  int
	prologues             []opcodeSeq
	breakpointInstruction []byte
	breakInstrMovesPC     bool
	derefTLS              bool

	// asmDecode decodes the assembly instruction starting at mem[0:] into asmInst.
	// It assumes that the Loc and AtPC fields of asmInst have already been filled.
	asmDecode func(asmInst *AsmInstruction, mem []byte, regs Registers, memrw MemoryReadWriter, bi *BinaryInfo) error
	// fixFrameUnwindContext applies architecture specific rules for unwinding a stack frame
	// on the given arch.
	fixFrameUnwindContext func(*frame.FrameContext, uint64, *BinaryInfo) *frame.FrameContext
	// switchStack will use the current frame to determine if it's time to
	// switch between the system stack and the goroutine stack or vice versa.
	switchStack func(it *stackIterator, callFrameRegs *op.DwarfRegisters) bool
	// regSize returns the size (in bytes) of register regnum.
	regSize func(uint64) int
	// RegistersToDwarfRegisters maps hardware registers to DWARF registers.
	RegistersToDwarfRegisters func(uint64, Registers) op.DwarfRegisters
	// addrAndStackRegsToDwarfRegisters returns DWARF registers from the passed in
	// PC, SP, and BP registers in the format used by the DWARF expression interpreter.
	addrAndStackRegsToDwarfRegisters func(uint64, uint64, uint64, uint64, uint64) op.DwarfRegisters
	// DwarfRegisterToString returns the name and value representation of the given register.
	DwarfRegisterToString func(int, *op.DwarfRegister) (string, bool, string)
	// inhibitStepInto returns whether StepBreakpoint can be set at pc.
	inhibitStepInto func(bi *BinaryInfo, pc uint64) bool

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

// PtrSize returns the size of a pointer for the architecture.
func (a *Arch) PtrSize() int {
	return a.ptrSize
}

// MaxInstructionLength is the maximum size in bytes of an instruction.
func (a *Arch) MaxInstructionLength() int {
	return a.maxInstructionLength
}

// Prologues returns a list of stack split prologues
// that are inserted at function entry.
func (a *Arch) Prologues() []opcodeSeq {
	return a.prologues
}

// BreakpointInstruction is the instruction that will trigger a breakpoint trap for
// the given architecture.
func (a *Arch) BreakpointInstruction() []byte {
	return a.breakpointInstruction
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

// crosscall2 is defined in $GOROOT/src/runtime/cgo/asm_amd64.s.
const (
	crosscall2SPOffsetBad        = 0x8
	crosscall2SPOffsetWindows    = 0x118
	crosscall2SPOffsetNonWindows = 0x58
)
