package debugger

import (
	"errors"
	"fmt"
	"go/parser"
	"log"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/derekparker/delve/pkg/proc"
	"github.com/derekparker/delve/pkg/proc/core"
	"github.com/derekparker/delve/pkg/proc/gdbserial"
	"github.com/derekparker/delve/pkg/proc/native"
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
	config *Config
	// arguments to launch a new process.
	processArgs []string
	// TODO(DO NOT MERGE WITHOUT) rename to targetMutex
	processMutex sync.Mutex
	target       proc.Process
}

// Config provides the configuration to start a Debugger.
//
// Only one of ProcessArgs or AttachPid should be specified. If ProcessArgs is
// provided, a new process will be launched. Otherwise, the debugger will try
// to attach to an existing process with AttachPid.
type Config struct {
	// WorkingDir is working directory of the new process. This field is used
	// only when launching a new process.
	WorkingDir string

	// AttachPid is the PID of an existing process to which the debugger should
	// attach.
	AttachPid int

	// CoreFile specifies the path to the core dump to open.
	CoreFile string
	// Backend specifies the debugger backend.
	Backend string
}

// New creates a new Debugger. ProcessArgs specify the commandline arguments for the
// new process.
func New(config *Config, processArgs []string) (*Debugger, error) {
	d := &Debugger{
		config:      config,
		processArgs: processArgs,
	}

	// Create the process by either attaching or launching.
	switch {
	case d.config.AttachPid > 0:
		log.Printf("attaching to pid %d", d.config.AttachPid)
		path := ""
		if len(d.processArgs) > 0 {
			path = d.processArgs[0]
		}
		p, err := d.Attach(d.config.AttachPid, path)
		if err != nil {
			return nil, attachErrorMessage(d.config.AttachPid, err)
		}
		d.target = p

	case d.config.CoreFile != "":
		var p proc.Process
		var err error
		switch d.config.Backend {
		case "rr":
			log.Printf("opening trace %s", d.config.CoreFile)
			p, err = gdbserial.Replay(d.config.CoreFile, false)
		default:
			log.Printf("opening core file %s (executable %s)", d.config.CoreFile, d.processArgs[0])
			p, err = core.OpenCore(d.config.CoreFile, d.processArgs[0])
		}
		if err != nil {
			return nil, err
		}
		d.target = p

	default:
		log.Printf("launching process with args: %v", d.processArgs)
		p, err := d.Launch(d.processArgs, d.config.WorkingDir)
		if err != nil {
			if err != proc.NotExecutableErr && err != proc.UnsupportedLinuxArchErr && err != proc.UnsupportedWindowsArchErr && err != proc.UnsupportedDarwinArchErr {
				err = fmt.Errorf("could not launch process: %s", err)
			}
			return nil, err
		}
		d.target = p
	}
	return d, nil
}

func (d *Debugger) Launch(processArgs []string, wd string) (proc.Process, error) {
	switch d.config.Backend {
	case "native":
		return native.Launch(processArgs, wd)
	case "lldb":
		return gdbserial.LLDBLaunch(processArgs, wd)
	case "rr":
		p, _, err := gdbserial.RecordAndReplay(processArgs, wd, false)
		return p, err
	case "default":
		if runtime.GOOS == "darwin" {
			return gdbserial.LLDBLaunch(processArgs, wd)
		}
		return native.Launch(processArgs, wd)
	default:
		return nil, fmt.Errorf("unknown backend %q", d.config.Backend)
	}
}

// ErrNoAttachPath is the error returned when the client tries to attach to
// a process on macOS using the lldb backend without specifying the path to
// the target's executable.
var ErrNoAttachPath = errors.New("must specify executable path on macOS")

func (d *Debugger) Attach(pid int, path string) (proc.Process, error) {
	switch d.config.Backend {
	case "native":
		return native.Attach(pid)
	case "lldb":
		return gdbserial.LLDBAttach(pid, path)
	case "default":
		if runtime.GOOS == "darwin" {
			return gdbserial.LLDBAttach(pid, path)
		}
		return native.Attach(pid)
	default:
		return nil, fmt.Errorf("unknown backend %q", d.config.Backend)
	}
}

// ProcessPid returns the PID of the process
// the debugger is debugging.
func (d *Debugger) ProcessPid() int {
	return d.target.Pid()
}

// LastModified returns the time that the process' executable was last
// modified.
func (d *Debugger) LastModified() time.Time {
	return d.target.BinInfo().LastModified()
}

