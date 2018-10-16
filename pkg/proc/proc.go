package proc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrNotExecutable is returned after attempting to execute a non-executable file
// to begin a debug session.
var ErrNotExecutable = errors.New("not an executable file")

// ErrNotRecorded is returned when an action is requested that is
// only possible on recorded (traced) programs.
var ErrNotRecorded = errors.New("not a recording")

// UnrecoveredPanic is the name given to the unrecovered panic breakpoint.
const UnrecoveredPanic = "unrecovered-panic"

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
func FindFileLocation(p Process, fileName string, lineno int) (uint64, error) {
	pc, fn, err := p.BinInfo().LineToPC(fileName, lineno)
	if err != nil {
		return 0, err
	}
	if fn.Entry == pc {
		pc, _ = FirstPCAfterPrologue(p, fn, true)
	}
	return pc, nil
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
// If firstLine == true is passed FindFunctionLocation will attempt to find the first line of the function
// If lineOffset is passed FindFunctionLocation will return the address of that line
// Pass lineOffset == 0 and firstLine == false if you want the address for the function's entry point
// Note that setting breakpoints at that address will cause surprising behavior:
// https://github.com/derekparker/delve/issues/170
func FindFunctionLocation(p Process, funcName string, firstLine bool, lineOffset int) (uint64, error) {
	bi := p.BinInfo()
	origfn := bi.LookupFunc[funcName]
	if origfn == nil {
		return 0, &ErrFunctionNotFound{funcName}
	}

	if firstLine {
		return FirstPCAfterPrologue(p, origfn, false)
	} else if lineOffset > 0 {
		filename, lineno := origfn.cu.lineInfo.PCToLine(origfn.Entry, origfn.Entry)
		breakAddr, _, err := bi.LineToPC(filename, lineno+lineOffset)
		return breakAddr, err
	}

	return origfn.Entry, nil
}

// Next continues execution until the next source line.
func Next(dbp Process) (err error) {
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

	return Continue(dbp)
}

// Continue continues execution of the debugged
// process. It will continue until it hits a breakpoint
// or is otherwise stopped.
func Continue(dbp Process) error {
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
			dbp.ClearInternalBreakpoints()
		}
	}()
	for {
		if dbp.CheckAndClearManualStopRequest() {
			dbp.ClearInternalBreakpoints()
			return nil
		}
		trapthread, err := dbp.ContinueOnce()
		if err != nil {
			return err
		}

		threads := dbp.ThreadList()

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

			switch {
			case loc.Fn.Name == "runtime.breakpoint":
				// Single-step current thread until we exit runtime.breakpoint and
				// runtime.Breakpoint.
				// On go < 1.8 it was sufficient to single-step twice on go1.8 a change
				// to the compiler requires 4 steps.
				if err := stepInstructionOut(dbp, curthread, "runtime.breakpoint", "runtime.Breakpoint"); err != nil {
					return err
				}
				return conditionErrors(threads)
			case strings.HasPrefix(loc.Fn.Name, debugCallFunctionNamePrefix1) || strings.HasPrefix(loc.Fn.Name, debugCallFunctionNamePrefix2):
				fncall := &dbp.Common().fncallState
				if !fncall.inProgress {
					return conditionErrors(threads)
				}
				fncall.step(dbp)
				// only stop execution if the function call finished
				if fncall.finished {
					fncall.inProgress = false
					if fncall.err != nil {
						return fncall.err
					}
					curthread.Common().returnValues = fncall.returnValues()
					return conditionErrors(threads)
				}
			default:
				return conditionErrors(threads)
			}
		case curbp.Active && curbp.Internal:
			if curbp.Kind == StepBreakpoint {
				// See description of proc.(*Process).next for the meaning of StepBreakpoints
				if err := conditionErrors(threads); err != nil {
					return err
				}
				regs, err := curthread.Registers(false)
				if err != nil {
					return err
				}
				pc := regs.PC()
				text, err := disassemble(curthread, regs, dbp.Breakpoints(), dbp.BinInfo(), pc, pc+maxInstructionLength, true)
				if err != nil {
					return err
				}
				// here we either set a breakpoint into the destination of the CALL
				// instruction or we determined that the called function is hidden,
				// either way we need to resume execution
				if err = setStepIntoBreakpoint(dbp, text, SameGoroutineCondition(dbp.SelectedGoroutine())); err != nil {
					return err
				}
			} else {
				curthread.Common().returnValues = curbp.Breakpoint.returnInfo.Collect(curthread)
				if err := dbp.ClearInternalBreakpoints(); err != nil {
					return err
				}
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
			return conditionErrors(threads)
		default:
			// not a manual stop, not on runtime.Breakpoint, not on a breakpoint, just repeat
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
func pickCurrentThread(dbp Process, trapthread Thread, threads []Thread) error {
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

// stepInstructionOut repeatedly calls StepInstruction until the current
// function is neither fnname1 or fnname2.
// This function is used to step out of runtime.Breakpoint as well as
// runtime.debugCallV1.
func stepInstructionOut(dbp Process, curthread Thread, fnname1, fnname2 string) error {
	for {
		if err := curthread.StepInstruction(); err != nil {
			return err
		}
		loc, err := curthread.Location()
		if err != nil || loc.Fn == nil || (loc.Fn.Name != fnname1 && loc.Fn.Name != fnname2) {
			if g := dbp.SelectedGoroutine(); g != nil {
				g.CurrentLoc = *loc
			}
			return curthread.SetCurrentBreakpoint()
		}
	}
}

// Step will continue until another source line is reached.
// Will step into functions.
func Step(dbp Process) (err error) {
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

	return Continue(dbp)
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
func StepOut(dbp Process) error {
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
		return Continue(dbp)
	}

	sameGCond := SameGoroutineCondition(selg)
	retFrameCond := andFrameoffCondition(sameGCond, retframe.FrameOffset())

	var deferpc uint64
	if filepath.Ext(topframe.Current.File) == ".go" {
		if topframe.TopmostDefer != nil && topframe.TopmostDefer.DeferredPC != 0 {
			deferfn := dbp.BinInfo().PCToFunc(topframe.TopmostDefer.DeferredPC)
			deferpc, err = FirstPCAfterPrologue(dbp, deferfn, false)
			if err != nil {
				return err
			}
		}
	}

	if deferpc != 0 && deferpc != topframe.Current.PC {
		bp, err := dbp.SetBreakpoint(deferpc, NextDeferBreakpoint, sameGCond)
		if err != nil {
			if _, ok := err.(BreakpointExistsError); !ok {
				return err
			}
		}
		if bp != nil {
			// For StepOut we do not want to step into the deferred function
			// when it's called by runtime.deferreturn so we do not populate
			// DeferReturns.
			bp.DeferReturns = []uint64{}
		}
	}

	if topframe.Ret == 0 && deferpc == 0 {
		return errors.New("nothing to stepout to")
	}

	if topframe.Ret != 0 {
		bp, err := dbp.SetBreakpoint(topframe.Ret, NextBreakpoint, retFrameCond)
		if err != nil {
			if _, isexists := err.(BreakpointExistsError); !isexists {
				return err
			}
		}
		if bp != nil {
			configureReturnBreakpoint(dbp.BinInfo(), bp, &topframe, retFrameCond)
		}
	}

	if bp := curthread.Breakpoint(); bp.Breakpoint == nil {
		curthread.SetCurrentBreakpoint()
	}

	success = true
	return Continue(dbp)
}

// GoroutinesInfo returns an array of G structures representing the information
// Delve cares about from the internal runtime G structure.
func GoroutinesInfo(dbp Process) ([]*G, error) {
	if _, err := dbp.Valid(); err != nil {
		return nil, err
	}
	if dbp.Common().allGCache != nil {
		return dbp.Common().allGCache, nil
	}

	var (
		threadg = map[int]*G{}
		allg    []*G
		rdr     = dbp.BinInfo().DwarfReader()
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

	addr, err := rdr.AddrFor("runtime.allglen", dbp.BinInfo().staticBase)
	if err != nil {
		return nil, err
	}
	allglenBytes := make([]byte, 8)
	_, err = dbp.CurrentThread().ReadMemory(allglenBytes, uintptr(addr))
	if err != nil {
		return nil, err
	}
	allglen := binary.LittleEndian.Uint64(allglenBytes)

	rdr.Seek(0)
	allgentryaddr, err := rdr.AddrFor("runtime.allgs", dbp.BinInfo().staticBase)
	if err != nil {
		// try old name (pre Go 1.6)
		allgentryaddr, err = rdr.AddrFor("runtime.allg", dbp.BinInfo().staticBase)
		if err != nil {
			return nil, err
		}
	}
	faddr := make([]byte, dbp.BinInfo().Arch.PtrSize())
	_, err = dbp.CurrentThread().ReadMemory(faddr, uintptr(allgentryaddr))
	if err != nil {
		return nil, err
	}
	allgptr := binary.LittleEndian.Uint64(faddr)

	for i := uint64(0); i < allglen; i++ {
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
				return nil, err
			}
			g.Thread = thg.Thread
			// Prefer actual thread location information.
			g.CurrentLoc = *loc
			g.SystemStack = thg.SystemStack
		}
		if g.Status != Gdead {
			allg = append(allg, g)
		}
	}
	dbp.Common().allGCache = allg

	return allg, nil
}

// FindGoroutine returns a G struct representing the goroutine
// specified by `gid`.
func FindGoroutine(dbp Process, gid int) (*G, error) {
	if gid == -1 {
		return dbp.SelectedGoroutine(), nil
	}

	gs, err := GoroutinesInfo(dbp)
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
	return nil, fmt.Errorf("Unknown goroutine %d", gid)
}

// ConvertEvalScope returns a new EvalScope in the context of the
// specified goroutine ID and stack frame.
// If deferCall is > 0 the eval scope will be relative to the specified deferred call.
func ConvertEvalScope(dbp Process, gid, frame, deferCall int) (*EvalScope, error) {
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

	locs, err := g.Stacktrace(frame+1, deferCall > 0)
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
	var gvar *Variable
	if g != nil {
		gvar = g.variable
	}

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

	s := &EvalScope{Location: frames[0].Call, Regs: frames[0].Regs, Mem: thread, Gvar: gvar, BinInfo: bi, frameOffset: frames[0].FrameOffset()}
	s.PC = frames[0].lastpc
	return s
}

// CreateUnrecoveredPanicBreakpoint creates the unrecoverable-panic breakpoint.
// This function is meant to be called by implementations of the Process interface.
func CreateUnrecoveredPanicBreakpoint(p Process, writeBreakpoint writeBreakpointFn, breakpoints *BreakpointMap) {
	panicpc, err := FindFunctionLocation(p, "runtime.startpanic", true, 0)
	if _, isFnNotFound := err.(*ErrFunctionNotFound); isFnNotFound {
		panicpc, err = FindFunctionLocation(p, "runtime.fatalpanic", true, 0)
	}
	if err == nil {
		bp, err := breakpoints.SetWithID(-1, panicpc, writeBreakpoint)
		if err == nil {
			bp.Name = UnrecoveredPanic
			bp.Variables = []string{"runtime.curg._panic.arg"}
		}
	}

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
		entryFile, entryLine := fn.cu.lineInfo.PCToLine(fn.Entry, fn.Entry)
		if pc, _, err := p.BinInfo().LineToPC(entryFile, entryLine); err == nil && pc >= fn.Entry && pc < fn.End {
			return pc, nil
		}
	}

	return pc, nil
}
