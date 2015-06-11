package proctl

import (
	"debug/gosym"
	"fmt"
	"path/filepath"

	"github.com/derekparker/delve/dwarf/frame"
	"github.com/derekparker/delve/source"
	sys "golang.org/x/sys/unix"
)

// ThreadContext represents a single thread in the traced process
// Id represents the thread id, Process holds a reference to the
// DebuggedProcess struct that contains info on the process as
// a whole, and Status represents the last result of a `wait` call
// on this thread.
type ThreadContext struct {
	Id                int
	Status            *sys.WaitStatus
	CurrentBreakpoint *BreakPoint
	dbp               *DebuggedProcess
	singleStepping    bool
	running           bool
	os                *OSSpecificDetails
}

type Location struct {
	PC   uint64
	File string
	Line int
	Fn   *gosym.Func
}

// Continue the execution of this thread. This method takes
// software breakpoints into consideration and ensures that
// we step over any breakpoints. It will restore the instruction,
// step, and then restore the breakpoint and continue.
func (thread *ThreadContext) Continue() error {
	pc, err := thread.PC()
	if err != nil {
		return err
	}

	// Check whether we are stopped at a breakpoint, and
	// if so, single step over it before continuing.
	if _, ok := thread.dbp.BreakPoints[pc]; ok {
		if err := thread.Step(); err != nil {
			return err
		}
	}
	return thread.resume()
}

// Single steps this thread a single instruction, ensuring that
// we correctly handle the likely case that we are at a breakpoint.
func (thread *ThreadContext) Step() (err error) {
	thread.singleStepping = true
	defer func() { thread.singleStepping = false }()
	pc, err := thread.PC()
	if err != nil {
		return err
	}

	bp, ok := thread.dbp.BreakPoints[pc]
	if ok {
		// Clear the breakpoint so that we can continue execution.
		_, err = thread.dbp.Clear(bp.Addr)
		if err != nil {
			return err
		}

		// Restore breakpoint now that we have passed it.
		defer func() {
			var nbp *BreakPoint
			nbp, err = thread.dbp.Break(bp.Addr)
			nbp.Temp = bp.Temp
		}()
	}

	err = thread.singleStep()
	if err != nil {
		return fmt.Errorf("step failed: %s", err.Error())
	}
	return nil
}

// Set breakpoint using this thread.
func (thread *ThreadContext) Break(addr uint64) (*BreakPoint, error) {
	return thread.dbp.setBreakpoint(thread.Id, addr, false)
}

// Set breakpoint using this thread.
func (thread *ThreadContext) TempBreak(addr uint64) (*BreakPoint, error) {
	return thread.dbp.setBreakpoint(thread.Id, addr, true)
}

// Clear breakpoint using this thread.
func (thread *ThreadContext) Clear(addr uint64) (*BreakPoint, error) {
	return thread.dbp.clearBreakpoint(thread.Id, addr)
}

func (thread *ThreadContext) Location() (*Location, error) {
	pc, err := thread.PC()
	if err != nil {
		return nil, err
	}
	f, l, fn := thread.dbp.PCToLine(pc)
	return &Location{PC: pc, File: f, Line: l, Fn: fn}, nil
}

// Step to next source line.
//
// Next will step over functions, and will follow through to the
// return address of a function.
//
// This functionality is implemented by finding all possible next lines
// and setting a breakpoint at them. Once we've set a breakpoint at each
// potential line, we continue the thread.
func (thread *ThreadContext) Next() (err error) {
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
		if err = thread.next(curpc, fde, loc.File, loc.Line); err != nil {
			return err
		}
	} else {
		if err = thread.cnext(curpc, fde); err != nil {
			return err
		}
	}
	return thread.Continue()
}

// Go routine is exiting.
type GoroutineExitingError struct {
	goid int
}

func (ge GoroutineExitingError) Error() string {
	return fmt.Sprintf("goroutine %d is exiting", ge.goid)
}

