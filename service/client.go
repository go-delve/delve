package service

import (
	"time"

	"github.com/derekparker/delve/service/api"
)

// Client represents a debugger service client. All client methods are
// synchronous.
type Client interface {
	// Returns the pid of the process we are debugging.
	ProcessPid() int

	// LastModified returns the time that the process' executable was modified.
	LastModified() time.Time

	// Detach detaches the debugger, optionally killing the process.
	Detach(killProcess bool) error

	// Restarts program.
	Restart() ([]api.DiscardedBreakpoint, error)
	// Restarts program from the specified position.
	RestartFrom(pos string, resetArgs bool, newArgs []string) ([]api.DiscardedBreakpoint, error)

	// GetState returns the current debugger state.
	GetState() (*api.DebuggerState, error)
	// GetStateNonBlocking returns the current debugger state, returning immediately if the target is already running.
	GetStateNonBlocking() (*api.DebuggerState, error)

	// Continue resumes process execution.
	Continue() <-chan *api.DebuggerState
	// Rewind resumes process execution backwards.
	Rewind() <-chan *api.DebuggerState
	// Next continues to the next source line, not entering function calls.
	Next() (*api.DebuggerState, error)
	// Step continues to the next source line, entering function calls.
	Step() (*api.DebuggerState, error)
	// StepOut continues to the return address of the current function
	StepOut() (*api.DebuggerState, error)
	// Call resumes process execution while making a function call.
	Call(expr string) (*api.DebuggerState, error)

	// SingleStep will step a single cpu instruction.
	StepInstruction() (*api.DebuggerState, error)
	// SwitchThread switches the current thread context.
	SwitchThread(threadID int) (*api.DebuggerState, error)
	// SwitchGoroutine switches the current goroutine (and the current thread as well)
	SwitchGoroutine(goroutineID int) (*api.DebuggerState, error)
	// Halt suspends the process.
	Halt() (*api.DebuggerState, error)

	// GetBreakpoint gets a breakpoint by ID.
	GetBreakpoint(id int) (*api.Breakpoint, error)
	// GetBreakpointByName gets a breakpoint by name.
	GetBreakpointByName(name string) (*api.Breakpoint, error)
	// CreateBreakpoint creates a new breakpoint.
	CreateBreakpoint(*api.Breakpoint) (*api.Breakpoint, error)
	// ListBreakpoints gets all breakpoints.
	ListBreakpoints() ([]*api.Breakpoint, error)
	// ClearBreakpoint deletes a breakpoint by ID.
	ClearBreakpoint(id int) (*api.Breakpoint, error)
	// ClearBreakpointByName deletes a breakpoint by name
	ClearBreakpointByName(name string) (*api.Breakpoint, error)
	// Allows user to update an existing breakpoint for example to change the information
	// retrieved when the breakpoint is hit or to change, add or remove the break condition
	AmendBreakpoint(*api.Breakpoint) error
	// Cancels a Next or Step call that was interrupted by a manual stop or by another breakpoint
	CancelNext() error

	// ListThreads lists all threads.
	ListThreads() ([]*api.Thread, error)
	// GetThread gets a thread by its ID.
	GetThread(id int) (*api.Thread, error)

	// ListPackageVariables lists all package variables in the context of the current thread.
	ListPackageVariables(filter string, cfg api.LoadConfig) ([]api.Variable, error)
	// EvalVariable returns a variable in the context of the current thread.
	EvalVariable(scope api.EvalScope, symbol string, cfg api.LoadConfig) (*api.Variable, error)

	// SetVariable sets the value of a variable
	SetVariable(scope api.EvalScope, symbol, value string) error

	// ListSources lists all source files in the process matching filter.
	ListSources(filter string) ([]string, error)
	// ListFunctions lists all functions in the process matching filter.
	ListFunctions(filter string) ([]string, error)
	// ListTypes lists all types in the process matching filter.
	ListTypes(filter string) ([]string, error)
	// ListLocals lists all local variables in scope.
	ListLocalVariables(scope api.EvalScope, cfg api.LoadConfig) ([]api.Variable, error)
	// ListFunctionArgs lists all arguments to the current function.
	ListFunctionArgs(scope api.EvalScope, cfg api.LoadConfig) ([]api.Variable, error)
	// ListRegisters lists registers and their values.
	ListRegisters(threadID int, includeFp bool) (api.Registers, error)

	// ListGoroutines lists all goroutines.
	ListGoroutines() ([]*api.Goroutine, error)

	// Returns stacktrace
	Stacktrace(int, int, *api.LoadConfig) ([]api.Stackframe, error)

	// Returns whether we attached to a running process or not
	AttachedToExistingProcess() bool

	// Returns concrete location information described by a location expression
	// loc ::= <filename>:<line> | <function>[:<line>] | /<regex>/ | (+|-)<offset> | <line> | *<address>
	// * <filename> can be the full path of a file or just a suffix
	// * <function> ::= <package>.<receiver type>.<name> | <package>.(*<receiver type>).<name> | <receiver type>.<name> | <package>.<name> | (*<receiver type>).<name> | <name>
	// * <function> must be unambiguous
	// * /<regex>/ will return a location for each function matched by regex
	// * +<offset> returns a location for the line that is <offset> lines after the current line
	// * -<offset> returns a location for the line that is <offset> lines before the current line
	// * <line> returns a location for a line in the current file
	// * *<address> returns the location corresponding to the specified address
	// NOTE: this function does not actually set breakpoints.
	FindLocation(scope api.EvalScope, loc string) ([]api.Location, error)

	// Disassemble code between startPC and endPC
	DisassembleRange(scope api.EvalScope, startPC, endPC uint64, flavour api.AssemblyFlavour) (api.AsmInstructions, error)
	// Disassemble code of the function containing PC
	DisassemblePC(scope api.EvalScope, pc uint64, flavour api.AssemblyFlavour) (api.AsmInstructions, error)

	// Recorded returns true if the target is a recording.
	Recorded() bool
	// TraceDirectory returns the path to the trace directory for a recording.
	TraceDirectory() (string, error)
	// Checkpoint sets a checkpoint at the current position.
	Checkpoint(where string) (checkpointID int, err error)
	// ListCheckpoints gets all checkpoints.
	ListCheckpoints() ([]api.Checkpoint, error)
	// ClearCheckpoint removes a checkpoint
	ClearCheckpoint(id int) error

	// SetReturnValuesLoadConfig sets the load configuration for return values.
	SetReturnValuesLoadConfig(*api.LoadConfig)

	// IsMulticlien returns true if the headless instance is multiclient.
	IsMulticlient() bool

	// Disconnect closes the connection to the server without sending a Detach request first.
	// If cont is true a continue command will be sent instead.
	Disconnect(cont bool) error
}
