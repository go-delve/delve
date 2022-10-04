package rpc2

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/proc"
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
	return s.debugger.Detach(arg.Kill)
}

type RestartIn struct {
	// Position to restart from, if it starts with 'c' it's a checkpoint ID,
	// otherwise it's an event number. Only valid for recorded targets.
	Position string

	// ResetArgs tell whether NewArgs and NewRedirects should take effect.
	ResetArgs bool
	// NewArgs are arguments to launch a new process.  They replace only the
	// argv[1] and later. Argv[0] cannot be changed.
	NewArgs []string

	// When Rerecord is set the target will be rerecorded
	Rerecord bool

	// When Rebuild is set the process will be build again
	Rebuild bool

	NewRedirects [3]string
}

type RestartOut struct {
	DiscardedBreakpoints []api.DiscardedBreakpoint
}

// Restart restarts program.
func (s *RPCServer) Restart(arg RestartIn, cb service.RPCCallback) {
	close(cb.SetupDoneChan())
	if s.config.Debugger.AttachPid != 0 {
		cb.Return(nil, errors.New("cannot restart process Delve did not create"))
		return
	}
	var out RestartOut
	var err error
	out.DiscardedBreakpoints, err = s.debugger.Restart(arg.Rerecord, arg.Position, arg.ResetArgs, arg.NewArgs, arg.NewRedirects, arg.Rebuild)
	cb.Return(out, err)
}

type StateIn struct {
	// If NonBlocking is true State will return immediately even if the target process is running.
	NonBlocking bool
}

type StateOut struct {
	State *api.DebuggerState
}

// State returns the current debugger state.
func (s *RPCServer) State(arg StateIn, cb service.RPCCallback) {
	close(cb.SetupDoneChan())
	var out StateOut
	st, err := s.debugger.State(arg.NonBlocking)
	if err != nil {
		cb.Return(nil, err)
		return
	}
	out.State = st
	cb.Return(out, nil)
}

type CommandOut struct {
	State api.DebuggerState
}

// Command interrupts, continues and steps through the program.
func (s *RPCServer) Command(command api.DebuggerCommand, cb service.RPCCallback) {
	st, err := s.debugger.Command(&command, cb.SetupDoneChan())
	if err != nil {
		cb.Return(nil, err)
		return
	}
	var out CommandOut
	out.State = *st
	cb.Return(out, nil)
}

type GetBufferedTracepointsIn struct {
}

type GetBufferedTracepointsOut struct {
	TracepointResults []api.TracepointResult
}

func (s *RPCServer) GetBufferedTracepoints(arg GetBufferedTracepointsIn, out *GetBufferedTracepointsOut) error {
	out.TracepointResults = s.debugger.GetBufferedTracepoints()
	return nil
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
	Id     int64
	Depth  int
	Full   bool
	Defers bool // read deferred functions (equivalent to passing StacktraceReadDefers in Opts)
	Opts   api.StacktraceOptions
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
		cfg = &api.LoadConfig{FollowPointers: true, MaxVariableRecurse: 1, MaxStringLen: 64, MaxArrayValues: 64, MaxStructFields: -1}
	}
	if arg.Defers {
		arg.Opts |= api.StacktraceReadDefers
	}
	var err error
	rawlocs, err := s.debugger.Stacktrace(arg.Id, arg.Depth, arg.Opts)
	if err != nil {
		return err
	}
	out.Locations, err = s.debugger.ConvertStacktrace(rawlocs, api.LoadConfigToProc(cfg))
	return err
}

type AncestorsIn struct {
	GoroutineID  int64
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
	All bool
}

type ListBreakpointsOut struct {
	Breakpoints []*api.Breakpoint
}

// ListBreakpoints gets all breakpoints.
func (s *RPCServer) ListBreakpoints(arg ListBreakpointsIn, out *ListBreakpointsOut) error {
	out.Breakpoints = s.debugger.Breakpoints(arg.All)
	return nil
}

