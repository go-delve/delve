package rpc2

import (
	"fmt"
	"log"
	"net/rpc"
	"net/rpc/jsonrpc"
	"time"

	"github.com/derekparker/delve/service"
	"github.com/derekparker/delve/service/api"
)

// Client is a RPC service.Client.
type RPCClient struct {
	addr   string
	client *rpc.Client

	retValLoadCfg *api.LoadConfig
}

// Ensure the implementation satisfies the interface.
var _ service.Client = &RPCClient{}

// NewClient creates a new RPCClient.
func NewClient(addr string) *RPCClient {
	client, err := jsonrpc.Dial("tcp", addr)
	if err != nil {
		log.Fatal("dialing:", err)
	}
	c := &RPCClient{addr: addr, client: client}
	c.call("SetApiVersion", api.SetAPIVersionIn{2}, &api.SetAPIVersionOut{})
	return c
}

func (c *RPCClient) ProcessPid() int {
	out := new(ProcessPidOut)
	c.call("ProcessPid", ProcessPidIn{}, out)
	return out.Pid
}

func (c *RPCClient) LastModified() time.Time {
	out := new(LastModifiedOut)
	c.call("LastModified", LastModifiedIn{}, out)
	return out.Time
}

func (c *RPCClient) Detach(kill bool) error {
	defer c.client.Close()
	out := new(DetachOut)
	return c.call("Detach", DetachIn{kill}, out)
}

func (c *RPCClient) Restart() ([]api.DiscardedBreakpoint, error) {
	out := new(RestartOut)
	err := c.call("Restart", RestartIn{"", false, nil}, out)
	return out.DiscardedBreakpoints, err
}

func (c *RPCClient) RestartFrom(pos string, resetArgs bool, newArgs []string) ([]api.DiscardedBreakpoint, error) {
	out := new(RestartOut)
	err := c.call("Restart", RestartIn{pos, resetArgs, newArgs}, out)
	return out.DiscardedBreakpoints, err
}

func (c *RPCClient) GetState() (*api.DebuggerState, error) {
	var out StateOut
	err := c.call("State", StateIn{}, &out)
	return out.State, err
}

func (c *RPCClient) GetStateNonBlocking() (*api.DebuggerState, error) {
	var out StateOut
	err := c.call("State", StateIn{NonBlocking: true}, &out)
	return out.State, err
}

func (c *RPCClient) Continue() <-chan *api.DebuggerState {
	return c.continueDir(api.Continue)
}

func (c *RPCClient) Rewind() <-chan *api.DebuggerState {
	return c.continueDir(api.Rewind)
}

