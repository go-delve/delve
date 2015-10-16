package api

// DebuggerState represents the current context of the debugger.
type DebuggerState struct {
	// Breakpoint is the current breakpoint at which the debugged process is
	// suspended, and may be empty if the process is not suspended.
	Breakpoint *Breakpoint `json:"breakPoint,omitempty"`
	// CurrentThread is the currently selected debugger thread.
	CurrentThread *Thread `json:"currentThread,omitempty"`
	// SelectedGoroutine is the currently selected goroutine
	SelectedGoroutine *Goroutine `json:"currentGoroutine,omitempty"`
	// Information requested by the current breakpoint
	BreakpointInfo *BreakpointInfo `json:"breakPointInfo,omitrempty"`
	// Exited indicates whether the debugged process has exited.
	Exited     bool `json:"exited"`
	ExitStatus int  `json:"exitStatus"`

	// Filled by RPCClient.Continue, indicates an error
	Err error `json:"-"`
}

// Breakpoint addresses a location at which process execution may be
// suspended.
type Breakpoint struct {
	// ID is a unique identifier for the breakpoint.
	ID int `json:"id"`
	// Addr is the address of the breakpoint.
	Addr uint64 `json:"addr"`
	// File is the source file for the breakpoint.
	File string `json:"file"`
	// Line is a line in File for the breakpoint.
	Line int `json:"line"`
	// FunctionName is the name of the function at the current breakpoint, and
	// may not always be available.
	FunctionName string `json:"functionName,omitempty"`

	// tracepoint flag
	Tracepoint bool `json:"continue"`
	// number of stack frames to retrieve
	Stacktrace int `json:"stacktrace"`
	// retrieve goroutine information
	Goroutine bool `json:"goroutine"`
	// variables to evaluate
	Variables []string `json:"variables,omitempty"`
	// number of times a breakpoint has been reached in a certain goroutine
	HitCount map[string]uint64 `json:"hitCount"`
	// number of times a breakpoint has been reached
	TotalHitCount uint64 `json:"totalHitCount"`
}

// Thread is a thread within the debugged process.
type Thread struct {
	// ID is a unique identifier for the thread.
	ID int `json:"id"`
	// PC is the current program counter for the thread.
	PC uint64 `json:"pc"`
	// File is the file for the program counter.
	File string `json:"file"`
	// Line is the line number for the program counter.
	Line int `json:"line"`
	// Function is function information at the program counter. May be nil.
	Function *Function `json:"function,omitempty"`
}

type Location struct {
	PC       uint64    `json:"pc"`
	File     string    `json:"file"`
	Line     int       `json:"line"`
	Function *Function `json:"function,omitempty"`
}

type Stackframe struct {
	Location
	Locals    []Variable
	Arguments []Variable
}

func (frame *Stackframe) Var(name string) *Variable {
	for i := range frame.Locals {
		if frame.Locals[i].Name == name {
			return &frame.Locals[i]
		}
	}
	for i := range frame.Arguments {
		if frame.Arguments[i].Name == name {
			return &frame.Arguments[i]
		}
	}
	return nil
}

// Function represents thread-scoped function information.
type Function struct {
	// Name is the function name.
	Name   string `json:"name"`
	Value  uint64 `json:"value"`
	Type   byte   `json:"type"`
	GoType uint64 `json:"goType"`
	// Args are the function arguments in a thread context.
	Args []Variable `json:"args"`
	// Locals are the thread local variables.
	Locals []Variable `json:"locals"`
}

// Variable describes a variable.
type Variable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Type  string `json:"type"`
}

// Goroutine represents the information relevant to Delve from the runtime's
// internal G structure.
type Goroutine struct {
	// ID is a unique identifier for the goroutine.
	ID int `json:"id"`
	// Current location of the goroutine
	Current Location
	// Current location of the goroutine, excluding calls inside runtime
	UserCurrent Location
	// Location of the go instruction that started this goroutine
	Go Location
}

// DebuggerCommand is a command which changes the debugger's execution state.
type DebuggerCommand struct {
	// Name is the command to run.
	Name string `json:"name"`
	// ThreadID is used to specify which thread to use with the SwitchThread
	// command.
	ThreadID int `json:"threadID,omitempty"`
	// GoroutineID is used to specify which thread to use with the SwitchGoroutine
	// command.
	GoroutineID int `json:"goroutineID,omitempty"`
}

// Informations about the current breakpoint
type BreakpointInfo struct {
	Stacktrace []Stackframe `json:"stacktrace,omitempty"`
	Goroutine  *Goroutine   `json:"goroutine,omitempty"`
	Variables  []Variable   `json:"variables,omitempty"`
	Arguments  []Variable   `json:"arguments,omitempty"`
}

type EvalScope struct {
	GoroutineID int
	Frame       int
}

const (
	// Continue resumes process execution.
	Continue = "continue"
	// Step continues for a single instruction, entering function calls.
	Step = "step"
	// Next continues to the next source line, not entering function calls.
	Next = "next"
	// SwitchThread switches the debugger's current thread context.
	SwitchThread = "switchThread"
	// SwitchGoroutine switches the debugger's current thread context to the thread running the specified goroutine
	SwitchGoroutine = "switchGoroutine"
	// Halt suspends the process.
	Halt = "halt"
)
