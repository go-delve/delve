package debug

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/go-delve/delve/pkg/dwarf/reader"
	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/core"
	"github.com/go-delve/delve/pkg/proc/gdbserial"
	"github.com/go-delve/delve/pkg/proc/native"
)

// ErrNoSourceForPC is returned when the given address
// does not correspond with a source file location.
type ErrNoSourceForPC struct {
	pc uint64
}

func (err *ErrNoSourceForPC) Error() string {
	return fmt.Sprintf("no source for PC %#x", err.pc)
}

// Target represents the process being debugged.
// It is responsible for implementing the high level logic
// that is used to manipulate and inspect a running process.
type Target struct {
	proc.Process

	bi *proc.BinaryInfo

	// Breakpoint table, holds information on breakpoints.
	// Maps instruction address to Breakpoint struct.
	breakpoints proc.BreakpointMap
}

// New returns an initialized Target.
func New(p proc.Process, os, arch string, debugInfoDirs []string) (*Target, error) {
	bi := proc.NewBinaryInfo(os, arch, debugInfoDirs)
	t := &Target{
		Process:     p,
		bi:          bi,
		breakpoints: proc.NewBreakpointMap(),
	}
	// TODO(refactor) REMOVE BEFORE MERGE
	p.SetTarget(t)
	if err := t.Initialize(); err != nil {
		return nil, err
	}
	return t, nil
}

// ErrNoAttachPath is the error returned when the client tries to attach to
// a process on macOS using the lldb backend without specifying the path to
// the target's executable.
var ErrNoAttachPath = errors.New("must specify executable path on macOS")

// Attach will attach to the process specified by 'pid' using the backend specified.
// If debugInfoDirs is provided those directories will be included when looking up
// debug information that is separate from the binary.
func Attach(pid int, path, backend string, debugInfoDirs []string) (*Target, error) {
	var (
		p   proc.Process
		err error
	)

	switch backend {
	case "native":
		p, err = native.Attach(pid)
	case "lldb":
		p, err = betterGdbserialLaunchError(gdbserial.LLDBAttach(pid, path))
	case "default":
		if runtime.GOOS == "darwin" {
			p, err = betterGdbserialLaunchError(gdbserial.LLDBAttach(pid, path))
			break
		}
		p, err = native.Attach(pid)
	default:
		return nil, fmt.Errorf("unknown backend %q", backend)
	}
	if err != nil {
		return nil, err
	}
	t, err := New(p, runtime.GOOS, runtime.GOARCH, debugInfoDirs)
	if err != nil {
		p.Detach(false)
		return nil, err
	}
	return t, nil
}

// Launch will start a process with the given args and working directory using the
// backend specified.
// If foreground is true the process will have access to stdin.
// If debugInfoDirs is provided those directories will be included when looking up
// debug information that is separate from the binary.
func Launch(processArgs []string, wd string, foreground bool, backend string, debugInfoDirs []string) (*Target, error) {
	var (
		p   proc.Process
		err error
	)
	switch backend {
	case "native":
		p, err = native.Launch(processArgs, wd, foreground)
	case "lldb":
		p, err = betterGdbserialLaunchError(gdbserial.LLDBLaunch(processArgs, wd, foreground))
	case "rr":
		p, _, err = gdbserial.RecordAndReplay(processArgs, wd, false)
	case "default":
		if runtime.GOOS == "darwin" {
			p, err = betterGdbserialLaunchError(gdbserial.LLDBLaunch(processArgs, wd, foreground))
			break
		}
		p, err = native.Launch(processArgs, wd, foreground)
	default:
		return nil, fmt.Errorf("unknown backend %q", backend)
	}
	if err != nil {
		return nil, err
	}
	t, err := New(p, runtime.GOOS, runtime.GOARCH, debugInfoDirs)
	if err != nil {
		p.Detach(true)
		return nil, err
	}
	return t, nil
}

