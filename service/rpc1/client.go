package rpc1

import (
	"errors"
	"fmt"
	"log"
	"net/rpc"
	"net/rpc/jsonrpc"

	"sync"

	"github.com/derekparker/delve/service/api"
)

// Client is a RPC service.Client.
type RPCClient struct {
	addr    string
	client  *rpc.Client
	haltMu  sync.Mutex
	haltReq bool
}

var unsupportedApiError = errors.New("unsupported")

// NewClient creates a new RPCClient.
func NewClient(addr string) *RPCClient {
	client, err := jsonrpc.Dial("tcp", addr)
	if err != nil {
		log.Fatal("dialing:", err)
	}
	return &RPCClient{
		addr:   addr,
		client: client,
	}
}

func (c *RPCClient) ProcessPid() int {
	var pid int
	c.call("ProcessPid", nil, &pid)
	return pid
}

func (c *RPCClient) Detach(kill bool) error {
	return c.call("Detach", kill, nil)
}

func (c *RPCClient) Restart() error {
	return c.call("Restart", nil, nil)
}

func (c *RPCClient) GetState() (*api.DebuggerState, error) {
	state := new(api.DebuggerState)
	err := c.call("State", nil, state)
	return state, err
}

func (c *RPCClient) Continue() <-chan *api.DebuggerState {
	ch := make(chan *api.DebuggerState)
	c.haltMu.Lock()
	c.haltReq = false
	c.haltMu.Unlock()
	go func() {
		for {
			c.haltMu.Lock()
			if c.haltReq {
				c.haltMu.Unlock()
				close(ch)
				return
			}
			c.haltMu.Unlock()
			state := new(api.DebuggerState)
			err := c.call("Command", &api.DebuggerCommand{Name: api.Continue}, state)
			if err != nil {
				state.Err = err
			}
			if state.Exited {
				// Error types apparently cannot be marshalled by Go correctly. Must reset error here.
				state.Err = fmt.Errorf("Process %d has exited with status %d", c.ProcessPid(), state.ExitStatus)
			}
			ch <- state
			if err != nil || state.Exited {
				close(ch)
				return
			}

			isbreakpoint := false
			istracepoint := true
			for i := range state.Threads {
				if state.Threads[i].Breakpoint != nil {
					isbreakpoint = true
					istracepoint = istracepoint && state.Threads[i].Breakpoint.Tracepoint
				}
			}

			if !isbreakpoint || !istracepoint {
				close(ch)
				return
			}
		}
	}()
	return ch
}

func (c *RPCClient) Next() (*api.DebuggerState, error) {
	state := new(api.DebuggerState)
	err := c.call("Command", &api.DebuggerCommand{Name: api.Next}, state)
	return state, err
}

func (c *RPCClient) Step() (*api.DebuggerState, error) {
	state := new(api.DebuggerState)
	err := c.call("Command", &api.DebuggerCommand{Name: api.Step}, state)
	return state, err
}

func (c *RPCClient) StepInstruction() (*api.DebuggerState, error) {
	state := new(api.DebuggerState)
	err := c.call("Command", &api.DebuggerCommand{Name: api.StepInstruction}, state)
	return state, err
}

func (c *RPCClient) SwitchThread(threadID int) (*api.DebuggerState, error) {
	state := new(api.DebuggerState)
	cmd := &api.DebuggerCommand{
		Name:     api.SwitchThread,
		ThreadID: threadID,
	}
	err := c.call("Command", cmd, state)
	return state, err
}

func (c *RPCClient) SwitchGoroutine(goroutineID int) (*api.DebuggerState, error) {
	state := new(api.DebuggerState)
	cmd := &api.DebuggerCommand{
		Name:        api.SwitchGoroutine,
		GoroutineID: goroutineID,
	}
	err := c.call("Command", cmd, state)
	return state, err
}

func (c *RPCClient) Halt() (*api.DebuggerState, error) {
	state := new(api.DebuggerState)
	c.haltMu.Lock()
	c.haltReq = true
	c.haltMu.Unlock()
	err := c.call("Command", &api.DebuggerCommand{Name: api.Halt}, state)
	return state, err
}

func (c *RPCClient) GetBreakpoint(id int) (*api.Breakpoint, error) {
	breakpoint := new(api.Breakpoint)
	err := c.call("GetBreakpoint", id, breakpoint)
	return breakpoint, err
}

func (c *RPCClient) GetBreakpointByName(name string) (*api.Breakpoint, error) {
	breakpoint := new(api.Breakpoint)
	err := c.call("GetBreakpointByName", name, breakpoint)
	return breakpoint, err
}

func (c *RPCClient) CreateBreakpoint(breakPoint *api.Breakpoint) (*api.Breakpoint, error) {
	newBreakpoint := new(api.Breakpoint)
	err := c.call("CreateBreakpoint", breakPoint, &newBreakpoint)
	return newBreakpoint, err
}

