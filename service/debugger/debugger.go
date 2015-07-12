package debugger

import (
	"errors"
	"fmt"
	"log"
	"regexp"

	"github.com/derekparker/delve/proc"
	"github.com/derekparker/delve/service/api"
	sys "golang.org/x/sys/unix"
)

// Debugger service.
//
// Debugger provides a higher level of
// abstraction over proc.Process.
// It handles converting from internal types to
// the types expected by clients. It also handles
// functionality needed by clients, but not needed in
// lower lever packages such as proc.
type Debugger struct {
	config  *Config
	process *proc.Process
}

// Config provides the configuration to start a Debugger.
//
// Only one of ProcessArgs or AttachPid should be specified. If ProcessArgs is
// provided, a new process will be launched. Otherwise, the debugger will try
// to attach to an existing process with AttachPid.
type Config struct {
	// ProcessArgs are the arguments to launch a new process.
	ProcessArgs []string
	// AttachPid is the PID of an existing process to which the debugger should
	// attach.
	AttachPid int
}

// New creates a new Debugger.
func New(config *Config) (*Debugger, error) {
	d := &Debugger{
		config: config,
	}

	// Create the process by either attaching or launching.
	if d.config.AttachPid > 0 {
		log.Printf("attaching to pid %d", d.config.AttachPid)
		p, err := proc.Attach(d.config.AttachPid)
		if err != nil {
			return nil, attachErrorMessage(d.config.AttachPid, err)
		}
		d.process = p
	} else {
		log.Printf("launching process with args: %v", d.config.ProcessArgs)
		p, err := proc.Launch(d.config.ProcessArgs)
		if err != nil {
			return nil, fmt.Errorf("could not launch process: %s", err)
		}
		d.process = p
	}
	return d, nil
}

func (d *Debugger) ProcessPid() int {
	return d.process.Pid
}

func (d *Debugger) Detach(kill bool) error {
	if d.config.AttachPid != 0 {
		return d.process.Detach(kill)
	} else {
		return d.process.Kill()
	}
}

func (d *Debugger) Restart() error {
	if !d.process.Exited() {
		if d.process.Running() {
			d.process.Halt()
		}
		// Ensure the process is in a PTRACE_STOP.
		if err := sys.Kill(d.ProcessPid(), sys.SIGSTOP); err != nil {
			return err
		}
		if err := d.Detach(true); err != nil {
			return err
		}
	}
	p, err := proc.Launch(d.config.ProcessArgs)
	if err != nil {
		return fmt.Errorf("could not launch process: %s", err)
	}
	d.process = p
	return nil
}

func (d *Debugger) State() (*api.DebuggerState, error) {
	var (
		state  *api.DebuggerState
		thread *api.Thread
	)
	th := d.process.CurrentThread
	if th != nil {
		thread = api.ConvertThread(th)
	}

	var breakpoint *api.Breakpoint
	bp := d.process.CurrentBreakpoint()
	if bp != nil {
		breakpoint = api.ConvertBreakpoint(bp)
	}

	state = &api.DebuggerState{
		Breakpoint:    breakpoint,
		CurrentThread: thread,
		Exited:        d.process.Exited(),
	}

	return state, nil
}

func (d *Debugger) CreateBreakpoint(requestedBp *api.Breakpoint) (*api.Breakpoint, error) {
	var createdBp *api.Breakpoint
	var loc string
	switch {
	case len(requestedBp.File) > 0:
		loc = fmt.Sprintf("%s:%d", requestedBp.File, requestedBp.Line)
	case len(requestedBp.FunctionName) > 0:
		loc = requestedBp.FunctionName
	default:
		return nil, fmt.Errorf("no file or function name specified")
	}

	bp, err := d.process.SetBreakpointByLocation(loc)
	if err != nil {
		return nil, err
	}
	bp.Tracepoint = requestedBp.Tracepoint
	bp.Goroutine = requestedBp.Goroutine
	bp.Stacktrace = requestedBp.Stacktrace
	bp.Variables = requestedBp.Variables
	createdBp = api.ConvertBreakpoint(bp)
	log.Printf("created breakpoint: %#v", createdBp)
	return createdBp, nil
}

