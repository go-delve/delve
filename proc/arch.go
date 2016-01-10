package proc

import "runtime"

// Arch defines an interface for representing a
// CPU architecture.
type Arch interface {
	SetGStructOffset(ver GoVersion, iscgo bool)
	PtrSize() int
	BreakpointInstruction() []byte
	BreakpointSize() int
	GStructOffset() uint64
}

// AMD64 represents hte AMD64 CPU architecture.
type AMD64 struct {
	ptrSize                 int
	breakInstruction        []byte
	breakInstructionLen     int
	gStructOffset           uint64
	hardwareBreakpointUsage []bool
}

// AMD64Arch returns an initialized AMD64
// struct.
func AMD64Arch() *AMD64 {
	var breakInstr = []byte{0xCC}

	return &AMD64{
		ptrSize:                 8,
		breakInstruction:        breakInstr,
		breakInstructionLen:     len(breakInstr),
		hardwareBreakpointUsage: make([]bool, 4),
	}
}

// SetGStructOffset sets the offset of the G struct on the AMD64
// arch struct. The offset is depandant on the Go compiler Version
// and whether or not the target program was externally linked.
func (a *AMD64) SetGStructOffset(ver GoVersion, isextld bool) {
	switch runtime.GOOS {
	case "darwin":
		a.gStructOffset = 0x8a0
	case "linux":
		a.gStructOffset = 0xfffffffffffffff0
		if isextld || ver.AfterOrEqual(GoVersion{1, 5, -1, 2, 0}) || ver.IsDevel() {
			a.gStructOffset += 8
		}
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

// GStructOffset returns the offset of the G
// struct in thread local storage.
func (a *AMD64) GStructOffset() uint64 {
	return a.gStructOffset
}
