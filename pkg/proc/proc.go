package proc

import (
	"debug/gosym"
	"encoding/binary"
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/debug/dwarf"
)

type functionDebugInfo struct {
	lowpc, highpc uint64
	offset        dwarf.Offset
}

var NotExecutableErr = errors.New("not an executable file")

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
func FindFileLocation(mem MemoryReadWriter, breakpoints map[uint64]*Breakpoint, bi *BinaryInfo, fileName string, lineno int) (uint64, error) {
	pc, fn, err := bi.goSymTable.LineToPC(fileName, lineno)
	if err != nil {
		return 0, err
	}
	if fn.Entry == pc {
		pc, _ = FirstPCAfterPrologue(mem, breakpoints, bi, fn, true)
	}
	return pc, nil
}

// FindFunctionLocation finds address of a function's line
// If firstLine == true is passed FindFunctionLocation will attempt to find the first line of the function
// If lineOffset is passed FindFunctionLocation will return the address of that line
// Pass lineOffset == 0 and firstLine == false if you want the address for the function's entry point
// Note that setting breakpoints at that address will cause surprising behavior:
// https://github.com/derekparker/delve/issues/170
func FindFunctionLocation(mem MemoryReadWriter, breakpoints map[uint64]*Breakpoint, bi *BinaryInfo, funcName string, firstLine bool, lineOffset int) (uint64, error) {
	origfn := bi.goSymTable.LookupFunc(funcName)
	if origfn == nil {
		return 0, fmt.Errorf("Could not find function %s\n", funcName)
	}

	if firstLine {
		return FirstPCAfterPrologue(mem, breakpoints, bi, origfn, false)
	} else if lineOffset > 0 {
		filename, lineno, _ := bi.goSymTable.PCToLine(origfn.Entry)
		breakAddr, _, err := bi.goSymTable.LineToPC(filename, lineno+lineOffset)
		return breakAddr, err
	}

	return origfn.Entry, nil
}

// Next continues execution until the next source line.
func Next(dbp Continuable) (err error) {
	if dbp.Exited() {
		return &ProcessExitedError{}
	}
	for _, bp := range dbp.Breakpoints() {
		if bp.Internal() {
			return fmt.Errorf("next while nexting")
		}
	}

	if err = next(dbp, false); err != nil {
		switch err.(type) {
		case ThreadBlockedError, NoReturnAddr: // Noop
		default:
			dbp.ClearInternalBreakpoints()
			return
		}
	}

	return Continue(dbp)
}

// Continuable is the subinterface of target.Interface used to implement
// Continue/Next/etc.
type Continuable interface {
	ContinueOnce() (trapthread IThread, err error)
	CurrentThread() IThread
	SelectedGoroutine() *G
	Breakpoints() map[uint64]*Breakpoint
	ThreadList() []IThread
	SwitchThread(int) error
	BinInfo() *BinaryInfo
	ClearInternalBreakpoints() error
	FirstPCAfterPrologue(fn *gosym.Func, sameline bool) (uint64, error)
	SetBreakpoint(addr uint64, kind BreakpointKind, cond ast.Expr) (*Breakpoint, error)
	Exited() bool
}

