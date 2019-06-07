package rpc2

import (
	"errors"
	"fmt"
	"time"

	"github.com/go-delve/delve/service"
	"github.com/go-delve/delve/service/api"
	"github.com/go-delve/delve/service/debugger"
)

type RPCServer struct {
	// config is all the information necessary to start the debugger and server.
	config *service.Config
	// debugger is a debugger service.
	debugger *debugger.Debugger
}

func NewServer(config *service.Config, debugger *debugger.Debugger) *RPCServer {
	return &RPCServer{config, debugger}
}

type ProcessPidIn struct {
}

type ProcessPidOut struct {
	Pid int
}

// ProcessPid returns the pid of the process we are debugging.
func (s *RPCServer) ProcessPid(arg ProcessPidIn, out *ProcessPidOut) error {
	out.Pid = s.debugger.ProcessPid()
	return nil
}

type LastModifiedIn struct {
}

type LastModifiedOut struct {
	Time time.Time
}

func (s *RPCServer) LastModified(arg LastModifiedIn, out *LastModifiedOut) error {
	out.Time = s.debugger.LastModified()
	return nil
}

type DetachIn struct {
	Kill bool
}

type DetachOut struct {
}

// Detach detaches the debugger, optionally killing the process.
func (s *RPCServer) Detach(arg DetachIn, out *DetachOut) error {
	err := s.debugger.Detach(arg.Kill)
	if s.config.DisconnectChan != nil {
		close(s.config.DisconnectChan)
		s.config.DisconnectChan = nil
	}
	return err
}

type RestartIn struct {
	// Position to restart from, if it starts with 'c' it's a checkpoint ID,
	// otherwise it's an event number. Only valid for recorded targets.
	Position string

	// ResetArgs tell whether NewArgs should take effect.
	ResetArgs bool
	// NewArgs are arguments to launch a new process.  They replace only the
	// argv[1] and later. Argv[0] cannot be changed.
	NewArgs []string
}

type RestartOut struct {
	DiscardedBreakpoints []api.DiscardedBreakpoint
}

// Restart restarts program.
func (s *RPCServer) Restart(arg RestartIn, out *RestartOut) error {
	if s.config.AttachPid != 0 {
		return errors.New("cannot restart process Delve did not create")
	}
	var err error
	out.DiscardedBreakpoints, err = s.debugger.Restart(arg.Position, arg.ResetArgs, arg.NewArgs)
	return err
}

type StateIn struct {
	// If NonBlocking is true State will return immediately even if the target process is running.
	NonBlocking bool
}

type StateOut struct {
	State *api.DebuggerState
}

// State returns the current debugger state.
func (s *RPCServer) State(arg StateIn, out *StateOut) error {
	st, err := s.debugger.State(arg.NonBlocking)
	if err != nil {
		return err
	}
	out.State = st
	return nil
}

type CommandOut struct {
	State api.DebuggerState
}

// Command interrupts, continues and steps through the program.
func (s *RPCServer) Command(command api.DebuggerCommand, cb service.RPCCallback) {
	st, err := s.debugger.Command(&command)
	if err != nil {
		cb.Return(nil, err)
		return
	}
	var out CommandOut
	out.State = *st
	cb.Return(out, nil)
}

type GetBreakpointIn struct {
	Id   int
	Name string
}

type GetBreakpointOut struct {
	Breakpoint api.Breakpoint
}

// GetBreakpoint gets a breakpoint by Name (if Name is not an empty string) or by ID.
func (s *RPCServer) GetBreakpoint(arg GetBreakpointIn, out *GetBreakpointOut) error {
	var bp *api.Breakpoint
	if arg.Name != "" {
		bp = s.debugger.FindBreakpointByName(arg.Name)
		if bp == nil {
			return fmt.Errorf("no breakpoint with name %s", arg.Name)
		}
	} else {
		bp = s.debugger.FindBreakpoint(arg.Id)
		if bp == nil {
			return fmt.Errorf("no breakpoint with id %d", arg.Id)
		}
	}
	out.Breakpoint = *bp
	return nil
}

