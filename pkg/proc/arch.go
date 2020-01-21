package proc

import (
	"github.com/go-delve/delve/pkg/dwarf/frame"
	"github.com/go-delve/delve/pkg/dwarf/op"
)

// Arch defines an interface for representing a
// CPU architecture.
type Arch interface {
	PtrSize() int
	MaxInstructionLength() int
	AsmDecode(asmInst *AsmInstruction, mem []byte, regs Registers, memrw MemoryReadWriter, bi *BinaryInfo) error
	Prologues() []opcodeSeq
	BreakpointInstruction() []byte
	BreakInstrMovesPC() bool
	BreakpointSize() int
	DerefTLS() bool
	FixFrameUnwindContext(*frame.FrameContext, uint64, *BinaryInfo) *frame.FrameContext
	SwitchStack(it *stackIterator, callFrameRegs *op.DwarfRegisters) bool
	RegSize(uint64) int
	RegistersToDwarfRegisters(uint64, Registers) op.DwarfRegisters
	AddrAndStackRegsToDwarfRegisters(uint64, uint64, uint64, uint64, uint64) op.DwarfRegisters
}

const (
	crosscall2SPOffsetBad        = 0x8
	crosscall2SPOffsetWindows    = 0x118
	crosscall2SPOffsetNonWindows = 0x58
)
