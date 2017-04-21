package proc

import (
	"debug/gosym"
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

	// FindFileLocation returns the address of the first instruction belonging
	// to line lineNumber in file fileName.
	FindFileLocation(fileName string, lineNumber int) (uint64, error)

	// FirstPCAfterPrologue returns the first instruction address after fn's
	// prologue.
	// If sameline is true and the first instruction after the prologue belongs
	// to a different source line the entry point will be returned instead.
	FirstPCAfterPrologue(fn *gosym.Func, sameline bool) (uint64, error)

	// FindFunctionLocation finds address of a function's line
	//
	// If firstLine == true is passed FindFunctionLocation will attempt to find
	// the first line of the function.
	//
	// If lineOffset is passed FindFunctionLocation will return the address of
	// that line.
	//
	// Pass lineOffset == 0 and firstLine == false if you want the address for
	// the function's entry point. Note that setting breakpoints at that
	// address will cause surprising behavior:
	// https://github.com/derekparker/delve/issues/170
	FindFunctionLocation(funcName string, firstLine bool, lineOffset int) (uint64, error)
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
