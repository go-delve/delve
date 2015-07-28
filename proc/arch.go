package proc

import "runtime"

type Arch interface {
	SetGStructOffset(ver GoVersion, iscgo bool)
	PtrSize() int
	BreakpointInstruction() []byte
	BreakpointSize() int
	GStructOffset() uint64
	HardwareBreakpointUsage() []bool
	SetHardwareBreakpointUsage(int, bool)
}

type AMD64 struct {
	ptrSize                 int
	breakInstruction        []byte
	breakInstructionLen     int
	gStructOffset           uint64
	hardwareBreakpointUsage []bool
}

func AMD64Arch() *AMD64 {
	var breakInstr = []byte{0xCC}

	return &AMD64{
		ptrSize:                 8,
		breakInstruction:        breakInstr,
		breakInstructionLen:     len(breakInstr),
		hardwareBreakpointUsage: make([]bool, 4),
	}
}

func (a *AMD64) SetGStructOffset(ver GoVersion, isextld bool) {
	switch runtime.GOOS {
	case "darwin":
		a.gStructOffset = 0x8a0
	case "linux":
		a.gStructOffset = 0xfffffffffffffff0
		if isextld || ver.AfterOrEqual(GoVersion{1, 5, -1, 2}) || ver.IsDevel() {
			a.gStructOffset += 8
		}
	}
}

func (a *AMD64) PtrSize() int {
	return a.ptrSize
}

func (a *AMD64) BreakpointInstruction() []byte {
	return a.breakInstruction
}

func (a *AMD64) BreakpointSize() int {
	return a.breakInstructionLen
}

func (a *AMD64) GStructOffset() uint64 {
	return a.gStructOffset
}

func (a *AMD64) HardwareBreakpointUsage() []bool {
	return a.hardwareBreakpointUsage
}

func (a *AMD64) SetHardwareBreakpointUsage(reg int, set bool) {
	a.hardwareBreakpointUsage[reg] = set
}