func (d *Debugger) ClearBreakpoint(requestedBp *api.Breakpoint) (*api.Breakpoint, error) {
	var clearedBp *api.Breakpoint
	bp, err := d.process.ClearBreakpoint(requestedBp.Addr)
	if err != nil {
		return nil, fmt.Errorf("Can't clear breakpoint @%x: %s", requestedBp.Addr, err)
	}
	clearedBp = api.ConvertBreakpoint(bp)
	log.Printf("cleared breakpoint: %#v", clearedBp)
	return clearedBp, err
}

func (d *Debugger) Breakpoints() []*api.Breakpoint {
	bps := []*api.Breakpoint{}
	for _, bp := range d.process.Breakpoints {
		if bp.Temp {
			continue
		}
		bps = append(bps, api.ConvertBreakpoint(bp))
	}
	return bps
}

func (d *Debugger) FindBreakpoint(id int) *api.Breakpoint {
	for _, bp := range d.Breakpoints() {
		if bp.ID == id {
			return bp
		}
	}
	return nil
}

func (d *Debugger) Threads() []*api.Thread {
	threads := []*api.Thread{}
	for _, th := range d.process.Threads {
		threads = append(threads, api.ConvertThread(th))
	}
	return threads
}

func (d *Debugger) FindThread(id int) *api.Thread {
	for _, thread := range d.Threads() {
		if thread.ID == id {
			return thread
		}
	}
	return nil
}

// Command handles commands which control the debugger lifecycle
func (d *Debugger) Command(command *api.DebuggerCommand) (*api.DebuggerState, error) {
	var err error
	switch command.Name {
	case api.Continue:
		log.Print("continuing")
		err = d.process.Continue()
		state, stateErr := d.State()
		if stateErr != nil {
			return state, stateErr
		}
		if err != nil {
			if exitedErr, exited := err.(proc.ProcessExitedError); exited {
				state.Exited = true
				state.ExitStatus = exitedErr.Status
				state.Err = errors.New(exitedErr.Error())
				return state, nil
			}
			return nil, err
		}
		err = d.collectBreakpointInformation(state)
		return state, err

	case api.Next:
		log.Print("nexting")
		err = d.process.Next()
	case api.Step:
		log.Print("stepping")
		err = d.process.Step()
	case api.SwitchThread:
		log.Printf("switching to thread %d", command.ThreadID)
		err = d.process.SwitchThread(command.ThreadID)
	case api.Halt:
		// RequestManualStop does not invoke any ptrace syscalls, so it's safe to
		// access the process directly.
		log.Print("halting")
		err = d.process.RequestManualStop()
	}
	if err != nil {
		return nil, err
	}
	return d.State()
}

func (d *Debugger) collectBreakpointInformation(state *api.DebuggerState) error {
	if state == nil || state.Breakpoint == nil {
		return nil
	}

	bp := state.Breakpoint
	bpi := &api.BreakpointInfo{}
	state.BreakpointInfo = bpi

	if bp.Goroutine {
		g, err := d.process.CurrentThread.GetG()
		if err != nil {
			return err
		}
		bpi.Goroutine = api.ConvertGoroutine(g)
	}

	if bp.Stacktrace > 0 {
		rawlocs, err := d.process.CurrentThread.Stacktrace(bp.Stacktrace)
		if err != nil {
			return err
		}
		bpi.Stacktrace = convertStacktrace(rawlocs)
	}

	if len(bp.Variables) > 0 {
		bpi.Variables = make([]api.Variable, len(bp.Variables))
	}
	for i := range bp.Variables {
		v, err := d.process.CurrentThread.EvalVariable(bp.Variables[i])
		if err != nil {
			return err
		}
		bpi.Variables[i] = api.ConvertVar(v)
	}
	vars, err := d.FunctionArguments(d.process.CurrentThread.Id)
	if err == nil {
		bpi.Arguments = vars
	}

	return nil
}

func (d *Debugger) Sources(filter string) ([]string, error) {
	regex, err := regexp.Compile(filter)
	if err != nil {
		return nil, fmt.Errorf("invalid filter argument: %s", err.Error())
	}

	files := []string{}
	for f := range d.process.Sources() {
		if regex.Match([]byte(f)) {
			files = append(files, f)
		}
	}
	return files, nil
}

