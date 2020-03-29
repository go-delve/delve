package proc

import (
	"errors"
)

// Thread represents a thread.
type Thread interface {
	MemoryReadWriter
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
	Registers(floatingPoint bool) (Registers, error)

	// RestoreRegisters restores saved registers
	RestoreRegisters(Registers) error
	BinInfo() *BinaryInfo
	StepInstruction() error
	// Blocked returns true if the thread is blocked
	Blocked() bool
	// SetCurrentBreakpoint updates the current breakpoint of this thread, if adjustPC is true also checks for breakpoints that were just hit (this should only be passed true after a thread resume)
	SetCurrentBreakpoint(adjustPC bool) error
	// Common returns the CommonThread structure for this thread
	Common() *CommonThread

	SetPC(uint64) error
	SetSP(uint64) error
	SetDX(uint64) error
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

// ErrThreadBlocked is returned when the thread
// is blocked in the scheduler.
type ErrThreadBlocked struct{}

func (tbe ErrThreadBlocked) Error() string {
	return "thread blocked"
}

// CommonThread contains fields used by this package, common to all
// implementations of the Thread interface.
type CommonThread struct {
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
		if thread.Blocked() {
			return Stackframe{}, Stackframe{}, ErrThreadBlocked{}
		}
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
