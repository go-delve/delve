package proc

// Arch defines an interface for representing a
// CPU architecture.
type Arch interface {
	SetGStructOffset(ver GoVersion, iscgo bool)
	PtrSize() int
	BreakpointInstruction() []byte
	BreakpointSize() int
	GStructOffset() uint64
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

// SetGStructOffset sets the offset of the G struct on the AMD64
// arch struct. The offset is dependent on the Go compiler Version
// and whether or not the target program was externally linked.
func (a *AMD64) SetGStructOffset(ver GoVersion, isextld bool) {
	switch a.goos {
	case "darwin":
		a.gStructOffset = 0x8a0
	case "linux":
		a.gStructOffset = 0xfffffffffffffff0
		if isextld || ver.AfterOrEqual(GoVersion{1, 5, -1, 2, 0}) || ver.IsDevel() {
			a.gStructOffset += 8
		}
	case "windows":
		// Use ArbitraryUserPointer (0x28) as pointer to pointer
		// to G struct per:
		// https://golang.org/src/runtime/cgo/gcc_windows_amd64.c
		a.gStructOffset = 0x28
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

// If DerefTLS returns true the value of regs.TLS()+GStructOffset() is a
// pointer to the G struct
func (a *AMD64) DerefTLS() bool {
	return a.goos == "windows"
}