func (d *Debugger) Functions(filter string) ([]string, error) {
	regex, err := regexp.Compile(filter)
	if err != nil {
		return nil, fmt.Errorf("invalid filter argument: %s", err.Error())
	}

	funcs := []string{}
	for _, f := range d.process.Funcs() {
		if f.Sym != nil && regex.Match([]byte(f.Name)) {
			funcs = append(funcs, f.Name)
		}
	}
	return funcs, nil
}

func (d *Debugger) PackageVariables(threadID int, filter string) ([]api.Variable, error) {
	regex, err := regexp.Compile(filter)
	if err != nil {
		return nil, fmt.Errorf("invalid filter argument: %s", err.Error())
	}

	vars := []api.Variable{}
	thread, found := d.process.Threads[threadID]
	if !found {
		return nil, fmt.Errorf("couldn't find thread %d", threadID)
	}
	pv, err := thread.PackageVariables()
	if err != nil {
		return nil, err
	}
	for _, v := range pv {
		if regex.Match([]byte(v.Name)) {
			vars = append(vars, api.ConvertVar(v))
		}
	}
	return vars, err
}

func (d *Debugger) Registers(threadID int) (string, error) {
	thread, found := d.process.Threads[threadID]
	if !found {
		return "", fmt.Errorf("couldn't find thread %d", threadID)
	}
	regs, err := thread.Registers()
	if err != nil {
		return "", err
	}
	return regs.String(), err
}

func (d *Debugger) LocalVariables(threadID int) ([]api.Variable, error) {
	vars := []api.Variable{}
	thread, found := d.process.Threads[threadID]
	if !found {
		return nil, fmt.Errorf("couldn't find thread %d", threadID)
	}
	pv, err := thread.LocalVariables()
	if err != nil {
		return nil, err
	}
	for _, v := range pv {
		vars = append(vars, api.ConvertVar(v))
	}
	return vars, err
}

func (d *Debugger) FunctionArguments(threadID int) ([]api.Variable, error) {
	vars := []api.Variable{}
	thread, found := d.process.Threads[threadID]
	if !found {
		return nil, fmt.Errorf("couldn't find thread %d", threadID)
	}
	pv, err := thread.FunctionArguments()
	if err != nil {
		return nil, err
	}
	for _, v := range pv {
		vars = append(vars, api.ConvertVar(v))
	}
	return vars, nil
}

func (d *Debugger) EvalVariableInThread(threadID int, symbol string) (*api.Variable, error) {
	thread, found := d.process.Threads[threadID]
	if !found {
		return nil, fmt.Errorf("couldn't find thread %d", threadID)
	}
	v, err := thread.EvalVariable(symbol)
	if err != nil {
		return nil, err
	}
	converted := api.ConvertVar(v)
	return &converted, err
}

func (d *Debugger) Goroutines() ([]*api.Goroutine, error) {
	goroutines := []*api.Goroutine{}
	gs, err := d.process.GoroutinesInfo()
	if err != nil {
		return nil, err
	}
	for _, g := range gs {
		goroutines = append(goroutines, api.ConvertGoroutine(g))
	}
	return goroutines, err
}

func (d *Debugger) Stacktrace(goroutineId, depth int) ([]api.Location, error) {
	var rawlocs []proc.Location
	var err error

	if goroutineId < 0 {
		rawlocs, err = d.process.CurrentThread.Stacktrace(depth)
		if err != nil {
			return nil, err
		}
	} else {
		gs, err := d.process.GoroutinesInfo()
		if err != nil {
			return nil, err
		}
		for _, g := range gs {
			if g.Id == goroutineId {
				rawlocs, err = d.process.GoroutineStacktrace(g, depth)
				if err != nil {
					return nil, err
				}
				break
			}
		}

		if rawlocs == nil {
			return nil, fmt.Errorf("Unknown goroutine id %d\n", goroutineId)
		}
	}

	return convertStacktrace(rawlocs), nil
}

func convertStacktrace(rawlocs []proc.Location) []api.Location {
	locations := make([]api.Location, 0, len(rawlocs))
	for i := range rawlocs {
		rawlocs[i].Line--
		locations = append(locations, api.ConvertLocation(rawlocs[i]))
	}

	return locations
}
