package proc

import (
	"errors"
	"fmt"
	"go/constant"
	"os"
	"strings"

	"github.com/go-delve/delve/pkg/goversion"
)

var (
	// ErrNotRecorded is returned when an action is requested that is
	// only possible on recorded (traced) programs.
	ErrNotRecorded = errors.New("not a recording")

	// ErrNoRuntimeAllG is returned when the runtime.allg list could
	// not be found.
	ErrNoRuntimeAllG = errors.New("could not find goroutine array")

	// ErrProcessDetached indicates that we detached from the target process.
	ErrProcessDetached = errors.New("detached from the process")
)

type LaunchFlags uint8

const (
	LaunchForeground LaunchFlags = 1 << iota
	LaunchDisableASLR
)

// Target represents the process being debugged.
type Target struct {
	Process

	proc ProcessInternal

	// StopReason describes the reason why the target process is stopped.
	// A process could be stopped for multiple simultaneous reasons, in which
	// case only one will be reported.
	StopReason StopReason

	// CanDump is true if core dumping is supported.
	CanDump bool

	// currentThread is the thread that will be used by next/step/stepout and to evaluate variables if no goroutine is selected.
	currentThread Thread

	// Goroutine that will be used by default to set breakpoint, eval variables, etc...
	// Normally selectedGoroutine is currentThread.GetG, it will not be only if SwitchGoroutine is called with a goroutine that isn't attached to a thread
	selectedGoroutine *G

	// fncallForG stores a mapping of current active function calls.
	fncallForG map[int]*callInjection

	asyncPreemptChanged bool  // runtime/debug.asyncpreemptoff was changed
	asyncPreemptOff     int64 // cached value of runtime/debug.asyncpreemptoff

	// gcache is a cache for Goroutines that we
	// have read and parsed from the targets memory.
	// This must be cleared whenever the target is resumed.
	gcache goroutineCache
	iscgo  *bool
}

// ErrProcessExited indicates that the process has exited and contains both
// process id and exit status.
type ErrProcessExited struct {
	Pid    int
	Status int
}

func (pe ErrProcessExited) Error() string {
	return fmt.Sprintf("Process %d has exited with status %d", pe.Pid, pe.Status)
}

// StopReason describes the reason why the target process is stopped.
// A process could be stopped for multiple simultaneous reasons, in which
// case only one will be reported.
type StopReason uint8

// String maps StopReason to string representation.
func (sr StopReason) String() string {
	switch sr {
	case StopUnknown:
		return "unkown"
	case StopLaunched:
		return "launched"
	case StopAttached:
		return "attached"
	case StopExited:
		return "exited"
	case StopBreakpoint:
		return "breakpoint"
	case StopHardcodedBreakpoint:
		return "hardcoded breakpoint"
	case StopManual:
		return "manual"
	case StopNextFinished:
		return "next finished"
	case StopCallReturned:
		return "call returned"
	default:
		return ""
	}
}

const (
	StopUnknown             StopReason = iota
	StopLaunched                       // The process was just launched
	StopAttached                       // The debugger stopped the process after attaching
	StopExited                         // The target process terminated
	StopBreakpoint                     // The target process hit one or more software breakpoints
	StopHardcodedBreakpoint            // The target process hit a hardcoded breakpoint (for example runtime.Breakpoint())
	StopManual                         // A manual stop was requested
	StopNextFinished                   // The next/step/stepout command terminated
	StopCallReturned                   // An injected call completed
)

// NewTargetConfig contains the configuration for a new Target object,
type NewTargetConfig struct {
	Path                string     // path of the main executable
	DebugInfoDirs       []string   // Directories to search for split debug info
	DisableAsyncPreempt bool       // Go 1.14 asynchronous preemption should be disabled
	StopReason          StopReason // Initial stop reason
	CanDump             bool       // Can create core dumps (must implement ProcessInternal.MemoryMap)
}

