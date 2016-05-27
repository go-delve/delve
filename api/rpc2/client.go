package rpc2

import (
	"fmt"
	"log"
	"net/rpc"
	"net/rpc/jsonrpc"

	"github.com/derekparker/delve/api"
	"github.com/derekparker/delve/api/types"
)

// Client is a RPC service.Client.
type RPCClient struct {
	addr       string
	processPid int
	client     *rpc.Client
}

// Ensure the implementation satisfies the interface.
var _ api.Client = &RPCClient{}

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
	out := new(ProcessPidOut)
	c.call("ProcessPid", ProcessPidIn{}, out)
	return out.Pid
}

func (c *RPCClient) Detach(kill bool) error {
	out := new(DetachOut)
	return c.call("Detach", DetachIn{kill}, out)
}

func (c *RPCClient) Restart() error {
	out := new(RestartOut)
	return c.call("Restart", RestartIn{}, out)
}

func (c *RPCClient) GetState() (*types.DebuggerState, error) {
	var out StateOut
	err := c.call("State", StateIn{}, &out)
	return out.State, err
}

func (c *RPCClient) Continue() <-chan *types.DebuggerState {
	ch := make(chan *types.DebuggerState)
	go func() {
		for {
			out := new(CommandOut)
			err := c.call("Command", &types.DebuggerCommand{Name: types.Continue}, &out)
			state := out.State
			if err != nil {
				state.Err = err
			}
			if state.Exited {
				// Error types apparently cannot be marshalled by Go correctly. Must reset error here.
				state.Err = fmt.Errorf("Process %d has exited with status %d", c.ProcessPid(), state.ExitStatus)
			}
			ch <- &state
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
	var out CommandOut
	err := c.call("Command", types.DebuggerCommand{Name: types.Next}, &out)
	return &out.State, err
}

func (c *RPCClient) Step() (*types.DebuggerState, error) {
	var out CommandOut
	err := c.call("Command", types.DebuggerCommand{Name: types.Step}, &out)
	return &out.State, err
}

func (c *RPCClient) StepInstruction() (*types.DebuggerState, error) {
	var out CommandOut
	err := c.call("Command", types.DebuggerCommand{Name: types.StepInstruction}, &out)
	return &out.State, err
}

func (c *RPCClient) SwitchThread(threadID int) (*types.DebuggerState, error) {
	var out CommandOut
	cmd := types.DebuggerCommand{
		Name:     types.SwitchThread,
		ThreadID: threadID,
	}
	err := c.call("Command", cmd, &out)
	return &out.State, err
}

func (c *RPCClient) SwitchGoroutine(goroutineID int) (*types.DebuggerState, error) {
	var out CommandOut
	cmd := types.DebuggerCommand{
		Name:        types.SwitchGoroutine,
		GoroutineID: goroutineID,
	}
	err := c.call("Command", cmd, &out)
	return &out.State, err
}

func (c *RPCClient) Halt() (*types.DebuggerState, error) {
	var out CommandOut
	err := c.call("Command", types.DebuggerCommand{Name: types.Halt}, &out)
	return &out.State, err
}

func (c *RPCClient) GetBreakpoint(id int) (*types.Breakpoint, error) {
	var out GetBreakpointOut
	err := c.call("GetBreakpoint", GetBreakpointIn{id, ""}, &out)
	return &out.Breakpoint, err
}

func (c *RPCClient) GetBreakpointByName(name string) (*types.Breakpoint, error) {
	var out GetBreakpointOut
	err := c.call("GetBreakpoint", GetBreakpointIn{0, name}, &out)
	return &out.Breakpoint, err
}

func (c *RPCClient) CreateBreakpoint(breakPoint *types.Breakpoint) (*types.Breakpoint, error) {
	var out CreateBreakpointOut
	err := c.call("CreateBreakpoint", CreateBreakpointIn{*breakPoint}, &out)
	return &out.Breakpoint, err
}

func (c *RPCClient) ListBreakpoints() ([]*types.Breakpoint, error) {
	var out ListBreakpointsOut
	err := c.call("ListBreakpoints", ListBreakpointsIn{}, &out)
	return out.Breakpoints, err
}

func (c *RPCClient) ClearBreakpoint(id int) (*types.Breakpoint, error) {
	var out ClearBreakpointOut
	err := c.call("ClearBreakpoint", ClearBreakpointIn{id, ""}, &out)
	return out.Breakpoint, err
}

func (c *RPCClient) ClearBreakpointByName(name string) (*types.Breakpoint, error) {
	var out ClearBreakpointOut
	err := c.call("ClearBreakpoint", ClearBreakpointIn{0, name}, &out)
	return out.Breakpoint, err
}

func (c *RPCClient) AmendBreakpoint(bp *types.Breakpoint) error {
	out := new(AmendBreakpointOut)
	err := c.call("AmendBreakpoint", AmendBreakpointIn{*bp}, out)
	return err
}

func (c *RPCClient) CancelNext() error {
	var out CancelNextOut
	return c.call("CancelNext", CancelNextIn{}, &out)
}

func (c *RPCClient) ListThreads() ([]*types.Thread, error) {
	var out ListThreadsOut
	err := c.call("ListThreads", ListThreadsIn{}, &out)
	return out.Threads, err
}

func (c *RPCClient) GetThread(id int) (*types.Thread, error) {
	var out GetThreadOut
	err := c.call("GetThread", GetThreadIn{id}, &out)
	return out.Thread, err
}

func (c *RPCClient) EvalVariable(scope types.EvalScope, expr string, cfg types.LoadConfig) (*types.Variable, error) {
	var out EvalOut
	err := c.call("Eval", EvalIn{scope, expr, &cfg}, &out)
	return out.Variable, err
}

func (c *RPCClient) SetVariable(scope types.EvalScope, symbol, value string) error {
	out := new(SetOut)
	return c.call("Set", SetIn{scope, symbol, value}, out)
}

func (c *RPCClient) ListSources(filter string) ([]string, error) {
	sources := new(ListSourcesOut)
	err := c.call("ListSources", ListSourcesIn{filter}, sources)
	return sources.Sources, err
}

func (c *RPCClient) ListFunctions(filter string) ([]string, error) {
	funcs := new(ListFunctionsOut)
	err := c.call("ListFunctions", ListFunctionsIn{filter}, funcs)
	return funcs.Funcs, err
}

func (c *RPCClient) ListTypes(filter string) ([]string, error) {
	types := new(ListTypesOut)
	err := c.call("ListTypes", ListTypesIn{filter}, types)
	return types.Types, err
}

func (c *RPCClient) ListPackageVariables(filter string, cfg types.LoadConfig) ([]types.Variable, error) {
	var out ListPackageVarsOut
	err := c.call("ListPackageVars", ListPackageVarsIn{filter, cfg}, &out)
	return out.Variables, err
}

func (c *RPCClient) ListLocalVariables(scope types.EvalScope, cfg types.LoadConfig) ([]types.Variable, error) {
	var out ListLocalVarsOut
	err := c.call("ListLocalVars", ListLocalVarsIn{scope, cfg}, &out)
	return out.Variables, err
}

func (c *RPCClient) ListRegisters() (string, error) {
	out := new(ListRegistersOut)
	err := c.call("ListRegisters", ListRegistersIn{}, out)
	return out.Registers, err
}

func (c *RPCClient) ListFunctionArgs(scope types.EvalScope, cfg types.LoadConfig) ([]types.Variable, error) {
	var out ListFunctionArgsOut
	err := c.call("ListFunctionArgs", ListFunctionArgsIn{scope, cfg}, &out)
	return out.Args, err
}

func (c *RPCClient) ListGoroutines() ([]*types.Goroutine, error) {
	var out ListGoroutinesOut
	err := c.call("ListGoroutines", ListGoroutinesIn{}, &out)
	return out.Goroutines, err
}

func (c *RPCClient) Stacktrace(goroutineId, depth int, cfg *types.LoadConfig) ([]types.Stackframe, error) {
	var out StacktraceOut
	err := c.call("Stacktrace", StacktraceIn{goroutineId, depth, false, cfg}, &out)
	return out.Locations, err
}

func (c *RPCClient) AttachedToExistingProcess() bool {
	out := new(AttachedToExistingProcessOut)
	c.call("AttachedToExistingProcess", AttachedToExistingProcessIn{}, out)
	return out.Answer
}

func (c *RPCClient) FindLocation(scope types.EvalScope, loc string) ([]types.Location, error) {
	var out FindLocationOut
	err := c.call("FindLocation", FindLocationIn{scope, loc}, &out)
	return out.Locations, err
}

// Disassemble code between startPC and endPC
func (c *RPCClient) DisassembleRange(scope types.EvalScope, startPC, endPC uint64, flavour types.AssemblyFlavour) (types.AsmInstructions, error) {
	var out DisassembleOut
	err := c.call("Disassemble", DisassembleIn{scope, startPC, endPC, flavour}, &out)
	return out.Disassemble, err
}

// Disassemble function containing pc
func (c *RPCClient) DisassemblePC(scope types.EvalScope, pc uint64, flavour types.AssemblyFlavour) (types.AsmInstructions, error) {
	var out DisassembleOut
	err := c.call("Disassemble", DisassembleIn{scope, pc, 0, flavour}, &out)
	return out.Disassemble, err
}

func (c *RPCClient) url(path string) string {
	return fmt.Sprintf("http://%s%s", c.addr, path)
}

func (c *RPCClient) call(method string, args, reply interface{}) error {
	return c.client.Call("RPCServer."+method, args, reply)
}
