package rpc2

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	grpc "net/rpc"
	"net/rpc/jsonrpc"

	"github.com/derekparker/delve/service"
	"github.com/derekparker/delve/service/api"
	"github.com/derekparker/delve/service/debugger"
)

type (
	AmendBreakpointIn struct {
		Breakpoint api.Breakpoint
	}

	AmendBreakpointOut struct {
	}

	AttachedToExistingProcessIn struct {
	}

	AttachedToExistingProcessOut struct {
		Answer bool
	}

	ClearBreakpointIn struct {
		Id   int
		Name string
	}

	ClearBreakpointOut struct {
		Breakpoint *api.Breakpoint
	}

	CommandOut struct {
		State api.DebuggerState
	}

	CreateBreakpointIn struct {
		Breakpoint api.Breakpoint
	}

	CreateBreakpointOut struct {
		Breakpoint api.Breakpoint
	}

	DetachIn struct {
		Kill bool
	}

	DetachOut struct {
	}

	DisassembleIn struct {
		Scope          api.EvalScope
		StartPC, EndPC uint64
		Flavour        api.AssemblyFlavour
	}

	DisassembleOut struct {
		Disassemble api.AsmInstructions
	}

	EvalIn struct {
		Scope api.EvalScope
		Expr  string
	}

	EvalOut struct {
		Variable *api.Variable
	}

	FindLocationIn struct {
		Scope api.EvalScope
		Loc   string
	}

	FindLocationOut struct {
		Locations []api.Location
	}

	GetBreakpointIn struct {
		Id   int
		Name string
	}

	GetBreakpointOut struct {
		Breakpoint api.Breakpoint
	}

	GetThreadIn struct {
		Id int
	}

	GetThreadOut struct {
		Thread *api.Thread
	}

	ListBreakpointsIn struct {
	}

	ListBreakpointsOut struct {
		Breakpoints []*api.Breakpoint
	}

	ListFunctionArgsIn struct {
		Scope api.EvalScope
	}

	ListFunctionArgsOut struct {
		Args []api.Variable
	}

	ListFunctionsIn struct {
		Filter string
	}

	ListFunctionsOut struct {
		Funcs []string
	}

	ListGoroutinesIn struct {
	}

	ListGoroutinesOut struct {
		Goroutines []*api.Goroutine
	}

	ListLocalVarsIn struct {
		Scope api.EvalScope
	}

	ListLocalVarsOut struct {
		Variables []api.Variable
	}

	ListPackageVarsIn struct {
		Filter string
	}

	ListPackageVarsOut struct {
		Variables []api.Variable
	}

	ListRegistersIn struct {
	}

	ListRegistersOut struct {
		Registers string
	}

	ListSourcesIn struct {
		Filter string
	}

	ListSourcesOut struct {
		Sources []string
	}

	ListThreadsIn struct {
	}

	ListThreadsOut struct {
		Threads []*api.Thread
	}

	ListTypesIn struct {
		Filter string
	}

	ListTypesOut struct {
		Types []string
	}

	ProcessPidIn struct {
	}

	ProcessPidOut struct {
		Pid int
	}

	RestartIn struct {
	}

	RestartOut struct {
	}

	RPCServer struct {
		// config is all the information necessary to start the debugger and server.
		config *service.Config
		// listener is used to serve HTTP.
		listener net.Listener
		// stopChan is used to stop the listener goroutine
		stopChan chan struct{}
		// debugger is a debugger service.
		debugger *debugger.Debugger
	}

	ServerImpl struct {
		s *RPCServer
	}

	SetIn struct {
		Scope  api.EvalScope
		Symbol string
		Value  string
	}

	SetOut struct {
	}

	StacktraceIn struct {
		Id    int
		Depth int
		Full  bool
	}

	StacktraceOut struct {
		Locations []api.Stackframe
	}

	StateIn struct {
	}

	StateOut struct {
		State *api.DebuggerState
	}
)

// NewServer creates a new RPCServer.
func NewServer(config *service.Config, logEnabled bool) *ServerImpl {
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	if !logEnabled {
		log.SetOutput(ioutil.Discard)
	}

	return &ServerImpl{
		&RPCServer{
			config:   config,
			listener: config.Listener,
			stopChan: make(chan struct{}),
		},
	}
}

func (s *ServerImpl) Restart() error {
	return s.s.Restart(RestartIn{}, nil)
}

// Run starts a debugger and exposes it with an HTTP server. The debugger
// itself can be stopped with the `detach` API. Run blocks until the HTTP
// server stops.
func (s *ServerImpl) Run() error {
	var err error
	// Create and start the debugger
	if s.s.debugger, err = debugger.New(&debugger.Config{
		ProcessArgs: s.s.config.ProcessArgs,
		AttachPid:   s.s.config.AttachPid,
	}); err != nil {
		return err
	}

	rpcs := grpc.NewServer()
	rpcs.Register(s.s)

	go func() {
		defer s.s.listener.Close()
		for {
			c, err := s.s.listener.Accept()
			if err != nil {
				select {
				case <-s.s.stopChan:
					// We were supposed to exit, do nothing and return
					return
				default:
					panic(err)
				}
			}
			go rpcs.ServeCodec(jsonrpc.NewServerCodec(c))
			if !s.s.config.AcceptMulti {
				break
			}
		}
	}()
	return nil
}

// Stop detaches from the debugger and waits for it to stop.
func (s *ServerImpl) Stop(kill bool) error {
	if s.s.config.AcceptMulti {
		close(s.s.stopChan)
		s.s.listener.Close()
	}
	err := s.s.debugger.Detach(kill)
	if err != nil {
		return err
	}
	return nil
}