// Detach detaches from the target process.
// If `kill` is true we will kill the process after
// detaching.
func (d *Debugger) Detach(kill bool) error {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	return d.detach(kill)
}

func (d *Debugger) detach(kill bool) error {
	if d.config.AttachPid == 0 {
		kill = true
	}
	return d.target.Detach(kill)
}

// Restart will restart the target process, first killing
// and then exec'ing it again.
// If the target process is a recording it will restart it from the given
// position. If pos starts with 'c' it's a checkpoint ID, otherwise it's an
// event number. If resetArgs is true, newArgs will replace the process args.
func (d *Debugger) Restart(pos string, resetArgs bool, newArgs []string) ([]api.DiscardedBreakpoint, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	if recorded, _ := d.target.Recorded(); recorded {
		return nil, d.target.Restart(pos)
	}

	if pos != "" {
		return nil, proc.NotRecordedErr
	}

	if !d.target.Exited() {
		// Ensure the process is in a PTRACE_STOP.
		if err := stopProcess(d.ProcessPid()); err != nil {
			return nil, err
		}
	}
	if err := d.detach(true); err != nil {
		return nil, err
	}
	if resetArgs {
		d.processArgs = append([]string{d.processArgs[0]}, newArgs...)
	}
	p, err := d.Launch(d.processArgs, d.config.WorkingDir)
	if err != nil {
		return nil, fmt.Errorf("could not launch process: %s", err)
	}
	discarded := []api.DiscardedBreakpoint{}
	for _, oldBp := range d.breakpoints() {
		if oldBp.ID < 0 {
			continue
		}
		if len(oldBp.File) > 0 {
			var err error
			oldBp.Addr, err = proc.FindFileLocation(p, oldBp.File, oldBp.Line)
			if err != nil {
				discarded = append(discarded, api.DiscardedBreakpoint{oldBp, err.Error()})
				continue
			}
		}
		newBp, err := p.SetBreakpoint(oldBp.Addr, proc.UserBreakpoint, nil)
		if err != nil {
			return nil, err
		}
		if err := copyBreakpointInfo(newBp, oldBp); err != nil {
			return nil, err
		}
	}
	d.target = p
	return discarded, nil
}

// State returns the current state of the debugger.
func (d *Debugger) State() (*api.DebuggerState, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()
	return d.state()
}

func (d *Debugger) state() (*api.DebuggerState, error) {
	if d.target.Exited() {
		return nil, proc.ProcessExitedError{Pid: d.ProcessPid()}
	}

	var (
		state     *api.DebuggerState
		goroutine *api.Goroutine
	)

	if d.target.SelectedGoroutine() != nil {
		goroutine = api.ConvertGoroutine(d.target.SelectedGoroutine())
	}

	state = &api.DebuggerState{
		SelectedGoroutine: goroutine,
		Exited:            d.target.Exited(),
	}

	for _, thread := range d.target.ThreadList() {
		th := api.ConvertThread(thread)
		state.Threads = append(state.Threads, th)
		if thread.ThreadID() == d.target.CurrentThread().ThreadID() {
			state.CurrentThread = th
		}
	}

	state.NextInProgress = d.target.Breakpoints().HasInternalBreakpoints()

	if recorded, _ := d.target.Recorded(); recorded {
		state.When, _ = d.target.When()
	}

	return state, nil
}

// CreateBreakpoint creates a breakpoint.
func (d *Debugger) CreateBreakpoint(requestedBp *api.Breakpoint) (*api.Breakpoint, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	var (
		createdBp *api.Breakpoint
		addr      uint64
		err       error
	)

	if requestedBp.Name != "" {
		if err = api.ValidBreakpointName(requestedBp.Name); err != nil {
			return nil, err
		}
		if d.findBreakpointByName(requestedBp.Name) != nil {
			return nil, errors.New("breakpoint name already exists")
		}
	}

	switch {
	case len(requestedBp.File) > 0:
		fileName := requestedBp.File
		if runtime.GOOS == "windows" {
			// Accept fileName which is case-insensitive and slash-insensitive match
			fileNameNormalized := strings.ToLower(filepath.ToSlash(fileName))
			for _, symFile := range d.target.BinInfo().Sources {
				if fileNameNormalized == strings.ToLower(filepath.ToSlash(symFile)) {
					fileName = symFile
					break
				}
			}
		}
		addr, err = proc.FindFileLocation(d.target, fileName, requestedBp.Line)
	case len(requestedBp.FunctionName) > 0:
		if requestedBp.Line >= 0 {
			addr, err = proc.FindFunctionLocation(d.target, requestedBp.FunctionName, false, requestedBp.Line)
		} else {
			addr, err = proc.FindFunctionLocation(d.target, requestedBp.FunctionName, true, 0)
		}
	default:
		addr = requestedBp.Addr
	}

	if err != nil {
		return nil, err
	}

	bp, err := d.target.SetBreakpoint(addr, proc.UserBreakpoint, nil)
	if err != nil {
		return nil, err
	}
	if err := copyBreakpointInfo(bp, requestedBp); err != nil {
		if _, err1 := d.target.ClearBreakpoint(bp.Addr); err1 != nil {
			err = fmt.Errorf("error while creating breakpoint: %v, additionally the breakpoint could not be properly rolled back: %v", err, err1)
		}
		return nil, err
	}
	createdBp = api.ConvertBreakpoint(bp)
	log.Printf("created breakpoint: %#v", createdBp)
	return createdBp, nil
}

