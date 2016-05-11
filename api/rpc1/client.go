package rpc1

import (
	"errors"
	"fmt"
	"log"
	"net/rpc"
	"net/rpc/jsonrpc"

	"github.com/derekparker/delve/api/types"

	"sync"
)

// Client is a RPC service.Client.
type RPCClient struct {
	addr       string
	processPid int
	client     *rpc.Client
	haltMu     sync.Mutex
	haltReq    bool
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

func (c *RPCClient) GetState() (*types.DebuggerState, error) {
	state := new(types.DebuggerState)
	err := c.call("State", nil, state)
	return state, err
}

func (c *RPCClient) Continue() <-chan *types.DebuggerState {
	ch := make(chan *types.DebuggerState)
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
			state := new(types.DebuggerState)
			err := c.call("Command", &types.DebuggerCommand{Name: types.Continue}, state)
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

func (c *RPCClient) Next() (*types.DebuggerState, error) {
	state := new(types.DebuggerState)
	err := c.call("Command", &types.DebuggerCommand{Name: types.Next}, state)
	return state, err
}

func (c *RPCClient) Step() (*types.DebuggerState, error) {
	state := new(types.DebuggerState)
	err := c.call("Command", &types.DebuggerCommand{Name: types.Step}, state)
	return state, err
}

func (c *RPCClient) StepInstruction() (*types.DebuggerState, error) {
	state := new(types.DebuggerState)
	err := c.call("Command", &types.DebuggerCommand{Name: types.StepInstruction}, state)
	return state, err
}

func (c *RPCClient) SwitchThread(threadID int) (*types.DebuggerState, error) {
	state := new(types.DebuggerState)
	cmd := &types.DebuggerCommand{
		Name:     types.SwitchThread,
		ThreadID: threadID,
	}
	err := c.call("Command", cmd, state)
	return state, err
}

func (c *RPCClient) SwitchGoroutine(goroutineID int) (*types.DebuggerState, error) {
	state := new(types.DebuggerState)
	cmd := &types.DebuggerCommand{
		Name:        types.SwitchGoroutine,
		GoroutineID: goroutineID,
	}
	err := c.call("Command", cmd, state)
	return state, err
}

func (c *RPCClient) Halt() (*types.DebuggerState, error) {
	state := new(types.DebuggerState)
	c.haltMu.Lock()
	c.haltReq = true
	c.haltMu.Unlock()
	err := c.call("Command", &types.DebuggerCommand{Name: types.Halt}, state)
	return state, err
}

func (c *RPCClient) GetBreakpoint(id int) (*types.Breakpoint, error) {
	breakpoint := new(types.Breakpoint)
	err := c.call("GetBreakpoint", id, breakpoint)
	return breakpoint, err
}

func (c *RPCClient) GetBreakpointByName(name string) (*types.Breakpoint, error) {
	breakpoint := new(types.Breakpoint)
	err := c.call("GetBreakpointByName", name, breakpoint)
	return breakpoint, err
}

func (c *RPCClient) CreateBreakpoint(breakPoint *types.Breakpoint) (*types.Breakpoint, error) {
	newBreakpoint := new(types.Breakpoint)
	err := c.call("CreateBreakpoint", breakPoint, &newBreakpoint)
	return newBreakpoint, err
}

func (c *RPCClient) ListBreakpoints() ([]*types.Breakpoint, error) {
	var breakpoints []*types.Breakpoint
	err := c.call("ListBreakpoints", nil, &breakpoints)
	return breakpoints, err
}

func (c *RPCClient) ClearBreakpoint(id int) (*types.Breakpoint, error) {
	bp := new(types.Breakpoint)
	err := c.call("ClearBreakpoint", id, bp)
	return bp, err
}

func (c *RPCClient) ClearBreakpointByName(name string) (*types.Breakpoint, error) {
	bp := new(types.Breakpoint)
	err := c.call("ClearBreakpointByName", name, bp)
	return bp, err
}

func (c *RPCClient) AmendBreakpoint(bp *types.Breakpoint) error {
	err := c.call("AmendBreakpoint", bp, nil)
	return err
}

func (c *RPCClient) CancelNext() error {
	return unsupportedApiError
}

func (c *RPCClient) ListThreads() ([]*types.Thread, error) {
	var threads []*types.Thread
	err := c.call("ListThreads", nil, &threads)
	return threads, err
}

func (c *RPCClient) GetThread(id int) (*types.Thread, error) {
	thread := new(types.Thread)
	err := c.call("GetThread", id, &thread)
	return thread, err
}

func (c *RPCClient) EvalVariable(scope types.EvalScope, symbol string) (*types.Variable, error) {
	v := new(types.Variable)
	err := c.call("EvalSymbol", EvalSymbolArgs{scope, symbol}, v)
	return v, err
}

func (c *RPCClient) SetVariable(scope types.EvalScope, symbol, value string) error {
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

func (c *RPCClient) ListPackageVariables(filter string) ([]types.Variable, error) {
	var vars []types.Variable
	err := c.call("ListPackageVars", filter, &vars)
	return vars, err
}

func (c *RPCClient) ListPackageVariablesFor(threadID int, filter string) ([]types.Variable, error) {
	var vars []types.Variable
	err := c.call("ListThreadPackageVars", &ThreadListArgs{Id: threadID, Filter: filter}, &vars)
	return vars, err
}

func (c *RPCClient) ListLocalVariables(scope types.EvalScope) ([]types.Variable, error) {
	var vars []types.Variable
	err := c.call("ListLocalVars", scope, &vars)
	return vars, err
}

func (c *RPCClient) ListRegisters() (string, error) {
	var regs string
	err := c.call("ListRegisters", nil, &regs)
	return regs, err
}

func (c *RPCClient) ListFunctionArgs(scope types.EvalScope) ([]types.Variable, error) {
	var vars []types.Variable
	err := c.call("ListFunctionArgs", scope, &vars)
	return vars, err
}

func (c *RPCClient) ListGoroutines() ([]*types.Goroutine, error) {
	var goroutines []*types.Goroutine
	err := c.call("ListGoroutines", nil, &goroutines)
	return goroutines, err
}

func (c *RPCClient) Stacktrace(goroutineId, depth int, full bool) ([]types.Stackframe, error) {
	var locations []types.Stackframe
	err := c.call("StacktraceGoroutine", &StacktraceGoroutineArgs{Id: goroutineId, Depth: depth, Full: full}, &locations)
	return locations, err
}

func (c *RPCClient) AttachedToExistingProcess() bool {
	var answer bool
	c.call("AttachedToExistingProcess", nil, &answer)
	return answer
}

func (c *RPCClient) FindLocation(scope types.EvalScope, loc string) ([]types.Location, error) {
	var answer []types.Location
	err := c.call("FindLocation", FindLocationArgs{scope, loc}, &answer)
	return answer, err
}

// Disassemble code between startPC and endPC
func (c *RPCClient) DisassembleRange(scope types.EvalScope, startPC, endPC uint64, flavour types.AssemblyFlavour) (types.AsmInstructions, error) {
	var r types.AsmInstructions
	err := c.call("Disassemble", DisassembleRequest{scope, startPC, endPC, flavour}, &r)
	return r, err
}

// Disassemble function containing pc
func (c *RPCClient) DisassemblePC(scope types.EvalScope, pc uint64, flavour types.AssemblyFlavour) (types.AsmInstructions, error) {
	var r types.AsmInstructions
	err := c.call("Disassemble", DisassembleRequest{scope, pc, 0, flavour}, &r)
	return r, err
}

func (c *RPCClient) url(path string) string {
	return fmt.Sprintf("http://%s%s", c.addr, path)
}

func (c *RPCClient) call(method string, args, reply interface{}) error {
	return c.client.Call("RPCServer."+method, args, reply)
}
