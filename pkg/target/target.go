package target

import (
	"debug/gosym"
	"go/ast"
	"time"

	"github.com/derekparker/delve/pkg/proc"
)

// Target represents the target of the debugger. This
// target could be a system process, core file, etc.
type Interface interface {
	Info
	ProcessManipulation
	BreakpointManipulation
	VariableEval
}

// Info is an interface that provides general information on the target.
type Info interface {
	Pid() int
	Exited() bool
	Running() bool

	BinaryInfo
	ThreadInfo
	GoroutineInfo

	FindFileLocation(fileName string, lineNumber int) (uint64, error)
	FirstPCAfterPrologue(fn *gosym.Func, sameline bool) (uint64, error)
	FindFunctionLocation(funcName string, firstLine bool, lineOffset int) (uint64, error)
}

// BinaryInfo is an interface for accessing information on the binary file
// and the contents of binary sections.
type BinaryInfo interface {
	LastModified() time.Time
	Sources() map[string]*gosym.Obj
	Funcs() []gosym.Func
	Types() ([]string, error)
	PCToLine(uint64) (string, int, *gosym.Func)
}

// ThreadInfo is an interface for getting information on active threads
// in the process.
type ThreadInfo interface {
	Threads() map[int]*proc.Thread
	CurrentThread() *proc.Thread
}

// GoroutineInfo is an interface for getting information on running goroutines.
type GoroutineInfo interface {
	GoroutinesInfo() ([]*proc.G, error)
	SelectedGoroutine() *proc.G
	FindGoroutine(int) (*proc.G, error)
}

// ProcessManipulation is an interface for changing the execution state of a process.
type ProcessManipulation interface {
	Continue() error
	Next() error
	Step() error
	StepOut() error
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
	Breakpoints() map[uint64]*proc.Breakpoint
	SetBreakpoint(addr uint64, kind proc.BreakpointKind, cond ast.Expr) (*proc.Breakpoint, error)
	ClearBreakpoint(addr uint64) (*proc.Breakpoint, error)
	ClearInternalBreakpoints() error
}

// VariableEval is an interface for dealing with eval scopes.
type VariableEval interface {
	ConvertEvalScope(gid, frame int) (*proc.EvalScope, error)
}

var _ BinaryInfo = &proc.BinaryInfo{}
var _ Interface = &proc.Process{}