func (d *Debugger) AmendBreakpoint(amend *api.Breakpoint) error {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	original := d.findBreakpoint(amend.ID)
	if original == nil {
		return fmt.Errorf("no breakpoint with ID %d", amend.ID)
	}
	if err := api.ValidBreakpointName(amend.Name); err != nil {
		return err
	}
	return copyBreakpointInfo(original, amend)
}

func (d *Debugger) CancelNext() error {
	return d.target.ClearInternalBreakpoints()
}

func copyBreakpointInfo(bp *proc.Breakpoint, requested *api.Breakpoint) (err error) {
	bp.Name = requested.Name
	bp.Tracepoint = requested.Tracepoint
	bp.Goroutine = requested.Goroutine
	bp.Stacktrace = requested.Stacktrace
	bp.Variables = requested.Variables
	bp.LoadArgs = api.LoadConfigToProc(requested.LoadArgs)
	bp.LoadLocals = api.LoadConfigToProc(requested.LoadLocals)
	bp.Cond = nil
	if requested.Cond != "" {
		bp.Cond, err = parser.ParseExpr(requested.Cond)
	}
	return err
}

// ClearBreakpoint clears a breakpoint.
func (d *Debugger) ClearBreakpoint(requestedBp *api.Breakpoint) (*api.Breakpoint, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	var clearedBp *api.Breakpoint
	bp, err := d.target.ClearBreakpoint(requestedBp.Addr)
	if err != nil {
		return nil, fmt.Errorf("Can't clear breakpoint @%x: %s", requestedBp.Addr, err)
	}
	clearedBp = api.ConvertBreakpoint(bp)
	log.Printf("cleared breakpoint: %#v", clearedBp)
	return clearedBp, err
}

// Breakpoints returns the list of current breakpoints.
func (d *Debugger) Breakpoints() []*api.Breakpoint {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()
	return d.breakpoints()
}

func (d *Debugger) breakpoints() []*api.Breakpoint {
	bps := []*api.Breakpoint{}
	for _, bp := range d.target.Breakpoints().M {
		if bp.IsUser() {
			bps = append(bps, api.ConvertBreakpoint(bp))
		}
	}
	return bps
}

// FindBreakpoint returns the breakpoint specified by 'id'.
func (d *Debugger) FindBreakpoint(id int) *api.Breakpoint {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	bp := d.findBreakpoint(id)
	if bp == nil {
		return nil
	}
	return api.ConvertBreakpoint(bp)
}

func (d *Debugger) findBreakpoint(id int) *proc.Breakpoint {
	for _, bp := range d.target.Breakpoints().M {
		if bp.ID == id {
			return bp
		}
	}
	return nil
}

// FindBreakpointByName returns the breakpoint specified by 'name'
func (d *Debugger) FindBreakpointByName(name string) *api.Breakpoint {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()
	return d.findBreakpointByName(name)
}

func (d *Debugger) findBreakpointByName(name string) *api.Breakpoint {
	for _, bp := range d.breakpoints() {
		if bp.Name == name {
			return bp
		}
	}
	return nil
}

// Threads returns the threads of the target process.
func (d *Debugger) Threads() ([]*api.Thread, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	if d.target.Exited() {
		return nil, proc.ProcessExitedError{Pid: d.ProcessPid()}
	}

	threads := []*api.Thread{}
	for _, th := range d.target.ThreadList() {
		threads = append(threads, api.ConvertThread(th))
	}
	return threads, nil
}

