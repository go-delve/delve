package proc

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-delve/delve/pkg/dwarf/reader"
)

// ErrNotExecutable is returned after attempting to execute a non-executable file
// to begin a debug session.
var ErrNotExecutable = errors.New("not an executable file")

// ErrNotRecorded is returned when an action is requested that is
// only possible on recorded (traced) programs.
var ErrNotRecorded = errors.New("not a recording")

var ErrNoRuntimeAllG = errors.New("could not find goroutine array")

const (
	// UnrecoveredPanic is the name given to the unrecovered panic breakpoint.
	UnrecoveredPanic = "unrecovered-panic"

	// FatalThrow is the name given to the breakpoint triggered when the target process dies because of a fatal runtime error
	FatalThrow = "runtime-fatal-throw"

	unrecoveredPanicID = -1
	fatalThrowID       = -2
)

// ErrProcessExited indicates that the process has exited and contains both
// process id and exit status.
type ErrProcessExited struct {
	Pid    int
	Status int
}

func (pe ErrProcessExited) Error() string {
	return fmt.Sprintf("Process %d has exited with status %d", pe.Pid, pe.Status)
}

// ProcessDetachedError indicates that we detached from the target process.
type ProcessDetachedError struct {
}

func (pe ProcessDetachedError) Error() string {
	return "detached from the process"
}

// FindFileLocation returns the PC for a given file:line.
// Assumes that `file` is normalized to lower case and '/' on Windows.
func FindFileLocation(p Process, fileName string, lineno int) ([]uint64, error) {
	pcs, err := p.BinInfo().LineToPC(fileName, lineno)
	if err != nil {
		return nil, err
	}
	var fn *Function
	for i := range pcs {
		if fn == nil || pcs[i] < fn.Entry || pcs[i] >= fn.End {
			fn = p.BinInfo().PCToFunc(pcs[i])
		}
		if fn != nil && fn.Entry == pcs[i] {
			pcs[i], _ = FirstPCAfterPrologue(p, fn, true)
		}
	}
	return pcs, nil
}

// ErrFunctionNotFound is returned when failing to find the
// function named 'FuncName' within the binary.
type ErrFunctionNotFound struct {
	FuncName string
}

func (err *ErrFunctionNotFound) Error() string {
	return fmt.Sprintf("Could not find function %s\n", err.FuncName)
}

// FindFunctionLocation finds address of a function's line
// If lineOffset is passed FindFunctionLocation will return the address of that line
func FindFunctionLocation(p Process, funcName string, lineOffset int) ([]uint64, error) {
	bi := p.BinInfo()
	origfn := bi.LookupFunc[funcName]
	if origfn == nil {
		return nil, &ErrFunctionNotFound{funcName}
	}

	if lineOffset <= 0 {
		r := make([]uint64, 0, len(origfn.InlinedCalls)+1)
		if origfn.Entry > 0 {
			// add concrete implementation of the function
			pc, err := FirstPCAfterPrologue(p, origfn, false)
			if err != nil {
				return nil, err
			}
			r = append(r, pc)
		}
		// add inlined calls to the function
		for _, call := range origfn.InlinedCalls {
			r = append(r, call.LowPC)
		}
		if len(r) == 0 {
			return nil, &ErrFunctionNotFound{funcName}
		}
		return r, nil
	}
	filename, lineno := origfn.cu.lineInfo.PCToLine(origfn.Entry, origfn.Entry)
	return bi.LineToPC(filename, lineno+lineOffset)
}

// Next continues execution until the next source line.
func (dbp *Target) Next() (err error) {
	if _, err := dbp.Valid(); err != nil {
		return err
	}
	if dbp.Breakpoints().HasInternalBreakpoints() {
		return fmt.Errorf("next while nexting")
	}

	if err = next(dbp, false, false); err != nil {
		dbp.ClearInternalBreakpoints()
		return
	}

	return dbp.Continue()
}

