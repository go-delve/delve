package proctl

import (
	"encoding/binary"
	"fmt"
	sys "golang.org/x/sys/unix"

	"github.com/derekparker/delve/dwarf/frame"
)

// ThreadContext represents a single thread of execution in the
// traced program.
type ThreadContext struct {
	Id      int
	Process *DebuggedProcess
	Status  *sys.WaitStatus
}

type Registers interface {
	PC() uint64
	SP() uint64
	SetPC(int, uint64) error
}

// Obtains register values from the debugged process.
func (thread *ThreadContext) Registers() (Registers, error) {
	regs, err := registers(thread.Id)
	if err != nil {
		return nil, fmt.Errorf("could not get registers %s", err)
	}

	return regs, nil
}

// Returns the current PC for this thread id.
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

// Sets a breakpoint at addr, and stores it in the process wide
// break point table. Setting a break point must be thread specific due to
// ptrace actions needing the thread to be in a signal-delivery-stop.
//
// Depending on hardware support, Delve will choose to either
// set a hardware or software breakpoint. Essentially, if the
// hardware supports it, and there are free debug registers, Delve
// will set a hardware breakpoint. Otherwise we fall back to software
// breakpoints, which are a bit more work for us.
func (thread *ThreadContext) Break(addr uint64) (*BreakPoint, error) {
	return thread.Process.setBreakpoint(thread.Id, addr)
}

// Clears a breakpoint, and removes it from the process level break point table.
func (thread *ThreadContext) Clear(addr uint64) (*BreakPoint, error) {
	return thread.Process.clearBreakpoint(thread.Id, addr)
}

func (thread *ThreadContext) Continue() error {
	// Check whether we are stopped at a breakpoint, and
	// if so, single step over it before continuing.
	regs, err := thread.Registers()
	if err != nil {
		return fmt.Errorf("could not get registers %s", err)
	}

	if _, ok := thread.Process.BreakPoints[regs.PC()-1]; ok {
		err := thread.Step()
		if err != nil {
			return fmt.Errorf("could not step %s", err)
		}
	}

	return sys.PtraceCont(thread.Id, 0)
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
		_, err = thread.Clear(bp.Addr)
		if err != nil {
			return err
		}

		// Reset program counter to our restored instruction.
		err = regs.SetPC(thread.Id, bp.Addr)
		if err != nil {
			return fmt.Errorf("could not set registers %s", err)
		}

		// Restore breakpoint now that we have passed it.
		defer func() {
			_, err = thread.Break(bp.Addr)
		}()
	}

	err = sys.PtraceSingleStep(thread.Id)
	if err != nil {
		return fmt.Errorf("step failed: %s", err.Error())
	}

	_, _, err = wait(thread.Id, 0)
	if err != nil {
		return err
	}

	return nil
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
		// Our offset here is be 0 because we
		// have stepped into the first instruction
		// of this function. Therefore the function
		// has not had a chance to modify its' stack
		// and change our offset.
		addr := thread.ReturnAddressFromOffset(0)
		bp, err := thread.Break(addr)
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
			// We wait on -1 here because once we continue this
			// thread, it's very possible the scheduler could of
			// change the goroutine context on us, we there is
			// no guarantee that waiting on this tid will ever
			// return.
			wpid, _, err := trapWait(thread.Process, -1)
			if err != nil {
				return err
			}
			if wpid != thread.Id {
				thread = thread.Process.Threads[wpid]
			}
			pc, _ = thread.CurrentPC()
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
	readMemory(thread.Id, uintptr(retaddr), data)
	return binary.LittleEndian.Uint64(data)
}

func (thread *ThreadContext) clearTempBreakpoint(pc uint64) error {
	var software bool
	if _, ok := thread.Process.BreakPoints[pc]; ok {
		software = true
	}
	if _, err := thread.Clear(pc); err != nil {
		return err
	}
	if software {
		// Reset program counter to our restored instruction.
		regs, err := thread.Registers()
		if err != nil {
			return err
		}

		return regs.SetPC(thread.Id, pc)
	}

	return nil
}
