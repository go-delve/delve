package proc

import (
	"debug/gosym"
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"

	"golang.org/x/debug/dwarf"

	"github.com/derekparker/delve/pkg/dwarf/frame"
)

var ThreadExitedErr = errors.New("thread has exited")

// Thread represents a single thread in the traced process
// ID represents the thread id or port, Process holds a reference to the
// Process struct that contains info on the process as
// a whole, and Status represents the last result of a `wait` call
// on this thread.
type Thread struct {
	ID                       int         // Thread ID or mach port
	Status                   *WaitStatus // Status returned from last wait call
	CurrentBreakpoint        *Breakpoint // Breakpoint thread is currently stopped at
	BreakpointConditionMet   bool        // Output of evaluating the breakpoint's condition
	BreakpointConditionError error       // Error evaluating the breakpoint's condition

	p              *Process
	singleStepping bool
	running        bool
	os             *OSSpecificDetails
}

// Location represents the location of a thread.
// Holds information on the current instruction
// address, the source file:line, and the function.
type Location struct {
	PC   uint64
	File string
	Line int
	Fn   *gosym.Func
}

func (l *Location) String() string {
	var fname string
	if l.Fn != nil {
		fname = l.Fn.Name
	}
	return fmt.Sprintf("%#v - %s:%d %s", l.PC, l.File, l.Line, fname)
}

// Continue the execution of this thread.
//
// If we are currently at a breakpoint, we'll clear it
// first and then resume execution. Thread will continue until
// it hits a breakpoint or is signaled.
func (t *Thread) Continue() error {
	return threadResume(t, ModeResume, 0)
}

func (t *Thread) ContinueWithSignal(sig int) error {
	return threadResume(t, ModeResume, sig)
}

// StepInstruction steps a single instruction.
//
// Executes exactly one instruction and then returns.
// If the thread is at a breakpoint, we first clear it,
// execute the instruction, and then replace the breakpoint.
// Otherwise we simply execute the next instruction.
func (t *Thread) StepInstruction() error {
	return threadResume(t, ModeStepInstruction, 0)
}

func threadResume(thread *Thread, mode ResumeMode, sig int) (err error) {
	// Check and see if this thread is stopped at a breakpoint. If so,
	// clear it and set a deferred function to reinsert it once we are
	// past it.
	if bp := thread.CurrentBreakpoint; bp != nil {
		// Clear the breakpoint so that we can continue execution.
		_, err = bp.Clear(thread)
		if err != nil {
			return err
		}
		// Restore breakpoint now that we have passed it.
		defer func() {
			fn := thread.p.Dwarf.LookupFunc(bp.FunctionName)
			loc := &Location{Fn: fn, File: bp.File, Line: bp.Line, PC: bp.Addr}
			_, nerr := createAndWriteBreakpoint(thread, loc, bp.Temp, thread.p.arch.BreakpointInstruction())
			if nerr != nil {
				log.WithError(nerr).Error("could not restore breakpoint on thread")
			}
		}()
	}

	// Clear state.
	thread.clearBreakpointState()
	thread.running = true

	switch mode {
	case ModeStepInstruction:
		thread.singleStepping = true
		defer func() {
			thread.singleStepping = false
			thread.running = false
		}()
		return thread.singleStep()
	case ModeResume:
		return thread.resumeWithSig(sig)
	default:
		// Programmer error, safe to panic here.
		panic("unknown mode passed to threadResume")
	}
}

// Location returns the threads location, including the file:line
// of the corresponding source code, the function we're in
// and the current instruction address.
func (t *Thread) Location() (*Location, error) {
	pc, err := t.PC()
	if err != nil {
		return nil, err
	}
	f, l, fn := t.p.Dwarf.PCToLine(pc)
	return &Location{PC: pc, File: f, Line: l, Fn: fn}, nil
}

// ThreadBlockedError is returned when the thread
// is blocked in the scheduler.
type ThreadBlockedError struct{}

func (tbe ThreadBlockedError) Error() string {
	return ""
}