// Continue continues execution of the debugged
// process. It will continue until it hits a breakpoint
// or is otherwise stopped.
func (dbp *Target) Continue() error {
	if _, err := dbp.Valid(); err != nil {
		return err
	}
	for _, thread := range dbp.ThreadList() {
		thread.Common().returnValues = nil
	}
	dbp.CheckAndClearManualStopRequest()
	defer func() {
		// Make sure we clear internal breakpoints if we simultaneously receive a
		// manual stop request and hit a breakpoint.
		if dbp.CheckAndClearManualStopRequest() {
			dbp.StopReason = StopManual
			dbp.ClearInternalBreakpoints()
		}
	}()
	for {
		if dbp.CheckAndClearManualStopRequest() {
			dbp.StopReason = StopManual
			dbp.ClearInternalBreakpoints()
			return nil
		}
		dbp.ClearAllGCache()
		trapthread, stopReason, err := dbp.proc.ContinueOnce()
		dbp.StopReason = stopReason
		if err != nil {
			return err
		}
		if dbp.StopReason == StopLaunched {
			dbp.ClearInternalBreakpoints()
		}

		threads := dbp.ThreadList()

		callInjectionDone, err := callInjectionProtocol(dbp, threads)
		if err != nil {
			return err
		}

		if err := pickCurrentThread(dbp, trapthread, threads); err != nil {
			return err
		}

		curthread := dbp.CurrentThread()
		curbp := curthread.Breakpoint()

		switch {
		case curbp.Breakpoint == nil:
			// runtime.Breakpoint, manual stop or debugCallV1-related stop
			recorded, _ := dbp.Recorded()
			if recorded {
				return conditionErrors(threads)
			}

			loc, err := curthread.Location()
			if err != nil || loc.Fn == nil {
				return conditionErrors(threads)
			}
			g, _ := GetG(curthread)
			arch := dbp.BinInfo().Arch

			switch {
			case loc.Fn.Name == "runtime.breakpoint":
				// In linux-arm64, PtraceSingleStep seems cannot step over BRK instruction
				// (linux-arm64 feature or kernel bug maybe).
				if !arch.BreakInstrMovesPC() {
					curthread.SetPC(loc.PC + uint64(arch.BreakpointSize()))
				}
				// Single-step current thread until we exit runtime.breakpoint and
				// runtime.Breakpoint.
				// On go < 1.8 it was sufficient to single-step twice on go1.8 a change
				// to the compiler requires 4 steps.
				if err := stepInstructionOut(dbp, curthread, "runtime.breakpoint", "runtime.Breakpoint"); err != nil {
					return err
				}
				dbp.StopReason = StopHardcodedBreakpoint
				return conditionErrors(threads)
			case g == nil || dbp.fncallForG[g.ID] == nil:
				// a hardcoded breakpoint somewhere else in the code (probably cgo), or manual stop in cgo
				if !arch.BreakInstrMovesPC() {
					bpsize := arch.BreakpointSize()
					bp := make([]byte, bpsize)
					_, err = dbp.CurrentThread().ReadMemory(bp, uintptr(loc.PC))
					if bytes.Equal(bp, arch.BreakpointInstruction()) {
						curthread.SetPC(loc.PC + uint64(bpsize))
					}
				}
				return conditionErrors(threads)
			}
		case curbp.Active && curbp.Internal:
			switch curbp.Kind {
			case StepBreakpoint:
				// See description of proc.(*Process).next for the meaning of StepBreakpoints
				if err := conditionErrors(threads); err != nil {
					return err
				}
				if dbp.GetDirection() == Forward {
					text, err := disassembleCurrentInstruction(dbp, curthread)
					// here we either set a breakpoint into the destination of the CALL
					// instruction or we determined that the called function is hidden,
					// either way we need to resume execution
					if err = setStepIntoBreakpoint(dbp, text, SameGoroutineCondition(dbp.SelectedGoroutine())); err != nil {
						return err
					}
				} else {
					if err := dbp.ClearInternalBreakpoints(); err != nil {
						return err
					}
					return dbp.StepInstruction()
				}
			default:
				curthread.Common().returnValues = curbp.Breakpoint.returnInfo.Collect(curthread)
				if err := dbp.ClearInternalBreakpoints(); err != nil {
					return err
				}
				dbp.StopReason = StopNextFinished
				return conditionErrors(threads)
			}
		case curbp.Active:
			onNextGoroutine, err := onNextGoroutine(curthread, dbp.Breakpoints())
			if err != nil {
				return err
			}
			if onNextGoroutine {
				err := dbp.ClearInternalBreakpoints()
				if err != nil {
					return err
				}
			}
			if curbp.Name == UnrecoveredPanic {
				dbp.ClearInternalBreakpoints()
			}
			dbp.StopReason = StopBreakpoint
			return conditionErrors(threads)
		default:
			// not a manual stop, not on runtime.Breakpoint, not on a breakpoint, just repeat
		}
		if callInjectionDone {
			// a call injection was finished, don't let a breakpoint with a failed
			// condition or a step breakpoint shadow this.
			dbp.StopReason = StopCallReturned
			return conditionErrors(threads)
		}
	}
}

func conditionErrors(threads []Thread) error {
	var condErr error
	for _, th := range threads {
		if bp := th.Breakpoint(); bp.Breakpoint != nil && bp.CondError != nil {
			if condErr == nil {
				condErr = bp.CondError
			} else {
				return fmt.Errorf("multiple errors evaluating conditions")
			}
		}
	}
	return condErr
}

// pick a new dbp.currentThread, with the following priority:
// 	- a thread with onTriggeredInternalBreakpoint() == true
// 	- a thread with onTriggeredBreakpoint() == true (prioritizing trapthread)
// 	- trapthread
func pickCurrentThread(dbp *Target, trapthread Thread, threads []Thread) error {
	for _, th := range threads {
		if bp := th.Breakpoint(); bp.Active && bp.Internal {
			return dbp.SwitchThread(th.ThreadID())
		}
	}
	if bp := trapthread.Breakpoint(); bp.Active {
		return dbp.SwitchThread(trapthread.ThreadID())
	}
	for _, th := range threads {
		if bp := th.Breakpoint(); bp.Active {
			return dbp.SwitchThread(th.ThreadID())
		}
	}
	return dbp.SwitchThread(trapthread.ThreadID())
}