type StacktraceIn struct {
	Id     int
	Depth  int
	Full   bool
	Defers bool // read deferred functions
	Cfg    *api.LoadConfig
}

type StacktraceOut struct {
	Locations []api.Stackframe
}

// Stacktrace returns stacktrace of goroutine Id up to the specified Depth.
//
// If Full is set it will also the variable of all local variables
// and function arguments of all stack frames.
func (s *RPCServer) Stacktrace(arg StacktraceIn, out *StacktraceOut) error {
	cfg := arg.Cfg
	if cfg == nil && arg.Full {
		cfg = &api.LoadConfig{true, 1, 64, 64, -1}
	}
	var err error
	out.Locations, err = s.debugger.Stacktrace(arg.Id, arg.Depth, arg.Defers, api.LoadConfigToProc(cfg))
	return err
}

type AncestorsIn struct {
	GoroutineID  int
	NumAncestors int
	Depth        int
}

type AncestorsOut struct {
	Ancestors []api.Ancestor
}

// Ancestors returns the stacktraces for the ancestors of a goroutine.
func (s *RPCServer) Ancestors(arg AncestorsIn, out *AncestorsOut) error {
	var err error
	out.Ancestors, err = s.debugger.Ancestors(arg.GoroutineID, arg.NumAncestors, arg.Depth)
	return err
}

type ListBreakpointsIn struct {
}

type ListBreakpointsOut struct {
	Breakpoints []*api.Breakpoint
}

// ListBreakpoints gets all breakpoints.
func (s *RPCServer) ListBreakpoints(arg ListBreakpointsIn, out *ListBreakpointsOut) error {
	out.Breakpoints = s.debugger.Breakpoints()
	return nil
}

type CreateBreakpointIn struct {
	Breakpoint api.Breakpoint
}

type CreateBreakpointOut struct {
	Breakpoint api.Breakpoint
}

// CreateBreakpoint creates a new breakpoint.
//
// - If arg.Breakpoint.File is not an empty string the breakpoint
// will be created on the specified file:line location
//
// - If arg.Breakpoint.FunctionName is not an empty string
// the breakpoint will be created on the specified function:line
// location.
//
// - Otherwise the value specified by arg.Breakpoint.Addr will be used.
func (s *RPCServer) CreateBreakpoint(arg CreateBreakpointIn, out *CreateBreakpointOut) error {
	createdbp, err := s.debugger.CreateBreakpoint(&arg.Breakpoint)
	if err != nil {
		return err
	}
	out.Breakpoint = *createdbp
	return nil
}

type ClearBreakpointIn struct {
	Id   int
	Name string
}

type ClearBreakpointOut struct {
	Breakpoint *api.Breakpoint
}

// ClearBreakpoint deletes a breakpoint by Name (if Name is not an
// empty string) or by ID.
func (s *RPCServer) ClearBreakpoint(arg ClearBreakpointIn, out *ClearBreakpointOut) error {
	var bp *api.Breakpoint
	if arg.Name != "" {
		bp = s.debugger.FindBreakpointByName(arg.Name)
		if bp == nil {
			return fmt.Errorf("no breakpoint with name %s", arg.Name)
		}
	} else {
		bp = s.debugger.FindBreakpoint(arg.Id)
		if bp == nil {
			return fmt.Errorf("no breakpoint with id %d", arg.Id)
		}
	}
	deleted, err := s.debugger.ClearBreakpoint(bp)
	if err != nil {
		return err
	}
	out.Breakpoint = deleted
	return nil
}

type AmendBreakpointIn struct {
	Breakpoint api.Breakpoint
}

type AmendBreakpointOut struct {
}

// AmendBreakpoint allows user to update an existing breakpoint
// for example to change the information retrieved when the
// breakpoint is hit or to change, add or remove the break condition.
//
// arg.Breakpoint.ID must be a valid breakpoint ID
func (s *RPCServer) AmendBreakpoint(arg AmendBreakpointIn, out *AmendBreakpointOut) error {
	return s.debugger.AmendBreakpoint(&arg.Breakpoint)
}

type CancelNextIn struct {
}

type CancelNextOut struct {
}

