package debugger

import (
	"debug/dwarf"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
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

	// ErrNotImplementedWithMultitarget is returned for operations that are not implemented with multiple targets
	ErrNotImplementedWithMultitarget = errors.New("not implemented for multiple targets")
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
	target      *proc.TargetGroup

	log logflags.Logger

	running      bool
	runningMutex sync.Mutex

	stopRecording func() error
	recordMutex   sync.Mutex

	dumpState proc.DumpState

	breakpointIDCounter int
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
	// If AttachWaitFor is set the debugger will wait for a process with a name
	// starting with WaitFor and attach to it.
	AttachWaitFor string
	// AttachWaitForInterval is the time (in milliseconds) that the debugger
	// waits between checks for WaitFor.
	AttachWaitForInterval float64
	// AttachWaitForDuration is the time (in milliseconds) that the debugger
	// waits for WaitFor.
	AttachWaitForDuration float64

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

	// Stdin Redirect file path for stdin
	Stdin string

	// Redirects specifies redirect rules for stdout
	Stdout proc.OutputRedirect

	// Redirects specifies redirect rules for stderr
	Stderr proc.OutputRedirect

	// DisableASLR disables ASLR
	DisableASLR bool

	RrOnProcessPid int
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
	case d.config.AttachPid > 0 || d.config.AttachWaitFor != "":
		d.log.Infof("attaching to pid %d", d.config.AttachPid)
		path := ""
		if len(d.processArgs) > 0 {
			path = d.processArgs[0]
		}
		var waitFor *proc.WaitFor
		if d.config.AttachWaitFor != "" {
			waitFor = &proc.WaitFor{
				Name:     d.config.AttachWaitFor,
				Interval: time.Duration(d.config.AttachWaitForInterval * float64(time.Millisecond)),
				Duration: time.Duration(d.config.AttachWaitForDuration * float64(time.Millisecond)),
			}
		}
		var err error
		d.target, err = d.Attach(d.config.AttachPid, path, waitFor)
		if err != nil {
			err = go11DecodeErrorCheck(err)
			err = noDebugErrorWarning(err)
			return nil, attachErrorMessage(d.config.AttachPid, err)
		}

	case d.config.CoreFile != "":
		var err error
		switch d.config.Backend {
		case "rr":
			d.log.Infof("opening trace %s", d.config.CoreFile)
			d.target, err = gdbserial.Replay(d.config.CoreFile, false, false, d.config.DebugInfoDirectories, d.config.RrOnProcessPid, "")
		default:
			d.log.Infof("opening core file %s (executable %s)", d.config.CoreFile, d.processArgs[0])
			d.target, err = core.OpenCore(d.config.CoreFile, d.processArgs[0], d.config.DebugInfoDirectories)
		}
		if err != nil {
			err = go11DecodeErrorCheck(err)
			return nil, err
		}
		if err := d.checkGoVersion(); err != nil {
			d.target.Detach(true)
			return nil, err
		}

	default:
		d.log.Infof("launching process with args: %v", d.processArgs)
		var err error
		d.target, err = d.Launch(d.processArgs, d.config.WorkingDir)
		if err != nil {
			if !errors.Is(err, &proc.ErrUnsupportedArch{}) {
				err = go11DecodeErrorCheck(err)
				err = noDebugErrorWarning(err)
				err = fmt.Errorf("could not launch process: %s", err)
			}
			return nil, err
		}
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
	if d.isRecording() {
		// do not do anything if we are still recording
		return nil
	}
	producer := d.target.Selected.BinInfo().Producer()
	if producer == "" {
		return nil
	}
	return goversion.Compatible(producer, !d.config.CheckGoVersion)
}

func (d *Debugger) TargetGoVersion() string {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
	return d.target.Selected.BinInfo().Producer()
}

