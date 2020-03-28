package proc

import (
	"github.com/go-delve/delve/pkg/dwarf/frame"
	"github.com/go-delve/delve/pkg/dwarf/op"
)

// Arch defines an interface for representing a
// CPU architecture.
type Arch interface {
	// PtrSize returns the size of a pointer for the architecture.
	PtrSize() int
	// MaxInstructionLength is the maximum size in bytes of an instruction.
	MaxInstructionLength() int
	// AsmDecode decodes the assembly instruction starting at mem[0:] into asmInst.
	// It assumes that the Loc and AtPC fields of asmInst have already been filled.
	AsmDecode(asmInst *AsmInstruction, mem []byte, regs Registers, memrw MemoryReadWriter, bi *BinaryInfo) error
	// Prologues returns a list of stack split prologues
	// that are inserted at function entry.
	Prologues() []opcodeSeq
	// BreakpointInstruction is the instruction that will trigger a breakpoint trap for
	// the given architecture.
	BreakpointInstruction() []byte
	// BreakInstrMovesPC is true if hitting the breakpoint instruction advances the
	// instruction counter by the size of the breakpoint instruction.
	BreakInstrMovesPC() bool
	// BreakpointSize is the size of the breakpoint instruction for the given architecture.
	BreakpointSize() int
	// DerefTLS is true if the G struct stored in the TLS section is a pointer
	// and the address must be dereferenced to find to actual G struct.
	DerefTLS() bool
	// FixFrameUnwindContext applies architecture specific rules for unwinding a stack frame
	// on the given arch.
	FixFrameUnwindContext(*frame.FrameContext, uint64, *BinaryInfo) *frame.FrameContext
	// SwitchStack will use the current frame to determine if it's time to
	// switch between the system stack and the goroutine stack or vice versa.
	SwitchStack(it *stackIterator, callFrameRegs *op.DwarfRegisters) bool
	// RegSize returns the size (in bytes) of register regnum.
	RegSize(uint64) int
	// RegistersToDwarfRegisters maps hardware registers to DWARF registers.
	RegistersToDwarfRegisters(uint64, Registers) op.DwarfRegisters
	// AddrAndStackRegsToDwarfRegisters returns DWARF registers from the passed in
	// PC, SP, and BP registers in the format used by the DWARF expression interpreter.
	AddrAndStackRegsToDwarfRegisters(uint64, uint64, uint64, uint64, uint64) op.DwarfRegisters
	// DwarfRegisterToString returns the name and value representation of the given register.
	DwarfRegisterToString(int, *op.DwarfRegister) (string, bool, string)
	// InhibitStepInto returns whether StepBreakpoint can be set at pc.
	InhibitStepInto(bi *BinaryInfo, pc uint64) bool
}

// crosscall2 is defined in $GOROOT/src/runtime/cgo/asm_amd64.s.
const (
	crosscall2SPOffsetBad        = 0x8
	crosscall2SPOffsetWindows    = 0x118
	crosscall2SPOffsetNonWindows = 0x58
)
