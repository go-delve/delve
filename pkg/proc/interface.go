package proc

import (
	"go/ast"
)

// Process represents the target of the debugger. This
// target could be a system process, core file, etc.
type Process interface {
	Info
	ProcessManipulation
	BreakpointManipulation
}

// Info is an interface that provides general information on the target.
type Info interface {
	Pid() int
	Exited() bool
	Running() bool
	BinInfo() *BinaryInfo

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
	ContinueOnce() (trapthread Thread, err error)
	StepInstruction() error
	SwitchThread(int) error
	SwitchGoroutine(int) error
	RequestManualStop() error
	Halt() error
	Kill() error
	Detach(bool) error
}

// BreakpointManipulation is an interface for managing breakpoints.
type BreakpointManipulation interface {
	Breakpoints() map[uint64]*Breakpoint
	SetBreakpoint(addr uint64, kind BreakpointKind, cond ast.Expr) (*Breakpoint, error)
	ClearBreakpoint(addr uint64) (*Breakpoint, error)
	ClearInternalBreakpoints() error
}

// VariableEval is an interface for dealing with eval scopes.
type VariableEval interface {
	FrameToScope(Stackframe) *EvalScope
	ConvertEvalScope(gid, frame int) (*EvalScope, error)
}