// Launch will start a process with the given args and working directory.
func (d *Debugger) Launch(processArgs []string, wd string) (*proc.TargetGroup, error) {
	fullpath, err := verifyBinaryFormat(processArgs[0])
	if err != nil {
		return nil, err
	}
	processArgs[0] = fullpath

	launchFlags := proc.LaunchFlags(0)
	if d.config.Foreground {
		launchFlags |= proc.LaunchForeground
	}
	if d.config.DisableASLR {
		launchFlags |= proc.LaunchDisableASLR
	}

	switch d.config.Backend {
	case "native":
		return native.Launch(processArgs, wd, launchFlags, d.config.DebugInfoDirectories, d.config.TTY, d.config.Stdin, d.config.Stdout, d.config.Stderr)
	case "lldb":
		return betterGdbserialLaunchError(gdbserial.LLDBLaunch(processArgs, wd, launchFlags, d.config.DebugInfoDirectories, d.config.TTY, [3]string{d.config.Stdin, d.config.Stdout.Path, d.config.Stderr.Path}))
	case "rr":
		if d.target != nil {
			// restart should not call us if the backend is 'rr'
			panic("internal error: call to Launch with rr backend and target already exists")
		}

		run, stop, err := gdbserial.RecordAsync(processArgs, wd, false, d.config.Stdin, d.config.Stdout, d.config.Stderr)
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

			grp, err := d.recordingRun(run)
			if err != nil {
				d.log.Errorf("could not record target: %v", err)
				// this is ugly, but we can't respond to any client requests at this
				// point, so it's better if we die.
				os.Exit(1)
			}
			d.recordingDone()
			d.target = grp
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
			return betterGdbserialLaunchError(gdbserial.LLDBLaunch(processArgs, wd, launchFlags, d.config.DebugInfoDirectories, d.config.TTY, [3]string{d.config.Stdin, d.config.Stdout.Path, d.config.Stderr.Path}))
		}
		return native.Launch(processArgs, wd, launchFlags, d.config.DebugInfoDirectories, d.config.TTY, d.config.Stdin, d.config.Stdout, d.config.Stderr)
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

func (d *Debugger) recordingRun(run func() (string, error)) (*proc.TargetGroup, error) {
	tracedir, err := run()
	if err != nil && tracedir == "" {
		return nil, err
	}

	return gdbserial.Replay(tracedir, false, true, d.config.DebugInfoDirectories, 0, strings.Join(d.processArgs, " "))
}

// Attach will attach to the process specified by 'pid'.
func (d *Debugger) Attach(pid int, path string, waitFor *proc.WaitFor) (*proc.TargetGroup, error) {
	switch d.config.Backend {
	case "native":
		return native.Attach(pid, waitFor, d.config.DebugInfoDirectories)
	case "lldb":
		return betterGdbserialLaunchError(gdbserial.LLDBAttach(pid, path, waitFor, d.config.DebugInfoDirectories))
	case "default":
		if runtime.GOOS == "darwin" {
			return betterGdbserialLaunchError(gdbserial.LLDBAttach(pid, path, waitFor, d.config.DebugInfoDirectories))
		}
		return native.Attach(pid, waitFor, d.config.DebugInfoDirectories)
	default:
		return nil, fmt.Errorf("unknown backend %q", d.config.Backend)
	}
}

var errMacOSBackendUnavailable = errors.New("debugserver or lldb-server not found: install Xcode's command line tools or lldb-server")

func betterGdbserialLaunchError(p *proc.TargetGroup, err error) (*proc.TargetGroup, error) {
	if runtime.GOOS != "darwin" {
		return p, err
	}
	if !errors.Is(err, &gdbserial.ErrBackendUnavailable{}) {
		return p, err
	}

	return p, errMacOSBackendUnavailable
}

// ProcessPid returns the PID of the process
// the debugger is debugging.
func (d *Debugger) ProcessPid() int {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
	return d.target.Selected.Pid()
}

// LastModified returns the time that the process' executable was last
// modified.
func (d *Debugger) LastModified() time.Time {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
	return d.target.Selected.BinInfo().LastModified()
}

// FunctionReturnLocations returns all return locations
// for the given function, a list of addresses corresponding
// to 'ret' or 'call runtime.deferreturn'.
func (d *Debugger) FunctionReturnLocations(fnName string) ([]uint64, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	if len(d.target.Targets()) > 1 {
		return nil, ErrNotImplementedWithMultitarget
	}

	var (
		p = d.target.Selected
		g = p.SelectedGoroutine()
	)

	fns, err := p.BinInfo().FindFunction(fnName)
	if err != nil {
		return nil, err
	}

	var addrs []uint64

	for _, fn := range fns {
		var regs proc.Registers
		mem := p.Memory()
		if g != nil && g.Thread != nil {
			regs, _ = g.Thread.Registers()
		}
		instructions, err := proc.Disassemble(mem, regs, p.Breakpoints(), p.BinInfo(), fn.Entry, fn.End)
		if err != nil {
			return nil, err
		}

		for _, instruction := range instructions {
			if instruction.IsRet() {
				addrs = append(addrs, instruction.Loc.PC)
			}
		}
		addrs = append(addrs, proc.FindDeferReturnCalls(instructions)...)
	}

	return addrs, nil
}

// Detach detaches from the target process.
// If `kill` is true we will kill the process after
// detaching.
func (d *Debugger) Detach(kill bool) error {
	d.log.Debug("detaching")
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
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
		d.target.ResumeNotify(nil)
		return nil, d.target.Restart(pos)
	}

	if pos != "" {
		return nil, proc.ErrNotRecorded
	}

	if !d.canRestart() {
		return nil, ErrCanNotRestart
	}

	if !resetArgs && (d.config.Stdout.File != nil || d.config.Stderr.File != nil) {
		return nil, ErrCanNotRestart

	}

	if err := d.detach(true); err != nil {
		return nil, err
	}
	if resetArgs {
		d.processArgs = append([]string{d.processArgs[0]}, newArgs...)
		d.config.Stdin = newRedirects[0]
		d.config.Stdout = proc.OutputRedirect{Path: newRedirects[1]}
		d.config.Stderr = proc.OutputRedirect{Path: newRedirects[2]}
	}
	var grp *proc.TargetGroup
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
		run, stop, err2 := gdbserial.RecordAsync(d.processArgs, d.config.WorkingDir, false, d.config.Stdin, d.config.Stdout, d.config.Stderr)
		if err2 != nil {
			return nil, err2
		}

		d.recordingStart(stop)
		grp, err = d.recordingRun(run)
		d.recordingDone()
	} else {
		grp, err = d.Launch(d.processArgs, d.config.WorkingDir)
	}
	if err != nil {
		return nil, fmt.Errorf("could not launch process: %s", err)
	}

	discarded := []api.DiscardedBreakpoint{}
	proc.Restart(grp, d.target, func(oldBp *proc.LogicalBreakpoint, err error) {
		discarded = append(discarded, api.DiscardedBreakpoint{Breakpoint: api.ConvertLogicalBreakpoint(oldBp), Reason: err.Error()})
	})
	d.target = grp
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
	return d.state(nil, false)
}

func (d *Debugger) state(retLoadCfg *proc.LoadConfig, withBreakpointInfo bool) (*api.DebuggerState, error) {
	if _, err := d.target.Valid(); err != nil {
		return nil, err
	}

	var (
		state     *api.DebuggerState
		goroutine *api.Goroutine
	)

	tgt := d.target.Selected

	if tgt.SelectedGoroutine() != nil {
		goroutine = api.ConvertGoroutine(tgt, tgt.SelectedGoroutine())
	}

	exited := false
	if _, err := tgt.Valid(); err != nil {
		var errProcessExited proc.ErrProcessExited
		exited = errors.As(err, &errProcessExited)
	}

	state = &api.DebuggerState{
		Pid:               tgt.Pid(),
		TargetCommandLine: tgt.CmdLine,
		SelectedGoroutine: goroutine,
		Exited:            exited,
	}

	for _, thread := range d.target.ThreadList() {
		th := api.ConvertThread(thread, d.ConvertThreadBreakpoint(thread))

		th.CallReturn = thread.Common().CallReturn
		if retLoadCfg != nil {
			th.ReturnValues = api.ConvertVars(thread.Common().ReturnValues(*retLoadCfg))
		}

		if withBreakpointInfo {
			err := d.collectBreakpointInformation(th, thread)
			if err != nil {
				return nil, err
			}
		}

		state.Threads = append(state.Threads, th)
		if thread.ThreadID() == tgt.CurrentThread().ThreadID() {
			state.CurrentThread = th
		}
	}

	state.NextInProgress = d.target.HasSteppingBreakpoints()

	if recorded, _ := d.target.Recorded(); recorded {
		state.When, _ = d.target.When()
	}

	t := proc.ValidTargets{Group: d.target}
	for t.Next() {
		for _, bp := range t.Breakpoints().WatchOutOfScope {
			abp := api.ConvertLogicalBreakpoint(bp.Logical)
			api.ConvertPhysicalBreakpoints(abp, bp.Logical, []int{t.Pid()}, []*proc.Breakpoint{bp})
			state.WatchOutOfScope = append(state.WatchOutOfScope, abp)
		}
	}

	return state, nil
}

