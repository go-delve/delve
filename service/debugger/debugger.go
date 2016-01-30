package debugger

import (
	"debug/gosym"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/derekparker/delve/proc"
	"github.com/derekparker/delve/service/api"
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

// ProcessPid returns the PID of the process
// the debugger is debugging.
func (d *Debugger) ProcessPid() int {
	return d.process.Pid
}

// Detach detaches from the target process.
// If `kill` is true we will kill the process after
// detaching.
func (d *Debugger) Detach(kill bool) error {
	if d.config.AttachPid != 0 {
		return d.process.Detach(kill)
	}
	return d.process.Kill()
}

// Restart will restart the target process, first killing
// and then exec'ing it again.
func (d *Debugger) Restart() error {
	if !d.process.Exited() {
		if d.process.Running() {
			d.process.Halt()
		}
		// Ensure the process is in a PTRACE_STOP.
		if err := stopProcess(d.ProcessPid()); err != nil {
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
	for addr, bp := range d.process.Breakpoints {
		if bp.Temp {
			continue
		}
		if _, err := p.SetBreakpoint(addr); err != nil {
			return err
		}
	}
	d.process = p
	return nil
}

// State returns the current state of the debugger.
func (d *Debugger) State() (*api.DebuggerState, error) {
	if d.process.Exited() {
		return nil, proc.ProcessExitedError{Pid: d.ProcessPid()}
	}

	var (
		state     *api.DebuggerState
		goroutine *api.Goroutine
	)

	if d.process.SelectedGoroutine != nil {
		goroutine = api.ConvertGoroutine(d.process.SelectedGoroutine)
	}

	state = &api.DebuggerState{
		SelectedGoroutine: goroutine,
		Exited:            d.process.Exited(),
	}

	for i := range d.process.Threads {
		th := api.ConvertThread(d.process.Threads[i])
		state.Threads = append(state.Threads, th)
		if i == d.process.CurrentThread.ID {
			state.CurrentThread = th
		}
	}

	return state, nil
}

// CreateBreakpoint creates a breakpoint.
func (d *Debugger) CreateBreakpoint(requestedBp *api.Breakpoint) (*api.Breakpoint, error) {
	var (
		createdBp *api.Breakpoint
		addr      uint64
		err       error
	)
	switch {
	case len(requestedBp.File) > 0:
		fileName := requestedBp.File
		if runtime.GOOS == "windows" {
			// Accept fileName which is case-insensitive and slash-insensitive match
			fileNameNormalized := strings.ToLower(filepath.ToSlash(fileName))
			for symFile := range d.process.Sources() {
				if fileNameNormalized == strings.ToLower(filepath.ToSlash(symFile)) {
					fileName = symFile
					break
				}
			}
		}
		addr, err = d.process.FindFileLocation(fileName, requestedBp.Line)
	case len(requestedBp.FunctionName) > 0:
		if requestedBp.Line >= 0 {
			addr, err = d.process.FindFunctionLocation(requestedBp.FunctionName, false, requestedBp.Line)
		} else {
			addr, err = d.process.FindFunctionLocation(requestedBp.FunctionName, true, 0)
		}
	default:
		addr = requestedBp.Addr
	}

	if err != nil {
		return nil, err
	}

	bp, err := d.process.SetBreakpoint(addr)
	if err != nil {
		return nil, err
	}
	bp.Tracepoint = requestedBp.Tracepoint
	bp.Goroutine = requestedBp.Goroutine
	bp.Stacktrace = requestedBp.Stacktrace
	bp.Variables = requestedBp.Variables
	bp.Cond = nil
	createdBp = api.ConvertBreakpoint(bp)
	log.Printf("created breakpoint: %#v", createdBp)
	return createdBp, nil
}

// ClearBreakpoint clears a breakpoint.
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

// Breakpoints returns the list of current breakpoints.
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

// FindBreakpoint returns the breakpoint specified by 'id'.
func (d *Debugger) FindBreakpoint(id int) *api.Breakpoint {
	for _, bp := range d.Breakpoints() {
		if bp.ID == id {
			return bp
		}
	}
	return nil
}

// Threads returns the threads of the target process.
func (d *Debugger) Threads() []*api.Thread {
	threads := []*api.Thread{}
	for _, th := range d.process.Threads {
		threads = append(threads, api.ConvertThread(th))
	}
	return threads
}

// FindThread returns the thread for the given 'id'.
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
		if err != nil {
			if exitedErr, exited := err.(proc.ProcessExitedError); exited {
				state := &api.DebuggerState{}
				state.Exited = true
				state.ExitStatus = exitedErr.Status
				state.Err = errors.New(exitedErr.Error())
				return state, nil
			}
			return nil, err
		}
		state, stateErr := d.State()
		if stateErr != nil {
			return state, stateErr
		}
		err = d.collectBreakpointInformation(state)
		return state, err

	case api.Next:
		log.Print("nexting")
		err = d.process.Next()
	case api.Step:
		log.Print("stepping")
		err = d.process.Step()
	case api.StepInstruction:
		log.Print("single stepping")
		err = d.process.StepInstruction()
	case api.SwitchThread:
		log.Printf("switching to thread %d", command.ThreadID)
		err = d.process.SwitchThread(command.ThreadID)
	case api.SwitchGoroutine:
		log.Printf("switching to goroutine %d", command.GoroutineID)
		err = d.process.SwitchGoroutine(command.GoroutineID)
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
	if state == nil {
		return nil
	}

	for i := range state.Threads {
		if state.Threads[i].Breakpoint == nil {
			continue
		}

		bp := state.Threads[i].Breakpoint
		bpi := &api.BreakpointInfo{}
		state.Threads[i].BreakpointInfo = bpi

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
			bpi.Stacktrace, err = d.convertStacktrace(rawlocs, false)
			if err != nil {
				return err
			}
		}

		s, err := d.process.CurrentThread.Scope()
		if err != nil {
			return err
		}

		if len(bp.Variables) > 0 {
			bpi.Variables = make([]api.Variable, len(bp.Variables))
		}
		for i := range bp.Variables {
			v, err := s.EvalVariable(bp.Variables[i])
			if err != nil {
				return err
			}
			bpi.Variables[i] = *api.ConvertVar(v)
		}
		vars, err := s.FunctionArguments()
		if err == nil {
			bpi.Arguments = convertVars(vars)
		}
	}

	return nil
}

// Sources returns a list of the source files for target binary.
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

// Functions returns a list of functions in the target process.
func (d *Debugger) Functions(filter string) ([]string, error) {
	return regexFilterFuncs(filter, d.process.Funcs())
}

func regexFilterFuncs(filter string, allFuncs []gosym.Func) ([]string, error) {
	regex, err := regexp.Compile(filter)
	if err != nil {
		return nil, fmt.Errorf("invalid filter argument: %s", err.Error())
	}

	funcs := []string{}
	for _, f := range allFuncs {
		if f.Sym != nil && regex.Match([]byte(f.Name)) {
			funcs = append(funcs, f.Name)
		}
	}
	return funcs, nil
}

// PackageVariables returns a list of package variables for the thread,
// optionally regexp filtered using regexp described in 'filter'.
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
	scope, err := thread.Scope()
	if err != nil {
		return nil, err
	}
	pv, err := scope.PackageVariables()
	if err != nil {
		return nil, err
	}
	for _, v := range pv {
		if regex.Match([]byte(v.Name)) {
			vars = append(vars, *api.ConvertVar(v))
		}
	}
	return vars, err
}