// DisableAsyncPreemptEnv returns a process environment (like os.Environ)
// where asyncpreemptoff is set to 1.
func DisableAsyncPreemptEnv() []string {
	env := os.Environ()
	for i := range env {
		if strings.HasPrefix(env[i], "GODEBUG=") {
			// Go 1.14 asynchronous preemption mechanism is incompatible with
			// debuggers, see: https://github.com/golang/go/issues/36494
			env[i] += ",asyncpreemptoff=1"
		}
	}
	return env
}

// NewTarget returns an initialized Target object.
func NewTarget(p Process, currentThread Thread, cfg NewTargetConfig) (*Target, error) {
	entryPoint, err := p.EntryPoint()
	if err != nil {
		return nil, err
	}

	err = p.BinInfo().LoadBinaryInfo(cfg.Path, entryPoint, cfg.DebugInfoDirs)
	if err != nil {
		return nil, err
	}
	for _, image := range p.BinInfo().Images {
		if image.loadErr != nil {
			return nil, image.loadErr
		}
	}

	t := &Target{
		Process:       p,
		proc:          p.(ProcessInternal),
		fncallForG:    make(map[int]*callInjection),
		StopReason:    cfg.StopReason,
		currentThread: currentThread,
		CanDump:       cfg.CanDump,
	}

	g, _ := GetG(currentThread)
	t.selectedGoroutine = g

	t.createUnrecoveredPanicBreakpoint()
	t.createFatalThrowBreakpoint()

	t.gcache.init(p.BinInfo())

	if cfg.DisableAsyncPreempt {
		setAsyncPreemptOff(t, 1)
	}

	return t, nil
}

// IsCgo returns the value of runtime.iscgo
func (t *Target) IsCgo() bool {
	if t.iscgo != nil {
		return *t.iscgo
	}
	scope := globalScope(t.BinInfo(), t.BinInfo().Images[0], t.Memory())
	iscgov, err := scope.findGlobal("runtime", "iscgo")
	if err == nil {
		iscgov.loadValue(loadFullValue)
		if iscgov.Unreadable == nil {
			t.iscgo = new(bool)
			*t.iscgo = constant.BoolVal(iscgov.Value)
			return constant.BoolVal(iscgov.Value)
		}
	}
	return false
}

// SupportsFunctionCalls returns whether or not the backend supports
// calling functions during a debug session.
// Currently only non-recorded processes running on AMD64 support
// function calls.
func (t *Target) SupportsFunctionCalls() bool {
	if ok, _ := t.Process.Recorded(); ok {
		return false
	}
	return t.Process.BinInfo().Arch.Name == "amd64"
}

// ClearAllGCache clears the internal Goroutine cache.
// This should be called anytime the target process executes instructions.
func (t *Target) ClearAllGCache() {
	t.gcache.Clear()
	for _, thread := range t.ThreadList() {
		thread.Common().g = nil
	}
}

// Restart will start the process over from the location specified by the "from" locspec.
// This is only useful for recorded targets.
// Restarting of a normal process happens at a higher level (debugger.Restart).
func (t *Target) Restart(from string) error {
	t.ClearAllGCache()
	currentThread, err := t.proc.Restart(from)
	if err != nil {
		return err
	}
	t.currentThread = currentThread
	t.selectedGoroutine, _ = GetG(t.CurrentThread())
	if from != "" {
		t.StopReason = StopManual
	} else {
		t.StopReason = StopLaunched
	}
	return nil
}

// SelectedGoroutine returns the currently selected goroutine.
func (t *Target) SelectedGoroutine() *G {
	return t.selectedGoroutine
}

// SwitchGoroutine will change the selected and active goroutine.
func (p *Target) SwitchGoroutine(g *G) error {
	if ok, err := p.Valid(); !ok {
		return err
	}
	if g == nil {
		return nil
	}
	if g.Thread != nil {
		return p.SwitchThread(g.Thread.ThreadID())
	}
	p.selectedGoroutine = g
	return nil
}

