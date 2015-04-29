package proctl

type Arch interface {
	PtrSize() int
	BreakpointInstruction() []byte
	BreakpointSize() int
}

type AMD64 struct {
	ptrSize             int
	breakInstruction    []byte
	breakInstructionLen int
}

func AMD64Arch() *AMD64 {
	return &AMD64{
		ptrSize:             8,
		breakInstruction:    []byte{0xCC},
		breakInstructionLen: 1,
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