// This version of next uses the AST from the current source file to figure out all of the potential source lines
// we could end up at.
func (thread *ThreadContext) next(curpc uint64, fde *frame.FrameDescriptionEntry, file string, line int) error {
	lines, err := thread.dbp.ast.NextLines(file, line)
	if err != nil {
		if _, ok := err.(source.NoNodeError); !ok {
			return err
		}
	}

	ret, err := thread.ReturnAddress()
	if err != nil {
		return err
	}

	pcs := make([]uint64, 0, len(lines))
	for i := range lines {
		pcs = append(pcs, thread.dbp.lineInfo.AllPCsForFileLine(file, lines[i])...)
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
			g, err := thread.getG()
			if err != nil {
				return err
			}
			return GoroutineExitingError{goid: g.Id}
		}
	}
	pcs = append(pcs, ret)
	return thread.setNextTempBreakpoints(curpc, pcs)
}

// Set a breakpoint at every reachable location, as well as the return address. Without
// the benefit of an AST we can't be sure we're not at a branching statement and thus
// cannot accurately predict where we may end up.
func (thread *ThreadContext) cnext(curpc uint64, fde *frame.FrameDescriptionEntry) error {
	pcs := thread.dbp.lineInfo.AllPCsBetween(fde.Begin(), fde.End())
	ret, err := thread.ReturnAddress()
	if err != nil {
		return err
	}
	pcs = append(pcs, ret)
	return thread.setNextTempBreakpoints(curpc, pcs)
}

func (thread *ThreadContext) setNextTempBreakpoints(curpc uint64, pcs []uint64) error {
	for i := range pcs {
		if pcs[i] == curpc || pcs[i] == curpc-1 {
			continue
		}
		if _, err := thread.dbp.TempBreak(pcs[i]); err != nil {
			if err, ok := err.(BreakPointExistsError); !ok {
				return err
			}
		}
	}
	return nil
}

// Sets the PC for this thread.
func (thread *ThreadContext) SetPC(pc uint64) error {
	regs, err := thread.Registers()
	if err != nil {
		return err
	}
	return regs.SetPC(thread, pc)
}

// Returns information on the G (goroutine) that is executing on this thread.
//
// The G structure for a thread is stored in thread local memory. Execute instructions
// that move the *G structure into a CPU register, and then grab
// the new registers and parse the G structure.
//
// We cannot simply use the allg linked list in order to find the M that represents
// the given OS thread and follow its G pointer because on Darwin mach ports are not
// universal, so our port for this thread would not map to the `id` attribute of the M
// structure. Also, when linked against libc, Go prefers the libc version of clone as
// opposed to the runtime version. This has the consequence of not setting M.id for
// any thread, regardless of OS.
//
// In order to get around all this craziness, we write the instructions to retrieve the G
// structure running on this thread (which is stored in thread local memory) into the
// current instruction stream. The instructions are obviously arch/os dependant, as they
// vary on how thread local storage is implemented, which MMU register is used and
// what the offset into thread local storage is.
func (thread *ThreadContext) getG() (g *G, err error) {
	var pcInt uint64
	pcInt, err = thread.PC()
	if err != nil {
		return
	}
	pc := uintptr(pcInt)
	// Read original instructions.
	originalInstructions := make([]byte, len(thread.dbp.arch.CurgInstructions()))
	if _, err = readMemory(thread, pc, originalInstructions); err != nil {
		return
	}
	// Write new instructions.
	if _, err = writeMemory(thread, pc, thread.dbp.arch.CurgInstructions()); err != nil {
		return
	}
	// We're going to be intentionally modifying the registers
	// once we execute the code we inject into the instruction stream,
	// so save them off here so we can restore them later.
	if _, err = thread.saveRegisters(); err != nil {
		return
	}
	// Ensure original instructions and PC are both restored.
	defer func() {
		// Do not shadow previous error, if there was one.
		originalErr := err
		// Restore the original instructions and register contents.
		if _, err = writeMemory(thread, pc, originalInstructions); err != nil {
			return
		}
		if err = thread.restoreRegisters(); err != nil {
			return
		}
		err = originalErr
		return
	}()
	// Execute new instructions.
	if err = thread.resume(); err != nil {
		return
	}
	// Set the halt flag so that trapWait will ignore the fact that
	// we hit a breakpoint that isn't captured in our list of
	// known breakpoints.
	thread.dbp.halt = true
	defer func(dbp *DebuggedProcess) { dbp.halt = false }(thread.dbp)
	if _, err = thread.dbp.trapWait(-1); err != nil {
		return
	}
	// Grab *G from RCX.
	regs, err := thread.Registers()
	if err != nil {
		return nil, err
	}
	g, err = parseG(thread, regs.CX(), false)
	return
}
