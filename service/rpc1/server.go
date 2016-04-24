package rpc1

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
	DisassembleRequest struct {
		Scope          api.EvalScope
		StartPC, EndPC uint64
		Flavour        api.AssemblyFlavour
	}

	EvalSymbolArgs struct {
		Scope  api.EvalScope
		Symbol string
	}

	FindLocationArgs struct {
		Scope api.EvalScope
		Loc   string
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

	SetSymbolArgs struct {
		Scope  api.EvalScope
		Symbol string
		Value  string
	}

	StacktraceGoroutineArgs struct {
		Id    int
		Depth int
		Full  bool
	}

	ThreadListArgs struct {
		Id     int
		Filter string
	}
)

// NewServer creates a new RPCServer.
func NewServer(config *service.Config, logEnabled bool) *ServerImpl {
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	if !logEnabled {
		log.SetOutput(ioutil.Discard)
	}
	log.Printf("Using API v1")

	return &ServerImpl{
		&RPCServer{
			config:   config,
			listener: config.Listener,
			stopChan: make(chan struct{}),
		},
	}
}

func (s *ServerImpl) Restart() error {
	return s.s.Restart(nil, nil)
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

func (s *RPCServer) AmendBreakpoint(amend *api.Breakpoint, unused *int) error {
	*unused = 0
	return s.debugger.AmendBreakpoint(amend)
}

func (c *RPCServer) AttachedToExistingProcess(arg interface{}, answer *bool) error {
	if c.config.AttachPid != 0 {
		*answer = true
	}
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

func (s *RPCServer) Command(command *api.DebuggerCommand, state *api.DebuggerState) error {
	st, err := s.debugger.Command(command)
	if err != nil {
		return err
	}
	*state = *st
	return nil
}

func (s *RPCServer) CreateBreakpoint(bp, newBreakpoint *api.Breakpoint) error {
	createdbp, err := s.debugger.CreateBreakpoint(bp)
	if err != nil {
		return err
	}
	*newBreakpoint = *createdbp
	return nil
}

func (s *RPCServer) Detach(kill bool, ret *int) error {
	return s.debugger.Detach(kill)
}

func (c *RPCServer) Disassemble(args DisassembleRequest, answer *api.AsmInstructions) error {
	var err error
	*answer, err = c.debugger.Disassemble(args.Scope, args.StartPC, args.EndPC, args.Flavour)
	return err
}

func (s *RPCServer) EvalSymbol(args EvalSymbolArgs, variable *api.Variable) error {
	v, err := s.debugger.EvalVariableInScope(args.Scope, args.Symbol)
	if err != nil {
		return err
	}
	*variable = *v
	return nil
}

func (c *RPCServer) FindLocation(args FindLocationArgs, answer *[]api.Location) error {
	var err error
	*answer, err = c.debugger.FindLocation(args.Scope, args.Loc)
	return err
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

func (s *RPCServer) GetThread(id int, thread *api.Thread) error {
	t, err := s.debugger.FindThread(id)
	if err != nil {
		return err
	}
	if t == nil {
		return fmt.Errorf("no thread with id %d", id)
	}
	*thread = *t
	return nil
}

func (s *RPCServer) ListBreakpoints(arg interface{}, breakpoints *[]*api.Breakpoint) error {
	*breakpoints = s.debugger.Breakpoints()
	return nil
}

func (s *RPCServer) ListFunctionArgs(scope api.EvalScope, variables *[]api.Variable) error {
	vars, err := s.debugger.FunctionArguments(scope)
	if err != nil {
		return err
	}
	*variables = vars
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

func (s *RPCServer) ListGoroutines(arg interface{}, goroutines *[]*api.Goroutine) error {
	gs, err := s.debugger.Goroutines()
	if err != nil {
		return err
	}
	*goroutines = gs
	return nil
}

func (s *RPCServer) ListLocalVars(scope api.EvalScope, variables *[]api.Variable) error {
	vars, err := s.debugger.LocalVariables(scope)
	if err != nil {
		return err
	}
	*variables = vars
	return nil
}

func (s *RPCServer) ListPackageVars(filter string, variables *[]api.Variable) error {
	state, err := s.debugger.State()
	if err != nil {
		return err
	}

	current := state.CurrentThread
	if current == nil {
		return fmt.Errorf("no current thread")
	}

	vars, err := s.debugger.PackageVariables(current.ID, filter)
	if err != nil {
		return err
	}
	*variables = vars
	return nil
}

func (s *RPCServer) ListRegisters(arg interface{}, registers *string) error {
	state, err := s.debugger.State()
	if err != nil {
		return err
	}

	regs, err := s.debugger.Registers(state.CurrentThread.ID)
	if err != nil {
		return err
	}
	*registers = regs
	return nil
}

func (s *RPCServer) ListSources(filter string, sources *[]string) error {
	ss, err := s.debugger.Sources(filter)
	if err != nil {
		return err
	}
	*sources = ss
	return nil
}

func (s *RPCServer) ListThreadPackageVars(args *ThreadListArgs, variables *[]api.Variable) error {
	thread, err := s.debugger.FindThread(args.Id)
	if err != nil {
		return err
	}
	if thread == nil {
		return fmt.Errorf("no thread with id %d", args.Id)
	}

	vars, err := s.debugger.PackageVariables(args.Id, args.Filter)
	if err != nil {
		return err
	}
	*variables = vars
	return nil
}

func (s *RPCServer) ListThreads(arg interface{}, threads *[]*api.Thread) (err error) {
	*threads, err = s.debugger.Threads()
	return err
}

func (s *RPCServer) ListTypes(filter string, types *[]string) error {
	tps, err := s.debugger.Types(filter)
	if err != nil {
		return err
	}
	*types = tps
	return nil
}

func (s *RPCServer) ProcessPid(arg1 interface{}, pid *int) error {
	*pid = s.debugger.ProcessPid()
	return nil
}

func (s *RPCServer) Restart(arg1 interface{}, arg2 *int) error {
	if s.config.AttachPid != 0 {
		return errors.New("cannot restart process Delve did not create")
	}
	return s.debugger.Restart()
}

func (s *RPCServer) SetSymbol(args SetSymbolArgs, unused *int) error {
	*unused = 0
	return s.debugger.SetVariableInScope(args.Scope, args.Symbol, args.Value)
}

func (s *RPCServer) StacktraceGoroutine(args *StacktraceGoroutineArgs, locations *[]api.Stackframe) error {
	locs, err := s.debugger.Stacktrace(args.Id, args.Depth, args.Full)
	if err != nil {
		return err
	}
	*locations = locs
	return nil
}

func (s *RPCServer) State(arg interface{}, state *api.DebuggerState) error {
	st, err := s.debugger.State()
	if err != nil {
		return err
	}
	*state = *st
	return nil
}