func (s *RPCServer) CancelNext(arg CancelNextIn, out *CancelNextOut) error {
	return s.debugger.CancelNext()
}

type ListThreadsIn struct {
}

type ListThreadsOut struct {
	Threads []*api.Thread
}

// ListThreads lists all threads.
func (s *RPCServer) ListThreads(arg ListThreadsIn, out *ListThreadsOut) (err error) {
	out.Threads, err = s.debugger.Threads()
	return err
}

type GetThreadIn struct {
	Id int
}

type GetThreadOut struct {
	Thread *api.Thread
}

// GetThread gets a thread by its ID.
func (s *RPCServer) GetThread(arg GetThreadIn, out *GetThreadOut) error {
	t, err := s.debugger.FindThread(arg.Id)
	if err != nil {
		return err
	}
	if t == nil {
		return fmt.Errorf("no thread with id %d", arg.Id)
	}
	out.Thread = t
	return nil
}

type ListPackageVarsIn struct {
	Filter string
	Cfg    api.LoadConfig
}

type ListPackageVarsOut struct {
	Variables []api.Variable
}

// ListPackageVars lists all package variables in the context of the current thread.
func (s *RPCServer) ListPackageVars(arg ListPackageVarsIn, out *ListPackageVarsOut) error {
	state, err := s.debugger.State(false)
	if err != nil {
		return err
	}

	current := state.CurrentThread
	if current == nil {
		return fmt.Errorf("no current thread")
	}

	vars, err := s.debugger.PackageVariables(current.ID, arg.Filter, *api.LoadConfigToProc(&arg.Cfg))
	if err != nil {
		return err
	}
	out.Variables = vars
	return nil
}

type ListRegistersIn struct {
	ThreadID  int
	IncludeFp bool
}

type ListRegistersOut struct {
	Registers string
	Regs      api.Registers
}

// ListRegisters lists registers and their values.
func (s *RPCServer) ListRegisters(arg ListRegistersIn, out *ListRegistersOut) error {
	if arg.ThreadID == 0 {
		state, err := s.debugger.State(false)
		if err != nil {
			return err
		}
		arg.ThreadID = state.CurrentThread.ID
	}

	regs, err := s.debugger.Registers(arg.ThreadID, arg.IncludeFp)
	if err != nil {
		return err
	}
	out.Regs = regs
	out.Registers = out.Regs.String()

	return nil
}

type ListLocalVarsIn struct {
	Scope api.EvalScope
	Cfg   api.LoadConfig
}

type ListLocalVarsOut struct {
	Variables []api.Variable
}

// ListLocalVars lists all local variables in scope.
func (s *RPCServer) ListLocalVars(arg ListLocalVarsIn, out *ListLocalVarsOut) error {
	vars, err := s.debugger.LocalVariables(arg.Scope, *api.LoadConfigToProc(&arg.Cfg))
	if err != nil {
		return err
	}
	out.Variables = vars
	return nil
}

type ListFunctionArgsIn struct {
	Scope api.EvalScope
	Cfg   api.LoadConfig
}

type ListFunctionArgsOut struct {
	Args []api.Variable
}

// ListFunctionArgs lists all arguments to the current function
func (s *RPCServer) ListFunctionArgs(arg ListFunctionArgsIn, out *ListFunctionArgsOut) error {
	vars, err := s.debugger.FunctionArguments(arg.Scope, *api.LoadConfigToProc(&arg.Cfg))
	if err != nil {
		return err
	}
	out.Args = vars
	return nil
}

type EvalIn struct {
	Scope api.EvalScope
	Expr  string
	Cfg   *api.LoadConfig
}

type EvalOut struct {
	Variable *api.Variable
}

// EvalVariable returns a variable in the specified context.
//
// See https://github.com/go-delve/delve/wiki/Expressions for
// a description of acceptable values of arg.Expr.
func (s *RPCServer) Eval(arg EvalIn, out *EvalOut) error {
	cfg := arg.Cfg
	if cfg == nil {
		cfg = &api.LoadConfig{true, 1, 64, 64, -1}
	}
	v, err := s.debugger.EvalVariableInScope(arg.Scope, arg.Expr, *api.LoadConfigToProc(cfg))
	if err != nil {
		return err
	}
	out.Variable = v
	return nil
}

