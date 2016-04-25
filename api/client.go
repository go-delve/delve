package api

import "github.com/derekparker/delve/api/types"

// Client represents a debugger service client. All client methods are
// synchronous.
type Client interface {
	// Returns the pid of the process we are debugging.
	ProcessPid() int

	// Detach detaches the debugger, optionally killing the process.
	Detach(killProcess bool) error

	// Restarts program.
	Restart() error

	// GetState returns the current debugger state.
	GetState() (*types.DebuggerState, error)

	// Continue resumes process execution.
	Continue() <-chan *types.DebuggerState
	// Next continues to the next source line, not entering function calls.
	Next() (*types.DebuggerState, error)
	// Step continues to the next source line, entering function calls.
	Step() (*types.DebuggerState, error)
	// SingleStep will step a single cpu instruction.
	StepInstruction() (*types.DebuggerState, error)
	// SwitchThread switches the current thread context.
	SwitchThread(threadID int) (*types.DebuggerState, error)
	// SwitchGoroutine switches the current goroutine (and the current thread as well)
	SwitchGoroutine(goroutineID int) (*types.DebuggerState, error)
	// Halt suspends the process.
	Halt() (*types.DebuggerState, error)

	// GetBreakpoint gets a breakpoint by ID.
	GetBreakpoint(id int) (*types.Breakpoint, error)
	// GetBreakpointByName gets a breakpoint by name.
	GetBreakpointByName(name string) (*types.Breakpoint, error)
	// CreateBreakpoint creates a new breakpoint.
	CreateBreakpoint(*types.Breakpoint) (*types.Breakpoint, error)
	// ListBreakpoints gets all breakpoints.
	ListBreakpoints() ([]*types.Breakpoint, error)
	// ClearBreakpoint deletes a breakpoint by ID.
	ClearBreakpoint(id int) (*types.Breakpoint, error)
	// ClearBreakpointByName deletes a breakpoint by name
	ClearBreakpointByName(name string) (*types.Breakpoint, error)
	// Allows user to update an existing breakpoint for example to change the information
	// retrieved when the breakpoint is hit or to change, add or remove the break condition
	AmendBreakpoint(*types.Breakpoint) error
	// Cancels a Next or Step call that was interrupted by a manual stop or by another breakpoint
	CancelNext() error

	// ListThreads lists all threads.
	ListThreads() ([]*types.Thread, error)
	// GetThread gets a thread by its ID.
	GetThread(id int) (*types.Thread, error)

	// ListPackageVariables lists all package variables in the context of the current thread.
	ListPackageVariables(filter string, cfg types.LoadConfig) ([]types.Variable, error)
	// EvalVariable returns a variable in the context of the current thread.
	EvalVariable(scope types.EvalScope, symbol string, cfg types.LoadConfig) (*types.Variable, error)

	// SetVariable sets the value of a variable
	SetVariable(scope types.EvalScope, symbol, value string) error

	// ListSources lists all source files in the process matching filter.
	ListSources(filter string) ([]string, error)
	// ListFunctions lists all functions in the process matching filter.
	ListFunctions(filter string) ([]string, error)
	// ListTypes lists all types in the process matching filter.
	ListTypes(filter string) ([]string, error)
	// ListLocals lists all local variables in scope.
	ListLocalVariables(scope types.EvalScope, cfg types.LoadConfig) ([]types.Variable, error)
	// ListFunctionArgs lists all arguments to the current function.
	ListFunctionArgs(scope types.EvalScope, cfg types.LoadConfig) ([]types.Variable, error)
	// ListRegisters lists registers and their values.
	ListRegisters() (string, error)

	// ListGoroutines lists all goroutines.
	ListGoroutines() ([]*types.Goroutine, error)

	// Returns stacktrace
	Stacktrace(int, int, *types.LoadConfig) ([]types.Stackframe, error)

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
	FindLocation(scope types.EvalScope, loc string) ([]types.Location, error)

	// Disassemble code between startPC and endPC
	DisassembleRange(scope types.EvalScope, startPC, endPC uint64, flavour types.AssemblyFlavour) (types.AsmInstructions, error)
	// Disassemble code of the function containing PC
	DisassemblePC(scope types.EvalScope, pc uint64, flavour types.AssemblyFlavour) (types.AsmInstructions, error)
}