// FindThread returns the thread for the given 'id'.
func (d *Debugger) FindThread(id int) (*api.Thread, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	if d.target.Exited() {
		return nil, proc.ProcessExitedError{Pid: d.ProcessPid()}
	}

	for _, th := range d.target.ThreadList() {
		if th.ThreadID() == id {
			return api.ConvertThread(th), nil
		}
	}
	return nil, nil
}

// Command handles commands which control the debugger lifecycle
func (d *Debugger) Command(command *api.DebuggerCommand) (*api.DebuggerState, error) {
	var err error

	if command.Name == api.Halt {
		// RequestManualStop does not invoke any ptrace syscalls, so it's safe to
		// access the process directly.
		log.Print("halting")
		err = d.target.RequestManualStop()
	}

	withBreakpointInfo := true

	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	switch command.Name {
	case api.Continue:
		log.Print("continuing")
		err = proc.Continue(d.target)
	case api.Rewind:
		log.Print("rewinding")
		if err := d.target.Direction(proc.Backward); err != nil {
			return nil, err
		}
		defer func() {
			d.target.Direction(proc.Forward)
		}()
		err = proc.Continue(d.target)
	case api.Next:
		log.Print("nexting")
		err = proc.Next(d.target)
	case api.Step:
		log.Print("stepping")
		err = proc.Step(d.target)
	case api.StepInstruction:
		log.Print("single stepping")
		err = d.target.StepInstruction()
	case api.StepOut:
		log.Print("step out")
		err = proc.StepOut(d.target)
	case api.SwitchThread:
		log.Printf("switching to thread %d", command.ThreadID)
		err = d.target.SwitchThread(command.ThreadID)
		withBreakpointInfo = false
	case api.SwitchGoroutine:
		log.Printf("switching to goroutine %d", command.GoroutineID)
		err = d.target.SwitchGoroutine(command.GoroutineID)
		withBreakpointInfo = false
	case api.Halt:
		// RequestManualStop already called
		withBreakpointInfo = false
	}

	if err != nil {
		if exitedErr, exited := err.(proc.ProcessExitedError); command.Name != api.SwitchGoroutine && command.Name != api.SwitchThread && exited {
			state := &api.DebuggerState{}
			state.Exited = true
			state.ExitStatus = exitedErr.Status
			state.Err = errors.New(exitedErr.Error())
			return state, nil
		}
		return nil, err
	}
	state, stateErr := d.state()
	if stateErr != nil {
		return state, stateErr
	}
	if withBreakpointInfo {
		err = d.collectBreakpointInformation(state)
	}
	return state, err
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
			g, err := proc.GetG(d.target.CurrentThread())
			if err != nil {
				return err
			}
			bpi.Goroutine = api.ConvertGoroutine(g)
		}

		if bp.Stacktrace > 0 {
			rawlocs, err := proc.ThreadStacktrace(d.target.CurrentThread(), bp.Stacktrace)
			if err != nil {
				return err
			}
			bpi.Stacktrace, err = d.convertStacktrace(rawlocs, nil)
			if err != nil {
				return err
			}
		}

		thread, found := d.target.FindThread(state.Threads[i].ID)
		if !found {
			return fmt.Errorf("could not find thread %d", state.Threads[i].ID)
		}

		if len(bp.Variables) == 0 && bp.LoadArgs == nil && bp.LoadLocals == nil {
			// don't try to create goroutine scope if there is nothing to load
			continue
		}

		s, err := proc.GoroutineScope(thread)
		if err != nil {
			return err
		}

		if len(bp.Variables) > 0 {
			bpi.Variables = make([]api.Variable, len(bp.Variables))
		}
		for i := range bp.Variables {
			v, err := s.EvalVariable(bp.Variables[i], proc.LoadConfig{true, 1, 64, 64, -1})
			if err != nil {
				bpi.Variables[i] = api.Variable{Name: bp.Variables[i], Unreadable: fmt.Sprintf("eval error: %v", err)}
			} else {
				bpi.Variables[i] = *api.ConvertVar(v)
			}
		}
		if bp.LoadArgs != nil {
			if vars, err := s.FunctionArguments(*api.LoadConfigToProc(bp.LoadArgs)); err == nil {
				bpi.Arguments = convertVars(vars)
			}
		}
		if bp.LoadLocals != nil {
			if locals, err := s.LocalVariables(*api.LoadConfigToProc(bp.LoadLocals)); err == nil {
				bpi.Locals = convertVars(locals)
			}
		}
	}

	return nil
}

