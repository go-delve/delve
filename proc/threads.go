package proc

import (
	"debug/gosym"
	"encoding/binary"
	"errors"
	"fmt"
	"go/ast"
	"path/filepath"
	"reflect"
	"runtime"

	"golang.org/x/debug/dwarf"
)

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

	dbp            *Process
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

// Continue the execution of this thread.
//
// If we are currently at a breakpoint, we'll clear it
// first and then resume execution. Thread will continue until
// it hits a breakpoint or is signaled.
func (thread *Thread) Continue() error {
	pc, err := thread.PC()
	if err != nil {
		return err
	}
	// Check whether we are stopped at a breakpoint, and
	// if so, single step over it before continuing.
	if _, ok := thread.dbp.FindBreakpoint(pc); ok {
		if err := thread.StepInstruction(); err != nil {
			return err
		}
	}
	return thread.resume()
}

// StepInstruction steps a single instruction.
//
// Executes exactly one instruction and then returns.
// If the thread is at a breakpoint, we first clear it,
// execute the instruction, and then replace the breakpoint.
// Otherwise we simply execute the next instruction.
func (thread *Thread) StepInstruction() (err error) {
	thread.running = true
	thread.singleStepping = true
	defer func() {
		thread.singleStepping = false
		thread.running = false
	}()
	pc, err := thread.PC()
	if err != nil {
		return err
	}

	bp, ok := thread.dbp.FindBreakpoint(pc)
	if ok {
		// Clear the breakpoint so that we can continue execution.
		_, err = bp.Clear(thread)
		if err != nil {
			return err
		}

		// Restore breakpoint now that we have passed it.
		defer func() {
			err = thread.dbp.writeSoftwareBreakpoint(thread, bp.Addr)
		}()
	}

	err = thread.singleStep()
	if err != nil {
		if _, exited := err.(ProcessExitedError); exited {
			return err
		}
		return fmt.Errorf("step failed: %s", err.Error())
	}
	return nil
}

// Location returns the threads location, including the file:line
// of the corresponding source code, the function we're in
// and the current instruction address.
func (thread *Thread) Location() (*Location, error) {
	pc, err := thread.PC()
	if err != nil {
		return nil, err
	}
	f, l, fn := thread.dbp.PCToLine(pc)
	return &Location{PC: pc, File: f, Line: l, Fn: fn}, nil
}

// ThreadBlockedError is returned when the thread
// is blocked in the scheduler.
type ThreadBlockedError struct{}

func (tbe ThreadBlockedError) Error() string {
	return ""
}

// returns topmost frame of g or thread if g is nil
func topframe(g *G, thread *Thread) (Stackframe, error) {
	var frames []Stackframe
	var err error

	if g == nil {
		if thread.blocked() {
			return Stackframe{}, ThreadBlockedError{}
		}
		frames, err = thread.Stacktrace(0)
	} else {
		frames, err = g.Stacktrace(0)
	}
	if err != nil {
		return Stackframe{}, err
	}
	if len(frames) < 1 {
		return Stackframe{}, errors.New("empty stack trace")
	}
	return frames[0], nil
}

// Set breakpoints for potential next lines.
func (dbp *Process) setNextBreakpoints() (err error) {
	topframe, err := topframe(dbp.SelectedGoroutine, dbp.CurrentThread)
	if err != nil {
		return err
	}

	if filepath.Ext(topframe.Current.File) != ".go" {
		return dbp.cnext(topframe)
	}

	return dbp.next(dbp.SelectedGoroutine, topframe)
}

