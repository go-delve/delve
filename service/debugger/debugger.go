package debugger

import (
	"debug/dwarf"
	"errors"
	"fmt"
	"go/parser"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/go-delve/delve/pkg/goversion"
	"github.com/go-delve/delve/pkg/logflags"
	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/core"
	"github.com/go-delve/delve/pkg/proc/gdbserial"
	"github.com/go-delve/delve/pkg/proc/native"
	"github.com/go-delve/delve/service/api"
	"github.com/sirupsen/logrus"
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
	log          *logrus.Entry

	running      bool
	runningMutex sync.Mutex
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

	// Foreground lets target process access stdin.
	Foreground bool

	// DebugInfoDirectories is the list of directories to look for
	// when resolving external debug info files.
	DebugInfoDirectories []string

	// CheckGoVersion is true if the debugger should check the version of Go
	// used to compile the executable and refuse to work on incompatible
	// versions.
	CheckGoVersion bool
}

// New creates a new Debugger. ProcessArgs specify the commandline arguments for the
// new process.
func New(config *Config, processArgs []string) (*Debugger, error) {
	logger := logflags.DebuggerLogger()
	d := &Debugger{
		config:      config,
		processArgs: processArgs,
		log:         logger,
	}

	// Create the process by either attaching or launching.
	switch {
	case d.config.AttachPid > 0:
		d.log.Infof("attaching to pid %d", d.config.AttachPid)
		path := ""
		if len(d.processArgs) > 0 {
			path = d.processArgs[0]
		}
		p, err := d.Attach(d.config.AttachPid, path)
		if err != nil {
			err = go11DecodeErrorCheck(err)
			return nil, attachErrorMessage(d.config.AttachPid, err)
		}
		d.target = p

	case d.config.CoreFile != "":
		var p proc.Process
		var err error
		switch d.config.Backend {
		case "rr":
			d.log.Infof("opening trace %s", d.config.CoreFile)
			p, err = gdbserial.Replay(d.config.CoreFile, false, false, d.config.DebugInfoDirectories)
		default:
			d.log.Infof("opening core file %s (executable %s)", d.config.CoreFile, d.processArgs[0])
			p, err = core.OpenCore(d.config.CoreFile, d.processArgs[0], d.config.DebugInfoDirectories)
		}
		if err != nil {
			err = go11DecodeErrorCheck(err)
			return nil, err
		}
		d.target = p
		if err := d.checkGoVersion(); err != nil {
			d.target.Detach(true)
			return nil, err
		}

	default:
		d.log.Infof("launching process with args: %v", d.processArgs)
		p, err := d.Launch(d.processArgs, d.config.WorkingDir)
		if err != nil {
			if err != proc.ErrNotExecutable && err != proc.ErrUnsupportedLinuxArch && err != proc.ErrUnsupportedWindowsArch && err != proc.ErrUnsupportedDarwinArch {
				err = go11DecodeErrorCheck(err)
				err = fmt.Errorf("could not launch process: %s", err)
			}
			return nil, err
		}
		d.target = p
		if err := d.checkGoVersion(); err != nil {
			d.target.Detach(true)
			return nil, err
		}
	}
	return d, nil
}

// canRestart returns true if the target was started with Launch and can be restarted
func (d *Debugger) canRestart() bool {
	switch {
	case d.config.AttachPid > 0:
		return false
	case d.config.CoreFile != "":
		return false
	default:
		return true
	}
}

func (d *Debugger) checkGoVersion() error {
	if !d.config.CheckGoVersion {
		return nil
	}
	producer := d.target.BinInfo().Producer()
	if producer == "" {
		return nil
	}
	return goversion.Compatible(producer)
}