// Sources returns a list of the source files for target binary.
func (d *Debugger) Sources(filter string) ([]string, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	regex, err := regexp.Compile(filter)
	if err != nil {
		return nil, fmt.Errorf("invalid filter argument: %s", err.Error())
	}

	files := []string{}
	for _, f := range d.target.BinInfo().Sources {
		if regex.Match([]byte(f)) {
			files = append(files, f)
		}
	}
	return files, nil
}

// Functions returns a list of functions in the target process.
func (d *Debugger) Functions(filter string) ([]string, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	return regexFilterFuncs(filter, d.target.BinInfo().Functions)
}

func (d *Debugger) Types(filter string) ([]string, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	regex, err := regexp.Compile(filter)
	if err != nil {
		return nil, fmt.Errorf("invalid filter argument: %s", err.Error())
	}

	types, err := d.target.BinInfo().Types()
	if err != nil {
		return nil, err
	}

	r := make([]string, 0, len(types))
	for _, typ := range types {
		if regex.Match([]byte(typ)) {
			r = append(r, typ)
		}
	}

	return r, nil
}

func regexFilterFuncs(filter string, allFuncs []proc.Function) ([]string, error) {
	regex, err := regexp.Compile(filter)
	if err != nil {
		return nil, fmt.Errorf("invalid filter argument: %s", err.Error())
	}

	funcs := []string{}
	for _, f := range allFuncs {
		if regex.Match([]byte(f.Name)) {
			funcs = append(funcs, f.Name)
		}
	}
	return funcs, nil
}