func (c *RPCClient) continueDir(cmd string) <-chan *api.DebuggerState {
	ch := make(chan *api.DebuggerState)
	go func() {
		for {
			out := new(CommandOut)
			err := c.call("Command", &api.DebuggerCommand{Name: cmd, ReturnInfoLoadConfig: c.retValLoadCfg}, &out)
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

func (c *RPCClient) Next() (*api.DebuggerState, error) {
	var out CommandOut
	err := c.call("Command", api.DebuggerCommand{Name: api.Next, ReturnInfoLoadConfig: c.retValLoadCfg}, &out)
	return &out.State, err
}

func (c *RPCClient) Step() (*api.DebuggerState, error) {
	var out CommandOut
	err := c.call("Command", api.DebuggerCommand{Name: api.Step, ReturnInfoLoadConfig: c.retValLoadCfg}, &out)
	return &out.State, err
}

func (c *RPCClient) StepOut() (*api.DebuggerState, error) {
	var out CommandOut
	err := c.call("Command", &api.DebuggerCommand{Name: api.StepOut, ReturnInfoLoadConfig: c.retValLoadCfg}, &out)
	return &out.State, err
}

func (c *RPCClient) Call(expr string) (*api.DebuggerState, error) {
	var out CommandOut
	err := c.call("Command", &api.DebuggerCommand{Name: api.Call, ReturnInfoLoadConfig: c.retValLoadCfg, Expr: expr}, &out)
	return &out.State, err
}

func (c *RPCClient) StepInstruction() (*api.DebuggerState, error) {
	var out CommandOut
	err := c.call("Command", api.DebuggerCommand{Name: api.StepInstruction}, &out)
	return &out.State, err
}

func (c *RPCClient) SwitchThread(threadID int) (*api.DebuggerState, error) {
	var out CommandOut
	cmd := api.DebuggerCommand{
		Name:     api.SwitchThread,
		ThreadID: threadID,
	}
	err := c.call("Command", cmd, &out)
	return &out.State, err
}

func (c *RPCClient) SwitchGoroutine(goroutineID int) (*api.DebuggerState, error) {
	var out CommandOut
	cmd := api.DebuggerCommand{
		Name:        api.SwitchGoroutine,
		GoroutineID: goroutineID,
	}
	err := c.call("Command", cmd, &out)
	return &out.State, err
}

func (c *RPCClient) Halt() (*api.DebuggerState, error) {
	var out CommandOut
	err := c.call("Command", api.DebuggerCommand{Name: api.Halt}, &out)
	return &out.State, err
}

func (c *RPCClient) GetBreakpoint(id int) (*api.Breakpoint, error) {
	var out GetBreakpointOut
	err := c.call("GetBreakpoint", GetBreakpointIn{id, ""}, &out)
	return &out.Breakpoint, err
}

func (c *RPCClient) GetBreakpointByName(name string) (*api.Breakpoint, error) {
	var out GetBreakpointOut
	err := c.call("GetBreakpoint", GetBreakpointIn{0, name}, &out)
	return &out.Breakpoint, err
}

func (c *RPCClient) CreateBreakpoint(breakPoint *api.Breakpoint) (*api.Breakpoint, error) {
	var out CreateBreakpointOut
	err := c.call("CreateBreakpoint", CreateBreakpointIn{*breakPoint}, &out)
	return &out.Breakpoint, err
}

func (c *RPCClient) ListBreakpoints() ([]*api.Breakpoint, error) {
	var out ListBreakpointsOut
	err := c.call("ListBreakpoints", ListBreakpointsIn{}, &out)
	return out.Breakpoints, err
}

func (c *RPCClient) ClearBreakpoint(id int) (*api.Breakpoint, error) {
	var out ClearBreakpointOut
	err := c.call("ClearBreakpoint", ClearBreakpointIn{id, ""}, &out)
	return out.Breakpoint, err
}

func (c *RPCClient) ClearBreakpointByName(name string) (*api.Breakpoint, error) {
	var out ClearBreakpointOut
	err := c.call("ClearBreakpoint", ClearBreakpointIn{0, name}, &out)
	return out.Breakpoint, err
}

func (c *RPCClient) AmendBreakpoint(bp *api.Breakpoint) error {
	out := new(AmendBreakpointOut)
	err := c.call("AmendBreakpoint", AmendBreakpointIn{*bp}, out)
	return err
}

func (c *RPCClient) CancelNext() error {
	var out CancelNextOut
	return c.call("CancelNext", CancelNextIn{}, &out)
}

func (c *RPCClient) ListThreads() ([]*api.Thread, error) {
	var out ListThreadsOut
	err := c.call("ListThreads", ListThreadsIn{}, &out)
	return out.Threads, err
}

func (c *RPCClient) GetThread(id int) (*api.Thread, error) {
	var out GetThreadOut
	err := c.call("GetThread", GetThreadIn{id}, &out)
	return out.Thread, err
}

func (c *RPCClient) EvalVariable(scope api.EvalScope, expr string, cfg api.LoadConfig) (*api.Variable, error) {
	var out EvalOut
	err := c.call("Eval", EvalIn{scope, expr, &cfg}, &out)
	return out.Variable, err
}

func (c *RPCClient) SetVariable(scope api.EvalScope, symbol, value string) error {
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

func (c *RPCClient) ListPackageVariables(filter string, cfg api.LoadConfig) ([]api.Variable, error) {
	var out ListPackageVarsOut
	err := c.call("ListPackageVars", ListPackageVarsIn{filter, cfg}, &out)
	return out.Variables, err
}

func (c *RPCClient) ListLocalVariables(scope api.EvalScope, cfg api.LoadConfig) ([]api.Variable, error) {
	var out ListLocalVarsOut
	err := c.call("ListLocalVars", ListLocalVarsIn{scope, cfg}, &out)
	return out.Variables, err
}

func (c *RPCClient) ListRegisters(threadID int, includeFp bool) (api.Registers, error) {
	out := new(ListRegistersOut)
	err := c.call("ListRegisters", ListRegistersIn{ThreadID: threadID, IncludeFp: includeFp}, out)
	return out.Regs, err
}

func (c *RPCClient) ListFunctionArgs(scope api.EvalScope, cfg api.LoadConfig) ([]api.Variable, error) {
	var out ListFunctionArgsOut
	err := c.call("ListFunctionArgs", ListFunctionArgsIn{scope, cfg}, &out)
	return out.Args, err
}

func (c *RPCClient) ListGoroutines() ([]*api.Goroutine, error) {
	var out ListGoroutinesOut
	err := c.call("ListGoroutines", ListGoroutinesIn{}, &out)
	return out.Goroutines, err
}

func (c *RPCClient) Stacktrace(goroutineId, depth int, cfg *api.LoadConfig) ([]api.Stackframe, error) {
	var out StacktraceOut
	err := c.call("Stacktrace", StacktraceIn{goroutineId, depth, false, cfg}, &out)
	return out.Locations, err
}

func (c *RPCClient) AttachedToExistingProcess() bool {
	out := new(AttachedToExistingProcessOut)
	c.call("AttachedToExistingProcess", AttachedToExistingProcessIn{}, out)
	return out.Answer
}

func (c *RPCClient) FindLocation(scope api.EvalScope, loc string) ([]api.Location, error) {
	var out FindLocationOut
	err := c.call("FindLocation", FindLocationIn{scope, loc}, &out)
	return out.Locations, err
}

// Disassemble code between startPC and endPC
func (c *RPCClient) DisassembleRange(scope api.EvalScope, startPC, endPC uint64, flavour api.AssemblyFlavour) (api.AsmInstructions, error) {
	var out DisassembleOut
	err := c.call("Disassemble", DisassembleIn{scope, startPC, endPC, flavour}, &out)
	return out.Disassemble, err
}

// Disassemble function containing pc
func (c *RPCClient) DisassemblePC(scope api.EvalScope, pc uint64, flavour api.AssemblyFlavour) (api.AsmInstructions, error) {
	var out DisassembleOut
	err := c.call("Disassemble", DisassembleIn{scope, pc, 0, flavour}, &out)
	return out.Disassemble, err
}

// Recorded returns true if the debugger target is a recording.
func (c *RPCClient) Recorded() bool {
	out := new(RecordedOut)
	c.call("Recorded", RecordedIn{}, out)
	return out.Recorded
}

// TraceDirectory returns the path to the trace directory for a recording.
func (c *RPCClient) TraceDirectory() (string, error) {
	var out RecordedOut
	err := c.call("Recorded", RecordedIn{}, &out)
	return out.TraceDirectory, err
}

// Checkpoint sets a checkpoint at the current position.
func (c *RPCClient) Checkpoint(where string) (checkpointID int, err error) {
	var out CheckpointOut
	err = c.call("Checkpoint", CheckpointIn{where}, &out)
	return out.ID, err
}

// ListCheckpoints gets all checkpoints.
func (c *RPCClient) ListCheckpoints() ([]api.Checkpoint, error) {
	var out ListCheckpointsOut
	err := c.call("ListCheckpoints", ListCheckpointsIn{}, &out)
	return out.Checkpoints, err
}

// ClearCheckpoint removes a checkpoint
func (c *RPCClient) ClearCheckpoint(id int) error {
	var out ClearCheckpointOut
	err := c.call("ClearCheckpoint", ClearCheckpointIn{id}, &out)
	return err
}

func (c *RPCClient) SetReturnValuesLoadConfig(cfg *api.LoadConfig) {
	c.retValLoadCfg = cfg
}

func (c *RPCClient) IsMulticlient() bool {
	var out IsMulticlientOut
	c.call("IsMulticlient", IsMulticlientIn{}, &out)
	return out.IsMulticlient
}

func (c *RPCClient) Disconnect(cont bool) error {
	if cont {
		out := new(CommandOut)
		c.client.Go("RPCServer.Command", &api.DebuggerCommand{Name: api.Continue, ReturnInfoLoadConfig: c.retValLoadCfg}, &out, nil)
	}
	return c.client.Close()
}

func (c *RPCClient) call(method string, args, reply interface{}) error {
	return c.client.Call("RPCServer."+method, args, reply)
}