type CreateBreakpointIn struct {
	Breakpoint api.Breakpoint

	LocExpr             string
	SubstitutePathRules [][2]string
	Suspended           bool
}

type CreateBreakpointOut struct {
	Breakpoint api.Breakpoint
}

// CreateBreakpoint creates a new breakpoint. The client is expected to populate `CreateBreakpointIn`
// with an `api.Breakpoint` struct describing where to set the breakpoing. For more information on
// how to properly request a breakpoint via the `api.Breakpoint` struct see the documentation for
// `debugger.CreateBreakpoint` here: https://pkg.go.dev/github.com/go-delve/delve/service/debugger#Debugger.CreateBreakpoint.
func (s *RPCServer) CreateBreakpoint(arg CreateBreakpointIn, out *CreateBreakpointOut) error {
	if err := api.ValidBreakpointName(arg.Breakpoint.Name); err != nil {
		return err
	}
	createdbp, err := s.debugger.CreateBreakpoint(&arg.Breakpoint, arg.LocExpr, arg.SubstitutePathRules, arg.Suspended)
	if err != nil {
		return err
	}
	out.Breakpoint = *createdbp
	return nil
}

type CreateEBPFTracepointIn struct {
	FunctionName string
}

type CreateEBPFTracepointOut struct {
	Breakpoint api.Breakpoint
}