func (c *RPCClient) ListBreakpoints() ([]*api.Breakpoint, error) {
	var breakpoints []*api.Breakpoint
	err := c.call("ListBreakpoints", nil, &breakpoints)
	return breakpoints, err
}

func (c *RPCClient) ClearBreakpoint(id int) (*api.Breakpoint, error) {
	bp := new(api.Breakpoint)
	err := c.call("ClearBreakpoint", id, bp)
	return bp, err
}

func (c *RPCClient) ClearBreakpointByName(name string) (*api.Breakpoint, error) {
	bp := new(api.Breakpoint)
	err := c.call("ClearBreakpointByName", name, bp)
	return bp, err
}

func (c *RPCClient) AmendBreakpoint(bp *api.Breakpoint) error {
	err := c.call("AmendBreakpoint", bp, nil)
	return err
}

func (c *RPCClient) CancelNext() error {
	return unsupportedApiError
}

func (c *RPCClient) ListThreads() ([]*api.Thread, error) {
	var threads []*api.Thread
	err := c.call("ListThreads", nil, &threads)
	return threads, err
}

func (c *RPCClient) GetThread(id int) (*api.Thread, error) {
	thread := new(api.Thread)
	err := c.call("GetThread", id, &thread)
	return thread, err
}

func (c *RPCClient) EvalVariable(scope api.EvalScope, symbol string) (*api.Variable, error) {
	v := new(api.Variable)
	err := c.call("EvalSymbol", EvalSymbolArgs{scope, symbol}, v)
	return v, err
}

func (c *RPCClient) SetVariable(scope api.EvalScope, symbol, value string) error {
	var unused int
	return c.call("SetSymbol", SetSymbolArgs{scope, symbol, value}, &unused)
}

func (c *RPCClient) ListSources(filter string) ([]string, error) {
	var sources []string
	err := c.call("ListSources", filter, &sources)
	return sources, err
}

func (c *RPCClient) ListFunctions(filter string) ([]string, error) {
	var funcs []string
	err := c.call("ListFunctions", filter, &funcs)
	return funcs, err
}

func (c *RPCClient) ListTypes(filter string) ([]string, error) {
	var types []string
	err := c.call("ListTypes", filter, &types)
	return types, err
}

func (c *RPCClient) ListPackageVariables(filter string) ([]api.Variable, error) {
	var vars []api.Variable
	err := c.call("ListPackageVars", filter, &vars)
	return vars, err
}

func (c *RPCClient) ListPackageVariablesFor(threadID int, filter string) ([]api.Variable, error) {
	var vars []api.Variable
	err := c.call("ListThreadPackageVars", &ThreadListArgs{Id: threadID, Filter: filter}, &vars)
	return vars, err
}

func (c *RPCClient) ListLocalVariables(scope api.EvalScope) ([]api.Variable, error) {
	var vars []api.Variable
	err := c.call("ListLocalVars", scope, &vars)
	return vars, err
}

func (c *RPCClient) ListRegisters() (string, error) {
	var regs string
	err := c.call("ListRegisters", nil, &regs)
	return regs, err
}

func (c *RPCClient) ListFunctionArgs(scope api.EvalScope) ([]api.Variable, error) {
	var vars []api.Variable
	err := c.call("ListFunctionArgs", scope, &vars)
	return vars, err
}

func (c *RPCClient) ListGoroutines() ([]*api.Goroutine, error) {
	var goroutines []*api.Goroutine
	err := c.call("ListGoroutines", nil, &goroutines)
	return goroutines, err
}

func (c *RPCClient) Stacktrace(goroutineId, depth int, full bool) ([]api.Stackframe, error) {
	var locations []api.Stackframe
	err := c.call("StacktraceGoroutine", &StacktraceGoroutineArgs{Id: goroutineId, Depth: depth, Full: full}, &locations)
	return locations, err
}

func (c *RPCClient) AttachedToExistingProcess() bool {
	var answer bool
	c.call("AttachedToExistingProcess", nil, &answer)
	return answer
}

func (c *RPCClient) FindLocation(scope api.EvalScope, loc string) ([]api.Location, error) {
	var answer []api.Location
	err := c.call("FindLocation", FindLocationArgs{scope, loc}, &answer)
	return answer, err
}

// Disassemble code between startPC and endPC
func (c *RPCClient) DisassembleRange(scope api.EvalScope, startPC, endPC uint64, flavour api.AssemblyFlavour) (api.AsmInstructions, error) {
	var r api.AsmInstructions
	err := c.call("Disassemble", DisassembleRequest{scope, startPC, endPC, flavour}, &r)
	return r, err
}

// Disassemble function containing pc
func (c *RPCClient) DisassemblePC(scope api.EvalScope, pc uint64, flavour api.AssemblyFlavour) (api.AsmInstructions, error) {
	var r api.AsmInstructions
	err := c.call("Disassemble", DisassembleRequest{scope, pc, 0, flavour}, &r)
	return r, err
}

func (c *RPCClient) call(method string, args, reply interface{}) error {
	return c.client.Call("RPCServer."+method, args, reply)
}
