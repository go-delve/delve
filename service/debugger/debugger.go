package debugger

import (
	"bytes"
	"debug/dwarf"
	"errors"
	"fmt"
	"go/parser"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/gobuild"
	"github.com/go-delve/delve/pkg/goversion"
	"github.com/go-delve/delve/pkg/locspec"
	"github.com/go-delve/delve/pkg/logflags"
	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/core"
	"github.com/go-delve/delve/pkg/proc/gdbserial"
	"github.com/go-delve/delve/pkg/proc/native"
	"github.com/go-delve/delve/service/api"
	"github.com/sirupsen/logrus"
)

var (
	// ErrCanNotRestart is returned when the target cannot be restarted.
	// This is returned for targets that have been attached to, or when
	// debugging core files.
	ErrCanNotRestart = errors.New("can not restart this target")

	// ErrNotRecording is returned when StopRecording is called while the
	// debugger is not recording the target.
	ErrNotRecording = errors.New("debugger is not recording")

	// ErrCoreDumpInProgress is returned when a core dump is already in progress.
	ErrCoreDumpInProgress = errors.New("core dump in progress")

	// ErrCoreDumpNotSupported is returned when core dumping is not supported
	ErrCoreDumpNotSupported = errors.New("core dumping not supported")
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

	targetMutex sync.Mutex
	target      *proc.Target

	log *logrus.Entry

	running      bool
	runningMutex sync.Mutex

	stopRecording func() error
	recordMutex   sync.Mutex

	dumpState proc.DumpState
	// Debugger keeps a map of disabled breakpoints
	// so lower layers like proc doesn't need to deal
	// with them
	disabledBreakpoints map[int]*api.Breakpoint
}

type ExecuteKind int

const (
	ExecutingExistingFile = ExecuteKind(iota)
	ExecutingGeneratedFile
	ExecutingGeneratedTest
	ExecutingOther
)

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

	// TTY is passed along to the target process on creation. Used to specify a
	// TTY for that process.
	TTY string

	// Packages contains the packages that we are debugging.
	Packages []string

	// BuildFlags contains the flags passed to the compiler.
	BuildFlags string

	// ExecuteKind contains the kind of the executed program.
	ExecuteKind ExecuteKind

	// Redirects specifies redirect rules for stdin, stdout and stderr
	Redirects [3]string

	// DisableASLR disables ASLR
	DisableASLR bool
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
		var p *proc.Target
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
			if _, ok := err.(*proc.ErrUnsupportedArch); !ok {
				err = go11DecodeErrorCheck(err)
				err = fmt.Errorf("could not launch process: %s", err)
			}
			return nil, err
		}
		if p != nil {
			// if p == nil and err == nil then we are doing a recording, don't touch d.target
			d.target = p
		}
		if err := d.checkGoVersion(); err != nil {
			d.target.Detach(true)
			return nil, err
		}
	}

	d.disabledBreakpoints = make(map[int]*api.Breakpoint)

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
	if d.isRecording() {
		// do not do anything if we are still recording
		return nil
	}
	producer := d.target.BinInfo().Producer()
	if producer == "" {
		return nil
	}
	return goversion.Compatible(producer)
}

// Launch will start a process with the given args and working directory.
func (d *Debugger) Launch(processArgs []string, wd string) (*proc.Target, error) {
	if err := verifyBinaryFormat(processArgs[0]); err != nil {
		return nil, err
	}

	launchFlags := proc.LaunchFlags(0)
	if d.config.Foreground {
		launchFlags |= proc.LaunchForeground
	}
	if d.config.DisableASLR {
		launchFlags |= proc.LaunchDisableASLR
	}

	switch d.config.Backend {
	case "native":
		return native.Launch(processArgs, wd, launchFlags, d.config.DebugInfoDirectories, d.config.TTY, d.config.Redirects)
	case "lldb":
		return betterGdbserialLaunchError(gdbserial.LLDBLaunch(processArgs, wd, launchFlags, d.config.DebugInfoDirectories, d.config.TTY, d.config.Redirects))
	case "rr":
		if d.target != nil {
			// restart should not call us if the backend is 'rr'
			panic("internal error: call to Launch with rr backend and target already exists")
		}

		run, stop, err := gdbserial.RecordAsync(processArgs, wd, false, d.config.Redirects)
		if err != nil {
			return nil, err
		}

		// let the initialization proceed but hold the targetMutex lock so that
		// any other request to debugger will block except State(nowait=true) and
		// Command(halt).
		d.targetMutex.Lock()
		d.recordingStart(stop)

		go func() {
			defer d.targetMutex.Unlock()

			p, err := d.recordingRun(run)
			if err != nil {
				d.log.Errorf("could not record target: %v", err)
				// this is ugly but we can't respond to any client requests at this
				// point so it's better if we die.
				os.Exit(1)
			}
			d.recordingDone()
			d.target = p
			if err := d.checkGoVersion(); err != nil {
				d.log.Error(err)
				err := d.target.Detach(true)
				if err != nil {
					d.log.Errorf("Error detaching from target: %v", err)
				}
			}
		}()
		return nil, nil

	case "default":
		if runtime.GOOS == "darwin" {
			return betterGdbserialLaunchError(gdbserial.LLDBLaunch(processArgs, wd, launchFlags, d.config.DebugInfoDirectories, d.config.TTY, d.config.Redirects))
		}
		return native.Launch(processArgs, wd, launchFlags, d.config.DebugInfoDirectories, d.config.TTY, d.config.Redirects)
	default:
		return nil, fmt.Errorf("unknown backend %q", d.config.Backend)
	}
}

