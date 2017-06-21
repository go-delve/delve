package proc

// Arch defines an interface for representing a
// CPU architecture.
type Arch interface {
	PtrSize() int
	BreakpointInstruction() []byte
	BreakpointSize() int
	DerefTLS() bool
}

// AMD64 represents the AMD64 CPU architecture.
type AMD64 struct {
	ptrSize                 int
	breakInstruction        []byte
	breakInstructionLen     int
	gStructOffset           uint64
	hardwareBreakpointUsage []bool
	goos                    string
}

// AMD64Arch returns an initialized AMD64
// struct.
func AMD64Arch(goos string) *AMD64 {
	var breakInstr = []byte{0xCC}

	return &AMD64{
		ptrSize:                 8,
		breakInstruction:        breakInstr,
		breakInstructionLen:     len(breakInstr),
		hardwareBreakpointUsage: make([]bool, 4),
		goos: goos,
	}
}

// PtrSize returns the size of a pointer
// on this architecture.
func (a *AMD64) PtrSize() int {
	return a.ptrSize
}

// BreakpointInstruction returns the Breakpoint
// instruction for this architecture.
func (a *AMD64) BreakpointInstruction() []byte {
	return a.breakInstruction
}

// BreakpointSize returns the size of the
// breakpoint instruction on this architecture.
func (a *AMD64) BreakpointSize() int {
	return a.breakInstructionLen
}

// If DerefTLS returns true the value of regs.TLS()+GStructOffset() is a
// pointer to the G struct
func (a *AMD64) DerefTLS() bool {
	return a.goos == "windows"
}