// CreateBreakpoint creates a breakpoint using information from the provided `requestedBp`.
// This function accepts several different ways of specifying where and how to create the
// breakpoint that has been requested. Any error encountered during the attempt to set the
// breakpoint will be returned to the caller.
//
// The ways of specifying a breakpoint are listed below in the order they are considered by
// this function:
//
// - If requestedBp.TraceReturn is true then it is expected that
// requestedBp.Addrs will contain the list of return addresses
// supplied by the caller.
//
// - If requestedBp.File is not an empty string the breakpoint
// will be created on the specified file:line location
//
// - If requestedBp.FunctionName is not an empty string
// the breakpoint will be created on the specified function:line
// location.
//
// - If requestedBp.Addrs is filled it will create a logical breakpoint
// corresponding to all specified addresses.
//
// - Otherwise the value specified by arg.Breakpoint.Addr will be used.
//
// Note that this method will use the first successful method in order to
// create a breakpoint, so mixing different fields will not result is multiple
// breakpoints being set.
//
// If LocExpr is specified it will be used, along with substitutePathRules,
// to re-enable the breakpoint after it is disabled.
//
// If suspended is true a logical breakpoint will be created even if the
// location can not be found, the backend will attempt to enable the
// breakpoint every time a new plugin is loaded.
func (d *Debugger) CreateBreakpoint(requestedBp *api.Breakpoint, locExpr string, substitutePathRules [][2]string, suspended bool) (*api.Breakpoint, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	var (
		setbp proc.SetBreakpoint
		err   error
	)

	if requestedBp.Name != "" {
		if d.findBreakpointByName(requestedBp.Name) != nil {
			return nil, errors.New("breakpoint name already exists")
		}
	}

	if lbp := d.target.LogicalBreakpoints[requestedBp.ID]; lbp != nil {
		abp := d.convertBreakpoint(lbp)
		return abp, proc.BreakpointExistsError{File: lbp.File, Line: lbp.Line}
	}

	switch {
	case requestedBp.TraceReturn:
		if len(d.target.Targets()) != 1 {
			return nil, ErrNotImplementedWithMultitarget
		}
		setbp.PidAddrs = []proc.PidAddr{{Pid: d.target.Selected.Pid(), Addr: requestedBp.Addr}}
	case len(requestedBp.File) > 0:
		fileName := requestedBp.File
		if runtime.GOOS == "windows" {
			// Accept fileName which is case-insensitive and slash-insensitive match
			fileNameNormalized := strings.ToLower(filepath.ToSlash(fileName))
			t := proc.ValidTargets{Group: d.target}
		caseInsensitiveSearch:
			for t.Next() {
				for _, symFile := range t.BinInfo().Sources {
					if fileNameNormalized == strings.ToLower(filepath.ToSlash(symFile)) {
						fileName = symFile
						break caseInsensitiveSearch
					}
				}
			}
		}
		setbp.File = fileName
		setbp.Line = requestedBp.Line
	case len(requestedBp.FunctionName) > 0:
		setbp.FunctionName = requestedBp.FunctionName
		setbp.Line = requestedBp.Line
	case len(requestedBp.Addrs) > 0:
		setbp.PidAddrs = make([]proc.PidAddr, len(requestedBp.Addrs))
		if len(d.target.Targets()) == 1 {
			pid := d.target.Selected.Pid()
			for i, addr := range requestedBp.Addrs {
				setbp.PidAddrs[i] = proc.PidAddr{Pid: pid, Addr: addr}
			}
		} else {
			if len(requestedBp.Addrs) != len(requestedBp.AddrPid) {
				return nil, errors.New("mismatched length in addrs and addrpid")
			}
			for i, addr := range requestedBp.Addrs {
				setbp.PidAddrs[i] = proc.PidAddr{Pid: requestedBp.AddrPid[i], Addr: addr}
			}
		}
	default:
		if requestedBp.Addr != 0 {
			setbp.PidAddrs = []proc.PidAddr{{Pid: d.target.Selected.Pid(), Addr: requestedBp.Addr}}
		}
	}

	if locExpr != "" {
		loc, err := locspec.Parse(locExpr)
		if err != nil {
			return nil, err
		}
		setbp.Expr = func(t *proc.Target) []uint64 {
			locs, _, err := loc.Find(t, d.processArgs, nil, locExpr, false, substitutePathRules)
			if err != nil || len(locs) != 1 {
				logflags.DebuggerLogger().Debugf("could not evaluate breakpoint expression %q: %v (number of results %d)", locExpr, err, len(locs))
				return nil
			}
			return locs[0].PCs
		}
		setbp.ExprString = locExpr
	}

	id := requestedBp.ID

	if id <= 0 {
		d.breakpointIDCounter++
		id = d.breakpointIDCounter
	} else {
		d.breakpointIDCounter = id
	}

	lbp := &proc.LogicalBreakpoint{LogicalID: id, HitCount: make(map[int64]uint64), Enabled: true}
	d.target.LogicalBreakpoints[id] = lbp

	err = copyLogicalBreakpointInfo(lbp, requestedBp)
	if err != nil {
		return nil, err
	}

	lbp.Set = setbp

	if lbp.Set.Expr != nil {
		addrs := lbp.Set.Expr(d.Target())
		if len(addrs) > 0 {
			f, l, fn := d.Target().BinInfo().PCToLine(addrs[0])
			lbp.File = f
			lbp.Line = l
			if fn != nil {
				lbp.FunctionName = fn.Name
			}
		}
	}

	err = d.target.EnableBreakpoint(lbp)
	if err != nil {
		if suspended {
			logflags.DebuggerLogger().Debugf("could not enable new breakpoint: %v (breakpoint will be suspended)", err)
		} else {
			delete(d.target.LogicalBreakpoints, lbp.LogicalID)
			return nil, err
		}
	}

	createdBp := d.convertBreakpoint(lbp)
	d.log.Infof("created breakpoint: %#v", createdBp)
	return createdBp, nil
}

func (d *Debugger) convertBreakpoint(lbp *proc.LogicalBreakpoint) *api.Breakpoint {
	abp := api.ConvertLogicalBreakpoint(lbp)
	bps := []*proc.Breakpoint{}
	pids := []int{}
	t := proc.ValidTargets{Group: d.target}
	for t.Next() {
		for _, bp := range t.Breakpoints().M {
			if bp.LogicalID() == lbp.LogicalID {
				bps = append(bps, bp)
				pids = append(pids, t.Pid())
			}
		}
	}
	api.ConvertPhysicalBreakpoints(abp, lbp, pids, bps)
	return abp
}

func (d *Debugger) ConvertThreadBreakpoint(thread proc.Thread) *api.Breakpoint {
	if b := thread.Breakpoint(); b.Active && b.Breakpoint.Logical != nil {
		return d.convertBreakpoint(b.Breakpoint.Logical)
	}
	return nil
}

func (d *Debugger) CreateEBPFTracepoint(fnName string) error {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
	if len(d.target.Targets()) != 1 {
		return ErrNotImplementedWithMultitarget
	}
	p := d.target.Selected
	return p.SetEBPFTracepoint(fnName)
}

