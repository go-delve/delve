package api

// DebuggerState represents the current context of the debugger.
type DebuggerState struct {
	// BreakPoint is the current breakpoint at which the debugged process
	// is suspended, and may be empty if the process is not suspended.
	BreakPoint *BreakPoint `json:"breakPoint,omitempty"`
	// CurrentThread is the currently selected debugger thread.
	CurrentThread *Thread `json:"currentThread,omitempty"`
	// List is the current focus for printing source lines.
	List *List `json:"list,omitempty"`
	// Exited indicates whether the debugged process has exited.
	Exited bool `json:"exited"`
}

// Arguments holds the unparsed command line argument(s).
type Arguments struct {
	Args []string `json:"args,omitempty"`
}

// LineSpec specifies a location in the target program.
type LineSpec struct {
	// Addr is the address corresponding to file:line or [file:]function.
	Addr uint64 `json:"addr"`
	// File is the source file for the location.
	File string `json:"file"`
	// Line is a line in File for the location.
	Line int `json:"line"`
	// FunctionName is the name of the function at the current location
	// and may not always be available.
	FunctionName string `json:"functionName,omitempty"`
	// Label is a label in the specified source file.
	Label string
	// Offset is the (signed) offset in lines from current location.
	Offset int `json:"offset,omitempty"`
}

// BreakPoint addresses a location at which process execution may be
// suspended.
type BreakPoint struct {
	// ID is a unique identifier for the breakpoint.
	ID int `json:"id"`
	// Location of the breakpoint.
	LineSpec
}

// Thread is a thread within the debugged process.
type Thread struct {
	// ID is a unique identifier for the thread.
	ID int `json:"id"`
	// LineSpec.Addr is the current program counter for the thread.
	LineSpec
	// Function is function information at the program counter. May be nil.
	Function *Function `json:"function,omitempty"`
}

// List is the current context for printing source lines.
type List struct {
	LineSpec
	// ListSize is the number of source lines to print.
	ListSize int `json:"listsize"`
	// Direction specifies the order in which lines are printed
	// (true == '+').
	Direction bool `json:"direction"`
	// FirstLine specifies the first line to be printed.
	FirstLine int
	// LastLine specifies the last line to be printed.
	LastLine int
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
	// PC is the current program counter for the goroutine.
	PC uint64 `json:"pc"`
	// File is the file for the program counter.
	File string `json:"file"`
	// Line is the line number for the program counter.
	Line int `json:"line"`
	// Function is function information at the program counter. May be nil.
	Function *Function `json:"function,omitempty"`
}

// DebuggerCommand is a command which changes the debugger's execution state.
type DebuggerCommand struct {
	// Name is the command to run.
	Name string `json:"name"`
	// ThreadID is used to specify which thread to use with the SwitchThread
	// command.
	ThreadID int `json:"threadID,omitempty"`
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
	// Halt suspends the process.
	Halt = "halt"
)