// Registers returns string representation of the CPU registers.
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

func convertVars(pv []*proc.Variable) []api.Variable {
	vars := make([]api.Variable, 0, len(pv))
	for _, v := range pv {
		vars = append(vars, *api.ConvertVar(v))
	}
	return vars
}

// LocalVariables returns a list of the local variables.
func (d *Debugger) LocalVariables(scope api.EvalScope) ([]api.Variable, error) {
	s, err := d.process.ConvertEvalScope(scope.GoroutineID, scope.Frame)
	if err != nil {
		return nil, err
	}
	pv, err := s.LocalVariables()
	if err != nil {
		return nil, err
	}
	return convertVars(pv), err
}

// FunctionArguments returns the arguments to the current function.
func (d *Debugger) FunctionArguments(scope api.EvalScope) ([]api.Variable, error) {
	s, err := d.process.ConvertEvalScope(scope.GoroutineID, scope.Frame)
	if err != nil {
		return nil, err
	}
	pv, err := s.FunctionArguments()
	if err != nil {
		return nil, err
	}
	return convertVars(pv), nil
}

// EvalVariableInScope will attempt to evaluate the variable represented by 'symbol'
// in the scope provided.
func (d *Debugger) EvalVariableInScope(scope api.EvalScope, symbol string) (*api.Variable, error) {
	s, err := d.process.ConvertEvalScope(scope.GoroutineID, scope.Frame)
	if err != nil {
		return nil, err
	}
	v, err := s.EvalVariable(symbol)
	if err != nil {
		return nil, err
	}
	return api.ConvertVar(v), err
}