func disassembleCurrentInstruction(p Process, thread Thread) ([]AsmInstruction, error) {
	regs, err := thread.Registers(false)
	if err != nil {
		return nil, err
	}
	pc := regs.PC()
	return disassemble(thread, regs, p.Breakpoints(), p.BinInfo(), pc, pc+uint64(p.BinInfo().Arch.MaxInstructionLength()), true)
}

// stepInstructionOut repeatedly calls StepInstruction until the current
// function is neither fnname1 or fnname2.
// This function is used to step out of runtime.Breakpoint as well as
// runtime.debugCallV1.
func stepInstructionOut(dbp *Target, curthread Thread, fnname1, fnname2 string) error {
	for {
		if err := curthread.StepInstruction(); err != nil {
			return err
		}
		loc, err := curthread.Location()
		if err != nil || loc.Fn == nil || (loc.Fn.Name != fnname1 && loc.Fn.Name != fnname2) {
			g, _ := GetG(curthread)
			selg := dbp.SelectedGoroutine()
			if g != nil && selg != nil && g.ID == selg.ID {
				selg.CurrentLoc = *loc
			}
			return curthread.SetCurrentBreakpoint(true)
		}
	}
}

// Step will continue until another source line is reached.
// Will step into functions.
func (dbp *Target) Step() (err error) {
	if _, err := dbp.Valid(); err != nil {
		return err
	}
	if dbp.Breakpoints().HasInternalBreakpoints() {
		return fmt.Errorf("next while nexting")
	}

	if err = next(dbp, true, false); err != nil {
		switch err.(type) {
		case ErrThreadBlocked: // Noop
		default:
			dbp.ClearInternalBreakpoints()
			return
		}
	}

	if bp := dbp.CurrentThread().Breakpoint().Breakpoint; bp != nil && bp.Kind == StepBreakpoint && dbp.GetDirection() == Backward {
		dbp.ClearInternalBreakpoints()
		return dbp.StepInstruction()
	}

	return dbp.Continue()
}

// SameGoroutineCondition returns an expression that evaluates to true when
// the current goroutine is g.
func SameGoroutineCondition(g *G) ast.Expr {
	if g == nil {
		return nil
	}
	return &ast.BinaryExpr{
		Op: token.EQL,
		X: &ast.SelectorExpr{
			X: &ast.SelectorExpr{
				X:   &ast.Ident{Name: "runtime"},
				Sel: &ast.Ident{Name: "curg"},
			},
			Sel: &ast.Ident{Name: "goid"},
		},
		Y: &ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(g.ID)},
	}
}

func frameoffCondition(frameoff int64) ast.Expr {
	return &ast.BinaryExpr{
		Op: token.EQL,
		X: &ast.SelectorExpr{
			X:   &ast.Ident{Name: "runtime"},
			Sel: &ast.Ident{Name: "frameoff"},
		},
		Y: &ast.BasicLit{Kind: token.INT, Value: strconv.FormatInt(frameoff, 10)},
	}
}

func andFrameoffCondition(cond ast.Expr, frameoff int64) ast.Expr {
	if cond == nil {
		return nil
	}
	return &ast.BinaryExpr{
		Op: token.LAND,
		X:  cond,
		Y:  frameoffCondition(frameoff),
	}
}

// StepOut will continue until the current goroutine exits the
// function currently being executed or a deferred function is executed
func (dbp *Target) StepOut() error {
	backward := dbp.GetDirection() == Backward
	if _, err := dbp.Valid(); err != nil {
		return err
	}
	if dbp.Breakpoints().HasInternalBreakpoints() {
		return fmt.Errorf("next while nexting")
	}

	selg := dbp.SelectedGoroutine()
	curthread := dbp.CurrentThread()

	topframe, retframe, err := topframe(selg, curthread)
	if err != nil {
		return err
	}

	success := false
	defer func() {
		if !success {
			dbp.ClearInternalBreakpoints()
		}
	}()

	if topframe.Inlined {
		if err := next(dbp, false, true); err != nil {
			return err
		}

		success = true
		return dbp.Continue()
	}

	sameGCond := SameGoroutineCondition(selg)
	retFrameCond := andFrameoffCondition(sameGCond, retframe.FrameOffset())

	if backward {
		if err := stepOutReverse(dbp, topframe, retframe, sameGCond); err != nil {
			return err
		}

		success = true
		return dbp.Continue()
	}

	var deferpc uint64
	if !backward {
		deferpc, err = setDeferBreakpoint(dbp, nil, topframe, sameGCond, false)
		if err != nil {
			return err
		}
	}

	if topframe.Ret == 0 && deferpc == 0 {
		return errors.New("nothing to stepout to")
	}

	if topframe.Ret != 0 {
		bp, err := allowDuplicateBreakpoint(dbp.SetBreakpoint(topframe.Ret, NextBreakpoint, retFrameCond))
		if err != nil {
			return err
		}
		if bp != nil {
			configureReturnBreakpoint(dbp.BinInfo(), bp, &topframe, retFrameCond)
		}
	}

	if bp := curthread.Breakpoint(); bp.Breakpoint == nil {
		curthread.SetCurrentBreakpoint(false)
	}

	success = true
	return dbp.Continue()
}

