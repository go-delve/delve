package rpc1

import (
	"errors"
	"fmt"

	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/service"
	"github.com/go-delve/delve/service/api"
	"github.com/go-delve/delve/service/debugger"
)

var defaultLoadConfig = proc.LoadConfig{
	FollowPointers:     true,
	MaxVariableRecurse: 1,
	MaxStringLen:       64,
	MaxArrayValues:     64,
	MaxStructFields:    -1,
}

type RPCServer struct {
	// config is all the information necessary to start the debugger and server.
	config *service.Config
	// debugger is a debugger service.
	debugger *debugger.Debugger
}

func NewServer(config *service.Config, debugger *debugger.Debugger) *RPCServer {
	return &RPCServer{config, debugger}
}

func (s *RPCServer) ProcessPid(arg1 interface{}, pid *int) error {
	*pid = s.debugger.ProcessPid()
	return nil
}

func (s *RPCServer) Detach(kill bool, ret *int) error {
	return s.debugger.Detach(kill)
}

func (s *RPCServer) Restart(arg1 interface{}, arg2 *int) error {
	if s.config.Debugger.AttachPid != 0 {
		return errors.New("cannot restart process Delve did not create")
	}
	_, err := s.debugger.Restart(false, "", false, nil, [3]string{}, false)
	return err
}

func (s *RPCServer) State(arg interface{}, state *api.DebuggerState) error {
	st, err := s.debugger.State(false)
	if err != nil {
		return err
	}
	*state = *st
	return nil
}

func (s *RPCServer) Command(command *api.DebuggerCommand, cb service.RPCCallback) {
	st, err := s.debugger.Command(command, cb.SetupDoneChan())
	cb.Return(st, err)
}

func (s *RPCServer) GetBreakpoint(id int, breakpoint *api.Breakpoint) error {
	bp := s.debugger.FindBreakpoint(id)
	if bp == nil {
		return fmt.Errorf("no breakpoint with id %d", id)
	}
	*breakpoint = *bp
	return nil
}

func (s *RPCServer) GetBreakpointByName(name string, breakpoint *api.Breakpoint) error {
	bp := s.debugger.FindBreakpointByName(name)
	if bp == nil {
		return fmt.Errorf("no breakpoint with name %s", name)
	}
	*breakpoint = *bp
	return nil
}

type StacktraceGoroutineArgs struct {
	Id    int
	Depth int
	Full  bool
}

func (s *RPCServer) StacktraceGoroutine(args *StacktraceGoroutineArgs, locations *[]api.Stackframe) error {
	var loadcfg *proc.LoadConfig = nil
	if args.Full {
		loadcfg = &defaultLoadConfig
	}
	locs, err := s.debugger.Stacktrace(args.Id, args.Depth, 0)
	if err != nil {
		return err
	}
	*locations, err = s.debugger.ConvertStacktrace(locs, loadcfg)
	return err
}

func (s *RPCServer) ListBreakpoints(arg interface{}, breakpoints *[]*api.Breakpoint) error {
	*breakpoints = s.debugger.Breakpoints(false)
	return nil
}

func (s *RPCServer) CreateBreakpoint(bp, newBreakpoint *api.Breakpoint) error {
	if err := api.ValidBreakpointName(bp.Name); err != nil {
		return err
	}
	createdbp, err := s.debugger.CreateBreakpoint(bp)
	if err != nil {
		return err
	}
	*newBreakpoint = *createdbp
	return nil
}

func (s *RPCServer) ClearBreakpoint(id int, breakpoint *api.Breakpoint) error {
	bp := s.debugger.FindBreakpoint(id)
	if bp == nil {
		return fmt.Errorf("no breakpoint with id %d", id)
	}
	deleted, err := s.debugger.ClearBreakpoint(bp)
	if err != nil {
		return err
	}
	*breakpoint = *deleted
	return nil
}

func (s *RPCServer) ClearBreakpointByName(name string, breakpoint *api.Breakpoint) error {
	bp := s.debugger.FindBreakpointByName(name)
	if bp == nil {
		return fmt.Errorf("no breakpoint with name %s", name)
	}
	deleted, err := s.debugger.ClearBreakpoint(bp)
	if err != nil {
		return err
	}
	*breakpoint = *deleted
	return nil
}

func (s *RPCServer) AmendBreakpoint(amend *api.Breakpoint, unused *int) error {
	*unused = 0
	if err := api.ValidBreakpointName(amend.Name); err != nil {
		return err
	}
	return s.debugger.AmendBreakpoint(amend)
}

func (s *RPCServer) ListThreads(arg interface{}, threads *[]*api.Thread) (err error) {
	pthreads, err := s.debugger.Threads()
	if err != nil {
		return err
	}
	s.debugger.LockTarget()
	defer s.debugger.UnlockTarget()
	*threads = api.ConvertThreads(pthreads)
	return nil
}

func (s *RPCServer) GetThread(id int, thread *api.Thread) error {
	t, err := s.debugger.FindThread(id)
	if err != nil {
		return err
	}
	if t == nil {
		return fmt.Errorf("no thread with id %d", id)
	}
	s.debugger.LockTarget()
	defer s.debugger.UnlockTarget()
	*thread = *api.ConvertThread(t)
	return nil
}

