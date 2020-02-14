package proc

import (
	"errors"
	"go/ast"
)

// ErrDirChange is returned when trying to change execution direction
// while there are still internal breakpoints set.
var ErrDirChange = errors.New("direction change with internal breakpoints")

// Target represents the process being debugged.
type Target struct {
	Process

	proc ProcessInternal

	// Breakpoint table, holds information on breakpoints.
	// Maps instruction address to Breakpoint struct.
	breakpoints BreakpointMap

	// fncallForG stores a mapping of current active function calls.
	fncallForG map[int]*callInjection

	asyncPreemptChanged bool  // runtime/debug.asyncpreemptoff was changed
	asyncPreemptOff     int64 // cached value of runtime/debug.asyncpreemptoff

	// gcache is a cache for Goroutines that we
	// have read and parsed from the targets memory.
	// This must be cleared whenever the target is resumed.
	gcache goroutineCache

	// threadToBreakpoint maps threads to the breakpoint that they
	// have were trapped on.
	threadToBreakpoint map[int]*BreakpointState
}

// NewTarget returns an initialized Target object.
func NewTarget(p Process, disableAsyncPreempt bool) *Target {
	t := &Target{
		Process:     p,
		proc:        p.(ProcessInternal),
		fncallForG:  make(map[int]*callInjection),
		breakpoints: NewBreakpointMap(),
	}
	t.gcache.init(p.BinInfo())

	if disableAsyncPreempt {
		setAsyncPreemptOff(t, 1)
	}

	g, _ := GetG(p.CurrentThread())
	t.proc.SetSelectedGoroutine(g)

	createUnrecoveredPanicBreakpoint(t, t.proc.WriteBreakpointFn)
	createFatalThrowBreakpoint(t, t.proc.WriteBreakpointFn)

	return t
}

// Breakpoints returns a list of breakpoints currently set.
func (t *Target) Breakpoints() *BreakpointMap {
	return &t.breakpoints
}

// Detach will stop tracing the debugged process, causing
// Delve to no longer receive any debug events.
// If 'kill' is true Delve will kill the process when detaching.
func (t *Target) Detach(kill bool) error {
	if !kill {
		if t.asyncPreemptChanged {
			setAsyncPreemptOff(t, t.asyncPreemptOff)
		}
		// Clean up any breakpoints we've set.
		for _, bp := range t.Breakpoints().M {
			if bp != nil {
				_, err := t.ClearBreakpoint(bp.Addr)
				if err != nil {
					return err
				}
			}
		}
	}
	return t.proc.Detach(kill)
}

// FindBreakpoint returns the breakpoint at the given address.
func (t *Target) FindBreakpoint(pc uint64) (*Breakpoint, bool) {
	bp, ok := t.Breakpoints().M[pc]
	return bp, ok
}

// Direction sets the execution direction for the process being debugged.
func (t *Target) Direction(dir Direction) error {
	if t.Breakpoints().HasInternalBreakpoints() {
		return ErrDirChange
	}
	return t.Process.Direction(dir)
}

// ThreadToBreakpoint returns the breakpoint that the given thread is stopped at.
func (t *Target) ThreadToBreakpoint(th Thread) *BreakpointState {
	if bps, ok := t.threadToBreakpoint[th.ThreadID()]; ok {
		return bps
	}
	return new(BreakpointState)
}

// SetBreakpoint sets a breakpoint at addr, and stores it in the process wide
// break point table.
func (t *Target) SetBreakpoint(addr uint64, kind BreakpointKind, cond ast.Expr) (*Breakpoint, error) {
	return t.breakpoints.Set(addr, kind, cond, t.proc.WriteBreakpointFn)
}

// ClearBreakpoint clears the breakpoint at addr.
func (t *Target) ClearBreakpoint(addr uint64) (*Breakpoint, error) {
	if ok, err := t.Valid(); !ok {
		return nil, err
	}
	bp, err := t.breakpoints.Clear(addr, t.proc.ClearBreakpointFn)
	if err != nil {
		return nil, err
	}
	for _, th := range t.Process.ThreadList() {
		bp := t.ThreadToBreakpoint(th)
		if bp.Breakpoint != nil && bp.Addr == addr {
			bp.Clear()
		}
	}
	return bp, nil
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
}

// Restart starts the process over from the given location.
// Only valid for recorded targets.
func (t *Target) Restart(from string) error {
	t.ClearAllGCache()
	t.threadToBreakpoint = make(map[int]*BreakpointState)
	if err := t.proc.Restart(from); err != nil {
		return err
	}
	for addr := range t.Breakpoints().M {
		_, _, _, _, err := t.proc.WriteBreakpointFn(addr)
		if err != nil {
			return err
		}
	}
	for _, th := range t.Process.ThreadList() {
		if err := t.setThreadBreakpointState(th, true); err != nil {
			return err
		}
	}
	return nil
}

// ClearInternalBreakpoints clears any breakpoints set during a step or next operation.
func (t *Target) ClearInternalBreakpoints() error {
	if err := t.Breakpoints().ClearInternalBreakpoints(func(addr uint64, originalData []byte) error {
		return t.proc.ClearBreakpointFn(addr, originalData)
	}); err != nil {
		return err
	}
	for tid, bp := range t.threadToBreakpoint {
		if bp.IsInternal() {
			delete(t.threadToBreakpoint, tid)
		}
	}
	return nil
}

func (t *Target) setThreadBreakpointState(th Thread, adjustPC bool) error {
	delete(t.threadToBreakpoint, th.ThreadID())

	regs, err := th.Registers(false)
	if err != nil {
		return err
	}
	pc := regs.PC()

	// If the breakpoint instruction does not change the value
	// of PC after being executed we should look for breakpoints
	// with bp.Addr == PC and there is no need to call SetPC
	// after finding one.
	adjustPC = adjustPC &&
		t.BinInfo().Arch.BreakInstrMovesPC() &&
		t.proc.AdjustPCAfterBreakpoint()

	if adjustPC {
		pc = pc - uint64(t.BinInfo().Arch.BreakpointSize())
	}

	if bp, ok := t.FindBreakpoint(pc); ok {
		if adjustPC {
			if err = th.SetPC(pc); err != nil {
				return err
			}
		}
		bps := bp.CheckCondition(th)
		if bps.Breakpoint != nil && bps.Active {
			if g, err := GetG(th); err == nil {
				bps.HitCount[g.ID]++
			}
			bps.TotalHitCount++
		}
		t.threadToBreakpoint[th.ThreadID()] = bps
	}
	return nil
}