// Set breakpoints for potential next lines.
func (t *Thread) setNextBreakpoints() (err error) {
	if t.blocked() {
		return ThreadBlockedError{}
	}

	// Get current file/line.
	loc, err := t.Location()
	if err != nil {
		return err
	}
	// Grab info on our current stack frame. Used to determine
	// whether we may be stepping outside of the current function.
	fde, err := t.p.Dwarf.Frame.FDEForPC(loc.PC)
	if err != nil {
		return err
	}
	if filepath.Ext(loc.File) == ".go" {
		err = t.next(loc, fde)
	} else {
		err = t.cnext(loc.PC, fde, loc.File)
	}
	return err
}

// GoroutineExitingError is returned when the
// goroutine specified by `goid` is in the process
// of exiting.
type GoroutineExitingError struct {
	goid int
}

func (ge GoroutineExitingError) Error() string {
	return fmt.Sprintf("goroutine %d is exiting", ge.goid)
}

// Set breakpoints at every line, and the return address. Also look for
// a deferred function and set a breakpoint there too.
func (t *Thread) next(curloc *Location, fde *frame.FrameDescriptionEntry) error {
	pcs := t.p.Dwarf.Line.AllPCsBetween(fde.Begin(), fde.End()-1, curloc.File)

	g, err := t.GetG()
	if err != nil {
		return err
	}
	if g.DeferPC != 0 {
		f, lineno, _ := t.p.Dwarf.PCToLine(g.DeferPC)
		for {
			lineno++
			dpc, _, err := t.p.Dwarf.LineToPC(f, lineno)
			if err == nil {
				// We want to avoid setting an actual breakpoint on the
				// entry point of the deferred function so instead create
				// a fake breakpoint which will be cleaned up later.
				t.p.Breakpoints[g.DeferPC] = new(Breakpoint)
				defer func() { delete(t.p.Breakpoints, g.DeferPC) }()
				if _, err = SetTempBreakpoint(t.p, dpc); err != nil {
					return err
				}
				break
			}
		}
	}

	ret, err := t.ReturnAddress()
	if err != nil {
		return err
	}

	var covered bool
	for i := range pcs {
		if fde.Cover(pcs[i]) {
			covered = true
			break
		}
	}

	if !covered {
		fn := t.p.Dwarf.PCToFunc(ret)
		if fn != nil && fn.Name == "runtime.goexit" {
			g, err := t.GetG()
			if err != nil {
				return err
			}
			return GoroutineExitingError{goid: g.ID}
		}
	}
	pcs = append(pcs, ret)
	return t.setNextTempBreakpoints(curloc.PC, pcs)
}

// Set a breakpoint at every reachable location, as well as the return address. Without
// the benefit of an AST we can't be sure we're not at a branching statement and thus
// cannot accurately predict where we may end up.
func (t *Thread) cnext(curpc uint64, fde *frame.FrameDescriptionEntry, file string) error {
	pcs := t.p.Dwarf.Line.AllPCsBetween(fde.Begin(), fde.End(), file)
	ret, err := t.ReturnAddress()
	if err != nil {
		return err
	}
	pcs = append(pcs, ret)
	return t.setNextTempBreakpoints(curpc, pcs)
}

func (t *Thread) setNextTempBreakpoints(curpc uint64, pcs []uint64) error {
	for i := range pcs {
		if pcs[i] == curpc || pcs[i] == curpc-1 {
			continue
		}
		if _, err := SetTempBreakpoint(t.p, pcs[i]); err != nil {
			if _, ok := err.(BreakpointExistsError); !ok {
				return err
			}
		}
	}
	return nil
}

// SetPC sets the PC for this thread.
func (t *Thread) SetPC(pc uint64) error {
	regs, err := t.Registers()
	if err != nil {
		return err
	}
	return regs.SetPC(t, pc)
}