func (s *RPCServer) ListPackageVars(filter string, variables *[]api.Variable) error {
	vars, err := s.debugger.PackageVariables(filter, defaultLoadConfig)
	if err != nil {
		return err
	}
	*variables = api.ConvertVars(vars)
	return nil
}

type ThreadListArgs struct {
	Id     int
	Filter string
}

func (s *RPCServer) ListThreadPackageVars(args *ThreadListArgs, variables *[]api.Variable) error {
	vars, err := s.debugger.PackageVariables(args.Filter, defaultLoadConfig)
	if err != nil {
		return err
	}
	*variables = api.ConvertVars(vars)
	return nil
}

func (s *RPCServer) ListRegisters(arg interface{}, registers *string) error {
	state, err := s.debugger.State(false)
	if err != nil {
		return err
	}

	dregs, err := s.debugger.ThreadRegisters(state.CurrentThread.ID, false)
	if err != nil {
		return err
	}
	regs := api.Registers(api.ConvertRegisters(dregs, s.debugger.DwarfRegisterToString, false))
	*registers = regs.String()
	return nil
}

func (s *RPCServer) ListLocalVars(scope api.EvalScope, variables *[]api.Variable) error {
	vars, err := s.debugger.LocalVariables(scope.GoroutineID, scope.Frame, scope.DeferredCall, defaultLoadConfig)
	if err != nil {
		return err
	}
	*variables = api.ConvertVars(vars)
	return nil
}

func (s *RPCServer) ListFunctionArgs(scope api.EvalScope, variables *[]api.Variable) error {
	vars, err := s.debugger.FunctionArguments(scope.GoroutineID, scope.Frame, scope.DeferredCall, defaultLoadConfig)
	if err != nil {
		return err
	}
	*variables = api.ConvertVars(vars)
	return nil
}

type EvalSymbolArgs struct {
	Scope  api.EvalScope
	Symbol string
}

func (s *RPCServer) EvalSymbol(args EvalSymbolArgs, variable *api.Variable) error {
	v, err := s.debugger.EvalVariableInScope(args.Scope.GoroutineID, args.Scope.Frame, args.Scope.DeferredCall, args.Symbol, defaultLoadConfig)
	if err != nil {
		return err
	}
	*variable = *api.ConvertVar(v)
	return nil
}

type SetSymbolArgs struct {
	Scope  api.EvalScope
	Symbol string
	Value  string
}

func (s *RPCServer) SetSymbol(args SetSymbolArgs, unused *int) error {
	*unused = 0
	return s.debugger.SetVariableInScope(args.Scope.GoroutineID, args.Scope.Frame, args.Scope.DeferredCall, args.Symbol, args.Value)
}

func (s *RPCServer) ListSources(filter string, sources *[]string) error {
	ss, err := s.debugger.Sources(filter)
	if err != nil {
		return err
	}
	*sources = ss
	return nil
}

func (s *RPCServer) ListFunctions(filter string, funcs *[]string) error {
	fns, err := s.debugger.Functions(filter)
	if err != nil {
		return err
	}
	*funcs = fns
	return nil
}

func (s *RPCServer) ListTypes(filter string, types *[]string) error {
	tps, err := s.debugger.Types(filter)
	if err != nil {
		return err
	}
	*types = tps
	return nil
}

func (s *RPCServer) ListGoroutines(arg interface{}, goroutines *[]*api.Goroutine) error {
	gs, _, err := s.debugger.Goroutines(0, 0)
	if err != nil {
		return err
	}
	s.debugger.LockTarget()
	s.debugger.UnlockTarget()
	*goroutines = api.ConvertGoroutines(s.debugger.Target(), gs)
	return nil
}

func (c *RPCServer) AttachedToExistingProcess(arg interface{}, answer *bool) error {
	if c.config.Debugger.AttachPid != 0 {
		*answer = true
	}
	return nil
}

type FindLocationArgs struct {
	Scope api.EvalScope
	Loc   string
}

func (c *RPCServer) FindLocation(args FindLocationArgs, answer *[]api.Location) error {
	var err error
	*answer, err = c.debugger.FindLocation(args.Scope.GoroutineID, args.Scope.Frame, args.Scope.DeferredCall, args.Loc, false, nil)
	return err
}

type DisassembleRequest struct {
	Scope          api.EvalScope
	StartPC, EndPC uint64
	Flavour        api.AssemblyFlavour
}

func (c *RPCServer) Disassemble(args DisassembleRequest, answer *api.AsmInstructions) error {
	var err error
	insts, err := c.debugger.Disassemble(args.Scope.GoroutineID, args.StartPC, args.EndPC)
	if err != nil {
		return err
	}
	*answer = make(api.AsmInstructions, len(insts))
	for i := range insts {
		(*answer)[i] = api.ConvertAsmInstruction(insts[i], c.debugger.AsmInstructionText(&insts[i], proc.AssemblyFlavour(args.Flavour)))
	}
	return nil
}