// Launch will start a process with the given args and working directory.
func (d *Debugger) Launch(processArgs []string, wd string) (proc.Process, error) {
	switch d.config.Backend {
	case "native":
		return native.Launch(processArgs, wd, d.config.Foreground, d.config.DebugInfoDirectories)
	case "lldb":
		return betterGdbserialLaunchError(gdbserial.LLDBLaunch(processArgs, wd, d.config.Foreground, d.config.DebugInfoDirectories))
	case "rr":
		p, _, err := gdbserial.RecordAndReplay(processArgs, wd, false, d.config.DebugInfoDirectories)
		return p, err
	case "default":
		if runtime.GOOS == "darwin" {
			return betterGdbserialLaunchError(gdbserial.LLDBLaunch(processArgs, wd, d.config.Foreground, d.config.DebugInfoDirectories))
		}
		return native.Launch(processArgs, wd, d.config.Foreground, d.config.DebugInfoDirectories)
	default:
		return nil, fmt.Errorf("unknown backend %q", d.config.Backend)
	}
}

// ErrNoAttachPath is the error returned when the client tries to attach to
// a process on macOS using the lldb backend without specifying the path to
// the target's executable.
var ErrNoAttachPath = errors.New("must specify executable path on macOS")

// Attach will attach to the process specified by 'pid'.
func (d *Debugger) Attach(pid int, path string) (proc.Process, error) {
	switch d.config.Backend {
	case "native":
		return native.Attach(pid, d.config.DebugInfoDirectories)
	case "lldb":
		return betterGdbserialLaunchError(gdbserial.LLDBAttach(pid, path, d.config.DebugInfoDirectories))
	case "default":
		if runtime.GOOS == "darwin" {
			return betterGdbserialLaunchError(gdbserial.LLDBAttach(pid, path, d.config.DebugInfoDirectories))
		}
		return native.Attach(pid, d.config.DebugInfoDirectories)
	default:
		return nil, fmt.Errorf("unknown backend %q", d.config.Backend)
	}
}

var errMacOSBackendUnavailable = errors.New("debugserver or lldb-server not found: install XCode's command line tools or lldb-server")

