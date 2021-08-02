package proc

import (
	"errors"
	"fmt"
	"go/constant"
	"os"
	"sort"
	"strings"

	"github.com/go-delve/delve/pkg/dwarf/op"
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

	// KeepsSteppingBreakpoints is true if the target will not clear
	// stepping breakpoints unless the step was completed.
	KeepsSteppingBreakpoints bool

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

	// exitStatus is the exit status of the process we are debugging.
	// Saved here to relay to any future commands.
	exitStatus int

	// fakeMemoryRegistry contains the list of all compositeMemory objects
	// created since the last restart, it exists so that registerized variables
	// can be given a unique address.
	fakeMemoryRegistry    []*compositeMemory
	fakeMemoryRegistryMap map[string]*compositeMemory
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
		return "unknown"
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
	case StopWatchpoint:
		return "watchpoint"
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
	StopWatchpoint                     // The target process hit one or more watchpoints
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
	t.fakeMemoryRegistryMap = make(map[string]*compositeMemory)

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

// Valid returns true if this Process can be used. When it returns false it
// also returns an error describing why the Process is invalid (either
// ErrProcessExited or ErrProcessDetached).
func (t *Target) Valid() (bool, error) {
	ok, err := t.proc.Valid()
	if !ok && err != nil {
		if pe, ok := err.(ErrProcessExited); ok {
			pe.Status = t.exitStatus
			err = pe
		}
	}
	return ok, err
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

// ClearCaches clears internal caches that should not survive a restart.
// This should be called anytime the target process executes instructions.
func (t *Target) ClearCaches() {
	t.clearFakeMemory()
	t.gcache.Clear()
	for _, thread := range t.ThreadList() {
		thread.Common().g = nil
	}
}

// Restart will start the process over from the location specified by the "from" locspec.
// This is only useful for recorded targets.
// Restarting of a normal process happens at a higher level (debugger.Restart).
func (t *Target) Restart(from string) error {
	t.ClearCaches()
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
		bp, err := t.SetBreakpointWithID(unrecoveredPanicID, panicpcs[0])
		if err == nil {
			bp.Name = UnrecoveredPanic
			bp.Variables = []string{"runtime.curg._panic.arg"}
		}
	}
}

// createFatalThrowBreakpoint creates the a breakpoint as runtime.fatalthrow.
func (t *Target) createFatalThrowBreakpoint() {
	fatalpcs, err := FindFunctionLocation(t.Process, "runtime.throw", 0)
	if err == nil {
		bp, err := t.SetBreakpointWithID(fatalThrowID, fatalpcs[0])
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

type UProbeTraceResult struct {
	FnAddr      int
	InputParams []*Variable
}

func (t *Target) GetBufferedTracepoints() []*UProbeTraceResult {
	var results []*UProbeTraceResult
	tracepoints := t.proc.GetBufferedTracepoints()
	for _, tp := range tracepoints {
		r := &UProbeTraceResult{}
		r.FnAddr = tp.FnAddr
		for _, ip := range tp.InputParams {
			v := &Variable{}
			v.RealType = ip.RealType
			v.Len = ip.Len
			v.Base = ip.Base
			v.Addr = ip.Addr
			v.Kind = ip.Kind

			cachedMem := CreateLoadedCachedMemory(ip.Data)
			compMem, _ := CreateCompositeMemory(cachedMem, t.BinInfo().Arch, op.DwarfRegisters{}, ip.Pieces)
			v.mem = compMem

			// Load the value here so that we don't have to export
			// loadValue outside of proc.
			v.loadValue(loadFullValue)
			r.InputParams = append(r.InputParams, v)
		}
		results = append(results, r)
	}
	return results
}

// SetNextBreakpointID sets the breakpoint ID of the next breakpoint
func (t *Target) SetNextBreakpointID(id int) {
	t.Breakpoints().breakpointIDCounter = id
}

const (
	FakeAddressBase     = 0xbeef000000000000
	fakeAddressUnresolv = 0xbeed000000000000 // this address never resloves to memory
)

// newCompositeMemory creates a new compositeMemory object and registers it.
// If the same composite memory has been created before it will return a
// cached object.
// This caching is primarily done so that registerized variables don't get a
// different address every time they are evaluated, which would be confusing
// and leak memory.
func (t *Target) newCompositeMemory(mem MemoryReadWriter, regs op.DwarfRegisters, pieces []op.Piece, descr *locationExpr) (int64, *compositeMemory, error) {
	var key string
	if regs.CFA != 0 && len(pieces) > 0 {
		// key is created by concatenating the location expression with the CFA,
		// this combination is guaranteed to be unique between resumes.
		buf := new(strings.Builder)
		fmt.Fprintf(buf, "%#x ", regs.CFA)
		op.PrettyPrint(buf, descr.instr)
		key = buf.String()

		if cmem := t.fakeMemoryRegistryMap[key]; cmem != nil {
			return int64(cmem.base), cmem, nil
		}
	}

	cmem, err := newCompositeMemory(mem, t.BinInfo().Arch, regs, pieces)
	if err != nil {
		return 0, cmem, err
	}
	t.registerFakeMemory(cmem)
	if key != "" {
		t.fakeMemoryRegistryMap[key] = cmem
	}
	return int64(cmem.base), cmem, nil
}

func (t *Target) registerFakeMemory(mem *compositeMemory) (addr uint64) {
	t.fakeMemoryRegistry = append(t.fakeMemoryRegistry, mem)
	addr = FakeAddressBase
	if len(t.fakeMemoryRegistry) > 1 {
		prevMem := t.fakeMemoryRegistry[len(t.fakeMemoryRegistry)-2]
		addr = uint64(alignAddr(int64(prevMem.base+uint64(len(prevMem.data))), 0x100)) // the call to alignAddr just makes the address look nicer, it is not necessary
	}
	mem.base = addr
	return addr
}

func (t *Target) findFakeMemory(addr uint64) *compositeMemory {
	i := sort.Search(len(t.fakeMemoryRegistry), func(i int) bool {
		mem := t.fakeMemoryRegistry[i]
		return addr <= mem.base || (mem.base <= addr && addr < (mem.base+uint64(len(mem.data))))
	})
	if i != len(t.fakeMemoryRegistry) {
		mem := t.fakeMemoryRegistry[i]
		if mem.base <= addr && addr < (mem.base+uint64(len(mem.data))) {
			return mem
		}
	}
	return nil
}

func (t *Target) clearFakeMemory() {
	for i := range t.fakeMemoryRegistry {
		t.fakeMemoryRegistry[i] = nil
	}
	t.fakeMemoryRegistry = t.fakeMemoryRegistry[:0]
	t.fakeMemoryRegistryMap = make(map[string]*compositeMemory)
}

// dwrapUnwrap checks if fn is a dwrap wrapper function and unwraps it if it is.
func (t *Target) dwrapUnwrap(fn *Function) *Function {
	if fn == nil {
		return nil
	}
	if !strings.Contains(fn.Name, "·dwrap·") {
		return fn
	}
	if unwrap := t.BinInfo().dwrapUnwrapCache[fn.Entry]; unwrap != nil {
		return unwrap
	}
	text, err := disassemble(t.Memory(), nil, t.Breakpoints(), t.BinInfo(), fn.Entry, fn.End, false)
	if err != nil {
		return fn
	}
	for _, instr := range text {
		if instr.IsCall() && instr.DestLoc != nil && instr.DestLoc.Fn != nil && !instr.DestLoc.Fn.privateRuntime() {
			t.BinInfo().dwrapUnwrapCache[fn.Entry] = instr.DestLoc.Fn
			return instr.DestLoc.Fn
		}
	}
	return fn
}