// OpenCoreOrRecording takes a path and opens either an RR recording or a core file
// depending on the backend selection.
// If a "rr" is not specified, it will attempt to open a core file.
// If opening a core file argv0 should be the path to the binary that produced the core file.
// If debugInfoDirs is provided those directories will be included when looking up
// debug information that is separate from the binary.
func OpenCoreOrRecording(backend, path, argv0 string, debugInfoDirs []string) (*Target, error) {
	var (
		p   proc.Process
		err error

		os   = runtime.GOOS
		arch = runtime.GOARCH
	)
	switch backend {
	case "rr":
		p, err = gdbserial.Replay(path, false, false)
	default:
		p, os, arch, err = core.OpenCore(path, argv0)
	}
	if err != nil {
		return nil, err
	}
	return New(p, os, arch, debugInfoDirs)
}

var errMacOSBackendUnavailable = errors.New("debugserver or lldb-server not found: install XCode's command line tools or lldb-server")

func betterGdbserialLaunchError(p proc.Process, err error) (proc.Process, error) {
	if runtime.GOOS != "darwin" {
		return p, err
	}
	if _, isUnavailable := err.(*gdbserial.ErrBackendUnavailable); !isUnavailable {
		return p, err
	}

	return p, errMacOSBackendUnavailable
}

// Initialize performs any setup that must be taken after
// we have successfully attached to the process we are going to debug.
// This includes any post-startup initialization the process must perform,
// as well as setting the default goroutine and creating some initial breakpoints
// that are set by default to catch when the process crashes or panics.
func (t *Target) Initialize() error {
	entry, err := t.Process.EntryPoint()
	if err != nil {
		return err
	}
	if err := t.bi.AddImage(t.Process.ExecutablePath(), entry); err != nil {
		return err
	}

	if err := t.Process.Initialize(); err != nil {
		return err
	}

	g, _ := proc.GetG(t.CurrentThread())
	t.SetSelectedGoroutine(g)

	createUnrecoveredPanicBreakpoint(t)
	createFatalThrowBreakpoint(t)
	return nil
}

// Next continues execution until the next source line.
func (t *Target) Next() (err error) {
	if _, err := t.Valid(); err != nil {
		return err
	}
	if t.Breakpoints().HasInternalBreakpoints() {
		return fmt.Errorf("next while nexting")
	}

	if err = next(t, false, false); err != nil {
		t.ClearInternalBreakpoints()
		return
	}

	return t.Continue()
}