type SetIn struct {
	Scope  api.EvalScope
	Symbol string
	Value  string
}

type SetOut struct {
}

// Set sets the value of a variable. Only numerical types and
// pointers are currently supported.
func (s *RPCServer) Set(arg SetIn, out *SetOut) error {
	return s.debugger.SetVariableInScope(arg.Scope, arg.Symbol, arg.Value)
}

type ListSourcesIn struct {
	Filter string
}

type ListSourcesOut struct {
	Sources []string
}

// ListSources lists all source files in the process matching filter.
func (s *RPCServer) ListSources(arg ListSourcesIn, out *ListSourcesOut) error {
	ss, err := s.debugger.Sources(arg.Filter)
	if err != nil {
		return err
	}
	out.Sources = ss
	return nil
}

type ListFunctionsIn struct {
	Filter string
}

type ListFunctionsOut struct {
	Funcs []string
}

// ListFunctions lists all functions in the process matching filter.
func (s *RPCServer) ListFunctions(arg ListFunctionsIn, out *ListFunctionsOut) error {
	fns, err := s.debugger.Functions(arg.Filter)
	if err != nil {
		return err
	}
	out.Funcs = fns
	return nil
}

type ListTypesIn struct {
	Filter string
}

type ListTypesOut struct {
	Types []string
}

// ListTypes lists all types in the process matching filter.
func (s *RPCServer) ListTypes(arg ListTypesIn, out *ListTypesOut) error {
	tps, err := s.debugger.Types(arg.Filter)
	if err != nil {
		return err
	}
	out.Types = tps
	return nil
}

type ListGoroutinesIn struct {
	Start int
	Count int
}

type ListGoroutinesOut struct {
	Goroutines []*api.Goroutine
	Nextg      int
}

// ListGoroutines lists all goroutines.
// If Count is specified ListGoroutines will return at the first Count
// goroutines and an index in Nextg, that can be passed as the Start
// parameter, to get more goroutines from ListGoroutines.
// Passing a value of Start that wasn't returned by ListGoroutines will skip
// an undefined number of goroutines.
func (s *RPCServer) ListGoroutines(arg ListGoroutinesIn, out *ListGoroutinesOut) error {
	gs, nextg, err := s.debugger.Goroutines(arg.Start, arg.Count)
	if err != nil {
		return err
	}
	out.Goroutines = gs
	out.Nextg = nextg
	return nil
}

type AttachedToExistingProcessIn struct {
}

type AttachedToExistingProcessOut struct {
	Answer bool
}

// AttachedToExistingProcess returns whether we attached to a running process or not
func (c *RPCServer) AttachedToExistingProcess(arg AttachedToExistingProcessIn, out *AttachedToExistingProcessOut) error {
	if c.config.AttachPid != 0 {
		out.Answer = true
	}
	return nil
}

type FindLocationIn struct {
	Scope api.EvalScope
	Loc   string
}

type FindLocationOut struct {
	Locations []api.Location
}

// FindLocation returns concrete location information described by a location expression
//
//  loc ::= <filename>:<line> | <function>[:<line>] | /<regex>/ | (+|-)<offset> | <line> | *<address>
//  * <filename> can be the full path of a file or just a suffix
//  * <function> ::= <package>.<receiver type>.<name> | <package>.(*<receiver type>).<name> | <receiver type>.<name> | <package>.<name> | (*<receiver type>).<name> | <name>
//  * <function> must be unambiguous
//  * /<regex>/ will return a location for each function matched by regex
//  * +<offset> returns a location for the line that is <offset> lines after the current line
//  * -<offset> returns a location for the line that is <offset> lines before the current line
//  * <line> returns a location for a line in the current file
//  * *<address> returns the location corresponding to the specified address
//
// NOTE: this function does not actually set breakpoints.
func (c *RPCServer) FindLocation(arg FindLocationIn, out *FindLocationOut) error {
	var err error
	out.Locations, err = c.debugger.FindLocation(arg.Scope, arg.Loc)
	return err
}