func (d *Debugger) recordingStart(stop func() error) {
	d.recordMutex.Lock()
	d.stopRecording = stop
	d.recordMutex.Unlock()
}

func (d *Debugger) recordingDone() {
	d.recordMutex.Lock()
	d.stopRecording = nil
	d.recordMutex.Unlock()
}

func (d *Debugger) isRecording() bool {
	d.recordMutex.Lock()
	defer d.recordMutex.Unlock()
	return d.stopRecording != nil
}

func (d *Debugger) recordingRun(run func() (string, error)) (*proc.Target, error) {
	tracedir, err := run()
	if err != nil && tracedir == "" {
		return nil, err
	}

	return gdbserial.Replay(tracedir, false, true, d.config.DebugInfoDirectories)
}

// Attach will attach to the process specified by 'pid'.
func (d *Debugger) Attach(pid int, path string) (*proc.Target, error) {
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

var errMacOSBackendUnavailable = errors.New("debugserver or lldb-server not found: install Xcode's command line tools or lldb-server")

func betterGdbserialLaunchError(p *proc.Target, err error) (*proc.Target, error) {
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
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
	return d.target.Pid()
}

// LastModified returns the time that the process' executable was last
// modified.
func (d *Debugger) LastModified() time.Time {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
	return d.target.BinInfo().LastModified()
}

// FunctionReturnLocations returns all return locations
// for the given function, a list of addresses corresponding
// to 'ret' or 'call runtime.deferreturn'.
func (d *Debugger) FunctionReturnLocations(fnName string) ([]uint64, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	var (
		p = d.target
		g = p.SelectedGoroutine()
	)

	fn, ok := p.BinInfo().LookupFunc[fnName]
	if !ok {
		return nil, fmt.Errorf("unable to find function %s", fnName)
	}

	var regs proc.Registers
	mem := p.Memory()
	if g != nil && g.Thread != nil {
		regs, _ = g.Thread.Registers()
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
	d.log.Debug("detaching")
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
	if ok, _ := d.target.Valid(); !ok {
		return nil
	}
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
func (d *Debugger) Restart(rerecord bool, pos string, resetArgs bool, newArgs []string, newRedirects [3]string, rebuild bool) ([]api.DiscardedBreakpoint, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

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
		if err := stopProcess(d.target.Pid()); err != nil {
			return nil, err
		}
	}
	if err := d.detach(true); err != nil {
		return nil, err
	}
	if resetArgs {
		d.processArgs = append([]string{d.processArgs[0]}, newArgs...)
		d.config.Redirects = newRedirects
	}
	var p *proc.Target
	var err error

	if rebuild {
		switch d.config.ExecuteKind {
		case ExecutingGeneratedFile:
			err = gobuild.GoBuild(d.processArgs[0], d.config.Packages, d.config.BuildFlags)
			if err != nil {
				return nil, fmt.Errorf("could not rebuild process: %s", err)
			}
		case ExecutingGeneratedTest:
			err = gobuild.GoTestBuild(d.processArgs[0], d.config.Packages, d.config.BuildFlags)
			if err != nil {
				return nil, fmt.Errorf("could not rebuild process: %s", err)
			}
		default:
			// We cannot build a process that we didn't start, because we don't know how it was built.
			return nil, fmt.Errorf("cannot rebuild a binary")
		}
	}

	if recorded {
		run, stop, err2 := gdbserial.RecordAsync(d.processArgs, d.config.WorkingDir, false, d.config.Redirects)
		if err2 != nil {
			return nil, err2
		}

		d.recordingStart(stop)
		p, err = d.recordingRun(run)
		d.recordingDone()
	} else {
		p, err = d.Launch(d.processArgs, d.config.WorkingDir)
	}
	if err != nil {
		return nil, fmt.Errorf("could not launch process: %s", err)
	}

	discarded := []api.DiscardedBreakpoint{}
	breakpoints := api.ConvertBreakpoints(d.breakpoints())
	d.target = p
	maxID := 0
	for _, oldBp := range breakpoints {
		if oldBp.ID < 0 {
			continue
		}
		if oldBp.ID > maxID {
			maxID = oldBp.ID
		}
		if len(oldBp.File) > 0 {
			addrs, err := proc.FindFileLocation(p, oldBp.File, oldBp.Line)
			if err != nil {
				discarded = append(discarded, api.DiscardedBreakpoint{Breakpoint: oldBp, Reason: err.Error()})
				continue
			}
			createLogicalBreakpoint(d, addrs, oldBp, oldBp.ID)
		} else {
			// Avoid setting a breakpoint based on address when rebuilding
			if rebuild {
				continue
			}
			newBp, err := p.SetBreakpointWithID(oldBp.ID, oldBp.Addr)
			if err != nil {
				return nil, err
			}
			if err := copyBreakpointInfo(newBp, oldBp); err != nil {
				return nil, err
			}
		}
	}
	for _, bp := range d.disabledBreakpoints {
		if bp.ID > maxID {
			maxID = bp.ID
		}
	}
	d.target.SetNextBreakpointID(maxID)
	return discarded, nil
}

// State returns the current state of the debugger.
func (d *Debugger) State(nowait bool) (*api.DebuggerState, error) {
	if d.IsRunning() && nowait {
		return &api.DebuggerState{Running: true}, nil
	}

	if d.isRecording() && nowait {
		return &api.DebuggerState{Recording: true}, nil
	}

	d.dumpState.Mutex.Lock()
	if d.dumpState.Dumping && nowait {
		return &api.DebuggerState{CoreDumping: true}, nil
	}
	d.dumpState.Mutex.Unlock()

	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
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
		_, exited = err.(proc.ErrProcessExited)
	}

	state = &api.DebuggerState{
		SelectedGoroutine: goroutine,
		Exited:            exited,
	}

	for _, thread := range d.target.ThreadList() {
		th := api.ConvertThread(thread)

		th.CallReturn = thread.Common().CallReturn
		if retLoadCfg != nil {
			th.ReturnValues = api.ConvertVars(thread.Common().ReturnValues(*retLoadCfg))
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
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	var (
		addrs []uint64
		err   error
	)

	if requestedBp.Name != "" {
		if err = api.ValidBreakpointName(requestedBp.Name); err != nil {
			return nil, err
		}
		if (d.findBreakpointByName(requestedBp.Name) != nil) || (d.findDisabledBreakpointByName(requestedBp.Name) != nil) {
			return nil, errors.New("breakpoint name already exists")
		}
	}

	switch {
	case requestedBp.TraceReturn:
		addrs = []uint64{requestedBp.Addr}
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
		addrs, err = proc.FindFileLocation(d.target, fileName, requestedBp.Line)
	case len(requestedBp.FunctionName) > 0:
		addrs, err = proc.FindFunctionLocation(d.target, requestedBp.FunctionName, requestedBp.Line)
	case len(requestedBp.Addrs) > 0:
		addrs = requestedBp.Addrs
	default:
		addrs = []uint64{requestedBp.Addr}
	}

	if err != nil {
		return nil, err
	}

	createdBp, err := createLogicalBreakpoint(d, addrs, requestedBp, 0)
	if err != nil {
		return nil, err
	}
	d.log.Infof("created breakpoint: %#v", createdBp)
	return createdBp, nil
}

// createLogicalBreakpoint creates one physical breakpoint for each address
// in addrs and associates all of them with the same logical breakpoint.
func createLogicalBreakpoint(d *Debugger, addrs []uint64, requestedBp *api.Breakpoint, id int) (*api.Breakpoint, error) {
	p := d.target

	if dbp, ok := d.disabledBreakpoints[requestedBp.ID]; ok {
		return dbp, proc.BreakpointExistsError{File: dbp.File, Line: dbp.Line, Addr: dbp.Addr}
	}

	bps := make([]*proc.Breakpoint, len(addrs))
	var err error
	for i := range addrs {
		if id > 0 {
			bps[i], err = p.SetBreakpointWithID(id, addrs[i])
		} else {
			bps[i], err = p.SetBreakpoint(addrs[i], proc.UserBreakpoint, nil)
		}
		if err != nil {
			break
		}
		if i > 0 {
			bps[i].LogicalID = bps[0].LogicalID
		}
		err = copyBreakpointInfo(bps[i], requestedBp)
		if err != nil {
			break
		}
	}
	if err != nil {
		if isBreakpointExistsErr(err) {
			return nil, err
		}
		for _, bp := range bps {
			if bp == nil {
				continue
			}
			if _, err1 := p.ClearBreakpoint(bp.Addr); err1 != nil {
				err = fmt.Errorf("error while creating breakpoint: %v, additionally the breakpoint could not be properly rolled back: %v", err, err1)
				return nil, err
			}
		}
		return nil, err
	}

	createdBp := api.ConvertBreakpoints(bps)
	return createdBp[0], nil // we created a single logical breakpoint, the slice here will always have len == 1
}

func isBreakpointExistsErr(err error) bool {
	_, r := err.(proc.BreakpointExistsError)
	return r
}

// AmendBreakpoint will update the breakpoint with the matching ID.
// It also enables or disables the breakpoint.
func (d *Debugger) AmendBreakpoint(amend *api.Breakpoint) error {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	originals := d.findBreakpoint(amend.ID)
	_, disabled := d.disabledBreakpoints[amend.ID]
	if originals == nil && !disabled {
		return fmt.Errorf("no breakpoint with ID %d", amend.ID)
	}
	if err := api.ValidBreakpointName(amend.Name); err != nil {
		return err
	}
	if !amend.Disabled && disabled { // enable the breakpoint
		bp, err := d.target.SetBreakpointWithID(amend.ID, amend.Addr)
		if err != nil {
			return err
		}
		copyBreakpointInfo(bp, amend)
		delete(d.disabledBreakpoints, amend.ID)
	}
	if amend.Disabled && !disabled { // disable the breakpoint
		if _, err := d.clearBreakpoint(amend); err != nil {
			return err
		}
		d.disabledBreakpoints[amend.ID] = amend
	}
	for _, original := range originals {
		if err := copyBreakpointInfo(original, amend); err != nil {
			return err
		}
	}

	return nil
}

// CancelNext will clear internal breakpoints, thus cancelling the 'next',
// 'step' or 'stepout' operation.
func (d *Debugger) CancelNext() error {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
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
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
	return d.clearBreakpoint(requestedBp)
}

// clearBreakpoint clears a breakpoint, we can consume this function to avoid locking a goroutine
func (d *Debugger) clearBreakpoint(requestedBp *api.Breakpoint) (*api.Breakpoint, error) {
	if bp, ok := d.disabledBreakpoints[requestedBp.ID]; ok {
		delete(d.disabledBreakpoints, bp.ID)
		return bp, nil
	}

	var bps []*proc.Breakpoint
	var errs []error

	clear := func(addr uint64) {
		bp, err := d.target.ClearBreakpoint(addr)
		if err != nil {
			errs = append(errs, fmt.Errorf("address %#x: %v", addr, err))
		}
		if bp != nil {
			bps = append(bps, bp)
		}
	}

	clearAddr := true
	for _, addr := range requestedBp.Addrs {
		if addr == requestedBp.Addr {
			clearAddr = false
		}
		clear(addr)
	}
	if clearAddr {
		clear(requestedBp.Addr)
	}

	if len(errs) > 0 {
		buf := new(bytes.Buffer)
		for i, err := range errs {
			fmt.Fprintf(buf, "%s", err)
			if i != len(errs)-1 {
				fmt.Fprintf(buf, ", ")
			}
		}

		if len(bps) == 0 {
			return nil, fmt.Errorf("unable to clear breakpoint %d: %v", requestedBp.ID, buf.String())
		}
		return nil, fmt.Errorf("unable to clear breakpoint %d (partial): %s", requestedBp.ID, buf.String())
	}

	clearedBp := api.ConvertBreakpoints(bps)
	if len(clearedBp) < 0 {
		return nil, nil
	}
	d.log.Infof("cleared breakpoint: %#v", clearedBp)
	return clearedBp[0], nil
}

// Breakpoints returns the list of current breakpoints.
func (d *Debugger) Breakpoints() []*api.Breakpoint {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	bps := api.ConvertBreakpoints(d.breakpoints())

	for _, bp := range d.disabledBreakpoints {
		bps = append(bps, bp)
	}

	return bps
}

func (d *Debugger) breakpoints() []*proc.Breakpoint {
	bps := []*proc.Breakpoint{}
	for _, bp := range d.target.Breakpoints().M {
		if bp.IsUser() {
			bps = append(bps, bp)
		}
	}
	sort.Sort(breakpointsByLogicalID(bps))
	return bps
}

// FindBreakpoint returns the breakpoint specified by 'id'.
func (d *Debugger) FindBreakpoint(id int) *api.Breakpoint {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
	bps := api.ConvertBreakpoints(d.findBreakpoint(id))
	bps = append(bps, d.findDisabledBreakpoint(id)...)
	if len(bps) <= 0 {
		return nil
	}
	return bps[0]
}

func (d *Debugger) findBreakpoint(id int) []*proc.Breakpoint {
	var bps []*proc.Breakpoint
	for _, bp := range d.target.Breakpoints().M {
		if bp.IsUser() && bp.LogicalID == id {
			bps = append(bps, bp)
		}
	}
	return bps
}

func (d *Debugger) findDisabledBreakpoint(id int) []*api.Breakpoint {
	var bps []*api.Breakpoint
	for _, dbp := range d.disabledBreakpoints {
		if dbp.ID == id {
			bps = append(bps, dbp)
		}
	}
	return bps
}

// FindBreakpointByName returns the breakpoint specified by 'name'
func (d *Debugger) FindBreakpointByName(name string) *api.Breakpoint {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	bp := d.findBreakpointByName(name)
	if bp == nil {
		bp = d.findDisabledBreakpointByName(name)
	}
	return bp
}

func (d *Debugger) findBreakpointByName(name string) *api.Breakpoint {
	var bps []*proc.Breakpoint
	for _, bp := range d.breakpoints() {
		if bp.Name == name {
			bps = append(bps, bp)
		}
	}
	if len(bps) == 0 {
		return nil
	}
	sort.Sort(breakpointsByLogicalID(bps))
	r := api.ConvertBreakpoints(bps)
	return r[0] // there can only be one logical breakpoint with the same name
}

func (d *Debugger) findDisabledBreakpointByName(name string) *api.Breakpoint {
	for _, dbp := range d.disabledBreakpoints {
		if dbp.Name == name {
			return dbp
		}
	}
	return nil
}

// Threads returns the threads of the target process.
func (d *Debugger) Threads() ([]proc.Thread, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	if _, err := d.target.Valid(); err != nil {
		return nil, err
	}

	return d.target.ThreadList(), nil
}

// FindThread returns the thread for the given 'id'.
func (d *Debugger) FindThread(id int) (proc.Thread, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	if _, err := d.target.Valid(); err != nil {
		return nil, err
	}

	for _, th := range d.target.ThreadList() {
		if th.ThreadID() == id {
			return th, nil
		}
	}
	return nil, nil
}

func (d *Debugger) setRunning(running bool) {
	d.runningMutex.Lock()
	d.running = running
	d.runningMutex.Unlock()
}

func (d *Debugger) IsRunning() bool {
	d.runningMutex.Lock()
	defer d.runningMutex.Unlock()
	return d.running
}

// Command handles commands which control the debugger lifecycle
func (d *Debugger) Command(command *api.DebuggerCommand, resumeNotify chan struct{}) (*api.DebuggerState, error) {
	var err error

	if command.Name == api.Halt {
		// RequestManualStop does not invoke any ptrace syscalls, so it's safe to
		// access the process directly.
		d.log.Debug("halting")

		d.recordMutex.Lock()
		if d.stopRecording == nil {
			err = d.target.RequestManualStop()
		}
		d.recordMutex.Unlock()
	}

	withBreakpointInfo := true

	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	d.setRunning(true)
	defer d.setRunning(false)

	if command.Name != api.SwitchGoroutine && command.Name != api.SwitchThread && command.Name != api.Halt {
		d.target.ResumeNotify(resumeNotify)
	} else if resumeNotify != nil {
		close(resumeNotify)
	}

	switch command.Name {
	case api.Continue:
		d.log.Debug("continuing")
		if err := d.target.ChangeDirection(proc.Forward); err != nil {
			return nil, err
		}
		err = d.target.Continue()
	case api.DirectionCongruentContinue:
		d.log.Debug("continuing (direction congruent)")
		err = d.target.Continue()
	case api.Call:
		d.log.Debugf("function call %s", command.Expr)
		if err := d.target.ChangeDirection(proc.Forward); err != nil {
			return nil, err
		}
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
		if err := d.target.ChangeDirection(proc.Backward); err != nil {
			return nil, err
		}
		err = d.target.Continue()
	case api.Next:
		d.log.Debug("nexting")
		if err := d.target.ChangeDirection(proc.Forward); err != nil {
			return nil, err
		}
		err = d.target.Next()
	case api.ReverseNext:
		d.log.Debug("reverse nexting")
		if err := d.target.ChangeDirection(proc.Backward); err != nil {
			return nil, err
		}
		err = d.target.Next()
	case api.Step:
		d.log.Debug("stepping")
		if err := d.target.ChangeDirection(proc.Forward); err != nil {
			return nil, err
		}
		err = d.target.Step()
	case api.ReverseStep:
		d.log.Debug("reverse stepping")
		if err := d.target.ChangeDirection(proc.Backward); err != nil {
			return nil, err
		}
		err = d.target.Step()
	case api.StepInstruction:
		d.log.Debug("single stepping")
		if err := d.target.ChangeDirection(proc.Forward); err != nil {
			return nil, err
		}
		err = d.target.StepInstruction()
	case api.ReverseStepInstruction:
		d.log.Debug("reverse single stepping")
		if err := d.target.ChangeDirection(proc.Backward); err != nil {
			return nil, err
		}
		err = d.target.StepInstruction()
	case api.StepOut:
		d.log.Debug("step out")
		if err := d.target.ChangeDirection(proc.Forward); err != nil {
			return nil, err
		}
		err = d.target.StepOut()
	case api.ReverseStepOut:
		d.log.Debug("reverse step out")
		if err := d.target.ChangeDirection(proc.Backward); err != nil {
			return nil, err
		}
		err = d.target.StepOut()
	case api.SwitchThread:
		d.log.Debugf("switching to thread %d", command.ThreadID)
		err = d.target.SwitchThread(command.ThreadID)
		withBreakpointInfo = false
	case api.SwitchGoroutine:
		d.log.Debugf("switching to goroutine %d", command.GoroutineID)
		var g *proc.G
		g, err = proc.FindGoroutine(d.target, command.GoroutineID)
		if err == nil {
			err = d.target.SwitchGoroutine(g)
		}
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
				bpi.Arguments = api.ConvertVars(vars)
			}
		}
		if bp.LoadLocals != nil {
			if locals, err := s.LocalVariables(*api.LoadConfigToProc(bp.LoadLocals)); err == nil {
				bpi.Locals = api.ConvertVars(locals)
			}
		}
	}

	return nil
}

// Sources returns a list of the source files for target binary.
func (d *Debugger) Sources(filter string) ([]string, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

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
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	regex, err := regexp.Compile(filter)
	if err != nil {
		return nil, fmt.Errorf("invalid filter argument: %s", err.Error())
	}

	funcs := []string{}
	for _, f := range d.target.BinInfo().Functions {
		if regex.MatchString(f.Name) {
			funcs = append(funcs, f.Name)
		}
	}
	return funcs, nil
}

// Types returns all type information in the binary.
func (d *Debugger) Types(filter string) ([]string, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

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

// PackageVariables returns a list of package variables for the thread,
// optionally regexp filtered using regexp described in 'filter'.
func (d *Debugger) PackageVariables(filter string, cfg proc.LoadConfig) ([]*proc.Variable, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	regex, err := regexp.Compile(filter)
	if err != nil {
		return nil, fmt.Errorf("invalid filter argument: %s", err.Error())
	}

	scope, err := proc.ThreadScope(d.target.CurrentThread())
	if err != nil {
		return nil, err
	}
	pv, err := scope.PackageVariables(cfg)
	if err != nil {
		return nil, err
	}
	pvr := pv[:0]
	for i := range pv {
		if regex.Match([]byte(pv[i].Name)) {
			pvr = append(pvr, pv[i])
		}
	}
	return pvr, nil
}

// ThreadRegisters returns registers of the specified thread.
func (d *Debugger) ThreadRegisters(threadID int, floatingPoint bool) (*op.DwarfRegisters, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	thread, found := d.target.FindThread(threadID)
	if !found {
		return nil, fmt.Errorf("couldn't find thread %d", threadID)
	}
	regs, err := thread.Registers()
	if err != nil {
		return nil, err
	}
	dregs := d.target.BinInfo().Arch.RegistersToDwarfRegisters(0, regs)
	return &dregs, nil
}

// ScopeRegisters returns registers for the specified scope.
func (d *Debugger) ScopeRegisters(goid, frame, deferredCall int, floatingPoint bool) (*op.DwarfRegisters, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	s, err := proc.ConvertEvalScope(d.target, goid, frame, deferredCall)
	if err != nil {
		return nil, err
	}
	return &s.Regs, nil
}

// DwarfRegisterToString returns the name and value representation of the given register.
func (d *Debugger) DwarfRegisterToString(i int, reg *op.DwarfRegister) (string, bool, string) {
	return d.target.BinInfo().Arch.DwarfRegisterToString(i, reg)
}

// LocalVariables returns a list of the local variables.
func (d *Debugger) LocalVariables(goid, frame, deferredCall int, cfg proc.LoadConfig) ([]*proc.Variable, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	s, err := proc.ConvertEvalScope(d.target, goid, frame, deferredCall)
	if err != nil {
		return nil, err
	}
	return s.LocalVariables(cfg)
}

// FunctionArguments returns the arguments to the current function.
func (d *Debugger) FunctionArguments(goid, frame, deferredCall int, cfg proc.LoadConfig) ([]*proc.Variable, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	s, err := proc.ConvertEvalScope(d.target, goid, frame, deferredCall)
	if err != nil {
		return nil, err
	}
	return s.FunctionArguments(cfg)
}

// EvalVariableInScope will attempt to evaluate the variable represented by 'symbol'
// in the scope provided.
func (d *Debugger) EvalVariableInScope(goid, frame, deferredCall int, symbol string, cfg proc.LoadConfig) (*proc.Variable, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	s, err := proc.ConvertEvalScope(d.target, goid, frame, deferredCall)
	if err != nil {
		return nil, err
	}
	return s.EvalVariable(symbol, cfg)
}

// SetVariableInScope will set the value of the variable represented by
// 'symbol' to the value given, in the given scope.
func (d *Debugger) SetVariableInScope(goid, frame, deferredCall int, symbol, value string) error {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	s, err := proc.ConvertEvalScope(d.target, goid, frame, deferredCall)
	if err != nil {
		return err
	}
	return s.SetVariable(symbol, value)
}

// Goroutines will return a list of goroutines in the target process.
func (d *Debugger) Goroutines(start, count int) ([]*proc.G, int, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
	return proc.GoroutinesInfo(d.target, start, count)
}

// Stacktrace returns a list of Stackframes for the given goroutine. The
// length of the returned list will be min(stack_len, depth).
// If 'full' is true, then local vars, function args, etc will be returned as well.
func (d *Debugger) Stacktrace(goroutineID, depth int, opts api.StacktraceOptions) ([]proc.Stackframe, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	if _, err := d.target.Valid(); err != nil {
		return nil, err
	}

	g, err := proc.FindGoroutine(d.target, goroutineID)
	if err != nil {
		return nil, err
	}

	if g == nil {
		return proc.ThreadStacktrace(d.target.CurrentThread(), depth)
	} else {
		return g.Stacktrace(depth, proc.StacktraceOptions(opts))
	}
}

// Ancestors returns the stacktraces for the ancestors of a goroutine.
func (d *Debugger) Ancestors(goroutineID, numAncestors, depth int) ([]api.Ancestor, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

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

// ConvertStacktrace converts a slice of proc.Stackframe into a slice of
// api.Stackframe, loading local variables and arguments of each frame if
// cfg is not nil.
func (d *Debugger) ConvertStacktrace(rawlocs []proc.Stackframe, cfg *proc.LoadConfig) ([]api.Stackframe, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
	return d.convertStacktrace(rawlocs, cfg)
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
			scope := proc.FrameToScope(d.target.BinInfo(), d.target.Memory(), nil, rawlocs[i:]...)
			locals, err := scope.LocalVariables(*cfg)
			if err != nil {
				return nil, err
			}
			arguments, err := scope.FunctionArguments(*cfg)
			if err != nil {
				return nil, err
			}

			frame.Locals = api.ConvertVars(locals)
			frame.Arguments = api.ConvertVars(arguments)
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

// CurrentPackage returns the fully qualified name of the
// package corresponding to the function location of the
// current thread.
func (d *Debugger) CurrentPackage() (string, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	if _, err := d.target.Valid(); err != nil {
		return "", err
	}
	loc, err := d.target.CurrentThread().Location()
	if err != nil {
		return "", err
	}
	if loc.Fn == nil {
		return "", fmt.Errorf("unable to determine current package due to unspecified function location")
	}
	return loc.Fn.PackageName(), nil
}

// FindLocation will find the location specified by 'locStr'.
func (d *Debugger) FindLocation(goid, frame, deferredCall int, locStr string, includeNonExecutableLines bool, substitutePathRules [][2]string) ([]api.Location, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	if _, err := d.target.Valid(); err != nil {
		return nil, err
	}

	loc, err := locspec.Parse(locStr)
	if err != nil {
		return nil, err
	}

	s, _ := proc.ConvertEvalScope(d.target, goid, frame, deferredCall)

	locs, err := loc.Find(d.target, d.processArgs, s, locStr, includeNonExecutableLines, substitutePathRules)
	for i := range locs {
		if locs[i].PC == 0 {
			continue
		}
		file, line, fn := d.target.BinInfo().PCToLine(locs[i].PC)
		locs[i].File = file
		locs[i].Line = line
		locs[i].Function = api.ConvertFunction(fn)
	}
	return locs, err
}

// Disassemble code between startPC and endPC.
// if endPC == 0 it will find the function containing startPC and disassemble the whole function.
func (d *Debugger) Disassemble(goroutineID int, addr1, addr2 uint64) ([]proc.AsmInstruction, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	if _, err := d.target.Valid(); err != nil {
		return nil, err
	}

	if addr2 == 0 {
		fn := d.target.BinInfo().PCToFunc(addr1)
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
	regs, _ := curthread.Registers()

	return proc.Disassemble(d.target.Memory(), regs, d.target.Breakpoints(), d.target.BinInfo(), addr1, addr2)
}

func (d *Debugger) AsmInstructionText(inst *proc.AsmInstruction, flavour proc.AssemblyFlavour) string {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
	return inst.Text(flavour, d.target.BinInfo())
}

// Recorded returns true if the target is a recording.
func (d *Debugger) Recorded() (recorded bool, tracedir string) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
	return d.target.Recorded()
}

// FindThreadReturnValues returns the return values of the function that
// the thread of the given 'id' just stepped out of.
func (d *Debugger) FindThreadReturnValues(id int, cfg proc.LoadConfig) ([]*proc.Variable, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	if _, err := d.target.Valid(); err != nil {
		return nil, err
	}

	thread, found := d.target.FindThread(id)
	if !found {
		return nil, fmt.Errorf("could not find thread %d", id)
	}

	return thread.Common().ReturnValues(cfg), nil
}

// Checkpoint will set a checkpoint specified by the locspec.
func (d *Debugger) Checkpoint(where string) (int, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
	return d.target.Checkpoint(where)
}

// Checkpoints will return a list of checkpoints.
func (d *Debugger) Checkpoints() ([]proc.Checkpoint, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
	return d.target.Checkpoints()
}

// ClearCheckpoint will clear the checkpoint of the given ID.
func (d *Debugger) ClearCheckpoint(id int) error {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
	return d.target.ClearCheckpoint(id)
}

// ListDynamicLibraries returns a list of loaded dynamic libraries.
func (d *Debugger) ListDynamicLibraries() []*proc.Image {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
	return d.target.BinInfo().Images[1:] // skips the first image because it's the executable file

}

// ExamineMemory returns the raw memory stored at the given address.
// The amount of data to be read is specified by length.
// This function will return an error if it reads less than `length` bytes.
func (d *Debugger) ExamineMemory(address uint64, length int) ([]byte, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	mem := d.target.Memory()
	data := make([]byte, length)
	n, err := mem.ReadMemory(data, address)
	if err != nil {
		return nil, err
	}
	if length != n {
		return nil, errors.New("the specific range has exceeded readable area")
	}
	return data, nil
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

	if !d.isRecording() && !d.IsRunning() {
		out.TargetGoVersion = d.target.BinInfo().Producer()
	}

	out.MinSupportedVersionOfGo = fmt.Sprintf("%d.%d.0", goversion.MinSupportedVersionOfGoMajor, goversion.MinSupportedVersionOfGoMinor)
	out.MaxSupportedVersionOfGo = fmt.Sprintf("%d.%d.0", goversion.MaxSupportedVersionOfGoMajor, goversion.MaxSupportedVersionOfGoMinor)

	return nil
}

// ListPackagesBuildInfo returns the list of packages used by the program along with
// the directory where each package was compiled and optionally the list of
// files constituting the package.
func (d *Debugger) ListPackagesBuildInfo(includeFiles bool) []*proc.PackageBuildInfo {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
	return d.target.BinInfo().ListPackagesBuildInfo(includeFiles)
}

// StopRecording stops a recording (if one is in progress)
func (d *Debugger) StopRecording() error {
	d.recordMutex.Lock()
	defer d.recordMutex.Unlock()
	if d.stopRecording == nil {
		return ErrNotRecording
	}
	return d.stopRecording()
}

// StopReason returns the reason the reason why the target process is stopped.
// A process could be stopped for multiple simultaneous reasons, in which
// case only one will be reported.
func (d *Debugger) StopReason() proc.StopReason {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
	return d.target.StopReason
}

// LockTarget acquires the target mutex.
func (d *Debugger) LockTarget() {
	d.targetMutex.Lock()
}

// UnlockTarget releases the target mutex.
func (d *Debugger) UnlockTarget() {
	d.targetMutex.Unlock()
}

// DumpStart starts a core dump to dest.
func (d *Debugger) DumpStart(dest string) error {
	d.targetMutex.Lock()
	// targetMutex will only be unlocked when the dump is done

	if !d.target.CanDump {
		d.targetMutex.Unlock()
		return ErrCoreDumpNotSupported
	}

	d.dumpState.Mutex.Lock()
	defer d.dumpState.Mutex.Unlock()

	if d.dumpState.Dumping {
		d.targetMutex.Unlock()
		return ErrCoreDumpInProgress
	}

	fh, err := os.Create(dest)
	if err != nil {
		d.targetMutex.Unlock()
		return err
	}

	d.dumpState.Dumping = true
	d.dumpState.AllDone = false
	d.dumpState.Canceled = false
	d.dumpState.DoneChan = make(chan struct{})
	d.dumpState.ThreadsDone = 0
	d.dumpState.ThreadsTotal = 0
	d.dumpState.MemDone = 0
	d.dumpState.MemTotal = 0
	d.dumpState.Err = nil
	go func() {
		defer d.targetMutex.Unlock()
		d.target.Dump(fh, 0, &d.dumpState)
	}()

	return nil
}

// DumpWait waits for the dump to finish, or for the duration of wait.
// Returns the state of the dump.
// If wait == 0 returns immediately.
func (d *Debugger) DumpWait(wait time.Duration) *proc.DumpState {
	d.dumpState.Mutex.Lock()
	if !d.dumpState.Dumping {
		d.dumpState.Mutex.Unlock()
		return &d.dumpState
	}
	d.dumpState.Mutex.Unlock()

	if wait > 0 {
		alarm := time.After(wait)
		select {
		case <-alarm:
		case <-d.dumpState.DoneChan:
		}
	}

	return &d.dumpState
}

// DumpCancel canels a dump in progress
func (d *Debugger) DumpCancel() error {
	d.dumpState.Mutex.Lock()
	d.dumpState.Canceled = true
	d.dumpState.Mutex.Unlock()
	return nil
}

func go11DecodeErrorCheck(err error) error {
	if _, isdecodeerr := err.(dwarf.DecodeError); !isdecodeerr {
		return err
	}

	gover, ok := goversion.Installed()
	if !ok || !gover.AfterOrEqual(goversion.GoVersion{Major: 1, Minor: 11, Rev: -1}) || goversion.VersionAfterOrEqual(runtime.Version(), 1, 11) {
		return err
	}

	return fmt.Errorf("executables built by Go 1.11 or later need Delve built by Go 1.11 or later")
}

type breakpointsByLogicalID []*proc.Breakpoint

func (v breakpointsByLogicalID) Len() int      { return len(v) }
func (v breakpointsByLogicalID) Swap(i, j int) { v[i], v[j] = v[j], v[i] }

func (v breakpointsByLogicalID) Less(i, j int) bool {
	if v[i].LogicalID == v[j].LogicalID {
		return v[i].Addr < v[j].Addr
	}
	return v[i].LogicalID < v[j].LogicalID
}