// StepInstruction will continue the current thread for exactly
// one instruction. This method affects only the thread
// associated with the selected goroutine. All other
// threads will remain stopped.
func (dbp *Target) StepInstruction() (err error) {
	thread := dbp.CurrentThread()
	g := dbp.SelectedGoroutine()
	if g != nil {
		if g.Thread == nil {
			// Step called on parked goroutine
			if _, err := dbp.SetBreakpoint(g.PC, NextBreakpoint,
				SameGoroutineCondition(dbp.SelectedGoroutine())); err != nil {
				return err
			}
			return dbp.Continue()
		}
		thread = g.Thread
	}
	dbp.ClearAllGCache()
	if ok, err := dbp.Valid(); !ok {
		return err
	}
	thread.Breakpoint().Clear()
	err = thread.StepInstruction()
	if err != nil {
		return err
	}
	err = thread.SetCurrentBreakpoint(true)
	if err != nil {
		return err
	}
	if tg, _ := GetG(thread); tg != nil {
		dbp.selectedGoroutine = tg
	}
	return nil
}

func allowDuplicateBreakpoint(bp *Breakpoint, err error) (*Breakpoint, error) {
	if err != nil {
		if _, isexists := err.(BreakpointExistsError); isexists {
			return bp, nil
		}
	}
	return bp, err
}

// setDeferBreakpoint is a helper function used by next and StepOut to set a
// breakpoint on the first deferred function.
func setDeferBreakpoint(p Process, text []AsmInstruction, topframe Stackframe, sameGCond ast.Expr, stepInto bool) (uint64, error) {
	// Set breakpoint on the most recently deferred function (if any)
	var deferpc uint64
	if topframe.TopmostDefer != nil && topframe.TopmostDefer.DeferredPC != 0 {
		deferfn := p.BinInfo().PCToFunc(topframe.TopmostDefer.DeferredPC)
		var err error
		deferpc, err = FirstPCAfterPrologue(p, deferfn, false)
		if err != nil {
			return 0, err
		}
	}
	if deferpc != 0 && deferpc != topframe.Current.PC {
		bp, err := allowDuplicateBreakpoint(p.SetBreakpoint(deferpc, NextDeferBreakpoint, sameGCond))
		if err != nil {
			return 0, err
		}
		if bp != nil && stepInto {
			// If DeferReturns is set then the breakpoint will also be triggered when
			// called from runtime.deferreturn. We only do this for the step command,
			// not for next or stepout.
			bp.DeferReturns = FindDeferReturnCalls(text)
		}
	}

	return deferpc, nil
}

// findCallInstrForRet returns the PC address of the CALL instruction
// immediately preceding the instruction at ret.
func findCallInstrForRet(p Process, mem MemoryReadWriter, ret uint64, fn *Function) (uint64, error) {
	text, err := disassemble(mem, nil, p.Breakpoints(), p.BinInfo(), fn.Entry, fn.End, false)
	if err != nil {
		return 0, err
	}
	var prevInstr AsmInstruction
	for _, instr := range text {
		if instr.Loc.PC == ret {
			return prevInstr.Loc.PC, nil
		}
		prevInstr = instr
	}
	return 0, fmt.Errorf("could not find CALL instruction for address %#x in %s", ret, fn.Name)
}

// stepOutReverse sets a breakpoint on the CALL instruction that created the current frame, this is either:
// - the CALL instruction immediately preceding the return address of the
//   current frame
// - the return address of the current frame if the current frame was
//   created by a runtime.deferreturn run
// - the return address of the runtime.gopanic frame if the current frame
//   was created by a panic
// This function is used to implement reversed StepOut
func stepOutReverse(p *Target, topframe, retframe Stackframe, sameGCond ast.Expr) error {
	curthread := p.CurrentThread()
	selg := p.SelectedGoroutine()

	if selg != nil && selg.Thread != nil {
		curthread = selg.Thread
	}

	callerText, err := disassemble(curthread, nil, p.Breakpoints(), p.BinInfo(), retframe.Current.Fn.Entry, retframe.Current.Fn.End, false)
	if err != nil {
		return err
	}
	deferReturns := FindDeferReturnCalls(callerText)

	var frames []Stackframe
	if selg == nil {
		if !curthread.Blocked() {
			frames, err = ThreadStacktrace(curthread, 3)
		}
	} else {
		frames, err = selg.Stacktrace(3, 0)
	}
	if err != nil {
		return err
	}

	var callpc uint64

	if isPanicCall(frames) {
		if len(frames) < 4 || frames[3].Current.Fn == nil {
			return &ErrNoSourceForPC{frames[2].Current.PC}
		}
		callpc, err = findCallInstrForRet(p, curthread, frames[2].Ret, frames[3].Current.Fn)
		if err != nil {
			return err
		}
	} else if ok, pc := isDeferReturnCall(frames, deferReturns); ok {
		callpc = pc
	} else {
		callpc, err = findCallInstrForRet(p, curthread, topframe.Ret, retframe.Current.Fn)
		if err != nil {
			return err
		}
	}

	_, err = allowDuplicateBreakpoint(p.SetBreakpoint(callpc, NextBreakpoint, sameGCond))

	return err
}