func (s *RPCServer) CreateEBPFTracepoint(arg CreateEBPFTracepointIn, out *CreateEBPFTracepointOut) error {
	return s.debugger.CreateEBPFTracepoint(arg.FunctionName)
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

type ToggleBreakpointIn struct {
	Id   int
	Name string
}

type ToggleBreakpointOut struct {
	Breakpoint *api.Breakpoint
}

// ToggleBreakpoint toggles on or off a breakpoint by Name (if Name is not an
// empty string) or by ID.
func (s *RPCServer) ToggleBreakpoint(arg ToggleBreakpointIn, out *ToggleBreakpointOut) error {
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
	bp.Disabled = !bp.Disabled
	if err := api.ValidBreakpointName(bp.Name); err != nil {
		return err
	}
	if err := s.debugger.AmendBreakpoint(bp); err != nil {
		return err
	}
	out.Breakpoint = bp
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
	if err := api.ValidBreakpointName(arg.Breakpoint.Name); err != nil {
		return err
	}
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
	threads, err := s.debugger.Threads()
	if err != nil {
		return err
	}
	s.debugger.LockTarget()
	defer s.debugger.UnlockTarget()
	out.Threads = api.ConvertThreads(threads, s.debugger.ConvertThreadBreakpoint)
	return nil
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
	s.debugger.LockTarget()
	defer s.debugger.UnlockTarget()
	out.Thread = api.ConvertThread(t, s.debugger.ConvertThreadBreakpoint(t))
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
	vars, err := s.debugger.PackageVariables(arg.Filter, *api.LoadConfigToProc(&arg.Cfg))
	if err != nil {
		return err
	}
	out.Variables = api.ConvertVars(vars)
	return nil
}

type ListRegistersIn struct {
	ThreadID  int
	IncludeFp bool
	Scope     *api.EvalScope
}

type ListRegistersOut struct {
	Registers string
	Regs      api.Registers
}

// ListRegisters lists registers and their values.
// If ListRegistersIn.Scope is not nil the registers of that eval scope will
// be returned, otherwise ListRegistersIn.ThreadID will be used.
func (s *RPCServer) ListRegisters(arg ListRegistersIn, out *ListRegistersOut) error {
	if arg.ThreadID == 0 && arg.Scope == nil {
		state, err := s.debugger.State(false)
		if err != nil {
			return err
		}
		arg.ThreadID = state.CurrentThread.ID
	}

	var regs *op.DwarfRegisters
	var err error

	if arg.Scope != nil {
		regs, err = s.debugger.ScopeRegisters(arg.Scope.GoroutineID, arg.Scope.Frame, arg.Scope.DeferredCall, arg.IncludeFp)
	} else {
		regs, err = s.debugger.ThreadRegisters(arg.ThreadID, arg.IncludeFp)
	}
	if err != nil {
		return err
	}
	out.Regs = api.ConvertRegisters(regs, s.debugger.DwarfRegisterToString, arg.IncludeFp)
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
	vars, err := s.debugger.LocalVariables(arg.Scope.GoroutineID, arg.Scope.Frame, arg.Scope.DeferredCall, *api.LoadConfigToProc(&arg.Cfg))
	if err != nil {
		return err
	}
	out.Variables = api.ConvertVars(vars)
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
	vars, err := s.debugger.FunctionArguments(arg.Scope.GoroutineID, arg.Scope.Frame, arg.Scope.DeferredCall, *api.LoadConfigToProc(&arg.Cfg))
	if err != nil {
		return err
	}
	out.Args = api.ConvertVars(vars)
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

// Eval returns a variable in the specified context.
//
// See https://github.com/go-delve/delve/blob/master/Documentation/cli/expr.md
// for a description of acceptable values of arg.Expr.
func (s *RPCServer) Eval(arg EvalIn, out *EvalOut) error {
	cfg := arg.Cfg
	if cfg == nil {
		cfg = &api.LoadConfig{FollowPointers: true, MaxVariableRecurse: 1, MaxStringLen: 64, MaxArrayValues: 64, MaxStructFields: -1}
	}
	v, err := s.debugger.EvalVariableInScope(arg.Scope.GoroutineID, arg.Scope.Frame, arg.Scope.DeferredCall, arg.Expr, *api.LoadConfigToProc(cfg))
	if err != nil {
		return err
	}
	out.Variable = api.ConvertVar(v)
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
	return s.debugger.SetVariableInScope(arg.Scope.GoroutineID, arg.Scope.Frame, arg.Scope.DeferredCall, arg.Symbol, arg.Value)
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

	Filters []api.ListGoroutinesFilter
	api.GoroutineGroupingOptions
}

type ListGoroutinesOut struct {
	Goroutines    []*api.Goroutine
	Nextg         int
	Groups        []api.GoroutineGroup
	TooManyGroups bool
}

// ListGoroutines lists all goroutines.
// If Count is specified ListGoroutines will return at the first Count
// goroutines and an index in Nextg, that can be passed as the Start
// parameter, to get more goroutines from ListGoroutines.
// Passing a value of Start that wasn't returned by ListGoroutines will skip
// an undefined number of goroutines.
//
// If arg.Filters are specified the list of returned goroutines is filtered
// applying the specified filters.
// For example:
//
//	ListGoroutinesFilter{ Kind: ListGoroutinesFilterUserLoc, Negated: false, Arg: "afile.go" }
//
// will only return goroutines whose UserLoc contains "afile.go" as a substring.
// More specifically a goroutine matches a location filter if the specified
// location, formatted like this:
//
//	filename:lineno in function
//
// contains Arg[0] as a substring.
//
// Filters can also be applied to goroutine labels:
//
//	ListGoroutineFilter{ Kind: ListGoroutinesFilterLabel, Negated: false, Arg: "key=value" }
//
// this filter will only return goroutines that have a key=value label.
//
// If arg.GroupBy is not GoroutineFieldNone then the goroutines will
// be grouped with the specified criterion.
// If the value of arg.GroupBy is GoroutineLabel goroutines will
// be grouped by the value of the label with key GroupByKey.
// For each group a maximum of MaxExamples example goroutines are
// returned, as well as the total number of goroutines in the group.
func (s *RPCServer) ListGoroutines(arg ListGoroutinesIn, out *ListGoroutinesOut) error {
	//TODO(aarzilli): if arg contains a running goroutines filter (not negated)
	// and start == 0 and count == 0 then we can optimize this by just looking
	// at threads directly.
	gs, nextg, err := s.debugger.Goroutines(arg.Start, arg.Count)
	if err != nil {
		return err
	}
	gs = s.debugger.FilterGoroutines(gs, arg.Filters)
	gs, out.Groups, out.TooManyGroups = s.debugger.GroupGoroutines(gs, &arg.GoroutineGroupingOptions)
	s.debugger.LockTarget()
	defer s.debugger.UnlockTarget()
	out.Goroutines = api.ConvertGoroutines(s.debugger.Target(), gs)
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
	if c.config.Debugger.AttachPid != 0 {
		out.Answer = true
	}
	return nil
}

type FindLocationIn struct {
	Scope                     api.EvalScope
	Loc                       string
	IncludeNonExecutableLines bool

	// SubstitutePathRules is a slice of source code path substitution rules,
	// the first entry of each pair is the path of a directory as it appears in
	// the executable file (i.e. the location of a source file when the program
	// was compiled), the second entry of each pair is the location of the same
	// directory on the client system.
	SubstitutePathRules [][2]string
}

type FindLocationOut struct {
	Locations []api.Location
}

// FindLocation returns concrete location information described by a location expression.
//
//	loc ::= <filename>:<line> | <function>[:<line>] | /<regex>/ | (+|-)<offset> | <line> | *<address>
//	* <filename> can be the full path of a file or just a suffix
//	* <function> ::= <package>.<receiver type>.<name> | <package>.(*<receiver type>).<name> | <receiver type>.<name> | <package>.<name> | (*<receiver type>).<name> | <name>
//	  <function> must be unambiguous
//	* /<regex>/ will return a location for each function matched by regex
//	* +<offset> returns a location for the line that is <offset> lines after the current line
//	* -<offset> returns a location for the line that is <offset> lines before the current line
//	* <line> returns a location for a line in the current file
//	* *<address> returns the location corresponding to the specified address
//
// NOTE: this function does not actually set breakpoints.
func (c *RPCServer) FindLocation(arg FindLocationIn, out *FindLocationOut) error {
	var err error
	out.Locations, err = c.debugger.FindLocation(arg.Scope.GoroutineID, arg.Scope.Frame, arg.Scope.DeferredCall, arg.Loc, arg.IncludeNonExecutableLines, arg.SubstitutePathRules)
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
	insts, err := c.debugger.Disassemble(arg.Scope.GoroutineID, arg.StartPC, arg.EndPC)
	if err != nil {
		return err
	}
	out.Disassemble = make(api.AsmInstructions, len(insts))
	for i := range insts {
		out.Disassemble[i] = api.ConvertAsmInstruction(insts[i], c.debugger.AsmInstructionText(&insts[i], proc.AssemblyFlavour(arg.Flavour)))
	}
	return nil
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
	cps, err := s.debugger.Checkpoints()
	if err != nil {
		return err
	}
	out.Checkpoints = make([]api.Checkpoint, len(cps))
	for i := range cps {
		out.Checkpoints[i] = api.Checkpoint(cps[i])
	}
	return nil
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
	imgs := s.debugger.ListDynamicLibraries()
	out.List = make([]api.Image, 0, len(imgs))
	for i := range imgs {
		out.List = append(out.List, api.ConvertImage(imgs[i]))
	}
	return nil
}

// ListPackagesBuildInfoIn holds the arguments of ListPackages.
type ListPackagesBuildInfoIn struct {
	IncludeFiles bool
}

// ListPackagesBuildInfoOut holds the return values of ListPackages.
type ListPackagesBuildInfoOut struct {
	List []api.PackageBuildInfo
}

// ListPackagesBuildInfo returns the list of packages used by the program along with
// the directory where each package was compiled and optionally the list of
// files constituting the package.
// Note that the directory path is a best guess and may be wrong is a tool
// other than cmd/go is used to perform the build.
func (s *RPCServer) ListPackagesBuildInfo(in ListPackagesBuildInfoIn, out *ListPackagesBuildInfoOut) error {
	pkgs := s.debugger.ListPackagesBuildInfo(in.IncludeFiles)
	out.List = make([]api.PackageBuildInfo, 0, len(pkgs))
	for _, pkg := range pkgs {
		var files []string

		if len(pkg.Files) > 0 {
			files = make([]string, 0, len(pkg.Files))
			for file := range pkg.Files {
				files = append(files, file)
			}
		}

		sort.Strings(files)

		out.List = append(out.List, api.PackageBuildInfo{
			ImportPath:    pkg.ImportPath,
			DirectoryPath: pkg.DirectoryPath,
			Files:         files,
		})
	}
	return nil
}

// ExamineMemoryIn holds the arguments of ExamineMemory
type ExamineMemoryIn struct {
	Address uint64
	Length  int
}

// ExaminedMemoryOut holds the return values of ExamineMemory
type ExaminedMemoryOut struct {
	Mem            []byte
	IsLittleEndian bool
}

func (s *RPCServer) ExamineMemory(arg ExamineMemoryIn, out *ExaminedMemoryOut) error {
	if arg.Length > 1000 {
		return fmt.Errorf("len must be less than or equal to 1000")
	}
	Mem, err := s.debugger.ExamineMemory(arg.Address, arg.Length)
	if err != nil {
		return err
	}

	out.Mem = Mem
	out.IsLittleEndian = true //TODO: get byte order from debugger.target.BinInfo().Arch

	return nil
}

type StopRecordingIn struct {
}

type StopRecordingOut struct {
}

func (s *RPCServer) StopRecording(arg StopRecordingIn, cb service.RPCCallback) {
	close(cb.SetupDoneChan())
	var out StopRecordingOut
	err := s.debugger.StopRecording()
	if err != nil {
		cb.Return(nil, err)
		return
	}
	cb.Return(out, nil)
}

type DumpStartIn struct {
	Destination string
}

type DumpStartOut struct {
	State api.DumpState
}

// DumpStart starts a core dump to arg.Destination.
func (s *RPCServer) DumpStart(arg DumpStartIn, out *DumpStartOut) error {
	err := s.debugger.DumpStart(arg.Destination)
	if err != nil {
		return err
	}
	out.State = *api.ConvertDumpState(s.debugger.DumpWait(0))
	return nil
}

type DumpWaitIn struct {
	Wait int
}

type DumpWaitOut struct {
	State api.DumpState
}

// DumpWait waits for the core dump to finish or for arg.Wait milliseconds.
// Wait == 0 means return immediately.
// Returns the core dump status
func (s *RPCServer) DumpWait(arg DumpWaitIn, out *DumpWaitOut) error {
	out.State = *api.ConvertDumpState(s.debugger.DumpWait(time.Duration(arg.Wait) * time.Millisecond))
	return nil
}

type DumpCancelIn struct {
}

type DumpCancelOut struct {
}

// DumpCancel cancels the core dump.
func (s *RPCServer) DumpCancel(arg DumpCancelIn, out *DumpCancelOut) error {
	return s.debugger.DumpCancel()
}

type CreateWatchpointIn struct {
	Scope api.EvalScope
	Expr  string
	Type  api.WatchType
}

type CreateWatchpointOut struct {
	*api.Breakpoint
}

func (s *RPCServer) CreateWatchpoint(arg CreateWatchpointIn, out *CreateWatchpointOut) error {
	var err error
	out.Breakpoint, err = s.debugger.CreateWatchpoint(arg.Scope.GoroutineID, arg.Scope.Frame, arg.Scope.DeferredCall, arg.Expr, arg.Type)
	return err
}

type BuildIDIn struct {
}

type BuildIDOut struct {
	BuildID string
}

func (s *RPCServer) BuildID(arg BuildIDIn, out *BuildIDOut) error {
	out.BuildID = s.debugger.BuildID()
	return nil
}