// PackageVariables returns a list of package variables for the thread,
// optionally regexp filtered using regexp described in 'filter'.
func (d *Debugger) PackageVariables(threadID int, filter string, cfg proc.LoadConfig) ([]api.Variable, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	regex, err := regexp.Compile(filter)
	if err != nil {
		return nil, fmt.Errorf("invalid filter argument: %s", err.Error())
	}

	vars := []api.Variable{}
	thread, found := d.target.FindThread(threadID)
	if !found {
		return nil, fmt.Errorf("couldn't find thread %d", threadID)
	}
	scope, err := proc.ThreadScope(thread)
	if err != nil {
		return nil, err
	}
	pv, err := scope.PackageVariables(cfg)
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
func (d *Debugger) Registers(threadID int, floatingPoint bool) (api.Registers, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	thread, found := d.target.FindThread(threadID)
	if !found {
		return nil, fmt.Errorf("couldn't find thread %d", threadID)
	}
	regs, err := thread.Registers(floatingPoint)
	if err != nil {
		return nil, err
	}
	return api.ConvertRegisters(regs.Slice()), err
}

func convertVars(pv []*proc.Variable) []api.Variable {
	vars := make([]api.Variable, 0, len(pv))
	for _, v := range pv {
		vars = append(vars, *api.ConvertVar(v))
	}
	return vars
}

// LocalVariables returns a list of the local variables.
func (d *Debugger) LocalVariables(scope api.EvalScope, cfg proc.LoadConfig) ([]api.Variable, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	s, err := proc.ConvertEvalScope(d.target, scope.GoroutineID, scope.Frame)
	if err != nil {
		return nil, err
	}
	pv, err := s.LocalVariables(cfg)
	if err != nil {
		return nil, err
	}
	return convertVars(pv), err
}

// FunctionArguments returns the arguments to the current function.
func (d *Debugger) FunctionArguments(scope api.EvalScope, cfg proc.LoadConfig) ([]api.Variable, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	s, err := proc.ConvertEvalScope(d.target, scope.GoroutineID, scope.Frame)
	if err != nil {
		return nil, err
	}
	pv, err := s.FunctionArguments(cfg)
	if err != nil {
		return nil, err
	}
	return convertVars(pv), nil
}

// EvalVariableInScope will attempt to evaluate the variable represented by 'symbol'
// in the scope provided.
func (d *Debugger) EvalVariableInScope(scope api.EvalScope, symbol string, cfg proc.LoadConfig) (*api.Variable, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	s, err := proc.ConvertEvalScope(d.target, scope.GoroutineID, scope.Frame)
	if err != nil {
		return nil, err
	}
	v, err := s.EvalVariable(symbol, cfg)
	if err != nil {
		return nil, err
	}
	return api.ConvertVar(v), err
}

// SetVariableInScope will set the value of the variable represented by
// 'symbol' to the value given, in the given scope.
func (d *Debugger) SetVariableInScope(scope api.EvalScope, symbol, value string) error {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	s, err := proc.ConvertEvalScope(d.target, scope.GoroutineID, scope.Frame)
	if err != nil {
		return err
	}
	return s.SetVariable(symbol, value)
}

// Goroutines will return a list of goroutines in the target process.
func (d *Debugger) Goroutines() ([]*api.Goroutine, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	goroutines := []*api.Goroutine{}
	gs, err := proc.GoroutinesInfo(d.target)
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
func (d *Debugger) Stacktrace(goroutineID, depth int, cfg *proc.LoadConfig) ([]api.Stackframe, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	if d.target.Exited() {
		return nil, proc.ProcessExitedError{Pid: d.ProcessPid()}
	}

	var rawlocs []proc.Stackframe

	g, err := proc.FindGoroutine(d.target, goroutineID)
	if err != nil {
		return nil, err
	}

	if g == nil {
		rawlocs, err = proc.ThreadStacktrace(d.target.CurrentThread(), depth)
	} else {
		rawlocs, err = g.Stacktrace(depth)
	}
	if err != nil {
		return nil, err
	}

	return d.convertStacktrace(rawlocs, cfg)
}

func (d *Debugger) convertStacktrace(rawlocs []proc.Stackframe, cfg *proc.LoadConfig) ([]api.Stackframe, error) {
	locations := make([]api.Stackframe, 0, len(rawlocs))
	for i := range rawlocs {
		frame := api.Stackframe{
			Location: api.ConvertLocation(rawlocs[i].Call),

			FrameOffset:        rawlocs[i].FrameOffset(),
			FramePointerOffset: rawlocs[i].FramePointerOffset(),
		}
		if rawlocs[i].Err != nil {
			frame.Err = rawlocs[i].Err.Error()
		}
		if cfg != nil && rawlocs[i].Current.Fn != nil {
			var err error
			scope := proc.FrameToScope(d.target, rawlocs[i])
			locals, err := scope.LocalVariables(*cfg)
			if err != nil {
				return nil, err
			}
			arguments, err := scope.FunctionArguments(*cfg)
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
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	if d.target.Exited() {
		return nil, &proc.ProcessExitedError{Pid: d.target.Pid()}
	}

	loc, err := parseLocationSpec(locStr)
	if err != nil {
		return nil, err
	}

	s, _ := proc.ConvertEvalScope(d.target, scope.GoroutineID, scope.Frame)

	locs, err := loc.Find(d, s, locStr)
	for i := range locs {
		file, line, fn := d.target.BinInfo().PCToLine(locs[i].PC)
		locs[i].File = file
		locs[i].Line = line
		locs[i].Function = api.ConvertFunction(fn)
	}
	return locs, err
}

// Disassemble code between startPC and endPC
// if endPC == 0 it will find the function containing startPC and disassemble the whole function
func (d *Debugger) Disassemble(scope api.EvalScope, startPC, endPC uint64, flavour api.AssemblyFlavour) (api.AsmInstructions, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	if d.target.Exited() {
		return nil, &proc.ProcessExitedError{Pid: d.target.Pid()}
	}

	if endPC == 0 {
		_, _, fn := d.target.BinInfo().PCToLine(startPC)
		if fn == nil {
			return nil, fmt.Errorf("Address 0x%x does not belong to any function", startPC)
		}
		startPC = fn.Entry
		endPC = fn.End
	}

	g, err := proc.FindGoroutine(d.target, scope.GoroutineID)
	if err != nil {
		return nil, err
	}

	insts, err := proc.Disassemble(d.target, g, startPC, endPC)
	if err != nil {
		return nil, err
	}
	disass := make(api.AsmInstructions, len(insts))

	for i := range insts {
		disass[i] = api.ConvertAsmInstruction(insts[i], insts[i].Text(proc.AssemblyFlavour(flavour)))
	}

	return disass, nil
}

// Recorded returns true if the target is a recording.
func (d *Debugger) Recorded() (recorded bool, tracedir string) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()
	return d.target.Recorded()
}

func (d *Debugger) Checkpoint(where string) (int, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()
	return d.target.Checkpoint(where)
}

func (d *Debugger) Checkpoints() ([]api.Checkpoint, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()
	cps, err := d.target.Checkpoints()
	if err != nil {
		return nil, err
	}
	r := make([]api.Checkpoint, len(cps))
	for i := range cps {
		r[i] = api.ConvertCheckpoint(cps[i])
	}
	return r, nil
}

func (d *Debugger) ClearCheckpoint(id int) error {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()
	return d.target.ClearCheckpoint(id)
}
