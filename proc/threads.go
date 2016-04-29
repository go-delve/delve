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

	"github.com/derekparker/delve/dwarf/frame"
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

// Set breakpoints for potential next lines.
func (thread *Thread) setNextBreakpoints() (err error) {
	if thread.blocked() {
		return ThreadBlockedError{}
	}
	curpc, err := thread.PC()
	if err != nil {
		return err
	}

	// Grab info on our current stack frame. Used to determine
	// whether we may be stepping outside of the current function.
	fde, err := thread.dbp.frameEntries.FDEForPC(curpc)
	if err != nil {
		return err
	}

	// Get current file/line.
	loc, err := thread.Location()
	if err != nil {
		return err
	}
	if filepath.Ext(loc.File) == ".go" {
		err = thread.next(curpc, fde, loc.File, loc.Line)
	} else {
		err = thread.cnext(curpc, fde, loc.File)
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
func (thread *Thread) next(curpc uint64, fde *frame.FrameDescriptionEntry, file string, line int) error {
	pcs := thread.dbp.lineInfo.AllPCsBetween(fde.Begin(), fde.End()-1, file)

	g, err := thread.GetG()
	if err != nil {
		return err
	}
	if g.DeferPC != 0 {
		f, lineno, _ := thread.dbp.goSymTable.PCToLine(g.DeferPC)
		for {
			lineno++
			dpc, _, err := thread.dbp.goSymTable.LineToPC(f, lineno)
			if err == nil {
				// We want to avoid setting an actual breakpoint on the
				// entry point of the deferred function so instead create
				// a fake breakpoint which will be cleaned up later.
				thread.dbp.Breakpoints[g.DeferPC] = new(Breakpoint)
				defer func() { delete(thread.dbp.Breakpoints, g.DeferPC) }()
				if _, err = thread.dbp.SetTempBreakpoint(dpc); err != nil {
					return err
				}
				break
			}
		}
	}

	ret, err := thread.ReturnAddress()
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
		fn := thread.dbp.goSymTable.PCToFunc(ret)
		if fn != nil && fn.Name == "runtime.goexit" {
			g, err := thread.GetG()
			if err != nil {
				return err
			}
			return GoroutineExitingError{goid: g.ID}
		}
	}
	pcs = append(pcs, ret)
	return thread.setNextTempBreakpoints(curpc, pcs)
}

// Set a breakpoint at every reachable location, as well as the return address. Without
// the benefit of an AST we can't be sure we're not at a branching statement and thus
// cannot accurately predict where we may end up.
func (thread *Thread) cnext(curpc uint64, fde *frame.FrameDescriptionEntry, file string) error {
	pcs := thread.dbp.lineInfo.AllPCsBetween(fde.Begin(), fde.End(), file)
	ret, err := thread.ReturnAddress()
	if err != nil {
		return err
	}
	pcs = append(pcs, ret)
	return thread.setNextTempBreakpoints(curpc, pcs)
}

func (thread *Thread) setNextTempBreakpoints(curpc uint64, pcs []uint64) error {
	for i := range pcs {
		if pcs[i] == curpc || pcs[i] == curpc-1 {
			continue
		}
		if _, err := thread.dbp.SetTempBreakpoint(pcs[i]); err != nil {
			if _, ok := err.(BreakpointExistsError); !ok {
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
	return bp.checkCondition(thread)
}
