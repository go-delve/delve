package proctl

import (
	"fmt"
	"path/filepath"

	"github.com/derekparker/delve/dwarf/frame"
	sys "golang.org/x/sys/unix"
)

// ThreadContext represents a single thread in the traced process
// Id represents the thread id, Process holds a reference to the
// DebuggedProcess struct that contains info on the process as
// a whole, and Status represents the last result of a `wait` call
// on this thread.
type ThreadContext struct {
	Id                int
	Process           *DebuggedProcess
	Status            *sys.WaitStatus
	CurrentBreakpoint *BreakPoint
	singleStepping    bool
	running           bool
	os                *OSSpecificDetails
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
	if _, ok := thread.Process.BreakPoints[pc]; ok {
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

	bp, ok := thread.Process.BreakPoints[pc]
	if ok {
		// Clear the breakpoint so that we can continue execution.
		_, err = thread.Process.Clear(bp.Addr)
		if err != nil {
			return err
		}

		// Restore breakpoint now that we have passed it.
		defer func() {
			var nbp *BreakPoint
			nbp, err = thread.Process.Break(bp.Addr)
			nbp.Temp = bp.Temp
		}()
	}

	err = thread.singleStep()
	if err != nil {
		return fmt.Errorf("step failed: %s", err.Error())
	}
	return nil
}

// Call a function named `name`. This is currently _NOT_ safe.
func (thread *ThreadContext) CallFn(name string, fn func() error) error {
	f := thread.Process.goSymTable.LookupFunc(name)
	if f == nil {
		return fmt.Errorf("could not find function %s", name)
	}

	// Set breakpoint at the end of the function (before it returns).
	bp, err := thread.TempBreak(f.End - 2)
	if err != nil {
		return err
	}
	defer thread.Process.Clear(bp.Addr)

	regs, err := thread.saveRegisters()
	if err != nil {
		return err
	}

	previousFrame := make([]byte, f.FrameSize)
	frameSize := uintptr(regs.SP() + uint64(f.FrameSize))
	if _, err := readMemory(thread, frameSize, previousFrame); err != nil {
		return err
	}
	defer func() { writeMemory(thread, frameSize, previousFrame) }()

	if err = thread.SetPC(f.Entry); err != nil {
		return err
	}
	defer thread.restoreRegisters()
	if err = thread.Continue(); err != nil {
		return err
	}
	th, err := thread.Process.trapWait(-1)
	if err != nil {
		return err
	}
	th.CurrentBreakpoint = nil
	return fn()
}

// Set breakpoint using this thread.
func (thread *ThreadContext) Break(addr uint64) (*BreakPoint, error) {
	return thread.Process.setBreakpoint(thread.Id, addr, false)
}

// Set breakpoint using this thread.
func (thread *ThreadContext) TempBreak(addr uint64) (*BreakPoint, error) {
	return thread.Process.setBreakpoint(thread.Id, addr, true)
}

// Clear breakpoint using this thread.
func (thread *ThreadContext) Clear(addr uint64) (*BreakPoint, error) {
	return thread.Process.clearBreakpoint(thread.Id, addr)
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
	fde, err := thread.Process.frameEntries.FDEForPC(curpc)
	if err != nil {
		return err
	}

	// Get current file/line.
	f, l, _ := thread.Process.goSymTable.PCToLine(curpc)
	if filepath.Ext(f) == ".go" {
		if err = thread.next(curpc, fde, f, l); err != nil {
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
	lines, err := thread.Process.ast.NextLines(file, line)
	if err != nil {
		return err
	}

	ret, err := thread.ReturnAddress()
	if err != nil {
		return err
	}

	pcs := make([]uint64, 0, len(lines))
	for i := range lines {
		pcs = append(pcs, thread.Process.lineInfo.AllPCsForFileLine(file, lines[i])...)
	}

	var covered bool
	for i := range pcs {
		if fde.Cover(pcs[i]) {
			covered = true
			break
		}
	}

	if !covered {
		fn := thread.Process.goSymTable.PCToFunc(ret)
		if fn != nil && fn.Name == "runtime.goexit" {
			g, err := thread.curG()
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
	pcs := thread.Process.lineInfo.AllPCsBetween(fde.Begin(), fde.End())
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
		if _, err := thread.Process.TempBreak(pcs[i]); err != nil {
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

func (thread *ThreadContext) curG() (*G, error) {
	var g *G
	err := thread.CallFn("runtime.getg", func() error {
		regs, err := thread.Registers()
		if err != nil {
			return err
		}
		g, err = parseG(thread, regs.SP()+uint64(thread.Process.arch.PtrSize()))
		return err
	})
	return g, err
}
