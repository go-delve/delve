package debug

import (
	"github.com/go-delve/delve/pkg/dwarf/frame"
	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/proc"
)

// Arch defines an interface for representing a
// CPU architecture.
type Arch interface {
	PtrSize() int
	MaxInstructionLength() int
	AsmDecode(asmInst *AsmInstruction, mem []byte, regs proc.Registers, memrw proc.MemoryReadWriter, bi *BinaryInfo) error
	Prologues() []opcodeSeq
	BreakpointInstruction() []byte
	BreakInstrMovesPC() bool
	BreakpointSize() int
	DerefTLS() bool
	FixFrameUnwindContext(*frame.FrameContext, uint64, *BinaryInfo) *frame.FrameContext
	RegSize(uint64) int
	RegistersToDwarfRegisters(uint64, proc.Registers) op.DwarfRegisters
	AddrAndStackRegsToDwarfRegisters(uint64, uint64, uint64, uint64) op.DwarfRegisters
}

const (
	crosscall2SPOffsetBad        = 0x8
	crosscall2SPOffsetWindows    = 0x118
	crosscall2SPOffsetNonWindows = 0x58
)
