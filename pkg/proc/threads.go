package proc

import (
	"debug/gosym"
	"encoding/binary"
	"errors"
	"fmt"
	"go/ast"
	"path/filepath"
	"reflect"
	"strings"

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

// IThread represents a thread.
type IThread interface {
	memoryReadWriter
	Location() (*Location, error)
	// Breakpoint will return the breakpoint that this thread is stopped at or
	// nil if the thread is not stopped at any breakpoint.
	// Active will be true if the thread is stopped at a breakpoint and the
	// breakpoint's condition is met.
	// If there was an error evaluating the breakpoint's condition it will be
	// returned as condErr
	Breakpoint() (breakpoint *Breakpoint, active bool, condErr error)
	ThreadID() int
	Registers(floatingPoint bool) (Registers, error)
	Arch() Arch
	BinInfo() *BinaryInfo
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
		_, err = thread.ClearBreakpoint(bp)
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
	f, l, fn := thread.dbp.bi.PCToLine(pc)
	return &Location{PC: pc, File: f, Line: l, Fn: fn}, nil
}

func (thread *Thread) Arch() Arch {
	return thread.dbp.bi.arch
}

func (thread *Thread) BinInfo() *BinaryInfo {
	return &thread.dbp.bi
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
		frames, err = ThreadStacktrace(thread, 0)
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

// Set breakpoints at every line, and the return address. Also look for
// a deferred function and set a breakpoint there too.
// If stepInto is true it will also set breakpoints inside all
// functions called on the current source line, for non-absolute CALLs
// a breakpoint of kind StepBreakpoint is set on the CALL instruction,
// Continue will take care of setting a breakpoint to the destination
// once the CALL is reached.
func (dbp *Process) next(stepInto bool) error {
	topframe, err := topframe(dbp.selectedGoroutine, dbp.currentThread)
	if err != nil {
		return err
	}

	success := false
	defer func() {
		if !success {
			dbp.ClearInternalBreakpoints()
		}
	}()

	csource := filepath.Ext(topframe.Current.File) != ".go"
	var thread memoryReadWriter = dbp.currentThread
	var regs Registers
	if dbp.selectedGoroutine != nil && dbp.selectedGoroutine.thread != nil {
		thread = dbp.selectedGoroutine.thread
		regs, err = dbp.selectedGoroutine.thread.Registers(false)
		if err != nil {
			return err
		}
	}

	text, err := disassemble(thread, regs, dbp.breakpoints, dbp.BinInfo(), topframe.FDE.Begin(), topframe.FDE.End())
	if err != nil && stepInto {
		return err
	}

	cond := sameGoroutineCondition(dbp.selectedGoroutine)

	if stepInto {
		for _, instr := range text {
			if instr.Loc.File != topframe.Current.File || instr.Loc.Line != topframe.Current.Line || !instr.IsCall() {
				continue
			}

			if instr.DestLoc != nil && instr.DestLoc.Fn != nil {
				if err := dbp.setStepIntoBreakpoint([]AsmInstruction{instr}, cond); err != nil {
					return err
				}
			} else {
				// Non-absolute call instruction, set a StepBreakpoint here
				if _, err := dbp.SetBreakpoint(instr.Loc.PC, StepBreakpoint, cond); err != nil {
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
		if dbp.selectedGoroutine != nil {
			deferPCEntry := dbp.selectedGoroutine.DeferPC()
			if deferPCEntry != 0 {
				_, _, deferfn := dbp.bi.goSymTable.PCToLine(deferPCEntry)
				var err error
				deferpc, err = dbp.FirstPCAfterPrologue(deferfn, false)
				if err != nil {
					return err
				}
			}
		}
		if deferpc != 0 && deferpc != topframe.Current.PC {
			bp, err := dbp.SetBreakpoint(deferpc, NextDeferBreakpoint, cond)
			if err != nil {
				if _, ok := err.(BreakpointExistsError); !ok {
					return err
				}
			}
			if bp != nil {
				bp.DeferReturns = deferreturns
			}
		}
	}

	// Add breakpoints on all the lines in the current function
	pcs, err := dbp.bi.lineInfo.AllPCsBetween(topframe.FDE.Begin(), topframe.FDE.End()-1, topframe.Current.File)
	if err != nil {
		return err
	}

	if !csource {
		var covered bool
		for i := range pcs {
			if topframe.FDE.Cover(pcs[i]) {
				covered = true
				break
			}
		}

		if !covered {
			fn := dbp.bi.goSymTable.PCToFunc(topframe.Ret)
			if dbp.selectedGoroutine != nil && fn != nil && fn.Name == "runtime.goexit" {
				return nil
			}
		}
	}

	// Add a breakpoint on the return address for the current frame
	pcs = append(pcs, topframe.Ret)
	success = true
	return dbp.setInternalBreakpoints(topframe.Current.PC, pcs, NextBreakpoint, cond)
}

func (dbp *Process) setStepIntoBreakpoint(text []AsmInstruction, cond ast.Expr) error {
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
	pc, _ := dbp.FirstPCAfterPrologue(fn, false)
	if _, err := dbp.SetBreakpoint(pc, NextBreakpoint, cond); err != nil {
		if _, ok := err.(BreakpointExistsError); !ok {
			return err
		}
	}

	return nil
}

// setInternalBreakpoints sets a breakpoint to all addresses specified in pcs
// skipping over curpc and curpc-1
func (dbp *Process) setInternalBreakpoints(curpc uint64, pcs []uint64, kind BreakpointKind, cond ast.Expr) error {
	for i := range pcs {
		if pcs[i] == curpc || pcs[i] == curpc-1 {
			continue
		}
		if _, err := dbp.SetBreakpoint(pcs[i], kind, cond); err != nil {
			if _, ok := err.(BreakpointExistsError); !ok {
				dbp.ClearInternalBreakpoints()
				return err
			}
		}
	}
	return nil
}

// SetPC sets the PC for this thread.
func (thread *Thread) SetPC(pc uint64) error {
	regs, err := thread.Registers(false)
	if err != nil {
		return err
	}
	return regs.SetPC(thread, pc)
}

func getGVariable(thread IThread) (*Variable, error) {
	arch := thread.Arch()
	regs, err := thread.Registers(false)
	if err != nil {
		return nil, err
	}

	if arch.GStructOffset() == 0 {
		// GetG was called through SwitchThread / updateThreadList during initialization
		// thread.dbp.arch isn't setup yet (it needs a current thread to read global variables from)
		return nil, fmt.Errorf("g struct offset not initialized")
	}

	gaddrbs, err := thread.readMemory(uintptr(regs.TLS()+arch.GStructOffset()), arch.PtrSize())
	if err != nil {
		return nil, err
	}
	gaddr := uintptr(binary.LittleEndian.Uint64(gaddrbs))

	return newGVariable(thread, gaddr, arch.DerefTLS())
}

func newGVariable(thread IThread, gaddr uintptr, deref bool) (*Variable, error) {
	typ, err := thread.BinInfo().findType("runtime.g")
	if err != nil {
		return nil, err
	}

	name := ""

	if deref {
		typ = &dwarf.PtrType{dwarf.CommonType{int64(thread.Arch().PtrSize()), "", reflect.Ptr, 0}, typ}
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
func GetG(thread IThread) (g *G, err error) {
	gaddr, err := getGVariable(thread)
	if err != nil {
		return nil, err
	}

	g, err = gaddr.parseG()
	if err == nil {
		g.thread = thread
		if loc, err := thread.Location(); err == nil {
			g.CurrentLoc = *loc
		}
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

// ThreadScope returns an EvalScope for this thread.
func ThreadScope(thread IThread) (*EvalScope, error) {
	locations, err := ThreadStacktrace(thread, 0)
	if err != nil {
		return nil, err
	}
	if len(locations) < 1 {
		return nil, errors.New("could not decode first frame")
	}
	return &EvalScope{locations[0].Current.PC, locations[0].CFA, thread, nil, thread.BinInfo()}, nil
}

// GoroutineScope returns an EvalScope for the goroutine running on this thread.
func GoroutineScope(thread IThread) (*EvalScope, error) {
	locations, err := ThreadStacktrace(thread, 0)
	if err != nil {
		return nil, err
	}
	if len(locations) < 1 {
		return nil, errors.New("could not decode first frame")
	}
	gvar, err := getGVariable(thread)
	if err != nil {
		return nil, err
	}
	return &EvalScope{locations[0].Current.PC, locations[0].CFA, thread, gvar, thread.BinInfo()}, nil
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
			if g, err := GetG(thread); err == nil {
				thread.CurrentBreakpoint.HitCount[g.ID]++
			}
			thread.CurrentBreakpoint.TotalHitCount++
		}
	}
	return nil
}

func (thread *Thread) clearBreakpointState() {
	thread.CurrentBreakpoint = nil
	thread.BreakpointConditionMet = false
	thread.BreakpointConditionError = nil
}

func (thread *Thread) onTriggeredBreakpoint() bool {
	return (thread.CurrentBreakpoint != nil) && thread.BreakpointConditionMet
}

func (thread *Thread) onTriggeredInternalBreakpoint() bool {
	return thread.onTriggeredBreakpoint() && thread.CurrentBreakpoint.Internal()
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
	for i := range thread.dbp.breakpoints {
		if thread.dbp.breakpoints[i].Internal() {
			bp = thread.dbp.breakpoints[i]
			break
		}
	}
	if bp == nil {
		return false, nil
	}
	if bp.Kind == NextDeferBreakpoint {
		// we just want to check the condition on the goroutine id here
		bp.Kind = NextBreakpoint
		defer func() {
			bp.Kind = NextDeferBreakpoint
		}()
	}
	return bp.checkCondition(thread)
}

func (th *Thread) Breakpoint() (*Breakpoint, bool, error) {
	return th.CurrentBreakpoint, (th.CurrentBreakpoint != nil && th.BreakpointConditionMet), th.BreakpointConditionError
}

func (th *Thread) ThreadID() int {
	return th.ID
}
