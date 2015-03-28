package proctl

import (
	"encoding/binary"
	"fmt"

	sys "golang.org/x/sys/unix"
)

// ThreadContext represents a single thread in the traced process
// Id represents the thread id, Process holds a reference to the
// DebuggedProcess struct that contains info on the process as
// a whole, and Status represents the last result of a `wait` call
// on this thread.
type ThreadContext struct {
	Id      int
	Process *DebuggedProcess
	Status  *sys.WaitStatus
	os      *OSSpecificDetails
}

// An interface for a generic register type. The
// interface encapsulates the generic values / actions
// we need independant of arch. The concrete register types
// will be different depending on OS/Arch.
type Registers interface {
	PC() uint64
	SP() uint64
	SetPC(*ThreadContext, uint64) error
}

// Obtains register values from the debugged process.
func (thread *ThreadContext) Registers() (Registers, error) {
	regs, err := registers(thread)
	if err != nil {
		return nil, fmt.Errorf("could not get registers: %s", err)
	}
	return regs, nil
}

// Returns the current PC for this thread.
func (thread *ThreadContext) CurrentPC() (uint64, error) {
	regs, err := thread.Registers()
	if err != nil {
		return 0, err
	}
	return regs.PC(), nil
}

// Continue the execution of this thread. This method takes
// software breakpoints into consideration and ensures that
// we step over any breakpoints. It will restore the instruction,
// step, and then restore the breakpoint and continue.
func (thread *ThreadContext) Continue() error {
	pc, err := thread.CurrentPC()
	if err != nil {
		return err
	}

	// Check whether we are stopped at a breakpoint, and
	// if so, single step over it before continuing.
	if _, ok := thread.Process.BreakPoints[pc-1]; ok {
		err := thread.Step()
		if err != nil {
			return fmt.Errorf("could not step %s", err)
		}
	}

	return thread.resume()
}

// Single steps this thread a single instruction, ensuring that
// we correctly handle the likely case that we are at a breakpoint.
func (thread *ThreadContext) Step() (err error) {
	pc, err := thread.CurrentPC()
	if err != nil {
		return err
	}

	bp, ok := thread.Process.BreakPoints[pc-1]
	if ok {
		// Clear the breakpoint so that we can continue execution.
		_, err = thread.Process.Clear(bp.Addr)
		if err != nil {
			return err
		}

		// Reset program counter to our restored instruction.
		err = thread.SetPC(bp.Addr)
		if err != nil {
			return fmt.Errorf("could not set registers %s", err)
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

	return err
}

// Call a function named `name`. This is currently _NOT_ safe.
func (thread *ThreadContext) CallFn(name string, fn func(*ThreadContext) error) error {
	f := thread.Process.GoSymTable.LookupFunc(name)
	if f == nil {
		return fmt.Errorf("could not find function %s", name)
	}

	// Set breakpoint at the end of the function (before it returns).
	bp, err := thread.Process.Break(f.End - 2)
	if err != nil {
		return err
	}
	defer thread.Process.Clear(bp.Addr)

	if err := thread.saveRegisters(); err != nil {
		return err
	}
	if err = thread.SetPC(f.Entry); err != nil {
		return err
	}
	defer thread.restoreRegisters()
	if err := thread.Continue(); err != nil {
		return err
	}
	if _, err = trapWait(thread.Process, -1); err != nil {
		return err
	}
	return fn(thread)
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
	curpc, err := thread.CurrentPC()
	if err != nil {
		return err
	}

	// Check and see if we're at a breakpoint, if so
	// correct the PC value for the breakpoint instruction.
	if bp, ok := thread.Process.BreakPoints[curpc-1]; ok {
		curpc = bp.Addr
	}

	// Grab info on our current stack frame. Used to determine
	// whether we may be stepping outside of the current function.
	fde, err := thread.Process.FrameEntries.FDEForPC(curpc)
	if err != nil {
		return err
	}

	// Get current file/line.
	f, l, _ := thread.Process.GoSymTable.PCToLine(curpc)

	// Find any line we could potentially get to.
	lines, err := thread.Process.ast.NextLines(f, l)
	if err != nil {
		return err
	}

	// Set a breakpoint at every line reachable from our location.
	for _, l := range lines {
		pcs := thread.Process.LineInfo.AllPCsForFileLine(f, l)
		for _, pc := range pcs {
			if pc == curpc {
				continue
			}
			if !fde.Cover(pc) {
				pc = thread.ReturnAddressFromOffset(fde.ReturnAddressOffset(pc))
			}
			bp, err := thread.Process.Break(pc)
			if err != nil {
				if err, ok := err.(BreakPointExistsError); !ok {
					return err
				}
				continue
			}
			bp.Temp = true
		}
	}
	return thread.Continue()
}

func (thread *ThreadContext) SetPC(pc uint64) error {
	regs, err := thread.Registers()
	if err != nil {
		return err
	}
	return regs.SetPC(thread, pc)
}

// Takes an offset from RSP and returns the address of the
// instruction the currect function is going to return to.
func (thread *ThreadContext) ReturnAddressFromOffset(offset int64) uint64 {
	regs, err := thread.Registers()
	if err != nil {
		panic("Could not obtain register values")
	}

	retaddr := int64(regs.SP()) + offset
	data := make([]byte, 8)
	readMemory(thread, uintptr(retaddr), data)
	return binary.LittleEndian.Uint64(data)
}

func (thread *ThreadContext) clearTempBreakpoint(pc uint64) error {
	clearbp := func(bp *BreakPoint) error {
		if _, err := thread.Process.Clear(bp.Addr); err != nil {
			return err
		}
		return thread.SetPC(bp.Addr)
	}
	for _, bp := range thread.Process.HWBreakPoints {
		if bp != nil && bp.Temp && bp.Addr == pc {
			return clearbp(bp)
		}
	}
	if bp, ok := thread.Process.BreakPoints[pc]; ok && bp.Temp {
		return clearbp(bp)
	}
	return nil
}

func (thread *ThreadContext) curG() (*G, error) {
	var g *G
	err := thread.CallFn("runtime.getg", func(t *ThreadContext) error {
		regs, err := t.Registers()
		if err != nil {
			return err
		}
		reader := t.Process.Dwarf.Reader()
		g, err = parseG(t.Process, regs.SP()+uint64(ptrsize), reader)
		return err
	})
	return g, err
}