// GoroutinesInfo searches for goroutines starting at index 'start', and
// returns an array of up to 'count' (or all found elements, if 'count' is 0)
// G structures representing the information Delve care about from the internal
// runtime G structure.
// GoroutinesInfo also returns the next index to be used as 'start' argument
// while scanning for all available goroutines, or -1 if there was an error
// or if the index already reached the last possible value.
func GoroutinesInfo(dbp *Target, start, count int) ([]*G, int, error) {
	if _, err := dbp.Valid(); err != nil {
		return nil, -1, err
	}
	if dbp.gcache.allGCache != nil {
		// We can't use the cached array to fulfill a subrange request
		if start == 0 && (count == 0 || count >= len(dbp.gcache.allGCache)) {
			return dbp.gcache.allGCache, -1, nil
		}
	}

	var (
		threadg = map[int]*G{}
		allg    []*G
	)

	threads := dbp.ThreadList()
	for _, th := range threads {
		if th.Blocked() {
			continue
		}
		g, _ := GetG(th)
		if g != nil {
			threadg[g.ID] = g
		}
	}

	allgptr, allglen, err := dbp.gcache.getRuntimeAllg(dbp.BinInfo(), dbp.CurrentThread())
	if err != nil {
		return nil, -1, err
	}

	for i := uint64(start); i < allglen; i++ {
		if count != 0 && len(allg) >= count {
			return allg, int(i), nil
		}
		gvar, err := newGVariable(dbp.CurrentThread(), uintptr(allgptr+(i*uint64(dbp.BinInfo().Arch.PtrSize()))), true)
		if err != nil {
			allg = append(allg, &G{Unreadable: err})
			continue
		}
		g, err := gvar.parseG()
		if err != nil {
			allg = append(allg, &G{Unreadable: err})
			continue
		}
		if thg, allocated := threadg[g.ID]; allocated {
			loc, err := thg.Thread.Location()
			if err != nil {
				return nil, -1, err
			}
			g.Thread = thg.Thread
			// Prefer actual thread location information.
			g.CurrentLoc = *loc
			g.SystemStack = thg.SystemStack
		}
		if g.Status != Gdead {
			allg = append(allg, g)
		}
		dbp.gcache.addGoroutine(g)
	}
	if start == 0 {
		dbp.gcache.allGCache = allg
	}

	return allg, -1, nil
}

// FindGoroutine returns a G struct representing the goroutine
// specified by `gid`.
func FindGoroutine(dbp *Target, gid int) (*G, error) {
	if selg := dbp.SelectedGoroutine(); (gid == -1) || (selg != nil && selg.ID == gid) || (selg == nil && gid == 0) {
		// Return the currently selected goroutine in the following circumstances:
		//
		// 1. if the caller asks for gid == -1 (because that's what a goroutine ID of -1 means in our API).
		// 2. if gid == selg.ID.
		//    this serves two purposes: (a) it's an optimizations that allows us
		//    to avoid reading any other goroutine and, more importantly, (b) we
		//    could be reading an incorrect value for the goroutine ID of a thread.
		//    This condition usually happens when a goroutine calls runtime.clone
		//    and for a short period of time two threads will appear to be running
		//    the same goroutine.
		// 3. if the caller asks for gid == 0 and the selected goroutine is
		//    either 0 or nil.
		//    Goroutine 0 is special, it either means we have no current goroutine
		//    (for example, running C code), or that we are running on a speical
		//    stack (system stack, signal handling stack) and we didn't properly
		//    detect it.
		//    Since there could be multiple goroutines '0' running simultaneously
		//    if the user requests it return the one that's already selected or
		//    nil if there isn't a selected goroutine.
		return selg, nil
	}

	if gid == 0 {
		return nil, fmt.Errorf("Unknown goroutine %d", gid)
	}

	// Calling GoroutinesInfo could be slow if there are many goroutines
	// running, check if a running goroutine has been requested first.
	for _, thread := range dbp.ThreadList() {
		g, _ := GetG(thread)
		if g != nil && g.ID == gid {
			return g, nil
		}
	}

	if g := dbp.gcache.partialGCache[gid]; g != nil {
		return g, nil
	}

	const goroutinesInfoLimit = 10
	nextg := 0
	for nextg >= 0 {
		var gs []*G
		var err error
		gs, nextg, err = GoroutinesInfo(dbp, nextg, goroutinesInfoLimit)
		if err != nil {
			return nil, err
		}
		for i := range gs {
			if gs[i].ID == gid {
				if gs[i].Unreadable != nil {
					return nil, gs[i].Unreadable
				}
				return gs[i], nil
			}
		}
	}

	return nil, fmt.Errorf("Unknown goroutine %d", gid)
}