func betterGdbserialLaunchError(p proc.Process, err error) (proc.Process, error) {
	if runtime.GOOS != "darwin" {
		return p, err
	}
	if _, isUnavailable := err.(*gdbserial.ErrBackendUnavailable); !isUnavailable {
		return p, err
	}

	return p, errMacOSBackendUnavailable
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

const deferReturn = "runtime.deferreturn"

// FunctionReturnLocations returns all return locations
// for the given function, a list of addresses corresponding
// to 'ret' or 'call runtime.deferreturn'.
func (d *Debugger) FunctionReturnLocations(fnName string) ([]uint64, error) {
	var (
		p = d.target
		g = p.SelectedGoroutine()
	)

	fn, ok := p.BinInfo().LookupFunc[fnName]
	if !ok {
		return nil, fmt.Errorf("unable to find function %s", fnName)
	}

	var regs proc.Registers
	var mem proc.MemoryReadWriter = p.CurrentThread()
	if g.Thread != nil {
		mem = g.Thread
		regs, _ = g.Thread.Registers(false)
	}
	instructions, err := proc.Disassemble(mem, regs, p.Breakpoints(), p.BinInfo(), fn.Entry, fn.End)
	if err != nil {
		return nil, err
	}

	var addrs []uint64
	for _, instruction := range instructions {
		if instruction.IsRet() {
			addrs = append(addrs, instruction.Loc.PC)
		}
	}
	addrs = append(addrs, proc.FindDeferReturnCalls(instructions)...)

	return addrs, nil
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

var ErrCanNotRestart = errors.New("can not restart this target")

// Restart will restart the target process, first killing
// and then exec'ing it again.
// If the target process is a recording it will restart it from the given
// position. If pos starts with 'c' it's a checkpoint ID, otherwise it's an
// event number. If resetArgs is true, newArgs will replace the process args.
func (d *Debugger) Restart(rerecord bool, pos string, resetArgs bool, newArgs []string) ([]api.DiscardedBreakpoint, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	recorded, _ := d.target.Recorded()
	if recorded && !rerecord {
		return nil, d.target.Restart(pos)
	}

	if pos != "" {
		return nil, proc.ErrNotRecorded
	}

	if !d.canRestart() {
		return nil, ErrCanNotRestart
	}

	if valid, _ := d.target.Valid(); valid && !recorded {
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
				discarded = append(discarded, api.DiscardedBreakpoint{Breakpoint: oldBp, Reason: err.Error()})
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
func (d *Debugger) State(nowait bool) (*api.DebuggerState, error) {
	if d.isRunning() && nowait {
		return &api.DebuggerState{Running: true}, nil
	}

	d.processMutex.Lock()
	defer d.processMutex.Unlock()
	return d.state(nil)
}

func (d *Debugger) state(retLoadCfg *proc.LoadConfig) (*api.DebuggerState, error) {
	if _, err := d.target.Valid(); err != nil {
		return nil, err
	}

	var (
		state     *api.DebuggerState
		goroutine *api.Goroutine
	)

	if d.target.SelectedGoroutine() != nil {
		goroutine = api.ConvertGoroutine(d.target.SelectedGoroutine())
	}

	exited := false
	if _, err := d.target.Valid(); err != nil {
		_, exited = err.(*proc.ErrProcessExited)
	}

	state = &api.DebuggerState{
		SelectedGoroutine: goroutine,
		Exited:            exited,
	}

	for _, thread := range d.target.ThreadList() {
		th := api.ConvertThread(thread)

		if retLoadCfg != nil {
			th.ReturnValues = convertVars(thread.Common().ReturnValues(*retLoadCfg))
		}

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
	case requestedBp.TraceReturn:
		addr = requestedBp.Addr
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
		addr, err = proc.FindFunctionLocation(d.target, requestedBp.FunctionName, requestedBp.Line)
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
	d.log.Infof("created breakpoint: %#v", createdBp)
	return createdBp, nil
}

// AmendBreakpoint will update the breakpoint with the matching ID.
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

// CancelNext will clear internal breakpoints, thus cancelling the 'next',
// 'step' or 'stepout' operation.
func (d *Debugger) CancelNext() error {
	return d.target.ClearInternalBreakpoints()
}

func copyBreakpointInfo(bp *proc.Breakpoint, requested *api.Breakpoint) (err error) {
	bp.Name = requested.Name
	bp.Tracepoint = requested.Tracepoint
	bp.TraceReturn = requested.TraceReturn
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
	d.log.Infof("cleared breakpoint: %#v", clearedBp)
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

	if _, err := d.target.Valid(); err != nil {
		return nil, err
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

	if _, err := d.target.Valid(); err != nil {
		return nil, err
	}

	for _, th := range d.target.ThreadList() {
		if th.ThreadID() == id {
			return api.ConvertThread(th), nil
		}
	}
	return nil, nil
}

func (d *Debugger) setRunning(running bool) {
	d.runningMutex.Lock()
	d.running = running
	d.runningMutex.Unlock()
}

func (d *Debugger) isRunning() bool {
	d.runningMutex.Lock()
	defer d.runningMutex.Unlock()
	return d.running
}

// Command handles commands which control the debugger lifecycle
func (d *Debugger) Command(command *api.DebuggerCommand) (*api.DebuggerState, error) {
	var err error

	if command.Name == api.Halt {
		// RequestManualStop does not invoke any ptrace syscalls, so it's safe to
		// access the process directly.
		d.log.Debug("halting")
		err = d.target.RequestManualStop()
	}

	withBreakpointInfo := true

	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	d.setRunning(true)
	defer d.setRunning(false)

	switch command.Name {
	case api.Continue:
		d.log.Debug("continuing")
		err = proc.Continue(d.target)
	case api.Call:
		d.log.Debugf("function call %s", command.Expr)
		if command.ReturnInfoLoadConfig == nil {
			return nil, errors.New("can not call function with nil ReturnInfoLoadConfig")
		}
		g := d.target.SelectedGoroutine()
		if command.GoroutineID > 0 {
			g, err = proc.FindGoroutine(d.target, command.GoroutineID)
			if err != nil {
				return nil, err
			}
		}
		err = proc.EvalExpressionWithCalls(d.target, g, command.Expr, *api.LoadConfigToProc(command.ReturnInfoLoadConfig), !command.UnsafeCall)
	case api.Rewind:
		d.log.Debug("rewinding")
		if err := d.target.Direction(proc.Backward); err != nil {
			return nil, err
		}
		defer func() {
			d.target.Direction(proc.Forward)
		}()
		err = proc.Continue(d.target)
	case api.Next:
		d.log.Debug("nexting")
		err = proc.Next(d.target)
	case api.Step:
		d.log.Debug("stepping")
		err = proc.Step(d.target)
	case api.StepInstruction:
		d.log.Debug("single stepping")
		err = d.target.StepInstruction()
	case api.ReverseStepInstruction:
		d.log.Debug("reverse single stepping")
		if err := d.target.Direction(proc.Backward); err != nil {
			return nil, err
		}
		defer func() {
			d.target.Direction(proc.Forward)
		}()
		err = d.target.StepInstruction()
	case api.StepOut:
		d.log.Debug("step out")
		err = proc.StepOut(d.target)
	case api.SwitchThread:
		d.log.Debugf("switching to thread %d", command.ThreadID)
		err = d.target.SwitchThread(command.ThreadID)
		withBreakpointInfo = false
	case api.SwitchGoroutine:
		d.log.Debugf("switching to goroutine %d", command.GoroutineID)
		err = d.target.SwitchGoroutine(command.GoroutineID)
		withBreakpointInfo = false
	case api.Halt:
		// RequestManualStop already called
		withBreakpointInfo = false
	}

	if err != nil {
		if exitedErr, exited := err.(proc.ErrProcessExited); command.Name != api.SwitchGoroutine && command.Name != api.SwitchThread && exited {
			state := &api.DebuggerState{}
			state.Exited = true
			state.ExitStatus = exitedErr.Status
			state.Err = errors.New(exitedErr.Error())
			return state, nil
		}
		return nil, err
	}
	state, stateErr := d.state(api.LoadConfigToProc(command.ReturnInfoLoadConfig))
	if stateErr != nil {
		return state, stateErr
	}
	if withBreakpointInfo {
		err = d.collectBreakpointInformation(state)
	}
	for _, th := range state.Threads {
		if th.Breakpoint != nil && th.Breakpoint.TraceReturn {
			for _, v := range th.BreakpointInfo.Arguments {
				if (v.Flags & api.VariableReturnArgument) != 0 {
					th.ReturnValues = append(th.ReturnValues, v)
				}
			}
		}
	}
	return state, err
}

func (d *Debugger) collectBreakpointInformation(state *api.DebuggerState) error {
	if state == nil {
		return nil
	}

	for i := range state.Threads {
		if state.Threads[i].Breakpoint == nil || state.Threads[i].BreakpointInfo != nil {
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
			v, err := s.EvalVariable(bp.Variables[i], proc.LoadConfig{FollowPointers: true, MaxVariableRecurse: 1, MaxStringLen: 64, MaxArrayValues: 64, MaxStructFields: -1})
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

// Types returns all type information in the binary.
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
	return api.ConvertRegisters(regs.Slice(floatingPoint)), err
}

func convertVars(pv []*proc.Variable) []api.Variable {
	if pv == nil {
		return nil
	}
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

	s, err := proc.ConvertEvalScope(d.target, scope.GoroutineID, scope.Frame, scope.DeferredCall)
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

	s, err := proc.ConvertEvalScope(d.target, scope.GoroutineID, scope.Frame, scope.DeferredCall)
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

	s, err := proc.ConvertEvalScope(d.target, scope.GoroutineID, scope.Frame, scope.DeferredCall)
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

	s, err := proc.ConvertEvalScope(d.target, scope.GoroutineID, scope.Frame, scope.DeferredCall)
	if err != nil {
		return err
	}
	return s.SetVariable(symbol, value)
}

// Goroutines will return a list of goroutines in the target process.
func (d *Debugger) Goroutines(start, count int) ([]*api.Goroutine, int, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	goroutines := []*api.Goroutine{}
	gs, nextg, err := proc.GoroutinesInfo(d.target, start, count)
	if err != nil {
		return nil, 0, err
	}
	for _, g := range gs {
		goroutines = append(goroutines, api.ConvertGoroutine(g))
	}
	return goroutines, nextg, err
}

// Stacktrace returns a list of Stackframes for the given goroutine. The
// length of the returned list will be min(stack_len, depth).
// If 'full' is true, then local vars, function args, etc will be returned as well.
func (d *Debugger) Stacktrace(goroutineID, depth int, opts api.StacktraceOptions, cfg *proc.LoadConfig) ([]api.Stackframe, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	if _, err := d.target.Valid(); err != nil {
		return nil, err
	}

	var rawlocs []proc.Stackframe

	g, err := proc.FindGoroutine(d.target, goroutineID)
	if err != nil {
		return nil, err
	}

	if g == nil {
		rawlocs, err = proc.ThreadStacktrace(d.target.CurrentThread(), depth)
	} else {
		rawlocs, err = g.Stacktrace(depth, proc.StacktraceOptions(opts))
	}
	if err != nil {
		return nil, err
	}

	return d.convertStacktrace(rawlocs, cfg)
}

// Ancestors returns the stacktraces for the ancestors of a goroutine.
func (d *Debugger) Ancestors(goroutineID, numAncestors, depth int) ([]api.Ancestor, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	if _, err := d.target.Valid(); err != nil {
		return nil, err
	}

	g, err := proc.FindGoroutine(d.target, goroutineID)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, errors.New("no selected goroutine")
	}

	ancestors, err := proc.Ancestors(d.target, g, numAncestors)
	if err != nil {
		return nil, err
	}

	r := make([]api.Ancestor, len(ancestors))
	for i := range ancestors {
		r[i].ID = ancestors[i].ID
		if ancestors[i].Unreadable != nil {
			r[i].Unreadable = ancestors[i].Unreadable.Error()
			continue
		}
		frames, err := ancestors[i].Stack(depth)
		if err != nil {
			r[i].Unreadable = fmt.Sprintf("could not read ancestor stacktrace: %v", err)
			continue
		}
		r[i].Stack, err = d.convertStacktrace(frames, nil)
		if err != nil {
			r[i].Unreadable = fmt.Sprintf("could not read ancestor stacktrace: %v", err)
		}
	}
	return r, nil
}

func (d *Debugger) convertStacktrace(rawlocs []proc.Stackframe, cfg *proc.LoadConfig) ([]api.Stackframe, error) {
	locations := make([]api.Stackframe, 0, len(rawlocs))
	for i := range rawlocs {
		frame := api.Stackframe{
			Location: api.ConvertLocation(rawlocs[i].Call),

			FrameOffset:        rawlocs[i].FrameOffset(),
			FramePointerOffset: rawlocs[i].FramePointerOffset(),

			Defers: d.convertDefers(rawlocs[i].Defers),

			Bottom: rawlocs[i].Bottom,
		}
		if rawlocs[i].Err != nil {
			frame.Err = rawlocs[i].Err.Error()
		}
		if cfg != nil && rawlocs[i].Current.Fn != nil {
			var err error
			scope := proc.FrameToScope(d.target.BinInfo(), d.target.CurrentThread(), nil, rawlocs[i:]...)
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

func (d *Debugger) convertDefers(defers []*proc.Defer) []api.Defer {
	r := make([]api.Defer, len(defers))
	for i := range defers {
		ddf, ddl, ddfn := d.target.BinInfo().PCToLine(defers[i].DeferredPC)
		drf, drl, drfn := d.target.BinInfo().PCToLine(defers[i].DeferPC)

		r[i] = api.Defer{
			DeferredLoc: api.ConvertLocation(proc.Location{
				PC:   defers[i].DeferredPC,
				File: ddf,
				Line: ddl,
				Fn:   ddfn,
			}),
			DeferLoc: api.ConvertLocation(proc.Location{
				PC:   defers[i].DeferPC,
				File: drf,
				Line: drl,
				Fn:   drfn,
			}),
			SP: defers[i].SP,
		}

		if defers[i].Unreadable != nil {
			r[i].Unreadable = defers[i].Unreadable.Error()
		}
	}

	return r
}

// FindLocation will find the location specified by 'locStr'.
func (d *Debugger) FindLocation(scope api.EvalScope, locStr string) ([]api.Location, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	if _, err := d.target.Valid(); err != nil {
		return nil, err
	}

	loc, err := parseLocationSpec(locStr)
	if err != nil {
		return nil, err
	}

	s, _ := proc.ConvertEvalScope(d.target, scope.GoroutineID, scope.Frame, scope.DeferredCall)

	locs, err := loc.Find(d, s, locStr)
	for i := range locs {
		file, line, fn := d.target.BinInfo().PCToLine(locs[i].PC)
		locs[i].File = file
		locs[i].Line = line
		locs[i].Function = api.ConvertFunction(fn)
	}
	return locs, err
}

// Disassemble code between startPC and endPC.
// if endPC == 0 it will find the function containing startPC and disassemble the whole function.
func (d *Debugger) Disassemble(goroutineID int, addr1, addr2 uint64, flavour api.AssemblyFlavour) (api.AsmInstructions, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	if _, err := d.target.Valid(); err != nil {
		return nil, err
	}

	if addr2 == 0 {
		_, _, fn := d.target.BinInfo().PCToLine(addr1)
		if fn == nil {
			return nil, fmt.Errorf("address %#x does not belong to any function", addr1)
		}
		addr1 = fn.Entry
		addr2 = fn.End
	}

	g, err := proc.FindGoroutine(d.target, goroutineID)
	if err != nil {
		return nil, err
	}

	curthread := d.target.CurrentThread()
	if g != nil && g.Thread != nil {
		curthread = g.Thread
	}
	regs, _ := curthread.Registers(false)

	insts, err := proc.Disassemble(curthread, regs, d.target.Breakpoints(), d.target.BinInfo(), addr1, addr2)
	if err != nil {
		return nil, err
	}
	disass := make(api.AsmInstructions, len(insts))

	for i := range insts {
		disass[i] = api.ConvertAsmInstruction(insts[i], insts[i].Text(proc.AssemblyFlavour(flavour), d.target.BinInfo()))
	}

	return disass, nil
}

// Recorded returns true if the target is a recording.
func (d *Debugger) Recorded() (recorded bool, tracedir string) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()
	return d.target.Recorded()
}

// Checkpoint will set a checkpoint specified by the locspec.
func (d *Debugger) Checkpoint(where string) (int, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()
	return d.target.Checkpoint(where)
}

// Checkpoints will return a list of checkpoints.
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

// ClearCheckpoint will clear the checkpoint of the given ID.
func (d *Debugger) ClearCheckpoint(id int) error {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()
	return d.target.ClearCheckpoint(id)
}

// ListDynamicLibraries returns a list of loaded dynamic libraries.
func (d *Debugger) ListDynamicLibraries() []api.Image {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()
	bi := d.target.BinInfo()
	r := make([]api.Image, 0, len(bi.Images)-1)
	// skips the first image because it's the executable file
	for i := range bi.Images[1:] {
		r = append(r, api.ConvertImage(bi.Images[i]))
	}
	return r
}

func (d *Debugger) GetVersion(out *api.GetVersionOut) error {
	if d.config.CoreFile != "" {
		if d.config.Backend == "rr" {
			out.Backend = "rr"
		} else {
			out.Backend = "core"
		}
	} else {
		if d.config.Backend == "default" {
			if runtime.GOOS == "darwin" {
				out.Backend = "lldb"
			} else {
				out.Backend = "native"
			}
		} else {
			out.Backend = d.config.Backend
		}
	}
	return nil
}

func go11DecodeErrorCheck(err error) error {
	if _, isdecodeerr := err.(dwarf.DecodeError); !isdecodeerr {
		return err
	}

	gover, ok := goversion.Installed()
	if !ok || !gover.AfterOrEqual(goversion.GoVersion{1, 11, -1, 0, 0, ""}) || goversion.VersionAfterOrEqual(runtime.Version(), 1, 11) {
		return err
	}

	return fmt.Errorf("executables built by Go 1.11 or later need Delve built by Go 1.11 or later")
}
