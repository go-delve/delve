package proc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"strconv"
)

var NotExecutableErr = errors.New("not an executable file")
var NotRecordedErr = errors.New("not a recording")

const UnrecoveredPanic = "unrecovered-panic"

// ProcessExitedError indicates that the process has exited and contains both
// process id and exit status.
type ProcessExitedError struct {
	Pid    int
	Status int
}

func (pe ProcessExitedError) Error() string {
	return fmt.Sprintf("Process %d has exited with status %d", pe.Pid, pe.Status)
}

// FindFileLocation returns the PC for a given file:line.
// Assumes that `file` is normailzed to lower case and '/' on Windows.
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
		return 0, fmt.Errorf("Could not find function %s\n", funcName)
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
	if dbp.Exited() {
		return &ProcessExitedError{Pid: dbp.Pid()}
	}
	if dbp.Breakpoints().HasInternalBreakpoints() {
		return fmt.Errorf("next while nexting")
	}

	if err = next(dbp, false); err != nil {
		dbp.ClearInternalBreakpoints()
		return
	}

	return Continue(dbp)
}

// Continue continues execution of the debugged
// process. It will continue until it hits a breakpoint
// or is otherwise stopped.
func Continue(dbp Process) error {
	if dbp.Exited() {
		return &ProcessExitedError{Pid: dbp.Pid()}
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
			// runtime.Breakpoint or manual stop
			if recorded, _ := dbp.Recorded(); onRuntimeBreakpoint(curthread) && !recorded {
				// Single-step current thread until we exit runtime.breakpoint and
				// runtime.Breakpoint.
				// On go < 1.8 it was sufficient to single-step twice on go1.8 a change
				// to the compiler requires 4 steps.
				for {
					if err = curthread.StepInstruction(); err != nil {
						return err
					}
					loc, err := curthread.Location()
					if err != nil || loc.Fn == nil || (loc.Fn.Name != "runtime.breakpoint" && loc.Fn.Name != "runtime.Breakpoint") {
						break
					}
				}
			}
			return conditionErrors(threads)
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

// Step will continue until another source line is reached.
// Will step into functions.
func Step(dbp Process) (err error) {
	if dbp.Exited() {
		return &ProcessExitedError{Pid: dbp.Pid()}
	}
	if dbp.Breakpoints().HasInternalBreakpoints() {
		return fmt.Errorf("next while nexting")
	}

	if err = next(dbp, true); err != nil {
		switch err.(type) {
		case ThreadBlockedError: // Noop
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
	if dbp.Exited() {
		return &ProcessExitedError{Pid: dbp.Pid()}
	}
	selg := dbp.SelectedGoroutine()
	curthread := dbp.CurrentThread()

	topframe, retframe, err := topframe(selg, curthread)
	if err != nil {
		return err
	}

	sameGCond := SameGoroutineCondition(selg)
	retFrameCond := andFrameoffCondition(sameGCond, retframe.FrameOffset())

	var deferpc uint64 = 0
	if filepath.Ext(topframe.Current.File) == ".go" {
		if selg != nil {
			deferPCEntry := selg.DeferPC()
			if deferPCEntry != 0 {
				_, _, deferfn := dbp.BinInfo().PCToLine(deferPCEntry)
				deferpc, err = FirstPCAfterPrologue(dbp, deferfn, false)
				if err != nil {
					return err
				}
			}
		}
	}

	if topframe.Ret == 0 && deferpc == 0 {
		return errors.New("nothing to stepout to")
	}

	if deferpc != 0 && deferpc != topframe.Current.PC {
		bp, err := dbp.SetBreakpoint(deferpc, NextDeferBreakpoint, sameGCond)
		if err != nil {
			if _, ok := err.(BreakpointExistsError); !ok {
				dbp.ClearInternalBreakpoints()
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

	if topframe.Ret != 0 {
		_, err := dbp.SetBreakpoint(topframe.Ret, NextBreakpoint, retFrameCond)
		if err != nil {
			if _, isexists := err.(BreakpointExistsError); !isexists {
				dbp.ClearInternalBreakpoints()
				return err
			}
		}
	}

	if bp := curthread.Breakpoint(); bp.Breakpoint == nil {
		curthread.SetCurrentBreakpoint()
	}

	return Continue(dbp)
}

// If the argument of GoroutinesInfo implements AllGCache GoroutinesInfo
// will use the pointer returned by AllGCache as a cache.
type AllGCache interface {
	AllGCache() *[]*G
}

// GoroutinesInfo returns an array of G structures representing the information
// Delve cares about from the internal runtime G structure.
func GoroutinesInfo(dbp Process) ([]*G, error) {
	if dbp.Exited() {
		return nil, &ProcessExitedError{Pid: dbp.Pid()}
	}
	if dbp, ok := dbp.(AllGCache); ok {
		if allGCache := dbp.AllGCache(); *allGCache != nil {
			return *allGCache, nil
		}
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

	addr, err := rdr.AddrFor("runtime.allglen")
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
	allgentryaddr, err := rdr.AddrFor("runtime.allgs")
	if err != nil {
		// try old name (pre Go 1.6)
		allgentryaddr, err = rdr.AddrFor("runtime.allg")
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
			return nil, err
		}
		g, err := gvar.parseG()
		if err != nil {
			return nil, err
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
	if dbp, ok := dbp.(AllGCache); ok {
		allGCache := dbp.AllGCache()
		*allGCache = allg
	}

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
			return gs[i], nil
		}
	}
	return nil, fmt.Errorf("Unknown goroutine %d", gid)
}

// ConvertEvalScope returns a new EvalScope in the context of the
// specified goroutine ID and stack frame.
func ConvertEvalScope(dbp Process, gid, frame int) (*EvalScope, error) {
	if dbp.Exited() {
		return nil, &ProcessExitedError{Pid: dbp.Pid()}
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

	locs, err := g.Stacktrace(frame)
	if err != nil {
		return nil, err
	}

	if frame >= len(locs) {
		return nil, fmt.Errorf("Frame %d does not exist in goroutine %d", frame, gid)
	}

	return FrameToScope(dbp.BinInfo(), thread, g, locs[frame]), nil
}

// FrameToScope returns a new EvalScope for this frame
func FrameToScope(bi *BinaryInfo, thread MemoryReadWriter, g *G, frame Stackframe) *EvalScope {
	var gvar *Variable
	if g != nil {
		gvar = g.variable
	}
	s := &EvalScope{PC: frame.Call.PC, Regs: frame.Regs, Mem: thread, Gvar: gvar, BinInfo: bi, frameOffset: frame.FrameOffset()}
	return s
}