type DisassembleIn struct {
	Scope          api.EvalScope
	StartPC, EndPC uint64
	Flavour        api.AssemblyFlavour
}

type DisassembleOut struct {
	Disassemble api.AsmInstructions
}

// Disassemble code.
//
// If both StartPC and EndPC are non-zero the specified range will be disassembled, otherwise the function containing StartPC will be disassembled.
//
// Scope is used to mark the instruction the specified goroutine is stopped at.
//
// Disassemble will also try to calculate the destination address of an absolute indirect CALL if it happens to be the instruction the selected goroutine is stopped at.
func (c *RPCServer) Disassemble(arg DisassembleIn, out *DisassembleOut) error {
	var err error
	out.Disassemble, err = c.debugger.Disassemble(arg.Scope, arg.StartPC, arg.EndPC, arg.Flavour)
	return err
}

type RecordedIn struct {
}

type RecordedOut struct {
	Recorded       bool
	TraceDirectory string
}

func (s *RPCServer) Recorded(arg RecordedIn, out *RecordedOut) error {
	out.Recorded, out.TraceDirectory = s.debugger.Recorded()
	return nil
}

type CheckpointIn struct {
	Where string
}

type CheckpointOut struct {
	ID int
}

func (s *RPCServer) Checkpoint(arg CheckpointIn, out *CheckpointOut) error {
	var err error
	out.ID, err = s.debugger.Checkpoint(arg.Where)
	return err
}

type ListCheckpointsIn struct {
}

type ListCheckpointsOut struct {
	Checkpoints []api.Checkpoint
}

func (s *RPCServer) ListCheckpoints(arg ListCheckpointsIn, out *ListCheckpointsOut) error {
	var err error
	out.Checkpoints, err = s.debugger.Checkpoints()
	return err
}

type ClearCheckpointIn struct {
	ID int
}

type ClearCheckpointOut struct {
}

func (s *RPCServer) ClearCheckpoint(arg ClearCheckpointIn, out *ClearCheckpointOut) error {
	return s.debugger.ClearCheckpoint(arg.ID)
}

type IsMulticlientIn struct {
}

type IsMulticlientOut struct {
	// IsMulticlient returns true if the headless instance was started with --accept-multiclient
	IsMulticlient bool
}

func (s *RPCServer) IsMulticlient(arg IsMulticlientIn, out *IsMulticlientOut) error {
	*out = IsMulticlientOut{
		IsMulticlient: s.config.AcceptMulti,
	}
	return nil
}

// FunctionReturnLocationsIn holds arguments for the
// FunctionReturnLocationsRPC call. It holds the name of
// the function for which all return locations should be
// given.
type FunctionReturnLocationsIn struct {
	// FnName is the name of the function for which all
	// return locations should be given.
	FnName string
}

// FunctionReturnLocationsOut holds the result of the FunctionReturnLocations
// RPC call. It provides the list of addresses that the given function returns,
// for example with a `RET` instruction or `CALL runtime.deferreturn`.
type FunctionReturnLocationsOut struct {
	// Addrs is the list of all locations where the given function returns.
	Addrs []uint64
}

// FunctionReturnLocations is the implements the client call of the same name. Look at client documentation for more information.
func (s *RPCServer) FunctionReturnLocations(in FunctionReturnLocationsIn, out *FunctionReturnLocationsOut) error {
	addrs, err := s.debugger.FunctionReturnLocations(in.FnName)
	if err != nil {
		return err
	}
	*out = FunctionReturnLocationsOut{
		Addrs: addrs,
	}
	return nil
}

// ListDynamicLibrariesIn holds the arguments of ListDynamicLibraries
type ListDynamicLibrariesIn struct {
}

// ListDynamicLibrariesOut holds the return values of ListDynamicLibraries
type ListDynamicLibrariesOut struct {
	List []api.Image
}

func (s *RPCServer) ListDynamicLibraries(in ListDynamicLibrariesIn, out *ListDynamicLibrariesOut) error {
	out.List = s.debugger.ListDynamicLibraries()
	return nil
}