func setStepIntoBreakpoint(t *Target, text []proc.AsmInstruction, cond ast.Expr) error {
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
	if fn != nil && strings.HasPrefix(fn.Name, "runtime.") && !proc.IsExportedRuntime(fn.Name) {
		return nil
	}

	//TODO(aarzilli): if we want to let users hide functions
	// or entire packages from being stepped into with 'step'
	// those extra checks should be done here.

	pc := instr.DestLoc.PC

	// We want to skip the function prologue but we should only do it if the
	// destination address of the CALL instruction is the entry point of the
	// function.
	// Calls to runtime.duffzero and duffcopy inserted by the compiler can
	// sometimes point inside the body of those functions, well after the
	// prologue.
	if fn != nil && fn.Entry == instr.DestLoc.PC {
		pc, _ = proc.FirstPCAfterPrologue(t, fn, false)
	}

	// Set a breakpoint after the function's prologue
	if _, err := t.SetBreakpoint(pc, proc.NextBreakpoint, cond); err != nil {
		if _, ok := err.(proc.BreakpointExistsError); !ok {
			return err
		}
	}

	return nil
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
func next(t *Target, stepInto, inlinedStepOut bool) error {
	selg := t.SelectedGoroutine()
	curthread := t.CurrentThread()
	topframe, retframe, err := proc.Topframe(selg, curthread)
	if err != nil {
		return err
	}

	if topframe.Current.Fn == nil {
		return &ErrNoSourceForPC{topframe.Current.PC}
	}

	// sanity check
	if inlinedStepOut && !topframe.Inlined {
		panic("next called with inlinedStepOut but topframe was not inlined")
	}

	success := false
	defer func() {
		if !success {
			t.ClearInternalBreakpoints()
		}
	}()

	ext := filepath.Ext(topframe.Current.File)
	csource := ext != ".go" && ext != ".s"
	var thread proc.MemoryReadWriter = curthread
	var regs proc.Registers
	if selg != nil && selg.Thread != nil {
		thread = selg.Thread
		regs, err = selg.Thread.Registers(false)
		if err != nil {
			return err
		}
	}

	text, err := proc.Disassemble(thread, regs, t.Breakpoints(), t.BinInfo(), topframe.Current.Fn.Entry, topframe.Current.Fn.End)
	if err != nil && stepInto {
		return err
	}

	sameGCond := proc.SameGoroutineCondition(selg)
	retFrameCond := proc.AndFrameoffCondition(sameGCond, retframe.FrameOffset())
	sameFrameCond := proc.AndFrameoffCondition(sameGCond, topframe.FrameOffset())
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
					X:  proc.FrameoffCondition(topframe.FrameOffset()),
					Y:  proc.FrameoffCondition(retframe.FrameOffset()),
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
				if err := setStepIntoBreakpoint(t, []proc.AsmInstruction{instr}, sameGCond); err != nil {
					return err
				}
			} else {
				// Non-absolute call instruction, set a StepBreakpoint here
				if _, err := t.SetBreakpoint(instr.Loc.PC, proc.StepBreakpoint, sameGCond); err != nil {
					if _, ok := err.(proc.BreakpointExistsError); !ok {
						return err
					}
				}
			}
		}
	}

	if !csource {
		deferreturns := FindDeferReturnCalls(text)

		// Set breakpoint on the most recently deferred function (if any)
		var deferpc uint64
		if topframe.TopmostDefer != nil && topframe.TopmostDefer.DeferredPC != 0 {
			deferfn := t.BinInfo().PCToFunc(topframe.TopmostDefer.DeferredPC)
			var err error
			deferpc, err = proc.FirstPCAfterPrologue(t, deferfn, false)
			if err != nil {
				return err
			}
		}
		if deferpc != 0 && deferpc != topframe.Current.PC {
			bp, err := t.SetBreakpoint(deferpc, proc.NextDeferBreakpoint, sameGCond)
			if err != nil {
				if _, ok := err.(proc.BreakpointExistsError); !ok {
					return err
				}
			}
			if bp != nil && stepInto {
				bp.DeferReturns = deferreturns
			}
		}
	}

	// Add breakpoints on all the lines in the current function
	pcs, err := topframe.Current.Fn.CompileUnit.LineInfo.AllPCsBetween(topframe.Current.Fn.Entry, topframe.Current.Fn.End-1, topframe.Current.File, topframe.Current.Line)
	if err != nil {
		return err
	}

	if !stepInto {
		// Removing any PC range belonging to an inlined call
		frame := topframe
		if inlinedStepOut {
			frame = retframe
		}
		pcs, err = removeInlinedCalls(t, pcs, frame)
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
			fn := t.BinInfo().PCToFunc(topframe.Ret)
			if selg != nil && fn != nil && fn.Name == "runtime.goexit" {
				return nil
			}
		}
	}

	for _, pc := range pcs {
		if _, err := t.SetBreakpoint(pc, proc.NextBreakpoint, sameFrameCond); err != nil {
			if _, ok := err.(proc.BreakpointExistsError); !ok {
				t.ClearInternalBreakpoints()
				return err
			}
		}

	}
	if !topframe.Inlined {
		// Add a breakpoint on the return address for the current frame.
		// For inlined functions there is no need to do this, the set of PCs
		// returned by the AllPCsBetween call above already cover all instructions
		// of the containing function.
		bp, err := t.SetBreakpoint(topframe.Ret, proc.NextBreakpoint, retFrameCond)
		if err != nil {
			if _, isexists := err.(proc.BreakpointExistsError); isexists {
				if bp.Kind == proc.NextBreakpoint {
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
			proc.ConfigureReturnBreakpoint(t.BinInfo(), bp, &topframe, retFrameCond)
		}
	}

	if bp := curthread.Breakpoint(); bp.Breakpoint == nil {
		curthread.SetCurrentBreakpoint(false)
	}
	success = true
	return nil
}

// Removes instructions belonging to inlined calls of topframe from pcs.
// If includeCurrentFn is true it will also remove all instructions
// belonging to the current function.
func removeInlinedCalls(t *Target, pcs []uint64, topframe proc.Stackframe) ([]uint64, error) {
	image := topframe.Call.Fn.CompileUnit.Image
	dwarf := image.Dwarf
	irdr := reader.InlineStack(dwarf, topframe.Call.Fn.Offset, 0)
	for irdr.Next() {
		e := irdr.Entry()
		if e.Offset == topframe.Call.Fn.Offset {
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

// Continue continues execution of the debugged
// process. It will continue until it hits a breakpoint
// or is otherwise stopped.
func (t *Target) Continue() error {
	if _, err := t.Valid(); err != nil {
		return err
	}
	for _, thread := range t.ThreadList() {
		thread.Common().RetVals = nil
	}
	t.CheckAndClearManualStopRequest()
	defer func() {
		// Make sure we clear internal breakpoints if we simultaneously receive a
		// manual stop request and hit a breakpoint.
		if t.CheckAndClearManualStopRequest() {
			t.ClearInternalBreakpoints()
		}
	}()
	for {
		if t.CheckAndClearManualStopRequest() {
			t.ClearInternalBreakpoints()
			return nil
		}
		trapthread, err := t.Resume()
		if err != nil {
			return err
		}

		threads := t.ThreadList()

		callInjectionDone, err := proc.CallInjectionProtocol(t, threads)
		if err != nil {
			return err
		}

		if err := pickCurrentThread(t, trapthread, threads); err != nil {
			return err
		}

		curthread := t.CurrentThread()
		curbp := curthread.Breakpoint()

		switch {
		case curbp.Breakpoint == nil:
			// runtime.Breakpoint, manual stop or debugCallV1-related stop
			recorded, _ := t.Recorded()
			if recorded {
				return conditionErrors(threads)
			}

			loc, err := curthread.Location()
			if err != nil || loc.Fn == nil {
				return conditionErrors(threads)
			}
			g, _ := proc.GetG(curthread)

			switch {
			case loc.Fn.Name == "runtime.breakpoint":
				// In linux-arm64, PtraceSingleStep seems cannot step over BRK instruction
				// (linux-arm64 feature or kernel bug maybe).
				if !curthread.Arch().BreakInstrMovesPC() {
					curthread.SetPC(loc.PC + uint64(curthread.Arch().BreakpointSize()))
				}
				// Single-step current thread until we exit runtime.breakpoint and
				// runtime.Breakpoint.
				// On go < 1.8 it was sufficient to single-step twice on go1.8 a change
				// to the compiler requires 4 steps.
				if err := t.StepInstructionOut(curthread, "runtime.breakpoint", "runtime.Breakpoint"); err != nil {
					return err
				}
				return conditionErrors(threads)
			case g == nil || t.Common().FnCallForG[g.ID] == nil:
				// a hardcoded breakpoint somewhere else in the code (probably cgo)
				return conditionErrors(threads)
			}
		case curbp.Active && curbp.Internal:
			switch curbp.Kind {
			case proc.StepBreakpoint:
				// See description of proc.(*Process).next for the meaning of StepBreakpoints
				if err := conditionErrors(threads); err != nil {
					return err
				}
				regs, err := curthread.Registers(false)
				if err != nil {
					return err
				}
				pc := regs.PC()
				text, err := proc.Disassemble(curthread, regs, t.Breakpoints(), t.BinInfo(), pc, pc+proc.MaxInstructionLength)
				if err != nil {
					return err
				}
				// here we either set a breakpoint into the destination of the CALL
				// instruction or we determined that the called function is hidden,
				// either way we need to resume execution
				if err = setStepIntoBreakpoint(t, text, proc.SameGoroutineCondition(t.SelectedGoroutine())); err != nil {
					return err
				}
			default:
				curthread.Common().RetVals = curbp.Breakpoint.ReturnInfo.Collect(curthread)
				if err := t.ClearInternalBreakpoints(); err != nil {
					return err
				}
				return conditionErrors(threads)
			}
		case curbp.Active:
			onNextGoroutine, err := onNextGoroutine(curthread, t.Breakpoints())
			if err != nil {
				return err
			}
			if onNextGoroutine {
				err := t.ClearInternalBreakpoints()
				if err != nil {
					return err
				}
			}
			if curbp.Name == UnrecoveredPanic {
				t.ClearInternalBreakpoints()
			}
			return conditionErrors(threads)
		default:
			// not a manual stop, not on runtime.Breakpoint, not on a breakpoint, just repeat
		}
		if callInjectionDone {
			// a call injection was finished, don't let a breakpoint with a failed
			// condition or a step breakpoint shadow this.
			return conditionErrors(threads)
		}
	}
}

// onNextGoroutine returns true if this thread is on the goroutine requested by the current 'next' command
func onNextGoroutine(thread proc.Thread, breakpoints *proc.BreakpointMap) (bool, error) {
	var bp *proc.Breakpoint
	for i := range breakpoints.M {
		if breakpoints.M[i].Kind != proc.UserBreakpoint && breakpoints.M[i].InternalCond() != nil {
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
	ast.Walk(&w, bp.InternalCond())
	return w.ret, w.err
}

type onNextGoroutineWalker struct {
	thread proc.Thread
	ret    bool
	err    error
}

func (w *onNextGoroutineWalker) Visit(n ast.Node) ast.Visitor {
	if binx, isbin := n.(*ast.BinaryExpr); isbin && binx.Op == token.EQL && proc.ExprToString(binx.X) == "runtime.curg.goid" {
		w.ret, w.err = proc.EvalBreakpointCondition(w.thread, n.(ast.Expr))
		return nil
	}
	return w
}

func conditionErrors(threads []proc.Thread) error {
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

// pick a new t.currentThread, with the following priority:
// 	- a thread with onTriggeredInternalBreakpoint() == true
// 	- a thread with onTriggeredBreakpoint() == true (prioritizing trapthread)
// 	- trapthread
func pickCurrentThread(t *Target, trapthread proc.Thread, threads []proc.Thread) error {
	for _, th := range threads {
		if bp := th.Breakpoint(); bp.Active && bp.Internal {
			return t.SwitchThread(th.ThreadID())
		}
	}
	if bp := trapthread.Breakpoint(); bp.Active {
		return t.SwitchThread(trapthread.ThreadID())
	}
	for _, th := range threads {
		if bp := th.Breakpoint(); bp.Active {
			return t.SwitchThread(th.ThreadID())
		}
	}
	return t.SwitchThread(trapthread.ThreadID())
}

// StepInstructionOut repeatedly calls StepInstruction until the current
// function is neither fnname1 or fnname2.
// This function is used to step out of runtime.Breakpoint as well as
// runtime.debugCallV1.
func (t *Target) StepInstructionOut(curthread proc.Thread, fnname1, fnname2 string) error {
	for {
		if err := curthread.StepInstruction(); err != nil {
			return err
		}
		loc, err := curthread.Location()
		if err != nil || loc.Fn == nil || (loc.Fn.Name != fnname1 && loc.Fn.Name != fnname2) {
			g, _ := proc.GetG(curthread)
			selg := t.SelectedGoroutine()
			if g != nil && selg != nil && g.ID == selg.ID {
				selg.CurrentLoc = *loc
			}
			return curthread.SetCurrentBreakpoint(true)
		}
	}
}

// Step will continue until another source line is reached.
// Will step into functions.
func (t *Target) Step() (err error) {
	if _, err := t.Valid(); err != nil {
		return err
	}
	if t.Breakpoints().HasInternalBreakpoints() {
		return fmt.Errorf("next while nexting")
	}

	if err = next(t, true, false); err != nil {
		switch err.(type) {
		case proc.ErrThreadBlocked: // Noop
		default:
			t.ClearInternalBreakpoints()
			return
		}
	}

	return t.Continue()
}

// StepOut will continue until the current goroutine exits the
// function currently being executed or a deferred function is executed
func (t *Target) StepOut() error {
	if _, err := t.Valid(); err != nil {
		return err
	}
	if t.Breakpoints().HasInternalBreakpoints() {
		return fmt.Errorf("next while nexting")
	}

	selg := t.SelectedGoroutine()
	curthread := t.CurrentThread()

	topframe, retframe, err := proc.Topframe(selg, curthread)
	if err != nil {
		return err
	}

	success := false
	defer func() {
		if !success {
			t.ClearInternalBreakpoints()
		}
	}()

	if topframe.Inlined {
		if err := next(t, false, true); err != nil {
			return err
		}

		success = true
		return t.Continue()
	}

	sameGCond := proc.SameGoroutineCondition(selg)
	retFrameCond := proc.AndFrameoffCondition(sameGCond, retframe.FrameOffset())

	var deferpc uint64
	if filepath.Ext(topframe.Current.File) == ".go" {
		if topframe.TopmostDefer != nil && topframe.TopmostDefer.DeferredPC != 0 {
			deferfn := t.BinInfo().PCToFunc(topframe.TopmostDefer.DeferredPC)
			deferpc, err = proc.FirstPCAfterPrologue(t, deferfn, false)
			if err != nil {
				return err
			}
		}
	}

	if deferpc != 0 && deferpc != topframe.Current.PC {
		bp, err := t.SetBreakpoint(deferpc, proc.NextDeferBreakpoint, sameGCond)
		if err != nil {
			if _, ok := err.(proc.BreakpointExistsError); !ok {
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
		bp, err := t.SetBreakpoint(topframe.Ret, proc.NextBreakpoint, retFrameCond)
		if err != nil {
			if _, isexists := err.(proc.BreakpointExistsError); !isexists {
				return err
			}
		}
		if bp != nil {
			proc.ConfigureReturnBreakpoint(t.BinInfo(), bp, &topframe, retFrameCond)
		}
	}

	if bp := curthread.Breakpoint(); bp.Breakpoint == nil {
		curthread.SetCurrentBreakpoint(false)
	}

	success = true
	return t.Continue()
}

// Detach will force the target to stop tracing the process.
// If kill is true the process will be killed during the detach.
func (t *Target) Detach(kill bool) error {
	return t.Process.Detach(kill)
}

// BinInfo returns information on the binary that is
// being debugged by this target.
// The information returned includes data gathered from
// parsing various sections of the binary.
// This is useful for getting line number translations, symbol
// information, and much more.
// See the documentation for BinaryInfo for more information.
func (t *Target) BinInfo() *proc.BinaryInfo {
	return t.bi
}

// Pid returns the PID of the process this target is attached to.
func (t *Target) Pid() int {
	return t.Process.Pid()
}

// SelectedGoroutine returns the goroutine which will be used as the default for
// operations if a specific goroutine is not specified.
// This is usually the goroutine that active on the thread which was stopped due to
// hitting a breakpoint.
// It could also be a goroutine the user selected.
func (t *Target) SelectedGoroutine() *proc.G {
	return t.Process.SelectedGoroutine()
}

// Recorded returns whether or not the target was recorded.
func (t *Target) Recorded() (bool, string) {
	return t.Process.Recorded()
}

// Restart allows you to restart the process from a given location.
// Only works when the selected backend is "rr".
func (t *Target) Restart(from string) error { return t.Process.Restart(from) }

// Direction controls whether execution goes forward or backward depending on the
// settings. This is only valid when using the "rr" backend.
func (t *Target) Direction(dir proc.Direction) error { return t.Process.Direction(dir) }

// When returns rr's current internal event number. Only valid when using the
// "rr" backend.
func (t *Target) When() (string, error) { return t.Process.When() }

// Checkpoint allow you to set a checkpoint at a certain location.
// Only valid with the "rr" backend.
func (t *Target) Checkpoint(where string) (int, error) { return t.Process.Checkpoint(where) }

// Checkpoints returns a list of currently active checkpoints.
// Only valid with the "rr" backend.
func (t *Target) Checkpoints() ([]proc.Checkpoint, error) { return t.Process.Checkpoints() }

// ClearCheckpoint will clear the checkpoint with the ID "n".
// Only valid with the "rr" backend.
func (t *Target) ClearCheckpoint(n int) error { return t.Process.ClearCheckpoint(n) }

// Valid returns true if the underlying process is in a state where
// it can be manipulated. This means it hasn't exited or been detached from.
func (t *Target) Valid() (bool, error) { return t.Process.Valid() }

// ResumeNotify specifies a channel that will be closed the next time
// Resume finishes resuming the underlying process.
func (t *Target) ResumeNotify(ch chan<- struct{}) { t.Process.ResumeNotify(ch) }

// ThreadList returns a list of threads in the underlying process.
func (t *Target) ThreadList() []proc.Thread { return t.Process.ThreadList() }

// FindThread returns the thread with the given ID.
func (t *Target) FindThread(id int) (proc.Thread, bool) { return t.Process.FindThread(id) }

// CurrentThread returns the default thread to be used for various operations.
// This is usually the last thread that threw an exception, however it could be
// a user set thread as well.
func (t *Target) CurrentThread() proc.Thread { return t.Process.CurrentThread() }

// Breakpoints returns a list of the active breakpoints that have been set in the
// underlying process.
func (t *Target) Breakpoints() *proc.BreakpointMap { return &t.breakpoints }

// RequestManualStop will attempt to stop the underlying process. Once stopped
// you may inspect process state.
func (t *Target) RequestManualStop() error { return t.Process.RequestManualStop() }

// SetBreakpoint sets a breakpoint at the provided address.
func (t *Target) SetBreakpoint(addr uint64, kind proc.BreakpointKind, cond ast.Expr) (*proc.Breakpoint, error) {
	if ok, err := t.Valid(); !ok {
		return nil, err
	}
	return t.Breakpoints().Set(addr, kind, cond, t.Process.WriteBreakpoint)
}

// ClearBreakpoint clears a breakpoint at the provided address.
func (t *Target) ClearBreakpoint(addr uint64) (*proc.Breakpoint, error) {
	if ok, err := t.Valid(); !ok {
		return nil, err
	}
	return t.Breakpoints().Clear(addr, t.Process.ClearBreakpointFn)
}

// StepInstruction will continue execution in the underlying process exactly 1 CPU instruction.
func (t *Target) StepInstruction() error {
	thread := t.CurrentThread()
	if sg := t.SelectedGoroutine(); sg != nil {
		if t.SelectedGoroutine().Thread == nil {
			if _, err := t.SetBreakpoint(sg.PC, proc.NextBreakpoint, proc.SameGoroutineCondition(sg)); err != nil {
				return err
			}
			return t.Continue()
		}
		thread = sg.Thread
	}
	t.Process.Common().ClearAllGCache()
	if ok, err := t.Valid(); !ok {
		return err
	}
	thread.ClearCurrentBreakpointState()
	if err := thread.StepInstruction(); err != nil {
		return err
	}
	if err := thread.SetCurrentBreakpoint(true); err != nil {
		return err
	}
	if g, _ := proc.GetG(thread); g != nil {
		t.SetSelectedGoroutine(g)
	}
	return nil
}

// SwitchThread will set the default thread to the one specified by "tid".
// That thread will then be used by default by any command that inspects process state.
func (t *Target) SwitchThread(tid int) error { return t.Process.SwitchThread(tid) }

// SwitchGoroutine will set the default goroutine to the one specified by "gid".
// This ennsures the selected goroutine remains active when continuing execution.
func (t *Target) SwitchGoroutine(gid int) error { return t.Process.SwitchGoroutine(gid) }

// ClearInternalBreakpoints will clear any non-user defined breakpoint.
func (t *Target) ClearInternalBreakpoints() error {
	return t.Breakpoints().ClearInternalBreakpoints(func(bp *proc.Breakpoint) error {
		if _, err := t.ClearBreakpoint(bp.Addr); err != nil {
			return err
		}
		for _, thread := range t.ThreadList() {
			if b := thread.Breakpoint(); b.Breakpoint == bp {
				thread.ClearCurrentBreakpointState()
			}
		}
		return nil
	})
}

func FindDeferReturnCalls(text []proc.AsmInstruction) []uint64 {
	const deferreturn = "runtime.deferreturn"
	deferreturns := []uint64{}

	// Find all runtime.deferreturn locations in the function
	// See documentation of Breakpoint.DeferCond for why this is necessary
	for _, instr := range text {
		if instr.IsCall() && instr.DestLoc != nil && instr.DestLoc.Fn != nil && instr.DestLoc.Fn.Name == deferreturn {
			deferreturns = append(deferreturns, instr.Loc.PC)
		}
	}
	return deferreturns
}