// SetVariableInScope will set the value of the variable represented by
// 'symbol' to the value given, in the given scope.
func (d *Debugger) SetVariableInScope(scope api.EvalScope, symbol, value string) error {
	s, err := d.process.ConvertEvalScope(scope.GoroutineID, scope.Frame)
	if err != nil {
		return err
	}
	return s.SetVariable(symbol, value)
}

// Goroutines will return a list of goroutines in the target process.
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

// Stacktrace returns a list of Stackframes for the given goroutine. The
// length of the returned list will be min(stack_len, depth).
// If 'full' is true, then local vars, function args, etc will be returned as well.
func (d *Debugger) Stacktrace(goroutineID, depth int, full bool) ([]api.Stackframe, error) {
	var rawlocs []proc.Stackframe

	g, err := d.process.FindGoroutine(goroutineID)
	if err != nil {
		return nil, err
	}

	if g == nil {
		rawlocs, err = d.process.CurrentThread.Stacktrace(depth)
	} else {
		rawlocs, err = d.process.GoroutineStacktrace(g, depth)
	}
	if err != nil {
		return nil, err
	}

	return d.convertStacktrace(rawlocs, full)
}

func (d *Debugger) convertStacktrace(rawlocs []proc.Stackframe, full bool) ([]api.Stackframe, error) {
	locations := make([]api.Stackframe, 0, len(rawlocs))
	for i := range rawlocs {
		frame := api.Stackframe{Location: api.ConvertLocation(rawlocs[i].Call)}
		if full {
			var err error
			scope := rawlocs[i].Scope(d.process.CurrentThread)
			locals, err := scope.LocalVariables()
			if err != nil {
				return nil, err
			}
			arguments, err := scope.FunctionArguments()
			if err != nil {
				return nil, err
			}

			frame.Locals = convertVars(locals)
			frame.Arguments = convertVars(arguments)
		}
		locations = append(locations, frame)
	}

	return locations, nil
}

// FindLocation will find the location specified by 'locStr'.
func (d *Debugger) FindLocation(scope api.EvalScope, locStr string) ([]api.Location, error) {
	loc, err := parseLocationSpec(locStr)
	if err != nil {
		return nil, err
	}

	s, _ := d.process.ConvertEvalScope(scope.GoroutineID, scope.Frame)

	locs, err := loc.Find(d, s, locStr)
	for i := range locs {
		file, line, fn := d.process.PCToLine(locs[i].PC)
		locs[i].File = file
		locs[i].Line = line
		locs[i].Function = api.ConvertFunction(fn)
	}
	return locs, err
}
