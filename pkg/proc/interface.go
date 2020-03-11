package proc

import (
	"go/ast"
)

// Process represents the target of the debugger. This
// target could be a system process, core file, etc.
//
// Implementations of Process are not required to be thread safe and users
// of Process should not assume they are.
// There is one exception to this rule: it is safe to call RequestManualStop
// concurrently with ContinueOnce.
type Process interface {
	Info
	ProcessManipulation
	BreakpointManipulation
	RecordingManipulation
}

// ProcessInternal holds a set of methods that are not meant to be called by
// anyone except for an instance of `proc.Target`. These methods are not
// safe to use by themselves and should never be called directly outside of
// the `proc` package.
// This is temporary and in support of an ongoing refactor.
type ProcessInternal interface {
	SetCurrentThread(Thread)
	// Restart restarts the recording from the specified position, or from the
	// last checkpoint if pos == "".
	// If pos starts with 'c' it's a checkpoint ID, otherwise it's an event
	// number.
	Restart(pos string) error
	Detach(bool) error
	ContinueOnce() (trapthread Thread, stopReason StopReason, err error)
}

// RecordingManipulation is an interface for manipulating process recordings.
type RecordingManipulation interface {
	// Recorded returns true if the current process is a recording and the path
	// to the trace directory.
	Recorded() (recorded bool, tracedir string)
	// Direction changes execution direction.
	ChangeDirection(Direction) error
	// GetDirection returns the current direction of execution.
	GetDirection() Direction
	// When returns current recording position.
	When() (string, error)
	// Checkpoint sets a checkpoint at the current position.
	Checkpoint(where string) (id int, err error)
	// Checkpoints returns the list of currently set checkpoint.
	Checkpoints() ([]Checkpoint, error)
	// ClearCheckpoint removes a checkpoint.
	ClearCheckpoint(id int) error
}

// Direction is the direction of execution for the target process.
type Direction int8

const (
	// Forward direction executes the target normally.
	Forward Direction = 0
	// Backward direction executes the target in reverse.
	Backward Direction = 1
)

// Checkpoint is a checkpoint
type Checkpoint struct {
	ID    int
	When  string
	Where string
}

// Info is an interface that provides general information on the target.
type Info interface {
	Pid() int
	// ResumeNotify specifies a channel that will be closed the next time
	// ContinueOnce finishes resuming the target.
	ResumeNotify(chan<- struct{})
	// Valid returns true if this Process can be used. When it returns false it
	// also returns an error describing why the Process is invalid (either
	// ErrProcessExited or ProcessDetachedError).
	Valid() (bool, error)
	BinInfo() *BinaryInfo
	EntryPoint() (uint64, error)

	ThreadInfo
}

// ThreadInfo is an interface for getting information on active threads
// in the process.
type ThreadInfo interface {
	FindThread(threadID int) (Thread, bool)
	ThreadList() []Thread
	CurrentThread() Thread
}

// ProcessManipulation is an interface for changing the execution state of a process.
type ProcessManipulation interface {
	RequestManualStop() error
	// CheckAndClearManualStopRequest returns true the first time it's called
	// after a call to RequestManualStop.
	CheckAndClearManualStopRequest() bool
}

// BreakpointManipulation is an interface for managing breakpoints.
type BreakpointManipulation interface {
	Breakpoints() *BreakpointMap
	SetBreakpoint(addr uint64, kind BreakpointKind, cond ast.Expr) (*Breakpoint, error)
	ClearBreakpoint(addr uint64) (*Breakpoint, error)
	ClearInternalBreakpoints() error
}
