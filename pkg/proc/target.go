package proc

import (
	"fmt"
	"go/constant"
	"os"
	"strings"

	"github.com/go-delve/delve/pkg/goversion"
)

const (
	// UnrecoveredPanic is the name given to the unrecovered panic breakpoint.
	UnrecoveredPanic = "unrecovered-panic"

	// FatalThrow is the name given to the breakpoint triggered when the target process dies because of a fatal runtime error
	FatalThrow = "runtime-fatal-throw"

	unrecoveredPanicID = -1
	fatalThrowID       = -2
)

// Target represents the process being debugged.
type Target struct {
	Process

	proc ProcessInternal

	// StopReason describes the reason why the target process is stopped.
	// A process could be stopped for multiple simultaneous reasons, in which
	// case only one will be reported.
	StopReason StopReason

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
}

// StopReason describes the reason why the target process is stopped.
// A process could be stopped for multiple simultaneous reasons, in which
// case only one will be reported.
type StopReason uint8

const (
	StopUnknown             StopReason = iota
	StopLaunched                       // The process was just launched
	StopAttached                       // The debugger stopped the process after attaching
	StopExited                         // The target proces terminated
	StopBreakpoint                     // The target process hit one or more software breakpoints
	StopHardcodedBreakpoint            // The target process hit a hardcoded breakpoint (for example runtime.Breakpoint())
	StopManual                         // A manual stop was requested
	StopNextFinished                   // The next/step/stepout command terminated
	StopCallReturned                   // An injected call commpleted
)

// NewTargetConfig contains the configuration for a new Target object,
type NewTargetConfig struct {
	Path                string            // path of the main executable
	DebugInfoDirs       []string          // Directories to search for split debug info
	WriteBreakpoint     WriteBreakpointFn // Function to write a breakpoint to the target process
	DisableAsyncPreempt bool              // Go 1.14 asynchronous preemption should be disabled
	StopReason          StopReason        // Initial stop reason
}

// NewTarget returns an initialized Target object.
func NewTarget(p Process, cfg NewTargetConfig) (*Target, error) {
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
		Process:    p,
		proc:       p.(ProcessInternal),
		fncallForG: make(map[int]*callInjection),
		StopReason: cfg.StopReason,
	}

	g, _ := GetG(p.CurrentThread())
	t.selectedGoroutine = g

	createUnrecoveredPanicBreakpoint(p, cfg.WriteBreakpoint)
	createFatalThrowBreakpoint(p, cfg.WriteBreakpoint)

	t.gcache.init(p.BinInfo())

	if cfg.DisableAsyncPreempt {
		setAsyncPreemptOff(t, 1)
	}

	return t, nil
}

// SupportsFunctionCalls returns whether or not the backend supports
// calling functions during a debug session.
// Currently only non-recorded processes running on AMD64 support
// function calls.
func (t *Target) SupportsFunctionCalls() bool {
	if ok, _ := t.Process.Recorded(); ok {
		return false
	}
	_, ok := t.Process.BinInfo().Arch.(*AMD64)
	return ok
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
	err := t.proc.Restart(from)
	if err != nil {
		return err
	}
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
		p.proc.SetCurrentThread(th)
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
	if !kill && t.asyncPreemptChanged {
		setAsyncPreemptOff(t, t.asyncPreemptOff)
	}
	t.StopReason = StopUnknown
	return t.proc.Detach(kill)
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

func setAsyncPreemptOff(p *Target, v int64) {
	logger := p.BinInfo().logger
	if producer := p.BinInfo().Producer(); producer == "" || !goversion.ProducerAfterOrEqual(producer, 1, 14) {
		return
	}
	scope := globalScope(p.BinInfo(), p.BinInfo().Images[0], p.CurrentThread())
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
	logger.Warnf("could not set asyncpreemptoff %v", err)
}

// createUnrecoveredPanicBreakpoint creates the unrecoverable-panic breakpoint.
// This function is meant to be called by implementations of the Process interface.
func createUnrecoveredPanicBreakpoint(p Process, writeBreakpoint WriteBreakpointFn) {
	panicpcs, err := FindFunctionLocation(p, "runtime.startpanic", 0)
	if _, isFnNotFound := err.(*ErrFunctionNotFound); isFnNotFound {
		panicpcs, err = FindFunctionLocation(p, "runtime.fatalpanic", 0)
	}
	if err == nil {
		bp, err := p.Breakpoints().SetWithID(unrecoveredPanicID, panicpcs[0], writeBreakpoint)
		if err == nil {
			bp.Name = UnrecoveredPanic
			bp.Variables = []string{"runtime.curg._panic.arg"}
		}
	}
}

func createFatalThrowBreakpoint(p Process, writeBreakpoint WriteBreakpointFn) {
	fatalpcs, err := FindFunctionLocation(p, "runtime.fatalthrow", 0)
	if err == nil {
		bp, err := p.Breakpoints().SetWithID(fatalThrowID, fatalpcs[0], writeBreakpoint)
		if err == nil {
			bp.Name = FatalThrow
		}
	}
}