// Set breakpoints at every line, and the return address. Also look for
// a deferred function and set a breakpoint there too.
func (dbp *Process) next(g *G, topframe Stackframe) error {
	cond := sameGoroutineCondition(dbp.SelectedGoroutine)

	// Disassembles function to find all runtime.deferreturn locations
	// See documentation of Breakpoint.DeferCond for why this is necessary
	deferreturns := []uint64{}
	text, err := dbp.CurrentThread.Disassemble(topframe.FDE.Begin(), topframe.FDE.End(), false)
	if err == nil {
		for _, instr := range text {
			if instr.IsCall() && instr.DestLoc != nil && instr.DestLoc.Fn != nil && instr.DestLoc.Fn.Name == "runtime.deferreturn" {
				deferreturns = append(deferreturns, instr.Loc.PC)
			}
		}
	}

	// Set breakpoint on the most recently deferred function (if any)
	var deferpc uint64 = 0
	if g != nil && g.DeferPC != 0 {
		_, _, deferfn := dbp.goSymTable.PCToLine(g.DeferPC)
		var err error
		deferpc, err = dbp.FirstPCAfterPrologue(deferfn, false)
		if err != nil {
			return err
		}
	}
	if deferpc != 0 {
		bp, err := dbp.SetTempBreakpoint(deferpc, cond)
		if err != nil {
			if _, ok := err.(BreakpointExistsError); !ok {
				dbp.ClearTempBreakpoints()
				return err
			}
		}
		bp.DeferCond = true
		bp.DeferReturns = deferreturns
	}

	// Add breakpoints on all the lines in the current function
	pcs := dbp.lineInfo.AllPCsBetween(topframe.FDE.Begin(), topframe.FDE.End()-1, topframe.Current.File)

	var covered bool
	for i := range pcs {
		if topframe.FDE.Cover(pcs[i]) {
			covered = true
			break
		}
	}

	if !covered {
		fn := dbp.goSymTable.PCToFunc(topframe.Ret)
		if g != nil && fn != nil && fn.Name == "runtime.goexit" {
			return nil
		}
	}

	// Add a breakpoint on the return address for the current frame
	pcs = append(pcs, topframe.Ret)
	return dbp.setTempBreakpoints(topframe.Current.PC, pcs, cond)
}

// Set a breakpoint at every reachable location, as well as the return address. Without
// the benefit of an AST we can't be sure we're not at a branching statement and thus
// cannot accurately predict where we may end up.
func (dbp *Process) cnext(topframe Stackframe) error {
	pcs := dbp.lineInfo.AllPCsBetween(topframe.FDE.Begin(), topframe.FDE.End(), topframe.Current.File)
	pcs = append(pcs, topframe.Ret)
	return dbp.setTempBreakpoints(topframe.Current.PC, pcs, sameGoroutineCondition(dbp.SelectedGoroutine))
}

// setTempBreakpoints sets a breakpoint to all addresses specified in pcs
// skipping over curpc and curpc-1
func (dbp *Process) setTempBreakpoints(curpc uint64, pcs []uint64, cond ast.Expr) error {
	for i := range pcs {
		if pcs[i] == curpc || pcs[i] == curpc-1 {
			continue
		}
		if _, err := dbp.SetTempBreakpoint(pcs[i], cond); err != nil {
			if _, ok := err.(BreakpointExistsError); !ok {
				dbp.ClearTempBreakpoints()
				return err
			}
		}
	}
	return nil
}

// SetPC sets the PC for this thread.
func (thread *Thread) SetPC(pc uint64) error {
	regs, err := thread.Registers()
	if err != nil {
		return err
	}
	return regs.SetPC(thread, pc)
}

func (thread *Thread) getGVariable() (*Variable, error) {
	regs, err := thread.Registers()
	if err != nil {
		return nil, err
	}

	if thread.dbp.arch.GStructOffset() == 0 {
		// GetG was called through SwitchThread / updateThreadList during initialization
		// thread.dbp.arch isn't setup yet (it needs a CurrentThread to read global variables from)
		return nil, fmt.Errorf("g struct offset not initialized")
	}

	gaddrbs, err := thread.readMemory(uintptr(regs.TLS()+thread.dbp.arch.GStructOffset()), thread.dbp.arch.PtrSize())
	if err != nil {
		return nil, err
	}
	gaddr := uintptr(binary.LittleEndian.Uint64(gaddrbs))

	// On Windows, the value at TLS()+GStructOffset() is a
	// pointer to the G struct.
	needsDeref := runtime.GOOS == "windows"

	return thread.newGVariable(gaddr, needsDeref)
}

