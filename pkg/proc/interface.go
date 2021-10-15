package proc

import (
	"github.com/go-delve/delve/pkg/elfwriter"
	"github.com/go-delve/delve/pkg/proc/internal/ebpf"
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
	RecordingManipulation

	Breakpoints() *BreakpointMap

	// Memory returns a memory read/writer for this process's memory.
	Memory() MemoryReadWriter
}

// ProcessInternal holds a set of methods that are not meant to be called by
// anyone except for an instance of `proc.Target`. These methods are not
// safe to use by themselves and should never be called directly outside of
// the `proc` package.
// This is temporary and in support of an ongoing refactor.
type ProcessInternal interface {
	// Valid returns true if this Process can be used. When it returns false it
	// also returns an error describing why the Process is invalid (either
	// ErrProcessExited or ErrProcessDetached).
	Valid() (bool, error)
	// Restart restarts the recording from the specified position, or from the
	// last checkpoint if pos == "".
	// If pos starts with 'c' it's a checkpoint ID, otherwise it's an event
	// number.
	// Returns the new current thread after the restart has completed.
	Restart(pos string) (Thread, error)
	Detach(bool) error
	ContinueOnce() (trapthread Thread, stopReason StopReason, err error)

	WriteBreakpoint(*Breakpoint) error
	EraseBreakpoint(*Breakpoint) error

	SupportsBPF() bool
	SetUProbe(string, int64, []ebpf.UProbeArgMap) error

	// DumpProcessNotes returns ELF core notes describing the process and its threads.
	// Implementing this method is optional.
	DumpProcessNotes(notes []elfwriter.Note, threadDone func()) (bool, []elfwriter.Note, error)
	// MemoryMap returns the memory map of the target process. This method must be implemented if CanDump is true.
	MemoryMap() ([]MemoryMapEntry, error)

	GetBufferedTracepoints() []ebpf.RawUProbeParams

	// StartCallInjection notifies the backend that we are about to inject a function call.
	StartCallInjection() (func(), error)
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
	BinInfo() *BinaryInfo
	EntryPoint() (uint64, error)

	ThreadInfo
}

// ThreadInfo is an interface for getting information on active threads
// in the process.
type ThreadInfo interface {
	FindThread(threadID int) (Thread, bool)
	ThreadList() []Thread
}

// ProcessManipulation is an interface for changing the execution state of a process.
type ProcessManipulation interface {
	// RequestManualStop attempts to stop all the process' threads.
	RequestManualStop() error
	// CheckAndClearManualStopRequest returns true the first time it's called
	// after a call to RequestManualStop.
	CheckAndClearManualStopRequest() bool
}