// SwitchThread will change the selected and active thread.
func (p *Target) SwitchThread(tid int) error {
	if ok, err := p.Valid(); !ok {
		return err
	}
	if th, ok := p.FindThread(tid); ok {
		p.currentThread = th
		p.selectedGoroutine, _ = GetG(p.CurrentThread())
		return nil
	}
	return fmt.Errorf("thread %d does not exist", tid)
}

// Detach will detach the target from the underylying process.
// This means the debugger will no longer receive events from the process
// we were previously debugging.
// If kill is true then the process will be killed when we detach.
func (t *Target) Detach(kill bool) error {
	if !kill {
		if t.asyncPreemptChanged {
			setAsyncPreemptOff(t, t.asyncPreemptOff)
		}
		for _, bp := range t.Breakpoints().M {
			if bp != nil {
				_, err := t.ClearBreakpoint(bp.Addr)
				if err != nil {
					return err
				}
			}
		}
	}
	t.StopReason = StopUnknown
	return t.proc.Detach(kill)
}

// setAsyncPreemptOff enables or disables async goroutine preemption by
// writing the value 'v' to runtime.debug.asyncpreemptoff.
// A value of '1' means off, a value of '0' means on.
func setAsyncPreemptOff(p *Target, v int64) {
	if producer := p.BinInfo().Producer(); producer == "" || !goversion.ProducerAfterOrEqual(producer, 1, 14) {
		return
	}
	logger := p.BinInfo().logger
	scope := globalScope(p.BinInfo(), p.BinInfo().Images[0], p.Memory())
	debugv, err := scope.findGlobal("runtime", "debug")
	if err != nil || debugv.Unreadable != nil {
		logger.Warnf("could not find runtime/debug variable (or unreadable): %v %v", err, debugv.Unreadable)
		return
	}
	asyncpreemptoffv, err := debugv.structMember("asyncpreemptoff")
	if err != nil {
		logger.Warnf("could not find asyncpreemptoff field: %v", err)
		return
	}
	asyncpreemptoffv.loadValue(loadFullValue)
	if asyncpreemptoffv.Unreadable != nil {
		logger.Warnf("asyncpreemptoff field unreadable: %v", asyncpreemptoffv.Unreadable)
		return
	}
	p.asyncPreemptChanged = true
	p.asyncPreemptOff, _ = constant.Int64Val(asyncpreemptoffv.Value)

	err = scope.setValue(asyncpreemptoffv, newConstant(constant.MakeInt64(v), scope.Mem), "")
	if err != nil {
		logger.Warnf("could not set asyncpreemptoff %v", err)
	}
}

// createUnrecoveredPanicBreakpoint creates the unrecoverable-panic breakpoint.
func (t *Target) createUnrecoveredPanicBreakpoint() {
	panicpcs, err := FindFunctionLocation(t.Process, "runtime.startpanic", 0)
	if _, isFnNotFound := err.(*ErrFunctionNotFound); isFnNotFound {
		panicpcs, err = FindFunctionLocation(t.Process, "runtime.fatalpanic", 0)
	}
	if err == nil {
		bp, err := t.setBreakpointWithID(unrecoveredPanicID, panicpcs[0])
		if err == nil {
			bp.Name = UnrecoveredPanic
			bp.Variables = []string{"runtime.curg._panic.arg"}
		}
	}
}

// createFatalThrowBreakpoint creates the a breakpoint as runtime.fatalthrow.
func (t *Target) createFatalThrowBreakpoint() {
	fatalpcs, err := FindFunctionLocation(t.Process, "runtime.fatalthrow", 0)
	if err == nil {
		bp, err := t.setBreakpointWithID(fatalThrowID, fatalpcs[0])
		if err == nil {
			bp.Name = FatalThrow
		}
	}
}

// CurrentThread returns the currently selected thread which will be used
// for next/step/stepout and for reading variables, unless a goroutine is
// selected.
func (t *Target) CurrentThread() Thread {
	return t.currentThread
}