// Continue continues execution of the debugged
// process. It will continue until it hits a breakpoint
// or is otherwise stopped.
func Continue(dbp Continuable) error {
	for {
		trapthread, err := dbp.ContinueOnce()
		if err != nil {
			return err
		}

		threads := dbp.ThreadList()

		if err := pickCurrentThread(dbp, trapthread, threads); err != nil {
			return err
		}

		curthread := dbp.CurrentThread()
		curbp, curbpActive, _ := curthread.Breakpoint()

		switch {
		case curbp == nil:
			// runtime.Breakpoint or manual stop
			if onRuntimeBreakpoint(curthread) {
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
		case curbpActive && curbp.Internal():
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
				text, err := disassemble(curthread, regs, dbp.Breakpoints(), dbp.BinInfo(), pc, pc+maxInstructionLength)
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
		case curbpActive:
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
			return conditionErrors(threads)
		default:
			// not a manual stop, not on runtime.Breakpoint, not on a breakpoint, just repeat
		}
	}
}

func conditionErrors(threads []IThread) error {
	var condErr error
	for _, th := range threads {
		if bp, _, bperr := th.Breakpoint(); bp != nil && bperr != nil {
			if condErr == nil {
				condErr = bperr
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
func pickCurrentThread(dbp Continuable, trapthread IThread, threads []IThread) error {
	for _, th := range threads {
		if bp, active, _ := th.Breakpoint(); active && bp.Internal() {
			return dbp.SwitchThread(th.ThreadID())
		}
	}
	if _, active, _ := trapthread.Breakpoint(); active {
		return dbp.SwitchThread(trapthread.ThreadID())
	}
	for _, th := range threads {
		if _, active, _ := th.Breakpoint(); active {
			return dbp.SwitchThread(th.ThreadID())
		}
	}
	return dbp.SwitchThread(trapthread.ThreadID())
}

// Step will continue until another source line is reached.
// Will step into functions.
func Step(dbp Continuable) (err error) {
	if dbp.Exited() {
		return &ProcessExitedError{}
	}
	for _, bp := range dbp.Breakpoints() {
		if bp.Internal() {
			return fmt.Errorf("next while nexting")
		}
	}

	if err = next(dbp, true); err != nil {
		switch err.(type) {
		case ThreadBlockedError, NoReturnAddr: // Noop
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

// StepOut will continue until the current goroutine exits the
// function currently being executed or a deferred function is executed
func StepOut(dbp Continuable) error {
	selg := dbp.SelectedGoroutine()
	curthread := dbp.CurrentThread()
	cond := SameGoroutineCondition(selg)

	topframe, err := topframe(selg, curthread)
	if err != nil {
		return err
	}

	pcs := []uint64{}

	var deferpc uint64 = 0
	if filepath.Ext(topframe.Current.File) == ".go" {
		if selg != nil {
			deferPCEntry := selg.DeferPC()
			if deferPCEntry != 0 {
				_, _, deferfn := dbp.BinInfo().PCToLine(deferPCEntry)
				deferpc, err = dbp.FirstPCAfterPrologue(deferfn, false)
				if err != nil {
					return err
				}
				pcs = append(pcs, deferpc)
			}
		}
	}

	if topframe.Ret == 0 && deferpc == 0 {
		return errors.New("nothing to stepout to")
	}

	if deferpc != 0 && deferpc != topframe.Current.PC {
		bp, err := dbp.SetBreakpoint(deferpc, NextDeferBreakpoint, cond)
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
		if err := setInternalBreakpoints(dbp, topframe.Current.PC, []uint64{topframe.Ret}, NextBreakpoint, cond); err != nil {
			return err
		}
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
func GoroutinesInfo(dbp EvalScopeConvertible) ([]*G, error) {
	if dbp.Exited() {
		return nil, &ProcessExitedError{}
	}
	if dbp, ok := dbp.(AllGCache); ok {
		if allGCache := dbp.AllGCache(); *allGCache != nil {
			return *allGCache, nil
		}
	}

	var (
		threadg = map[int]IThread{}
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
			threadg[g.ID] = th
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
		if thread, allocated := threadg[g.ID]; allocated {
			loc, err := thread.Location()
			if err != nil {
				return nil, err
			}
			g.Thread = thread
			// Prefer actual thread location information.
			g.CurrentLoc = *loc
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

func GetGoInformation(p Continuable) (ver GoVersion, isextld bool, err error) {
	scope := &EvalScope{0, 0, p.CurrentThread(), nil, p.BinInfo()}
	vv, err := scope.packageVarAddr("runtime.buildVersion")
	if err != nil {
		return ver, false, fmt.Errorf("Could not determine version number: %v", err)
	}
	vv.loadValue(LoadConfig{true, 0, 64, 0, 0})
	if vv.Unreadable != nil {
		err = fmt.Errorf("Unreadable version number: %v\n", vv.Unreadable)
		return
	}

	ver, ok := ParseVersionString(constant.StringVal(vv.Value))
	if !ok {
		err = fmt.Errorf("Could not parse version number: %v\n", vv.Value)
		return
	}

	rdr := scope.BinInfo.DwarfReader()
	rdr.Seek(0)
	for entry, err := rdr.NextCompileUnit(); entry != nil; entry, err = rdr.NextCompileUnit() {
		if err != nil {
			return ver, isextld, err
		}
		if prod, ok := entry.Val(dwarf.AttrProducer).(string); ok && (strings.HasPrefix(prod, "GNU AS")) {
			isextld = true
			break
		}
	}
	return
}

// FindGoroutine returns a G struct representing the goroutine
// specified by `gid`.
func FindGoroutine(dbp EvalScopeConvertible, gid int) (*G, error) {
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

// EvalScopeConvertible is a subset of target.Interface with the methods
// used by ConvertEvalScope/GoroutinesInfo/etc.
type EvalScopeConvertible interface {
	Exited() bool
	SelectedGoroutine() *G
	CurrentThread() IThread
	BinInfo() *BinaryInfo
	ThreadList() []IThread
}

// ConvertEvalScope returns a new EvalScope in the context of the
// specified goroutine ID and stack frame.
func ConvertEvalScope(dbp EvalScopeConvertible, gid, frame int) (*EvalScope, error) {
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

	PC, CFA := locs[frame].Current.PC, locs[frame].CFA

	return &EvalScope{PC, CFA, thread, g.variable, dbp.BinInfo()}, nil
}

// FrameToScope returns a new EvalScope for this frame
func FrameToScope(p EvalScopeConvertible, frame Stackframe) *EvalScope {
	return &EvalScope{frame.Current.PC, frame.CFA, p.CurrentThread(), nil, p.BinInfo()}
}