// ConvertEvalScope returns a new EvalScope in the context of the
// specified goroutine ID and stack frame.
// If deferCall is > 0 the eval scope will be relative to the specified deferred call.
func ConvertEvalScope(dbp *Target, gid, frame, deferCall int) (*EvalScope, error) {
	if _, err := dbp.Valid(); err != nil {
		return nil, err
	}
	ct := dbp.CurrentThread()
	g, err := FindGoroutine(dbp, gid)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return ThreadScope(ct)
	}

	var thread MemoryReadWriter
	if g.Thread == nil {
		thread = ct
	} else {
		thread = g.Thread
	}

	var opts StacktraceOptions
	if deferCall > 0 {
		opts = StacktraceReadDefers
	}

	locs, err := g.Stacktrace(frame+1, opts)
	if err != nil {
		return nil, err
	}

	if frame >= len(locs) {
		return nil, fmt.Errorf("Frame %d does not exist in goroutine %d", frame, gid)
	}

	if deferCall > 0 {
		if deferCall-1 >= len(locs[frame].Defers) {
			return nil, fmt.Errorf("Frame %d only has %d deferred calls", frame, len(locs[frame].Defers))
		}

		d := locs[frame].Defers[deferCall-1]
		if d.Unreadable != nil {
			return nil, d.Unreadable
		}

		return d.EvalScope(ct)
	}

	return FrameToScope(dbp.BinInfo(), thread, g, locs[frame:]...), nil
}

// FrameToScope returns a new EvalScope for frames[0].
// If frames has at least two elements all memory between
// frames[0].Regs.SP() and frames[1].Regs.CFA will be cached.
// Otherwise all memory between frames[0].Regs.SP() and frames[0].Regs.CFA
// will be cached.
func FrameToScope(bi *BinaryInfo, thread MemoryReadWriter, g *G, frames ...Stackframe) *EvalScope {
	// Creates a cacheMem that will preload the entire stack frame the first
	// time any local variable is read.
	// Remember that the stack grows downward in memory.
	minaddr := frames[0].Regs.SP()
	var maxaddr uint64
	if len(frames) > 1 && frames[0].SystemStack == frames[1].SystemStack {
		maxaddr = uint64(frames[1].Regs.CFA)
	} else {
		maxaddr = uint64(frames[0].Regs.CFA)
	}
	if maxaddr > minaddr && maxaddr-minaddr < maxFramePrefetchSize {
		thread = cacheMemory(thread, uintptr(minaddr), int(maxaddr-minaddr))
	}

	s := &EvalScope{Location: frames[0].Call, Regs: frames[0].Regs, Mem: thread, g: g, BinInfo: bi, frameOffset: frames[0].FrameOffset()}
	s.PC = frames[0].lastpc
	return s
}