// amendBreakpoint will update the breakpoint with the matching ID.
// It also enables or disables the breakpoint.
// We can consume this function to avoid locking a goroutine.
func (d *Debugger) amendBreakpoint(amend *api.Breakpoint) error {
	original := d.target.LogicalBreakpoints[amend.ID]
	if original == nil {
		return fmt.Errorf("no breakpoint with ID %d", amend.ID)
	}
	enabledBefore := original.Enabled
	err := copyLogicalBreakpointInfo(original, amend)
	if err != nil {
		return err
	}
	original.Enabled = !amend.Disabled

	switch {
	case enabledBefore && !original.Enabled:
		if d.isWatchpoint(original) {
			return errors.New("can not disable watchpoints")
		}
		err = d.target.DisableBreakpoint(original)
	case !enabledBefore && original.Enabled:
		err = d.target.EnableBreakpoint(original)
	}
	if err != nil {
		return err
	}

	t := proc.ValidTargets{Group: d.target}
	for t.Next() {
		for _, bp := range t.Breakpoints().M {
			if bp.LogicalID() == amend.ID {
				bp.UserBreaklet().Cond = original.Cond
			}
		}
	}
	return nil
}

func (d *Debugger) isWatchpoint(lbp *proc.LogicalBreakpoint) bool {
	t := proc.ValidTargets{Group: d.target}
	for t.Next() {
		for _, bp := range t.Breakpoints().M {
			if bp.LogicalID() == lbp.LogicalID {
				return bp.WatchType != 0
			}
		}
	}
	return false
}

// AmendBreakpoint will update the breakpoint with the matching ID.
// It also enables or disables the breakpoint.
func (d *Debugger) AmendBreakpoint(amend *api.Breakpoint) error {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	return d.amendBreakpoint(amend)
}

// CancelNext will clear internal breakpoints, thus cancelling the 'next',
// 'step' or 'stepout' operation.
func (d *Debugger) CancelNext() error {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
	return d.target.ClearSteppingBreakpoints()
}

func copyLogicalBreakpointInfo(lbp *proc.LogicalBreakpoint, requested *api.Breakpoint) error {
	lbp.Name = requested.Name
	lbp.Tracepoint = requested.Tracepoint
	lbp.TraceReturn = requested.TraceReturn
	lbp.Goroutine = requested.Goroutine
	lbp.Stacktrace = requested.Stacktrace
	lbp.Variables = requested.Variables
	lbp.LoadArgs = api.LoadConfigToProc(requested.LoadArgs)
	lbp.LoadLocals = api.LoadConfigToProc(requested.LoadLocals)
	lbp.UserData = requested.UserData
	lbp.Cond = nil
	if requested.Cond != "" {
		var err error
		lbp.Cond, err = parser.ParseExpr(requested.Cond)
		if err != nil {
			return err
		}
	}

	lbp.HitCond = nil
	if requested.HitCond != "" {
		opTok, val, err := parseHitCondition(requested.HitCond)
		if err != nil {
			return err
		}
		lbp.HitCond = &struct {
			Op  token.Token
			Val int
		}{opTok, val}
		lbp.HitCondPerG = requested.HitCondPerG
	}

	return nil
}

func parseHitCondition(hitCond string) (token.Token, int, error) {
	// A hit condition can be in the following formats:
	// - "number"
	// - "OP number"
	hitConditionRegex := regexp.MustCompile(`(([=><%!])+|)( |)((\d|_)+)`)

	match := hitConditionRegex.FindStringSubmatch(strings.TrimSpace(hitCond))
	if match == nil || len(match) != 6 {
		return 0, 0, fmt.Errorf("unable to parse breakpoint hit condition: %q\nhit conditions should be of the form \"number\" or \"OP number\"", hitCond)
	}

	opStr := match[1]
	var opTok token.Token
	switch opStr {
	case "==", "":
		opTok = token.EQL
	case ">=":
		opTok = token.GEQ
	case "<=":
		opTok = token.LEQ
	case ">":
		opTok = token.GTR
	case "<":
		opTok = token.LSS
	case "%":
		opTok = token.REM
	case "!=":
		opTok = token.NEQ
	default:
		return 0, 0, fmt.Errorf("unable to parse breakpoint hit condition: %q\ninvalid operator: %q", hitCond, opStr)
	}

	numStr := match[4]
	val, parseErr := strconv.Atoi(numStr)
	if parseErr != nil {
		return 0, 0, fmt.Errorf("unable to parse breakpoint hit condition: %q\ninvalid number: %q", hitCond, numStr)
	}

	return opTok, val, nil
}

// ClearBreakpoint clears a breakpoint.
func (d *Debugger) ClearBreakpoint(requestedBp *api.Breakpoint) (*api.Breakpoint, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
	if requestedBp.ID <= 0 {
		if len(d.target.Targets()) != 1 {
			return nil, ErrNotImplementedWithMultitarget
		}
		bp := d.target.Selected.Breakpoints().M[requestedBp.Addr]
		requestedBp.ID = bp.LogicalID()
	}

	lbp := d.target.LogicalBreakpoints[requestedBp.ID]
	clearedBp := d.convertBreakpoint(lbp)

	err := d.target.DisableBreakpoint(lbp)
	if err != nil {
		return nil, err
	}

	delete(d.target.LogicalBreakpoints, requestedBp.ID)

	d.log.Infof("cleared breakpoint: %#v", clearedBp)
	return clearedBp, nil
}

// isBpHitCondNotSatisfiable returns true if the breakpoint bp has a hit
// condition that is no more satisfiable.
// The hit condition is considered no more satisfiable if it can no longer be
// hit again, for example with {Op: "==", Val: 1} and TotalHitCount == 1.
func isBpHitCondNotSatisfiable(bp *api.Breakpoint) bool {
	if bp.HitCond == "" {
		return false
	}

	tok, val, err := parseHitCondition(bp.HitCond)
	if err != nil {
		return false
	}
	switch tok {
	case token.EQL, token.LEQ:
		if int(bp.TotalHitCount) >= val {
			return true
		}
	case token.LSS:
		if int(bp.TotalHitCount) >= val-1 {
			return true
		}
	}

	return false
}

// Breakpoints returns the list of current breakpoints.
func (d *Debugger) Breakpoints(all bool) []*api.Breakpoint {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	abps := []*api.Breakpoint{}
	if all {
		t := proc.ValidTargets{Group: d.target}
		for t.Next() {
			for _, bp := range t.Breakpoints().M {
				var abp *api.Breakpoint
				if bp.Logical != nil {
					abp = api.ConvertLogicalBreakpoint(bp.Logical)
				} else {
					abp = &api.Breakpoint{}
				}
				api.ConvertPhysicalBreakpoints(abp, bp.Logical, []int{t.Pid()}, []*proc.Breakpoint{bp})
				abp.VerboseDescr = bp.VerboseDescr()
				abps = append(abps, abp)
			}
		}
	} else {
		for _, lbp := range d.target.LogicalBreakpoints {
			abps = append(abps, d.convertBreakpoint(lbp))
		}
	}
	return abps
}

// FindBreakpoint returns the breakpoint specified by 'id'.
func (d *Debugger) FindBreakpoint(id int) *api.Breakpoint {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
	lbp := d.target.LogicalBreakpoints[id]
	if lbp == nil {
		return nil
	}
	return d.convertBreakpoint(lbp)
}