func (t *Thread) getGVariable() (*Variable, error) {
	regs, err := t.Registers()
	if err != nil {
		return nil, err
	}

	if t.p.arch.GStructOffset() == 0 {
		// GetG was called through SwitchThread / updateThreadList during initialization
		// thread.p.arch isn't setup yet (it needs a CurrentThread to read global variables from)
		return nil, fmt.Errorf("g struct offset not initialized")
	}

	gaddrbs, err := t.readMemory(uintptr(regs.TLS()+t.p.arch.GStructOffset()), t.p.arch.PtrSize())
	if err != nil {
		return nil, err
	}
	gaddr := uintptr(binary.LittleEndian.Uint64(gaddrbs))

	// On Windows, the value at TLS()+GStructOffset() is a
	// pointer to the G struct.
	needsDeref := runtime.GOOS == "windows"

	return t.newGVariable(gaddr, needsDeref)
}

func (t *Thread) newGVariable(gaddr uintptr, deref bool) (*Variable, error) {
	typ, err := t.p.findType("runtime.g")
	if err != nil {
		return nil, err
	}

	name := ""

	if deref {
		typ = &dwarf.PtrType{dwarf.CommonType{int64(t.p.arch.PtrSize()), "", reflect.Ptr, 0}, typ}
	} else {
		name = "runtime.curg"
	}

	return t.newVariable(name, gaddr, typ), nil
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
func (t *Thread) GetG() (g *G, err error) {
	gaddr, err := t.getGVariable()
	if err != nil {
		return nil, err
	}

	g, err = gaddr.parseG()
	if err == nil {
		g.thread = t
	}
	return
}

// Stopped returns whether the thread is stopped at
// the operating system level. Actual implementation
// is OS dependant, look in OS thread file.
func (t *Thread) Stopped() bool {
	return t.stopped()
}

// Halt stops this thread from executing. Actual
// implementation is OS dependant. Look in OS
// thread file.
func (t *Thread) Halt() (err error) {
	defer func() {
		if err == nil {
			t.running = false
		}
	}()
	if t.Stopped() {
		return
	}
	err = t.halt()
	return
}

// Scope returns the current EvalScope for this thread.
func (t *Thread) Scope() (*EvalScope, error) {
	locations, err := t.Stacktrace(0)
	if err != nil {
		return nil, err
	}
	if len(locations) < 1 {
		return nil, errors.New("could not decode first frame")
	}
	return locations[0].Scope(t), nil
}

// SetCurrentBreakpoint sets the current breakpoint that this
// thread is stopped at as CurrentBreakpoint on the thread struct.
func (t *Thread) SetCurrentBreakpoint() error {
	pc, err := t.PC()
	if err != nil {
		return err
	}
	if bp, ok := t.p.Breakpoints[pc-uint64(t.p.arch.BreakpointSize())]; ok {
		t.CurrentBreakpoint = bp
		if err = t.SetPC(bp.Addr); err != nil {
			return err
		}
		t.BreakpointConditionMet, t.BreakpointConditionError = bp.checkCondition(t)
		if t.onTriggeredBreakpoint() {
			if g, err := t.GetG(); err == nil {
				t.CurrentBreakpoint.HitCount[g.ID]++
			}
			t.CurrentBreakpoint.TotalHitCount++
		}
	}
	return nil
}

func (t *Thread) onTriggeredBreakpoint() bool {
	return (t.CurrentBreakpoint != nil) && t.BreakpointConditionMet
}

func (t *Thread) onTriggeredTempBreakpoint() bool {
	return t.onTriggeredBreakpoint() && t.CurrentBreakpoint.Temp
}

func (t *Thread) onRuntimeBreakpoint() bool {
	loc, err := t.Location()
	if err != nil {
		return false
	}
	return loc.Fn != nil && loc.Fn.Name == "runtime.breakpoint"
}

// onNextGoroutine returns true if this thread is on the goroutine requested by the current 'next' command
func (t *Thread) onNextGoroutine() (bool, error) {
	for _, bp := range t.p.Breakpoints {
		if bp.Temp {
			return bp.checkCondition(t)
		}
	}
	return false, nil
}

func (t *Thread) clearBreakpointState() {
	t.CurrentBreakpoint = nil
	t.BreakpointConditionMet = false
	t.BreakpointConditionError = nil
}