// FirstPCAfterPrologue returns the address of the first
// instruction after the prologue for function fn.
// If sameline is set FirstPCAfterPrologue will always return an
// address associated with the same line as fn.Entry.
func FirstPCAfterPrologue(p Process, fn *Function, sameline bool) (uint64, error) {
	pc, _, line, ok := fn.cu.lineInfo.PrologueEndPC(fn.Entry, fn.End)
	if ok {
		if !sameline {
			return pc, nil
		}
		_, entryLine := fn.cu.lineInfo.PCToLine(fn.Entry, fn.Entry)
		if entryLine == line {
			return pc, nil
		}
	}

	pc, err := firstPCAfterPrologueDisassembly(p, fn, sameline)
	if err != nil {
		return fn.Entry, err
	}

	if pc == fn.Entry {
		// Look for the first instruction with the stmt flag set, so that setting a
		// breakpoint with file:line and with the function name always result on
		// the same instruction being selected.
		if pc2, _, _, ok := fn.cu.lineInfo.FirstStmtForLine(fn.Entry, fn.End); ok {
			return pc2, nil
		}
	}

	return pc, nil
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

// FindDeferReturnCalls will find all runtime.deferreturn locations in the given buffer of
// assembly instructions.
// See documentation of Breakpoint.DeferCond for why this is necessary
func FindDeferReturnCalls(text []AsmInstruction) []uint64 {
	const deferreturn = "runtime.deferreturn"
	deferreturns := []uint64{}

	for _, instr := range text {
		if instr.IsCall() && instr.DestLoc != nil && instr.DestLoc.Fn != nil && instr.DestLoc.Fn.Name == deferreturn {
			deferreturns = append(deferreturns, instr.Loc.PC)
		}
	}
	return deferreturns
}

// Set breakpoints at every line, and the return address. Also look for
// a deferred function and set a breakpoint there too.
// If stepInto is true it will also set breakpoints inside all
// functions called on the current source line, for non-absolute CALLs
// a breakpoint of kind StepBreakpoint is set on the CALL instruction,
// Continue will take care of setting a breakpoint to the destination
// once the CALL is reached.
//
// Regardless of stepInto the following breakpoints will be set:
// - a breakpoint on the first deferred function with NextDeferBreakpoint
//   kind, the list of all the addresses to deferreturn calls in this function
//   and condition checking that we remain on the same goroutine
// - a breakpoint on each line of the function, with a condition checking
//   that we stay on the same stack frame and goroutine.
// - a breakpoint on the return address of the function, with a condition
//   checking that we move to the previous stack frame and stay on the same
//   goroutine.
//
// The breakpoint on the return address is *not* set if the current frame is
// an inlined call. For inlined calls topframe.Current.Fn is the function
// where the inlining happened and the second set of breakpoints will also
// cover the "return address".
//
// If inlinedStepOut is true this function implements the StepOut operation
// for an inlined function call. Everything works the same as normal except
// when removing instructions belonging to inlined calls we also remove all
// instructions belonging to the current inlined call.
func next(dbp *Target, stepInto, inlinedStepOut bool) error {
	backward := dbp.GetDirection() == Backward
	selg := dbp.SelectedGoroutine()
	curthread := dbp.CurrentThread()
	topframe, retframe, err := topframe(selg, curthread)
	if err != nil {
		return err
	}

	if topframe.Current.Fn == nil {
		return &ErrNoSourceForPC{topframe.Current.PC}
	}

	if backward && retframe.Current.Fn == nil {
		return &ErrNoSourceForPC{retframe.Current.PC}
	}

	// sanity check
	if inlinedStepOut && !topframe.Inlined {
		panic("next called with inlinedStepOut but topframe was not inlined")
	}

	success := false
	defer func() {
		if !success {
			dbp.ClearInternalBreakpoints()
		}
	}()

	ext := filepath.Ext(topframe.Current.File)
	csource := ext != ".go" && ext != ".s"
	var thread MemoryReadWriter = curthread
	var regs Registers
	if selg != nil && selg.Thread != nil {
		thread = selg.Thread
		regs, err = selg.Thread.Registers(false)
		if err != nil {
			return err
		}
	}

	sameGCond := SameGoroutineCondition(selg)

	var firstPCAfterPrologue uint64

	if backward {
		firstPCAfterPrologue, err = FirstPCAfterPrologue(dbp, topframe.Current.Fn, false)
		if err != nil {
			return err
		}
		if firstPCAfterPrologue == topframe.Current.PC {
			// We don't want to step into the prologue so we just execute a reverse step out instead
			if err := stepOutReverse(dbp, topframe, retframe, sameGCond); err != nil {
				return err
			}

			success = true
			return nil
		}

		topframe.Ret, err = findCallInstrForRet(dbp, thread, topframe.Ret, retframe.Current.Fn)
		if err != nil {
			return err
		}
	}

	text, err := disassemble(thread, regs, dbp.Breakpoints(), dbp.BinInfo(), topframe.Current.Fn.Entry, topframe.Current.Fn.End, false)
	if err != nil && stepInto {
		return err
	}

	retFrameCond := andFrameoffCondition(sameGCond, retframe.FrameOffset())
	sameFrameCond := andFrameoffCondition(sameGCond, topframe.FrameOffset())
	var sameOrRetFrameCond ast.Expr
	if sameGCond != nil {
		if topframe.Inlined {
			sameOrRetFrameCond = sameFrameCond
		} else {
			sameOrRetFrameCond = &ast.BinaryExpr{
				Op: token.LAND,
				X:  sameGCond,
				Y: &ast.BinaryExpr{
					Op: token.LOR,
					X:  frameoffCondition(topframe.FrameOffset()),
					Y:  frameoffCondition(retframe.FrameOffset()),
				},
			}
		}
	}

	if stepInto && !backward {
		err := setStepIntoBreakpoints(dbp, text, topframe, sameGCond)
		if err != nil {
			return err
		}
	}

	if !backward {
		_, err = setDeferBreakpoint(dbp, text, topframe, sameGCond, stepInto)
		if err != nil {
			return err
		}
	}

	// Add breakpoints on all the lines in the current function
	pcs, err := topframe.Current.Fn.cu.lineInfo.AllPCsBetween(topframe.Current.Fn.Entry, topframe.Current.Fn.End-1, topframe.Current.File, topframe.Current.Line)
	if err != nil {
		return err
	}

	if backward {
		// Ensure that pcs contains firstPCAfterPrologue when reverse stepping.
		found := false
		for _, pc := range pcs {
			if pc == firstPCAfterPrologue {
				found = true
				break
			}
		}
		if !found {
			pcs = append(pcs, firstPCAfterPrologue)
		}
	}

	if !stepInto {
		// Removing any PC range belonging to an inlined call
		frame := topframe
		if inlinedStepOut {
			frame = retframe
		}
		pcs, err = removeInlinedCalls(dbp, pcs, frame)
		if err != nil {
			return err
		}
	}

	if !csource {
		var covered bool
		for i := range pcs {
			if topframe.Current.Fn.Entry <= pcs[i] && pcs[i] < topframe.Current.Fn.End {
				covered = true
				break
			}
		}

		if !covered {
			fn := dbp.BinInfo().PCToFunc(topframe.Ret)
			if selg != nil && fn != nil && fn.Name == "runtime.goexit" {
				return nil
			}
		}
	}

	for _, pc := range pcs {
		if _, err := allowDuplicateBreakpoint(dbp.SetBreakpoint(pc, NextBreakpoint, sameFrameCond)); err != nil {
			dbp.ClearInternalBreakpoints()
			return err
		}
	}

	if stepInto && backward {
		err := setStepIntoBreakpointsReverse(dbp, text, topframe, sameGCond)
		if err != nil {
			return err
		}
	}

	if !topframe.Inlined {
		// Add a breakpoint on the return address for the current frame.
		// For inlined functions there is no need to do this, the set of PCs
		// returned by the AllPCsBetween call above already cover all instructions
		// of the containing function.
		bp, err := dbp.SetBreakpoint(topframe.Ret, NextBreakpoint, retFrameCond)
		if err != nil {
			if _, isexists := err.(BreakpointExistsError); isexists {
				if bp.Kind == NextBreakpoint {
					// If the return address shares the same address with one of the lines
					// of the function (because we are stepping through a recursive
					// function) then the corresponding breakpoint should be active both on
					// this frame and on the return frame.
					bp.Cond = sameOrRetFrameCond
				}
			}
			// Return address could be wrong, if we are unable to set a breakpoint
			// there it's ok.
		}
		if bp != nil {
			configureReturnBreakpoint(dbp.BinInfo(), bp, &topframe, retFrameCond)
		}
	}

	if bp := curthread.Breakpoint(); bp.Breakpoint == nil {
		curthread.SetCurrentBreakpoint(false)
	}
	success = true
	return nil
}

func setStepIntoBreakpoints(dbp Process, text []AsmInstruction, topframe Stackframe, sameGCond ast.Expr) error {
	for _, instr := range text {
		if instr.Loc.File != topframe.Current.File || instr.Loc.Line != topframe.Current.Line || !instr.IsCall() {
			continue
		}

		if instr.DestLoc != nil {
			if err := setStepIntoBreakpoint(dbp, []AsmInstruction{instr}, sameGCond); err != nil {
				return err
			}
		} else {
			// Non-absolute call instruction, set a StepBreakpoint here
			if _, err := allowDuplicateBreakpoint(dbp.SetBreakpoint(instr.Loc.PC, StepBreakpoint, sameGCond)); err != nil {
				return err
			}
		}
	}
	return nil
}

func setStepIntoBreakpointsReverse(dbp Process, text []AsmInstruction, topframe Stackframe, sameGCond ast.Expr) error {
	// Set a breakpoint after every CALL instruction
	for i, instr := range text {
		if instr.Loc.File != topframe.Current.File || !instr.IsCall() || instr.DestLoc == nil || instr.DestLoc.Fn == nil {
			continue
		}

		if fn := instr.DestLoc.Fn; strings.HasPrefix(fn.Name, "runtime.") && !isExportedRuntime(fn.Name) {
			continue
		}

		if nextIdx := i + 1; nextIdx < len(text) {
			if _, err := allowDuplicateBreakpoint(dbp.SetBreakpoint(text[nextIdx].Loc.PC, StepBreakpoint, sameGCond)); err != nil {
				return err
			}
		}
	}
	return nil
}

// Removes instructions belonging to inlined calls of topframe from pcs.
// If includeCurrentFn is true it will also remove all instructions
// belonging to the current function.
func removeInlinedCalls(dbp Process, pcs []uint64, topframe Stackframe) ([]uint64, error) {
	image := topframe.Call.Fn.cu.image
	dwarf := image.dwarf
	irdr := reader.InlineStack(dwarf, topframe.Call.Fn.offset, 0)
	for irdr.Next() {
		e := irdr.Entry()
		if e.Offset == topframe.Call.Fn.offset {
			continue
		}
		ranges, err := dwarf.Ranges(e)
		if err != nil {
			return pcs, err
		}
		for _, rng := range ranges {
			pcs = removePCsBetween(pcs, rng[0], rng[1], image.StaticBase)
		}
		irdr.SkipChildren()
	}
	return pcs, irdr.Err()
}

func removePCsBetween(pcs []uint64, start, end, staticBase uint64) []uint64 {
	out := pcs[:0]
	for _, pc := range pcs {
		if pc < start+staticBase || pc >= end+staticBase {
			out = append(out, pc)
		}
	}
	return out
}

func setStepIntoBreakpoint(dbp Process, text []AsmInstruction, cond ast.Expr) error {
	if len(text) <= 0 {
		return nil
	}

	instr := text[0]

	if instr.DestLoc == nil {
		// Call destination couldn't be resolved because this was not the
		// current instruction, therefore the step-into breakpoint can not be set.
		return nil
	}

	fn := instr.DestLoc.Fn

	// Skip unexported runtime functions
	if fn != nil && strings.HasPrefix(fn.Name, "runtime.") && !isExportedRuntime(fn.Name) {
		return nil
	}

	//TODO(aarzilli): if we want to let users hide functions
	// or entire packages from being stepped into with 'step'
	// those extra checks should be done here.

	pc := instr.DestLoc.PC

	// Skip InhibitStepInto functions for different arch.
	if dbp.BinInfo().Arch.InhibitStepInto(dbp.BinInfo(), pc) {
		return nil
	}

	// We want to skip the function prologue but we should only do it if the
	// destination address of the CALL instruction is the entry point of the
	// function.
	// Calls to runtime.duffzero and duffcopy inserted by the compiler can
	// sometimes point inside the body of those functions, well after the
	// prologue.
	if fn != nil && fn.Entry == instr.DestLoc.PC {
		pc, _ = FirstPCAfterPrologue(dbp, fn, false)
	}

	// Set a breakpoint after the function's prologue
	if _, err := allowDuplicateBreakpoint(dbp.SetBreakpoint(pc, NextBreakpoint, cond)); err != nil {
		return err
	}

	return nil
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
