package proc

// AsmInstruction represents one assembly instruction.
type AsmInstruction struct {
	Loc        Location
	DestLoc    *Location
	Bytes      []byte
	Breakpoint bool
	AtPC       bool
	Inst       *archInst
}

// AssemblyFlavour is the assembly syntax to display.
type AssemblyFlavour int

const (
	// GNUFlavour will display GNU assembly syntax.
	GNUFlavour = AssemblyFlavour(iota)
	// IntelFlavour will display Intel assembly syntax.
	IntelFlavour
	// GoFlavour will display Go assembly syntax.
	GoFlavour
)

// Disassemble disassembles target memory between startAddr and endAddr, marking
// the current instruction being executed in goroutine g.
// If currentGoroutine is set and thread is stopped at a CALL instruction Disassemble
// will evaluate the argument of the CALL instruction using the thread's registers.
// Be aware that the Bytes field of each returned instruction is a slice of a larger array of size startAddr - endAddr.
func Disassemble(mem MemoryReadWriter, regs Registers, breakpoints *BreakpointMap, bi *BinaryInfo, startAddr, endAddr uint64) ([]AsmInstruction, error) {
	return disassemble(mem, regs, breakpoints, bi, startAddr, endAddr, false)
}
