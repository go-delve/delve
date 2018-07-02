package proc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/derekparker/delve/pkg/dwarf/godwarf"
	"github.com/derekparker/delve/pkg/dwarf/reader"
)

// Thread represents a thread.
type Thread interface {
	MemoryReadWriter
	Location() (*Location, error)
	// Breakpoint will return the breakpoint that this thread is stopped at or
	// nil if the thread is not stopped at any breakpoint.
	Breakpoint() BreakpointState
	ThreadID() int
	Registers(floatingPoint bool) (Registers, error)
	Arch() Arch
	BinInfo() *BinaryInfo
	StepInstruction() error
	// Blocked returns true if the thread is blocked
	Blocked() bool
	// SetCurrentBreakpoint updates the current breakpoint of this thread
	SetCurrentBreakpoint() error
	// Common returns the CommonThread structure for this thread
	Common() *CommonThread
}

// Location represents the location of a thread.
// Holds information on the current instruction
// address, the source file:line, and the function.
type Location struct {
	PC   uint64
	File string
	Line int
	Fn   *Function
}

// ThreadBlockedError is returned when the thread
// is blocked in the scheduler.
type ThreadBlockedError struct{}

func (tbe ThreadBlockedError) Error() string {
	return "thread blocked"
}

// CommonThread contains fields used by this package, common to all
// implementations of the Thread interface.
type CommonThread struct {
	returnValues []*Variable
}

func (t *CommonThread) ReturnValues(cfg LoadConfig) []*Variable {
	loadValues(t.returnValues, cfg)
	return t.returnValues
}

// topframe returns the two topmost frames of g, or thread if g is nil.
func topframe(g *G, thread Thread) (Stackframe, Stackframe, error) {
	var frames []Stackframe
	var err error

	if g == nil {
		if thread.Blocked() {
			return Stackframe{}, Stackframe{}, ThreadBlockedError{}
		}
		frames, err = ThreadStacktrace(thread, 1)
	} else {
		frames, err = g.Stacktrace(1)
	}
	if err != nil {
		return Stackframe{}, Stackframe{}, err
	}
	switch len(frames) {
	case 0:
		return Stackframe{}, Stackframe{}, errors.New("empty stack trace")
	case 1:
		return frames[0], Stackframe{}, nil
	default:
		return frames[0], frames[1], nil
	}
}

type NoSourceForPCError struct {
	pc uint64
}

