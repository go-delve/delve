package proc

import (
	"errors"
	"github.com/go-delve/delve/pkg/dwarf/op"
)

// Thread represents a thread.
type Thread interface {
	Location() (*Location, error)
	// Breakpoint will return the breakpoint that this thread is stopped at or
	// nil if the thread is not stopped at any breakpoint.
	Breakpoint() *BreakpointState
	ThreadID() int

	// Registers returns the CPU registers of this thread. The contents of the
	// variable returned may or may not change to reflect the new CPU status
	// when the thread is resumed or the registers are changed by calling
	// SetPC/SetSP/etc.
	// To insure that the the returned variable won't change call the Copy
	// method of Registers.
	Registers() (Registers, error)

	// RestoreRegisters restores saved registers
	RestoreRegisters(Registers) error
	BinInfo() *BinaryInfo
	// ProcessMemory returns the process memory.
	ProcessMemory() MemoryReadWriter
	StepInstruction() error
	// SetCurrentBreakpoint updates the current breakpoint of this thread, if adjustPC is true also checks for breakpoints that were just hit (this should only be passed true after a thread resume)
	SetCurrentBreakpoint(adjustPC bool) error
	// SoftExc returns true if this thread received a software exception during the last resume.
	SoftExc() bool
	// Common returns the CommonThread structure for this thread
	Common() *CommonThread

	// SetReg changes the value of the specified register. A minimal
	// implementation of this interface can support just setting the PC
	// register.
	SetReg(uint64, *op.DwarfRegister) error
}

// Location represents the location of a thread.
// Holds information on the current instruction
// address, the source file:line, and the function.
type Location struct {
	PC   uint64
	File string
	Line int
	Fn   *Function
}

// CommonThread contains fields used by this package, common to all
// implementations of the Thread interface.
type CommonThread struct {
	CallReturn   bool // returnValues are the return values of a call injection
	returnValues []*Variable
	g            *G // cached g for this thread
}

// ReturnValues reads the return values from the function executing on
// this thread using the provided LoadConfig.
func (t *CommonThread) ReturnValues(cfg LoadConfig) []*Variable {
	loadValues(t.returnValues, cfg)
	return t.returnValues
}

// topframe returns the two topmost frames of g, or thread if g is nil.
func topframe(g *G, thread Thread) (Stackframe, Stackframe, error) {
	var frames []Stackframe
	var err error

	if g == nil {
		frames, err = ThreadStacktrace(thread, 1)
	} else {
		frames, err = g.Stacktrace(1, StacktraceReadDefers)
	}
	if err != nil {
		return Stackframe{}, Stackframe{}, err
	}
	switch len(frames) {
	case 0:
		return Stackframe{}, Stackframe{}, errors.New("empty stack trace")
	case 1:
		return frames[0], Stackframe{}, nil
	default:
		return frames[0], frames[1], nil
	}
}

func setPC(thread Thread, newPC uint64) error {
	return thread.SetReg(thread.BinInfo().Arch.PCRegNum, op.DwarfRegisterFromUint64(newPC))
}

func setSP(thread Thread, newSP uint64) error {
	return thread.SetReg(thread.BinInfo().Arch.SPRegNum, op.DwarfRegisterFromUint64(newSP))
}

func setClosureReg(thread Thread, newClosureReg uint64) error {
	return thread.SetReg(thread.BinInfo().Arch.ContextRegNum, op.DwarfRegisterFromUint64(newClosureReg))
}

func setLR(thread Thread, newLR uint64) error {
	return thread.SetReg(thread.BinInfo().Arch.LRRegNum, op.DwarfRegisterFromUint64(newLR))
}
