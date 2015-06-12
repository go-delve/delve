package service

import (
	"github.com/derekparker/delve/service/api"
)

// Client represents a debugger service client. All client methods are
// synchronous.
type Client interface {
	// Detach detaches the debugger, optionally killing the process.
	Detach(killProcess bool) error

	// GetState returns the current debugger state.
	GetState() (*api.DebuggerState, error)

	// Continue resumes process execution.
	Continue() (*api.DebuggerState, error)
	// Next continues to the next source line, not entering function calls.
	Next() (*api.DebuggerState, error)
	// Step continues to the next source line, entering function calls.
	Step() (*api.DebuggerState, error)
	// SwitchThread switches the current thread context.
	SwitchThread(threadID int) (*api.DebuggerState, error)
	// Halt suspends the process.
	Halt() (*api.DebuggerState, error)

	// GetBreakPoint gets a breakpoint by ID.
	GetBreakPoint(id int) (*api.BreakPoint, error)
	// CreateBreakPoint creates a new breakpoint.
	CreateBreakPoint(location *api.Arguments) (*api.BreakPoint, error)
	// ListBreakPoints gets all breakpoints.
	ListBreakPoints() ([]*api.BreakPoint, error)
	// ClearBreakPoint deletes a breakpoint by ID.
	ClearBreakPoint(id int) (*api.BreakPoint, error)

	// ListThreads lists all threads.
	ListThreads() ([]*api.Thread, error)
	// GetThread gets a thread by its ID.
	GetThread(id int) (*api.Thread, error)

	// ListPackageVariables lists all package variables in the context of the current thread.
	ListPackageVariables(filter string) ([]api.Variable, error)
	// EvalVariable returns a variable in the context of the current thread.
	EvalVariable(symbol string) (*api.Variable, error)
	// ListPackageVariablesFor lists all package variables in the context of a thread.
	ListPackageVariablesFor(threadID int, filter string) ([]api.Variable, error)
	// EvalVariableFor returns a variable in the context of the specified thread.
	EvalVariableFor(threadID int, symbol string) (*api.Variable, error)

	// ListSources lists all source files in the process matching filter.
	ListSources(filter string) ([]string, error)
	// ListFunctions lists all functions in the process matching filter.
	ListFunctions(filter string) ([]string, error)
	// ListLocals lists all local variables in scope.
	ListLocalVariables() ([]api.Variable, error)
	// ListFunctionArgs lists all arguments to the current function.
	ListFunctionArgs() ([]api.Variable, error)
	// List prints out lines of source code.
	List(location string) (*api.DebuggerState, error)

	// ListGoroutines lists all goroutines.
	ListGoroutines() ([]*api.Goroutine, error)
}