func (thread *Thread) newGVariable(gaddr uintptr, deref bool) (*Variable, error) {
	typ, err := thread.dbp.findType("runtime.g")
	if err != nil {
		return nil, err
	}

	name := ""

	if deref {
		typ = &dwarf.PtrType{dwarf.CommonType{int64(thread.dbp.arch.PtrSize()), "", reflect.Ptr, 0}, typ}
	} else {
		name = "runtime.curg"
	}

	return thread.newVariable(name, gaddr, typ), nil
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
func (thread *Thread) GetG() (g *G, err error) {
	gaddr, err := thread.getGVariable()
	if err != nil {
		return nil, err
	}

	g, err = gaddr.parseG()
	if err == nil {
		g.thread = thread
	}
	return
}

// Stopped returns whether the thread is stopped at
// the operating system level. Actual implementation
// is OS dependant, look in OS thread file.
func (thread *Thread) Stopped() bool {
	return thread.stopped()
}

// Halt stops this thread from executing. Actual
// implementation is OS dependant. Look in OS
// thread file.
func (thread *Thread) Halt() (err error) {
	defer func() {
		if err == nil {
			thread.running = false
		}
	}()
	if thread.Stopped() {
		return
	}
	err = thread.halt()
	return
}

// Scope returns the current EvalScope for this thread.
func (thread *Thread) Scope() (*EvalScope, error) {
	locations, err := thread.Stacktrace(0)
	if err != nil {
		return nil, err
	}
	if len(locations) < 1 {
		return nil, errors.New("could not decode first frame")
	}
	return locations[0].Scope(thread), nil
}

// SetCurrentBreakpoint sets the current breakpoint that this
// thread is stopped at as CurrentBreakpoint on the thread struct.
func (thread *Thread) SetCurrentBreakpoint() error {
	thread.CurrentBreakpoint = nil
	pc, err := thread.PC()
	if err != nil {
		return err
	}
	if bp, ok := thread.dbp.FindBreakpoint(pc); ok {
		thread.CurrentBreakpoint = bp
		if err = thread.SetPC(bp.Addr); err != nil {
			return err
		}
		thread.BreakpointConditionMet, thread.BreakpointConditionError = bp.checkCondition(thread)
		if thread.onTriggeredBreakpoint() {
			if g, err := thread.GetG(); err == nil {
				thread.CurrentBreakpoint.HitCount[g.ID]++
			}
			thread.CurrentBreakpoint.TotalHitCount++
		}
	}
	return nil
}

func (thread *Thread) onTriggeredBreakpoint() bool {
	return (thread.CurrentBreakpoint != nil) && thread.BreakpointConditionMet
}

func (thread *Thread) onTriggeredTempBreakpoint() bool {
	return thread.onTriggeredBreakpoint() && thread.CurrentBreakpoint.Temp
}

func (thread *Thread) onRuntimeBreakpoint() bool {
	loc, err := thread.Location()
	if err != nil {
		return false
	}
	return loc.Fn != nil && loc.Fn.Name == "runtime.breakpoint"
}

// onNextGorutine returns true if this thread is on the goroutine requested by the current 'next' command
func (thread *Thread) onNextGoroutine() (bool, error) {
	var bp *Breakpoint
	for i := range thread.dbp.Breakpoints {
		if thread.dbp.Breakpoints[i].Temp {
			bp = thread.dbp.Breakpoints[i]
		}
	}
	if bp == nil {
		return false, nil
	}
	// we just want to check the condition on the goroutine id here
	dc := bp.DeferCond
	bp.DeferCond = false
	defer func() {
		bp.DeferCond = dc
	}()
	return bp.checkCondition(thread)
}