func (err *NoSourceForPCError) Error() string {
	return fmt.Sprintf("no source for pc %#x", err.pc)
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
func next(dbp Process, stepInto, inlinedStepOut bool) error {
	selg := dbp.SelectedGoroutine()
	curthread := dbp.CurrentThread()
	topframe, retframe, err := topframe(selg, curthread)
	if err != nil {
		return err
	}

	if topframe.Current.Fn == nil {
		return &NoSourceForPCError{topframe.Current.PC}
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

	text, err := disassemble(thread, regs, dbp.Breakpoints(), dbp.BinInfo(), topframe.Current.Fn.Entry, topframe.Current.Fn.End, false)
	if err != nil && stepInto {
		return err
	}

	sameGCond := SameGoroutineCondition(selg)
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

	if stepInto {
		for _, instr := range text {
			if instr.Loc.File != topframe.Current.File || instr.Loc.Line != topframe.Current.Line || !instr.IsCall() {
				continue
			}

			if instr.DestLoc != nil && instr.DestLoc.Fn != nil {
				if err := setStepIntoBreakpoint(dbp, []AsmInstruction{instr}, sameGCond); err != nil {
					return err
				}
			} else {
				// Non-absolute call instruction, set a StepBreakpoint here
				if _, err := dbp.SetBreakpoint(instr.Loc.PC, StepBreakpoint, sameGCond); err != nil {
					if _, ok := err.(BreakpointExistsError); !ok {
						return err
					}
				}
			}
		}
	}

	if !csource {
		deferreturns := []uint64{}

		// Find all runtime.deferreturn locations in the function
		// See documentation of Breakpoint.DeferCond for why this is necessary
		for _, instr := range text {
			if instr.IsCall() && instr.DestLoc != nil && instr.DestLoc.Fn != nil && instr.DestLoc.Fn.Name == "runtime.deferreturn" {
				deferreturns = append(deferreturns, instr.Loc.PC)
			}
		}

		// Set breakpoint on the most recently deferred function (if any)
		var deferpc uint64 = 0
		if selg != nil {
			deferPCEntry := selg.DeferPC()
			if deferPCEntry != 0 {
				deferfn := dbp.BinInfo().PCToFunc(deferPCEntry)
				var err error
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
			if bp != nil && stepInto {
				bp.DeferReturns = deferreturns
			}
		}
	}

	// Add breakpoints on all the lines in the current function
	pcs, err := topframe.Current.Fn.cu.lineInfo.AllPCsBetween(topframe.Current.Fn.Entry, topframe.Current.Fn.End-1, topframe.Current.File, topframe.Current.Line)
	if err != nil {
		return err
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
		if _, err := dbp.SetBreakpoint(pc, NextBreakpoint, sameFrameCond); err != nil {
			if _, ok := err.(BreakpointExistsError); !ok {
				dbp.ClearInternalBreakpoints()
				return err
			}
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
		curthread.SetCurrentBreakpoint()
	}
	success = true
	return nil
}

// Removes instructions belonging to inlined calls of topframe from pcs.
// If includeCurrentFn is true it will also remove all instructions
// belonging to the current function.
func removeInlinedCalls(dbp Process, pcs []uint64, topframe Stackframe) ([]uint64, error) {
	bi := dbp.BinInfo()
	irdr := reader.InlineStack(bi.dwarf, topframe.Call.Fn.offset, 0)
	for irdr.Next() {
		e := irdr.Entry()
		if e.Offset == topframe.Call.Fn.offset {
			continue
		}
		ranges, err := bi.dwarf.Ranges(e)
		if err != nil {
			return pcs, err
		}
		for _, rng := range ranges {
			pcs = removePCsBetween(pcs, rng[0], rng[1])
		}
		irdr.SkipChildren()
	}
	return pcs, irdr.Err()
}

func removePCsBetween(pcs []uint64, start, end uint64) []uint64 {
	out := pcs[:0]
	for _, pc := range pcs {
		if pc < start || pc >= end {
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

	if instr.DestLoc == nil || instr.DestLoc.Fn == nil {
		return nil
	}

	fn := instr.DestLoc.Fn

	// Ensure PC and Entry match, otherwise StepInto is likely to set
	// its breakpoint before DestLoc.PC and hence run too far ahead.
	// Calls to runtime.duffzero and duffcopy have this problem.
	if fn.Entry != instr.DestLoc.PC {
		return nil
	}

	// Skip unexported runtime functions
	if strings.HasPrefix(fn.Name, "runtime.") && !isExportedRuntime(fn.Name) {
		return nil
	}

	//TODO(aarzilli): if we want to let users hide functions
	// or entire packages from being stepped into with 'step'
	// those extra checks should be done here.

	// Set a breakpoint after the function's prologue
	pc, _ := FirstPCAfterPrologue(dbp, fn, false)
	if _, err := dbp.SetBreakpoint(pc, NextBreakpoint, cond); err != nil {
		if _, ok := err.(BreakpointExistsError); !ok {
			return err
		}
	}

	return nil
}

func getGVariable(thread Thread) (*Variable, error) {
	regs, err := thread.Registers(false)
	if err != nil {
		return nil, err
	}

	gaddr, hasgaddr := regs.GAddr()
	if !hasgaddr {
		gaddrbs := make([]byte, thread.Arch().PtrSize())
		_, err := thread.ReadMemory(gaddrbs, uintptr(regs.TLS()+thread.BinInfo().GStructOffset()))
		if err != nil {
			return nil, err
		}
		gaddr = binary.LittleEndian.Uint64(gaddrbs)
	}

	return newGVariable(thread, uintptr(gaddr), thread.Arch().DerefTLS())
}

func newGVariable(thread Thread, gaddr uintptr, deref bool) (*Variable, error) {
	typ, err := thread.BinInfo().findType("runtime.g")
	if err != nil {
		return nil, err
	}

	name := ""

	if deref {
		typ = &godwarf.PtrType{godwarf.CommonType{int64(thread.Arch().PtrSize()), "", reflect.Ptr, 0}, typ}
	} else {
		name = "runtime.curg"
	}

	return newVariableFromThread(thread, name, gaddr, typ), nil
}

// GetG returns information on the G (goroutine) that is executing on this thread.
//
// The G structure for a thread is stored in thread local storage. Here we simply
// calculate the address and read and parse the G struct.
//
// We cannot simply use the allg linked list in order to find the M that represents
// the given OS thread and follow its G pointer because on Darwin mach ports are not
// universal, so our port for this thread would not map to the `id` attribute of the M
// structure. Also, when linked against libc, Go prefers the libc version of clone as
// opposed to the runtime version. This has the consequence of not setting M.id for
// any thread, regardless of OS.
//
// In order to get around all this craziness, we read the address of the G structure for
// the current thread from the thread local storage area.
func GetG(thread Thread) (*G, error) {
	gaddr, err := getGVariable(thread)
	if err != nil {
		return nil, err
	}

	g, err := gaddr.parseG()
	if err != nil {
		return nil, err
	}
	if g.ID == 0 {
		// The runtime uses a special goroutine with ID == 0 to mark that the
		// current goroutine is executing on the system stack (sometimes also
		// referred to as the g0 stack or scheduler stack, I'm not sure if there's
		// actually any difference between those).
		// For our purposes it's better if we always return the real goroutine
		// since the rest of the code assumes the goroutine ID is univocal.
		// The real 'current goroutine' is stored in g0.m.curg
		curgvar, err := g.variable.fieldVariable("m").structMember("curg")
		if err != nil {
			return nil, err
		}
		g, err = curgvar.parseG()
		if err != nil {
			return nil, err
		}
		g.SystemStack = true
	}
	g.Thread = thread
	if loc, err := thread.Location(); err == nil {
		g.CurrentLoc = *loc
	}
	return g, nil
}

// ThreadScope returns an EvalScope for this thread.
func ThreadScope(thread Thread) (*EvalScope, error) {
	locations, err := ThreadStacktrace(thread, 1)
	if err != nil {
		return nil, err
	}
	if len(locations) < 1 {
		return nil, errors.New("could not decode first frame")
	}
	return FrameToScope(thread.BinInfo(), thread, nil, locations...), nil
}

// GoroutineScope returns an EvalScope for the goroutine running on this thread.
func GoroutineScope(thread Thread) (*EvalScope, error) {
	locations, err := ThreadStacktrace(thread, 1)
	if err != nil {
		return nil, err
	}
	if len(locations) < 1 {
		return nil, errors.New("could not decode first frame")
	}
	g, err := GetG(thread)
	if err != nil {
		return nil, err
	}
	return FrameToScope(thread.BinInfo(), thread, g, locations...), nil
}

func onRuntimeBreakpoint(thread Thread) bool {
	loc, err := thread.Location()
	if err != nil {
		return false
	}
	return loc.Fn != nil && loc.Fn.Name == "runtime.breakpoint"
}

// onNextGoroutine returns true if this thread is on the goroutine requested by the current 'next' command
func onNextGoroutine(thread Thread, breakpoints *BreakpointMap) (bool, error) {
	var bp *Breakpoint
	for i := range breakpoints.M {
		if breakpoints.M[i].Kind != UserBreakpoint && breakpoints.M[i].internalCond != nil {
			bp = breakpoints.M[i]
			break
		}
	}
	if bp == nil {
		return false, nil
	}
	// Internal breakpoint conditions can take multiple different forms:
	// Step into breakpoints:
	//   runtime.curg.goid == X
	// Next or StepOut breakpoints:
	//   runtime.curg.goid == X && runtime.frameoff == Y
	// Breakpoints that can be hit either by stepping on a line in the same
	// function or by returning from the function:
	//   runtime.curg.goid == X && (runtime.frameoff == Y || runtime.frameoff == Z)
	// Here we are only interested in testing the runtime.curg.goid clause.
	w := onNextGoroutineWalker{thread: thread}
	ast.Walk(&w, bp.internalCond)
	return w.ret, w.err
}

type onNextGoroutineWalker struct {
	thread Thread
	ret    bool
	err    error
}

func (w *onNextGoroutineWalker) Visit(n ast.Node) ast.Visitor {
	if binx, isbin := n.(*ast.BinaryExpr); isbin && binx.Op == token.EQL && exprToString(binx.X) == "runtime.curg.goid" {
		w.ret, w.err = evalBreakpointCondition(w.thread, n.(ast.Expr))
		return nil
	}
	return w
}
