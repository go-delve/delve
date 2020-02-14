package proc

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
}

// ProcessInternal holds a set of methods that are
// not meant to be called by anyone except for an instance of
// `proc.Target`. These methods are not safe to use by themselves
// and should never be called directly outside of the `proc` package.
// This is temporary and in support of an ongoing refactor.
type ProcessInternal interface {
	// WriteBreakpointFn is a low-level function which sets a breakpoint at
	// the given address. The address corresponds to an instruction address
	// where the breakpoint should be set.
	// If the breakpoint is successfully set, the filename, line number, and
	// enclosing function are returned, otherwise an error is returned.
	WriteBreakpointFn(addr uint64) (string, int, *Function, []byte, error)
	// ClearBreakpointFn will clear from memory a breakpoint instruction that
	// has been set at the given address. Callers should also provide the original
	// instruction that was overwritten when setting the breakpoint.
	ClearBreakpointFn(uint64, []byte) error
	// AdjustPCAfterBreakpoint returns whether the PC value of a thread must be adjusted when
	// a breakpoint is hit. Somebackends (gdbserial) will automatically adjust the PC register
	// for a thread when it hits a breakpoint. Other backends (native) will not make this adjustment
	// automatically.
	AdjustPCAfterBreakpoint() bool
	// CurrentDirection returns the current execution direction. For recorded
	// processes this can be forward or backward, otherwise it will always return
	// forward.
	CurrentDirection() Direction
	// Restart restarts the recording from the specified position, or from the
	// last checkpoint if pos == "".
	// If pos starts with 'c' it's a checkpoint ID, otherwise it's an event
	// number.
	Restart(pos string) error
	// SetSelectedGoroutine sets the default goroutine to be used when determining
	// the flow of control. Unless otherwise specified, this goroutine will be used
	// and followed during the execution of the process to ensure we follow it even
	// if it begins running on another thread. It will also be used as the default for
	// evaluation scopes, etc.
	SetSelectedGoroutine(*G)
	// ContinueOnce will continue execution of the process until it hits a breakpoint
	// or is otherwise stopped by a signal, etc. The call will return and the caller
	// will be able to inspect the state of the process and determine if we should
	// continue again or return control back to the user.
	ContinueOnce() (trapthread Thread, additionalTrapThreads []Thread, err error)
	// Detach will detach the debugger from the process. This means the debugger will
	// no longer receive events or notifications, or have any control over the process.
	// If kill is true, the debugger will kill the process during the detach.
	Detach(kill bool) error
}

// RecordingManipulation is an interface for manipulating process recordings.
type RecordingManipulation interface {
	// Recorded returns true if the current process is a recording and the path
	// to the trace directory.
	Recorded() (recorded bool, tracedir string)
	// Direction changes execution direction.
	Direction(Direction) error
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
	GoroutineInfo
}

// ThreadInfo is an interface for getting information on active threads
// in the process.
type ThreadInfo interface {
	FindThread(threadID int) (Thread, bool)
	ThreadList() []Thread
	CurrentThread() Thread
}

// GoroutineInfo is an interface for getting information on running goroutines.
type GoroutineInfo interface {
	SelectedGoroutine() *G
}

// ProcessManipulation is an interface for changing the execution state of a process.
type ProcessManipulation interface {
	SwitchThread(int) error
	SwitchGoroutine(*G) error
	RequestManualStop() error
	// CheckAndClearManualStopRequest returns true the first time it's called
	// after a call to RequestManualStop.
	CheckAndClearManualStopRequest() bool
}
