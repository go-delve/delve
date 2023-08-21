package proc

import (
	"errors"

	"github.com/go-delve/delve/pkg/astutil"
	"github.com/go-delve/delve/pkg/logflags"
)

// This file implements most of the details needed to support stack
// watchpoints. Some of the remaining details are in breakpoints, along with
// the code to support non-stack allocated watchpoints.
//
// In Go goroutine stacks start small and are frequently resized by the
// runtime according to the needs of the goroutine.
// To support this behavior we create a StackResizeBreakpoint, deep inside
// the Go runtime, when this breakpoint is hit all the stack watchpoints on
// the goroutine being resized are adjusted for the new stack.
// Furthermore, we need to detect when a goroutine leaves the stack frame
// where the variable we are watching was declared, so that we can notify
// the user that the variable went out of scope and clear the watchpoint.
//
// These breakpoints are created by setStackWatchBreakpoints and cleared by
// clearStackWatchBreakpoints.

// setStackWatchBreakpoints sets the out of scope sentinel breakpoints for
// watchpoint and a stack resize breakpoint.
func (t *Target) setStackWatchBreakpoints(scope *EvalScope, watchpoint *Breakpoint) error {
	// Watchpoint Out-of-scope Sentinel

	woos := func(_ Thread, _ *Target) (bool, error) {
		watchpointOutOfScope(t, watchpoint)
		return true, nil
	}

	topframe, retframe, err := topframe(t, scope.g, nil)
	if err != nil {
		return err
	}

	sameGCond := sameGoroutineCondition(scope.BinInfo, scope.g, 0)
	retFrameCond := astutil.And(sameGCond, frameoffCondition(&retframe))

	var deferpc uint64
	if topframe.TopmostDefer != nil {
		_, _, deferfn := topframe.TopmostDefer.DeferredFunc(t)
		if deferfn != nil {
			var err error
			deferpc, err = FirstPCAfterPrologue(t, deferfn, false)
			if err != nil {
				return err
			}
		}
	}
	if deferpc != 0 && deferpc != topframe.Current.PC {
		deferbp, err := t.SetBreakpoint(0, deferpc, WatchOutOfScopeBreakpoint, sameGCond)
		if err != nil {
			return err
		}
		deferbreaklet := deferbp.Breaklets[len(deferbp.Breaklets)-1]
		deferbreaklet.checkPanicCall = true
		deferbreaklet.watchpoint = watchpoint
		deferbreaklet.callback = woos
	}

	retbp, err := t.SetBreakpoint(0, retframe.Current.PC, WatchOutOfScopeBreakpoint, retFrameCond)
	if err != nil {
		return err
	}

	retbreaklet := retbp.Breaklets[len(retbp.Breaklets)-1]
	retbreaklet.watchpoint = watchpoint
	retbreaklet.callback = woos

	if recorded, _ := t.recman.Recorded(); recorded && retframe.Current.Fn != nil {
		// Must also set a breakpoint on the call instruction immediately
		// preceding retframe.Current.PC, because the watchpoint could also go out
		// of scope while we are running backwards.
		callerText, err := disassemble(t.Memory(), nil, t.Breakpoints(), t.BinInfo(), retframe.Current.Fn.Entry, retframe.Current.Fn.End, false)
		if err != nil {
			return err
		}
		for i, instr := range callerText {
			if instr.Loc.PC == retframe.Current.PC && i > 0 {
				retbp2, err := t.SetBreakpoint(0, callerText[i-1].Loc.PC, WatchOutOfScopeBreakpoint, retFrameCond)
				if err != nil {
					return err
				}
				retbreaklet2 := retbp2.Breaklets[len(retbp.Breaklets)-1]
				retbreaklet2.watchpoint = watchpoint
				retbreaklet2.callback = woos
				break
			}
		}
	}

	// Stack Resize Sentinel

	retpcs, err := findRetPC(t, "runtime.copystack")
	if err != nil {
		return err
	}
	if len(retpcs) > 1 {
		return errors.New("runtime.copystack has too many return instructions")
	}

	rszbp, err := t.SetBreakpoint(0, retpcs[0], StackResizeBreakpoint, sameGCond)
	if err != nil {
		return err
	}

	rszbreaklet := rszbp.Breaklets[len(rszbp.Breaklets)-1]
	rszbreaklet.watchpoint = watchpoint
	rszbreaklet.callback = func(th Thread, _ *Target) (bool, error) {
		adjustStackWatchpoint(t, th, watchpoint)
		return false, nil // we never want this breakpoint to be shown to the user
	}

	return nil
}

// clearStackWatchBreakpoints clears all accessory breakpoints for
// watchpoint.
func (t *Target) clearStackWatchBreakpoints(watchpoint *Breakpoint) error {
	bpmap := t.Breakpoints()
	for _, bp := range bpmap.M {
		changed := false
		for i, breaklet := range bp.Breaklets {
			if breaklet.watchpoint == watchpoint {
				bp.Breaklets[i] = nil
				changed = true
			}
		}
		if changed {
			_, err := t.finishClearBreakpoint(bp)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// watchpointOutOfScope is called when the watchpoint goes out of scope. It
// is used as a breaklet callback function.
// Its responsibility is to delete the watchpoint and make sure that the
// user is notified of the watchpoint going out of scope.
func watchpointOutOfScope(t *Target, watchpoint *Breakpoint) {
	t.Breakpoints().WatchOutOfScope = append(t.Breakpoints().WatchOutOfScope, watchpoint)
	err := t.ClearBreakpoint(watchpoint.Addr)
	if err != nil {
		log := logflags.DebuggerLogger()
		log.Errorf("could not clear out-of-scope watchpoint: %v", err)
	}
	delete(t.Breakpoints().Logical, watchpoint.LogicalID())
}

// adjustStackWatchpoint is called when the goroutine of watchpoint resizes
// its stack. It is used as a breaklet callback function.
// Its responsibility is to move the watchpoint to a its new address.
func adjustStackWatchpoint(t *Target, th Thread, watchpoint *Breakpoint) {
	g, _ := GetG(th)
	if g == nil {
		return
	}
	err := t.proc.EraseBreakpoint(watchpoint)
	if err != nil {
		log := logflags.DebuggerLogger()
		log.Errorf("could not adjust watchpoint at %#x: %v", watchpoint.Addr, err)
		return
	}
	delete(t.Breakpoints().M, watchpoint.Addr)
	watchpoint.Addr = uint64(int64(g.stack.hi) + watchpoint.watchStackOff)
	err = t.proc.WriteBreakpoint(watchpoint)
	if err != nil {
		log := logflags.DebuggerLogger()
		log.Errorf("could not adjust watchpoint at %#x: %v", watchpoint.Addr, err)
		return
	}
	t.Breakpoints().M[watchpoint.Addr] = watchpoint
}
