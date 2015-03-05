package proctl

import (
	"encoding/binary"
	"fmt"

	sys "golang.org/x/sys/unix"

	"github.com/derekparker/delve/dwarf/frame"
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

// PrintInfo prints out the thread status
// including: PC, tid, file, line, and function.
func (thread *ThreadContext) PrintInfo() error {
	pc, err := thread.CurrentPC()
	if err != nil {
		return err
	}
	f, l, fn := thread.Process.GoSymTable.PCToLine(pc)
	if fn != nil {
		fmt.Printf("Thread %d at %#v %s:%d %s\n", thread.Id, pc, f, l, fn.Name)
	} else {
		fmt.Printf("Thread %d at %#v\n", thread.Id, pc)
	}
	return nil
}

// Continue the execution of this thread. This method takes
// software breakpoints into consideration and ensures that
// we step over any breakpoints. It will restore the instruction,
// step, and then restore the breakpoint and continue.
func (thread *ThreadContext) Continue() error {
	regs, err := thread.Registers()
	if err != nil {
		return err
	}

	// Check whether we are stopped at a breakpoint, and
	// if so, single step over it before continuing.
	if _, ok := thread.Process.BreakPoints[regs.PC()-1]; ok {
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
	regs, err := thread.Registers()
	if err != nil {
		return err
	}

	bp, ok := thread.Process.BreakPoints[regs.PC()-1]
	if ok {
		// Clear the breakpoint so that we can continue execution.
		_, err = thread.Process.Clear(bp.Addr)
		if err != nil {
			return err
		}

		// Reset program counter to our restored instruction.
		err = regs.SetPC(thread, bp.Addr)
		if err != nil {
			return fmt.Errorf("could not set registers %s", err)
		}

		// Restore breakpoint now that we have passed it.
		defer func() {
			_, err = thread.Process.Break(bp.Addr)
		}()
	}

	err = thread.singleStep()
	if err != nil {
		return fmt.Errorf("step failed: %s", err.Error())
	}

	return err
}

// Step to next source line. Next will step over functions,
// and will follow through to the return address of a function.
// Next is implemented on the thread context, however during the
// course of this function running, it's very likely that the
// goroutine our M is executing will switch to another M, therefore
// this function cannot assume all execution will happen on this thread
// in the traced process.
func (thread *ThreadContext) Next() (err error) {
	pc, err := thread.CurrentPC()
	if err != nil {
		return err
	}

	if bp, ok := thread.Process.BreakPoints[pc-1]; ok {
		pc = bp.Addr
	}

	fde, err := thread.Process.FrameEntries.FDEForPC(pc)
	if err != nil {
		return err
	}

	_, l, _ := thread.Process.GoSymTable.PCToLine(pc)
	ret := thread.ReturnAddressFromOffset(fde.ReturnAddressOffset(pc))
	for {
		if err = thread.Step(); err != nil {
			return err
		}

		if pc, err = thread.CurrentPC(); err != nil {
			return err
		}

		if !fde.Cover(pc) && pc != ret {
			if err := thread.continueToReturnAddress(pc, fde); err != nil {
				if _, ok := err.(InvalidAddressError); !ok {
					return err
				}
			}
			if pc, err = thread.CurrentPC(); err != nil {
				return err
			}
		}

		if _, nl, _ := thread.Process.GoSymTable.PCToLine(pc); nl != l {
			break
		}
	}

	return nil
}

func (thread *ThreadContext) continueToReturnAddress(pc uint64, fde *frame.FrameDescriptionEntry) error {
	for !fde.Cover(pc) {
		// Offset is 0 because we have just stepped into this function.
		addr := thread.ReturnAddressFromOffset(0)
		bp, err := thread.Process.Break(addr)
		if err != nil {
			if _, ok := err.(BreakPointExistsError); !ok {
				return err
			}
		}
		bp.temp = true
		// Ensure we cleanup after ourselves no matter what.
		defer thread.clearTempBreakpoint(bp.Addr)

		for {
			err = thread.Continue()
			if err != nil {
				return err
			}
			// Wait on -1, just in case scheduler switches threads for this G.
			wpid, err := trapWait(thread.Process, -1)
			if err != nil {
				return err
			}
			if wpid != thread.Id {
				thread = thread.Process.Threads[wpid]
			}
			pc, err = thread.CurrentPC()
			if err != nil {
				return err
			}
			if (pc-1) == bp.Addr || pc == bp.Addr {
				break
			}
		}
	}

	return nil
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
	var software bool
	if _, ok := thread.Process.BreakPoints[pc]; ok {
		software = true
	}
	if _, err := thread.Process.Clear(pc); err != nil {
		return err
	}
	if software {
		// Reset program counter to our restored instruction.
		regs, err := thread.Registers()
		if err != nil {
			return err
		}

		return regs.SetPC(thread, pc)
	}

	return nil
}