func (s *RPCServer) AmendBreakpoint(arg AmendBreakpointIn, out *AmendBreakpointOut) error {
	return s.debugger.AmendBreakpoint(&arg.Breakpoint)
}

func (c *RPCServer) AttachedToExistingProcess(arg AttachedToExistingProcessIn, out *AttachedToExistingProcessOut) error {
	if c.config.AttachPid != 0 {
		out.Answer = true
	}
	return nil
}

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

func (s *RPCServer) Command(command api.DebuggerCommand, out *CommandOut) error {
	st, err := s.debugger.Command(&command)
	if err != nil {
		return err
	}
	out.State = *st
	return nil
}

func (s *RPCServer) CreateBreakpoint(arg CreateBreakpointIn, out *CreateBreakpointOut) error {
	createdbp, err := s.debugger.CreateBreakpoint(&arg.Breakpoint)
	if err != nil {
		return err
	}
	out.Breakpoint = *createdbp
	return nil
}

func (s *RPCServer) Detach(arg DetachIn, out *DetachOut) error {
	return s.debugger.Detach(arg.Kill)
}

func (c *RPCServer) Disassemble(arg DisassembleIn, out *DisassembleOut) error {
	var err error
	out.Disassemble, err = c.debugger.Disassemble(arg.Scope, arg.StartPC, arg.EndPC, arg.Flavour)
	return err
}

func (s *RPCServer) Eval(arg EvalIn, out *EvalOut) error {
	v, err := s.debugger.EvalVariableInScope(arg.Scope, arg.Expr)
	if err != nil {
		return err
	}
	out.Variable = v
	return nil
}

func (c *RPCServer) FindLocation(arg FindLocationIn, out *FindLocationOut) error {
	var err error
	out.Locations, err = c.debugger.FindLocation(arg.Scope, arg.Loc)
	return err
}

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

func (s *RPCServer) ListBreakpoints(arg ListBreakpointsIn, out *ListBreakpointsOut) error {
	out.Breakpoints = s.debugger.Breakpoints()
	return nil
}

func (s *RPCServer) ListFunctionArgs(arg ListFunctionArgsIn, out *ListFunctionArgsOut) error {
	vars, err := s.debugger.FunctionArguments(arg.Scope)
	if err != nil {
		return err
	}
	out.Args = vars
	return nil
}

func (s *RPCServer) ListFunctions(arg ListFunctionsIn, out *ListFunctionsOut) error {
	fns, err := s.debugger.Functions(arg.Filter)
	if err != nil {
		return err
	}
	out.Funcs = fns
	return nil
}

func (s *RPCServer) ListGoroutines(arg ListGoroutinesIn, out *ListGoroutinesOut) error {
	gs, err := s.debugger.Goroutines()
	if err != nil {
		return err
	}
	out.Goroutines = gs
	return nil
}

func (s *RPCServer) ListLocalVars(arg ListLocalVarsIn, out *ListLocalVarsOut) error {
	vars, err := s.debugger.LocalVariables(arg.Scope)
	if err != nil {
		return err
	}
	out.Variables = vars
	return nil
}

func (s *RPCServer) ListPackageVars(arg ListPackageVarsIn, out *ListPackageVarsOut) error {
	state, err := s.debugger.State()
	if err != nil {
		return err
	}

	current := state.CurrentThread
	if current == nil {
		return fmt.Errorf("no current thread")
	}

	vars, err := s.debugger.PackageVariables(current.ID, arg.Filter)
	if err != nil {
		return err
	}
	out.Variables = vars
	return nil
}

func (s *RPCServer) ListRegisters(arg ListRegistersIn, out *ListRegistersOut) error {
	state, err := s.debugger.State()
	if err != nil {
		return err
	}

	regs, err := s.debugger.Registers(state.CurrentThread.ID)
	if err != nil {
		return err
	}
	out.Registers = regs
	return nil
}

func (s *RPCServer) ListSources(arg ListSourcesIn, out *ListSourcesOut) error {
	ss, err := s.debugger.Sources(arg.Filter)
	if err != nil {
		return err
	}
	out.Sources = ss
	return nil
}

func (s *RPCServer) ListThreads(arg ListThreadsIn, out *ListThreadsOut) (err error) {
	out.Threads, err = s.debugger.Threads()
	return err
}

func (s *RPCServer) ListTypes(arg ListTypesIn, out *ListTypesOut) error {
	tps, err := s.debugger.Types(arg.Filter)
	if err != nil {
		return err
	}
	out.Types = tps
	return nil
}

func (s *RPCServer) ProcessPid(arg ProcessPidIn, out *ProcessPidOut) error {
	out.Pid = s.debugger.ProcessPid()
	return nil
}

func (s *RPCServer) Restart(arg RestartIn, out *RestartOut) error {
	if s.config.AttachPid != 0 {
		return errors.New("cannot restart process Delve did not create")
	}
	return s.debugger.Restart()
}

func (s *RPCServer) Set(arg SetIn, out *SetOut) error {
	return s.debugger.SetVariableInScope(arg.Scope, arg.Symbol, arg.Value)
}

func (s *RPCServer) Stacktrace(arg StacktraceIn, out *StacktraceOut) error {
	locs, err := s.debugger.Stacktrace(arg.Id, arg.Depth, arg.Full)
	if err != nil {
		return err
	}
	out.Locations = locs
	return nil
}

func (s *RPCServer) State(arg StateIn, out *StateOut) error {
	st, err := s.debugger.State()
	if err != nil {
		return err
	}
	out.State = st
	return nil
}