// FindBreakpointByName returns the breakpoint specified by 'name'
func (d *Debugger) FindBreakpointByName(name string) *api.Breakpoint {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
	return d.findBreakpointByName(name)
}

func (d *Debugger) findBreakpointByName(name string) *api.Breakpoint {
	for _, lbp := range d.target.LogicalBreakpoints {
		if lbp.Name == name {
			return d.convertBreakpoint(lbp)
		}
	}
	return nil
}

// CreateWatchpoint creates a watchpoint on the specified expression.
func (d *Debugger) CreateWatchpoint(goid int64, frame, deferredCall int, expr string, wtype api.WatchType) (*api.Breakpoint, error) {
	p := d.target.Selected

	s, err := proc.ConvertEvalScope(p, goid, frame, deferredCall)
	if err != nil {
		return nil, err
	}
	d.breakpointIDCounter++
	bp, err := p.SetWatchpoint(d.breakpointIDCounter, s, expr, proc.WatchType(wtype), nil)
	if err != nil {
		return nil, err
	}
	if d.findBreakpointByName(expr) == nil {
		bp.Logical.Name = expr
	}
	return d.convertBreakpoint(bp.Logical), nil
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

// FindGoroutine returns the goroutine for the given 'id'.
func (d *Debugger) FindGoroutine(id int64) (*proc.G, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	return proc.FindGoroutine(d.target.Selected, id)
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
			// The error returned from d.target.Valid will have more context
			// about the exited process.
			if _, valErr := d.target.Valid(); valErr != nil {
				err = valErr
			}
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
		g := d.target.Selected.SelectedGoroutine()
		if command.GoroutineID > 0 {
			g, err = proc.FindGoroutine(d.target.Selected, command.GoroutineID)
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
		t := proc.ValidTargets{Group: d.target}
		for t.Next() {
			if _, ok := t.FindThread(command.ThreadID); ok {
				d.target.Selected = t.Target
				break
			}
		}
		err = d.target.Selected.SwitchThread(command.ThreadID)
		withBreakpointInfo = false
	case api.SwitchGoroutine:
		d.log.Debugf("switching to goroutine %d", command.GoroutineID)
		var g *proc.G
		g, err = proc.FindGoroutine(d.target.Selected, command.GoroutineID)
		if err == nil {
			err = d.target.Selected.SwitchGoroutine(g)
		}
		withBreakpointInfo = false
	case api.Halt:
		// RequestManualStop already called
		withBreakpointInfo = false
	}

	if err != nil {
		var errProcessExited proc.ErrProcessExited
		if errors.As(err, &errProcessExited) && command.Name != api.SwitchGoroutine && command.Name != api.SwitchThread {
			state := &api.DebuggerState{}
			state.Pid = d.target.Selected.Pid()
			state.Exited = true
			state.ExitStatus = errProcessExited.Status
			state.Err = errProcessExited
			return state, nil
		}
		return nil, err
	}
	state, stateErr := d.state(api.LoadConfigToProc(command.ReturnInfoLoadConfig), withBreakpointInfo)
	if stateErr != nil {
		return state, stateErr
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
	if bp := state.CurrentThread.Breakpoint; bp != nil && isBpHitCondNotSatisfiable(bp) {
		bp.Disabled = true
		d.amendBreakpoint(bp)
	}
	return state, err
}

func (d *Debugger) collectBreakpointInformation(apiThread *api.Thread, thread proc.Thread) error {
	if apiThread.Breakpoint == nil || apiThread.BreakpointInfo != nil {
		return nil
	}

	bp := apiThread.Breakpoint
	bpi := &api.BreakpointInfo{}
	apiThread.BreakpointInfo = bpi

	tgt := d.target.TargetForThread(thread.ThreadID())

	// If we're dealing with a stripped binary don't attempt to load more
	// information, we won't be able to.
	img := tgt.BinInfo().PCToImage(bp.Addr)
	if img != nil && img.Stripped() {
		return nil
	}

	if bp.Goroutine {
		g, err := proc.GetG(thread)
		if err != nil {
			return err
		}
		bpi.Goroutine = api.ConvertGoroutine(tgt, g)
	}

	if bp.Stacktrace > 0 {
		rawlocs, err := proc.ThreadStacktrace(tgt, thread, bp.Stacktrace)
		if err != nil {
			return err
		}
		bpi.Stacktrace, err = d.convertStacktrace(rawlocs, nil)
		if err != nil {
			return err
		}
	}

	if len(bp.Variables) == 0 && bp.LoadArgs == nil && bp.LoadLocals == nil {
		// don't try to create goroutine scope if there is nothing to load
		return nil
	}

	s, err := proc.GoroutineScope(tgt, thread)
	if err != nil {
		return err
	}

	if len(bp.Variables) > 0 {
		bpi.Variables = make([]api.Variable, len(bp.Variables))
	}
	for i := range bp.Variables {
		v, err := s.EvalExpression(bp.Variables[i], proc.LoadConfig{FollowPointers: true, MaxVariableRecurse: 1, MaxStringLen: 64, MaxArrayValues: 64, MaxStructFields: -1})
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
	t := proc.ValidTargets{Group: d.target}
	for t.Next() {
		for _, f := range t.BinInfo().Sources {
			if regex.MatchString(f) {
				files = append(files, f)
			}
		}
	}
	sort.Strings(files)
	files = uniq(files)
	return files, nil
}

func uniq(s []string) []string {
	if len(s) == 0 {
		return s
	}
	src, dst := 1, 1
	for src < len(s) {
		if s[src] != s[dst-1] {
			s[dst] = s[src]
			dst++
		}
		src++
	}
	return s[:dst]
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
	t := proc.ValidTargets{Group: d.target}
	for t.Next() {
		for _, f := range t.BinInfo().Functions {
			if regex.MatchString(f.Name) {
				funcs = append(funcs, f.Name)
			}
		}
	}
	sort.Strings(funcs)
	funcs = uniq(funcs)
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

	r := []string{}

	t := proc.ValidTargets{Group: d.target}
	for t.Next() {
		types, err := t.BinInfo().Types()
		if err != nil {
			return nil, err
		}

		for _, typ := range types {
			if regex.MatchString(typ) {
				r = append(r, typ)
			}
		}
	}
	sort.Strings(r)
	r = uniq(r)

	return r, nil
}

// PackageVariables returns a list of package variables for the thread,
// optionally regexp filtered using regexp described in 'filter'.
func (d *Debugger) PackageVariables(filter string, cfg proc.LoadConfig) ([]*proc.Variable, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
	p := d.target.Selected

	regex, err := regexp.Compile(filter)
	if err != nil {
		return nil, fmt.Errorf("invalid filter argument: %s", err.Error())
	}

	scope, err := proc.ThreadScope(p, p.CurrentThread())
	if err != nil {
		return nil, err
	}
	pv, err := scope.PackageVariables(cfg)
	if err != nil {
		return nil, err
	}
	pvr := pv[:0]
	for i := range pv {
		if regex.MatchString(pv[i].Name) {
			pvr = append(pvr, pv[i])
		}
	}
	return pvr, nil
}

// ThreadRegisters returns registers of the specified thread.
func (d *Debugger) ThreadRegisters(threadID int) (*op.DwarfRegisters, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	thread, found := d.target.Selected.FindThread(threadID)
	if !found {
		return nil, fmt.Errorf("couldn't find thread %d", threadID)
	}
	regs, err := thread.Registers()
	if err != nil {
		return nil, err
	}
	return d.target.Selected.BinInfo().Arch.RegistersToDwarfRegisters(0, regs), nil
}

// ScopeRegisters returns registers for the specified scope.
func (d *Debugger) ScopeRegisters(goid int64, frame, deferredCall int) (*op.DwarfRegisters, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	s, err := proc.ConvertEvalScope(d.target.Selected, goid, frame, deferredCall)
	if err != nil {
		return nil, err
	}
	return &s.Regs, nil
}

// DwarfRegisterToString returns the name and value representation of the given register.
func (d *Debugger) DwarfRegisterToString(i int, reg *op.DwarfRegister) (string, bool, string) {
	return d.target.Selected.BinInfo().Arch.DwarfRegisterToString(i, reg)
}

// LocalVariables returns a list of the local variables.
func (d *Debugger) LocalVariables(goid int64, frame, deferredCall int, cfg proc.LoadConfig) ([]*proc.Variable, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	s, err := proc.ConvertEvalScope(d.target.Selected, goid, frame, deferredCall)
	if err != nil {
		return nil, err
	}
	return s.LocalVariables(cfg)
}

// FunctionArguments returns the arguments to the current function.
func (d *Debugger) FunctionArguments(goid int64, frame, deferredCall int, cfg proc.LoadConfig) ([]*proc.Variable, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	s, err := proc.ConvertEvalScope(d.target.Selected, goid, frame, deferredCall)
	if err != nil {
		return nil, err
	}
	return s.FunctionArguments(cfg)
}

// Function returns the current function.
func (d *Debugger) Function(goid int64, frame, deferredCall int) (*proc.Function, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	s, err := proc.ConvertEvalScope(d.target.Selected, goid, frame, deferredCall)
	if err != nil {
		return nil, err
	}
	return s.Fn, nil
}

// EvalVariableInScope will attempt to evaluate the 'expr' in the scope
// corresponding to the given 'frame' on the goroutine identified by 'goid'.
func (d *Debugger) EvalVariableInScope(goid int64, frame, deferredCall int, expr string, cfg proc.LoadConfig) (*proc.Variable, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	s, err := proc.ConvertEvalScope(d.target.Selected, goid, frame, deferredCall)
	if err != nil {
		return nil, err
	}
	return s.EvalExpression(expr, cfg)
}

// LoadResliced will attempt to 'reslice' a map, array or slice so that the values
// up to cfg.MaxArrayValues children are loaded starting from index start.
func (d *Debugger) LoadResliced(v *proc.Variable, start int, cfg proc.LoadConfig) (*proc.Variable, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
	return v.LoadResliced(start, cfg)
}

// SetVariableInScope will set the value of the variable represented by
// 'symbol' to the value given, in the given scope.
func (d *Debugger) SetVariableInScope(goid int64, frame, deferredCall int, symbol, value string) error {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	s, err := proc.ConvertEvalScope(d.target.Selected, goid, frame, deferredCall)
	if err != nil {
		return err
	}
	return s.SetVariable(symbol, value)
}

// Goroutines will return a list of goroutines in the target process.
func (d *Debugger) Goroutines(start, count int) ([]*proc.G, int, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
	return proc.GoroutinesInfo(d.target.Selected, start, count)
}

// FilterGoroutines returns the goroutines in gs that satisfy the specified filters.
func (d *Debugger) FilterGoroutines(gs []*proc.G, filters []api.ListGoroutinesFilter) []*proc.G {
	if len(filters) == 0 {
		return gs
	}
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
	r := []*proc.G{}
	for _, g := range gs {
		ok := true
		for i := range filters {
			if !matchGoroutineFilter(d.target.Selected, g, &filters[i]) {
				ok = false
				break
			}
		}
		if ok {
			r = append(r, g)
		}
	}
	return r
}

func matchGoroutineFilter(tgt *proc.Target, g *proc.G, filter *api.ListGoroutinesFilter) bool {
	var val bool
	switch filter.Kind {
	default:
		fallthrough
	case api.GoroutineFieldNone:
		val = true
	case api.GoroutineCurrentLoc:
		val = matchGoroutineLocFilter(g.CurrentLoc, filter.Arg)
	case api.GoroutineUserLoc:
		val = matchGoroutineLocFilter(g.UserCurrent(), filter.Arg)
	case api.GoroutineGoLoc:
		val = matchGoroutineLocFilter(g.Go(), filter.Arg)
	case api.GoroutineStartLoc:
		val = matchGoroutineLocFilter(g.StartLoc(tgt), filter.Arg)
	case api.GoroutineLabel:
		idx := strings.Index(filter.Arg, "=")
		if idx >= 0 {
			val = g.Labels()[filter.Arg[:idx]] == filter.Arg[idx+1:]
		} else {
			_, val = g.Labels()[filter.Arg]
		}
	case api.GoroutineRunning:
		val = g.Thread != nil
	case api.GoroutineUser:
		val = !g.System(tgt)
	case api.GoroutineWaitingOnChannel:
		val = true // handled elsewhere
	}
	if filter.Negated {
		val = !val
	}
	return val
}

func matchGoroutineLocFilter(loc proc.Location, arg string) bool {
	return strings.Contains(formatLoc(loc), arg)
}

func formatLoc(loc proc.Location) string {
	fnname := "?"
	if loc.Fn != nil {
		fnname = loc.Fn.Name
	}
	return fmt.Sprintf("%s:%d in %s", loc.File, loc.Line, fnname)
}

// GroupGoroutines divides goroutines in gs into groups as specified by
// group.{GroupBy,GroupByKey}. A maximum of group.MaxGroupMembers are saved in
// each group, but the total number of goroutines in each group is recorded. If
// group.MaxGroups is set, then at most that many groups are returned. If some
// groups end up being dropped because of this limit, the tooManyGroups return
// value is set.
//
// The first return value represents the goroutines that have been included in
// one of the returned groups (subject to the MaxGroupMembers and MaxGroups
// limits). The second return value represents the groups.
func (d *Debugger) GroupGoroutines(gs []*proc.G, group *api.GoroutineGroupingOptions) ([]*proc.G, []api.GoroutineGroup, bool) {
	if group.GroupBy == api.GoroutineFieldNone {
		return gs, nil, false
	}
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	groupMembers := map[string][]*proc.G{}
	totals := map[string]int{}

	for _, g := range gs {
		var key string
		switch group.GroupBy {
		case api.GoroutineCurrentLoc:
			key = formatLoc(g.CurrentLoc)
		case api.GoroutineUserLoc:
			key = formatLoc(g.UserCurrent())
		case api.GoroutineGoLoc:
			key = formatLoc(g.Go())
		case api.GoroutineStartLoc:
			key = formatLoc(g.StartLoc(d.target.Selected))
		case api.GoroutineLabel:
			key = fmt.Sprintf("%s=%s", group.GroupByKey, g.Labels()[group.GroupByKey])
		case api.GoroutineRunning:
			key = fmt.Sprintf("running=%v", g.Thread != nil)
		case api.GoroutineUser:
			key = fmt.Sprintf("user=%v", !g.System(d.target.Selected))
		}
		if len(groupMembers[key]) < group.MaxGroupMembers {
			groupMembers[key] = append(groupMembers[key], g)
		}
		totals[key]++
	}

	keys := make([]string, 0, len(groupMembers))
	for key := range groupMembers {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	tooManyGroups := false
	gsout := []*proc.G{}
	groups := []api.GoroutineGroup{}
	for _, key := range keys {
		if group.MaxGroups > 0 && len(groups) >= group.MaxGroups {
			tooManyGroups = true
			break
		}
		groups = append(groups, api.GoroutineGroup{Name: key, Offset: len(gsout), Count: len(groupMembers[key]), Total: totals[key]})
		gsout = append(gsout, groupMembers[key]...)
	}
	return gsout, groups, tooManyGroups
}

// Stacktrace returns a list of Stackframes for the given goroutine. The
// length of the returned list will be min(stack_len, depth).
// If 'full' is true, then local vars, function args, etc. will be returned as well.
func (d *Debugger) Stacktrace(goroutineID int64, depth int, opts api.StacktraceOptions) ([]proc.Stackframe, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	if _, err := d.target.Valid(); err != nil {
		return nil, err
	}

	g, err := proc.FindGoroutine(d.target.Selected, goroutineID)
	if err != nil {
		return nil, err
	}

	if g == nil {
		return proc.ThreadStacktrace(d.target.Selected, d.target.Selected.CurrentThread(), depth)
	} else {
		return proc.GoroutineStacktrace(d.target.Selected, g, depth, proc.StacktraceOptions(opts))
	}
}

// Ancestors returns the stacktraces for the ancestors of a goroutine.
func (d *Debugger) Ancestors(goroutineID int64, numAncestors, depth int) ([]api.Ancestor, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	if _, err := d.target.Valid(); err != nil {
		return nil, err
	}

	g, err := proc.FindGoroutine(d.target.Selected, goroutineID)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, errors.New("no selected goroutine")
	}

	ancestors, err := proc.Ancestors(d.target.Selected, g, numAncestors)
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
			scope := proc.FrameToScope(d.target.Selected, d.target.Selected.Memory(), nil, 0, rawlocs[i:]...)
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
		ddf, ddl, ddfn := defers[i].DeferredFunc(d.target.Selected)
		drf, drl, drfn := d.target.Selected.BinInfo().PCToLine(defers[i].DeferPC)

		if defers[i].Unreadable != nil {
			r[i].Unreadable = defers[i].Unreadable.Error()
		} else {
			var entry = defers[i].DeferPC
			if ddfn != nil {
				entry = ddfn.Entry
			}
			r[i] = api.Defer{
				DeferredLoc: api.ConvertLocation(proc.Location{
					PC:   entry,
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
	loc, err := d.target.Selected.CurrentThread().Location()
	if err != nil {
		return "", err
	}
	if loc.Fn == nil {
		return "", fmt.Errorf("unable to determine current package due to unspecified function location")
	}
	return loc.Fn.PackageName(), nil
}

// FindLocation will find the location specified by 'locStr'.
func (d *Debugger) FindLocation(goid int64, frame, deferredCall int, locStr string, includeNonExecutableLines bool, substitutePathRules [][2]string) ([]api.Location, string, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	if _, err := d.target.Valid(); err != nil {
		return nil, "", err
	}

	loc, err := locspec.Parse(locStr)
	if err != nil {
		return nil, "", err
	}

	return d.findLocation(goid, frame, deferredCall, locStr, loc, includeNonExecutableLines, substitutePathRules)
}

// FindLocationSpec will find the location specified by 'locStr' and 'locSpec'.
// 'locSpec' should be the result of calling 'locspec.Parse(locStr)'. 'locStr'
// is also passed, because it made be used to broaden the search criteria, if
// the parsed result did not find anything.
func (d *Debugger) FindLocationSpec(goid int64, frame, deferredCall int, locStr string, locSpec locspec.LocationSpec, includeNonExecutableLines bool, substitutePathRules [][2]string) ([]api.Location, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	if _, err := d.target.Valid(); err != nil {
		return nil, err
	}

	locs, _, err := d.findLocation(goid, frame, deferredCall, locStr, locSpec, includeNonExecutableLines, substitutePathRules)
	return locs, err
}

func (d *Debugger) findLocation(goid int64, frame, deferredCall int, locStr string, locSpec locspec.LocationSpec, includeNonExecutableLines bool, substitutePathRules [][2]string) ([]api.Location, string, error) {
	locations := []api.Location{}
	t := proc.ValidTargets{Group: d.target}
	subst := ""
	for t.Next() {
		pid := t.Pid()
		s, _ := proc.ConvertEvalScope(t.Target, goid, frame, deferredCall)
		locs, s1, err := locSpec.Find(t.Target, d.processArgs, s, locStr, includeNonExecutableLines, substitutePathRules)
		if s1 != "" {
			subst = s1
		}
		if err != nil {
			return nil, "", err
		}
		for i := range locs {
			if locs[i].PC == 0 {
				continue
			}
			file, line, fn := t.BinInfo().PCToLine(locs[i].PC)
			locs[i].File = file
			locs[i].Line = line
			locs[i].Function = api.ConvertFunction(fn)
			locs[i].PCPids = make([]int, len(locs[i].PCs))
			for j := range locs[i].PCs {
				locs[i].PCPids[j] = pid
			}
		}
		locations = append(locations, locs...)
	}
	return locations, subst, nil
}

// Disassemble code between startPC and endPC.
// if endPC == 0 it will find the function containing startPC and disassemble the whole function.
func (d *Debugger) Disassemble(goroutineID int64, addr1, addr2 uint64) ([]proc.AsmInstruction, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	if _, err := d.target.Valid(); err != nil {
		return nil, err
	}

	if addr2 == 0 {
		fn := d.target.Selected.BinInfo().PCToFunc(addr1)
		if fn == nil {
			return nil, fmt.Errorf("address %#x does not belong to any function", addr1)
		}
		addr1 = fn.Entry
		addr2 = fn.End
	}

	g, err := proc.FindGoroutine(d.target.Selected, goroutineID)
	if err != nil {
		return nil, err
	}

	curthread := d.target.Selected.CurrentThread()
	if g != nil && g.Thread != nil {
		curthread = g.Thread
	}
	regs, _ := curthread.Registers()

	return proc.Disassemble(d.target.Selected.Memory(), regs, d.target.Selected.Breakpoints(), d.target.Selected.BinInfo(), addr1, addr2)
}

func (d *Debugger) AsmInstructionText(inst *proc.AsmInstruction, flavour proc.AssemblyFlavour) string {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
	return inst.Text(flavour, d.target.Selected.BinInfo())
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

	thread, found := d.target.Selected.FindThread(id)
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
	return d.target.Selected.BinInfo().Images[1:] // skips the first image because it's the executable file
}

// ExamineMemory returns the raw memory stored at the given address.
// The amount of data to be read is specified by length.
// This function will return an error if it reads less than `length` bytes.
func (d *Debugger) ExamineMemory(address uint64, length int) ([]byte, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()

	mem := d.target.Selected.Memory()
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
		out.TargetGoVersion = d.target.Selected.BinInfo().Producer()
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
	return d.target.Selected.BinInfo().ListPackagesBuildInfo(includeFiles)
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

// StopReason returns the reason why the target process is stopped.
// A process could be stopped for multiple simultaneous reasons, in which
// case only one will be reported.
func (d *Debugger) StopReason() proc.StopReason {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
	return d.target.Selected.StopReason
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

	//TODO(aarzilli): what do we do if the user switches to a different target after starting a dump but before it's finished?

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
		d.target.Selected.Dump(fh, 0, &d.dumpState)
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

// DumpCancel cancels a dump in progress
func (d *Debugger) DumpCancel() error {
	d.dumpState.Mutex.Lock()
	d.dumpState.Canceled = true
	d.dumpState.Mutex.Unlock()
	return nil
}

func (d *Debugger) Target() *proc.Target {
	return d.target.Selected
}

func (d *Debugger) TargetGroup() *proc.TargetGroup {
	return d.target
}

func (d *Debugger) BuildID() string {
	return d.target.Selected.BinInfo().BuildID
}

func (d *Debugger) AttachPid() int {
	return d.config.AttachPid
}

func (d *Debugger) GetBufferedTracepoints() []api.TracepointResult {
	traces := d.target.Selected.GetBufferedTracepoints()
	if traces == nil {
		return nil
	}
	results := make([]api.TracepointResult, len(traces))
	for i, trace := range traces {
		results[i].IsRet = trace.IsRet

		f, l, fn := d.target.Selected.BinInfo().PCToLine(uint64(trace.FnAddr))

		results[i].FunctionName = fn.Name
		results[i].Line = l
		results[i].File = f
		results[i].GoroutineID = trace.GoroutineID

		for _, p := range trace.InputParams {
			results[i].InputParams = append(results[i].InputParams, *api.ConvertVar(p))
		}
		for _, p := range trace.ReturnParams {
			results[i].ReturnParams = append(results[i].ReturnParams, *api.ConvertVar(p))
		}
	}
	return results
}

// FollowExec enabled or disables follow exec mode.
func (d *Debugger) FollowExec(enabled bool, regex string) error {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
	return d.target.FollowExec(enabled, regex)
}

// FollowExecEnabled returns true if follow exec mode is enabled.
func (d *Debugger) FollowExecEnabled() bool {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
	return d.target.FollowExecEnabled()
}

func (d *Debugger) SetDebugInfoDirectories(v []string) {
	d.recordMutex.Lock()
	defer d.recordMutex.Unlock()
	it := proc.ValidTargets{Group: d.target}
	for it.Next() {
		it.BinInfo().DebugInfoDirectories = v
	}
}

func (d *Debugger) DebugInfoDirectories() []string {
	d.recordMutex.Lock()
	defer d.recordMutex.Unlock()
	return d.target.Selected.BinInfo().DebugInfoDirectories
}

// ChanGoroutines returns the list of goroutines waiting on the channel specified by expr.
func (d *Debugger) ChanGoroutines(goid int64, frame, deferredCall int, expr string, start, count int) ([]*proc.G, error) {
	d.targetMutex.Lock()
	defer d.targetMutex.Unlock()
	s, err := proc.ConvertEvalScope(d.target.Selected, goid, frame, deferredCall)
	if err != nil {
		return nil, err
	}

	goids, err := s.ChanGoroutines(expr, start, count)
	if err != nil {
		return nil, err
	}

	gs := make([]*proc.G, len(goids))
	for i := range goids {
		g, err := proc.FindGoroutine(d.target.Selected, goids[i])
		if g == nil {
			g = &proc.G{Unreadable: err}
		}
		gs[i] = g
	}
	return gs, nil
}

func go11DecodeErrorCheck(err error) error {
	if !errors.Is(err, dwarf.DecodeError{}) {
		return err
	}

	gover, ok := goversion.Installed()
	if !ok || !gover.AfterOrEqual(goversion.GoVersion{Major: 1, Minor: 11, Rev: -1}) || goversion.VersionAfterOrEqual(runtime.Version(), 1, 11) {
		return err
	}

	return fmt.Errorf("executables built by Go 1.11 or later need Delve built by Go 1.11 or later")
}

const NoDebugWarning string = "debuggee must not be built with 'go run' or -ldflags='-s -w', which strip debug info"

func noDebugErrorWarning(err error) error {
	if errors.Is(err, dwarf.DecodeError{}) || strings.Contains(err.Error(), "could not open debug info") {
		return fmt.Errorf("%s - %s", err.Error(), NoDebugWarning)
	}
	return err
}

func verifyBinaryFormat(exePath string) (string, error) {
	fullpath, err := filepath.Abs(exePath)
	if err != nil {
		return "", err
	}

	f, err := os.Open(fullpath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	// Skip this check on Windows.
	// TODO(derekparker) exec.LookPath looks for valid Windows extensions.
	// We don't create our binaries with valid extensions, even though we should.
	// Skip this check for now.
	if runtime.GOOS != "windows" {
		_, err = exec.LookPath(fullpath)
		if err != nil {
			return "", api.ErrNotExecutable
		}
	}

	// check that the binary format is what we expect for the host system
	var exe io.Closer
	switch runtime.GOOS {
	case "darwin":
		exe, err = macho.NewFile(f)
	case "linux", "freebsd":
		exe, err = elf.NewFile(f)
	case "windows":
		exe, err = pe.NewFile(f)
	default:
		panic("attempting to open file Delve cannot parse")
	}

	if err != nil {
		return "", api.ErrNotExecutable
	}
	exe.Close()
	return fullpath, nil
}

var attachErrorMessage = attachErrorMessageDefault

func attachErrorMessageDefault(pid int, err error) error {
	return fmt.Errorf("could not attach to pid %d: %s", pid, err)
}
