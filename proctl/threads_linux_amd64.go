package proctl

import (
	"fmt"
	"os"
	"strconv"
	"syscall"

	"github.com/derekparker/delve/dwarf/frame"
)

// ThreadContext represents a single thread of execution in the
// traced program.
type ThreadContext struct {
	Id      int
	Process *DebuggedProcess
	Status  *syscall.WaitStatus
	Regs    *syscall.PtraceRegs
}

// Obtains register values from the debugged process.
func (thread *ThreadContext) Registers() (*syscall.PtraceRegs, error) {
	err := syscall.PtraceGetRegs(thread.Id, thread.Regs)
	if err != nil {
		syscall.Tgkill(thread.Process.Pid, thread.Id, syscall.SIGSTOP)
		err = thread.wait()
		if err != nil {
			return nil, err
		}

		err := syscall.PtraceGetRegs(thread.Id, thread.Regs)
		if err != nil {
			return nil, fmt.Errorf("Registers(): %s", err)
		}
	}

	return thread.Regs, nil
}

func (thread *ThreadContext) CurrentPC() (uint64, error) {
	regs, err := thread.Registers()
	if err != nil {
		return 0, err
	}

	return regs.PC(), nil
}

func (thread *ThreadContext) Continue() error {
	// Stepping first will ensure we are able to continue
	// past a breakpoint if that's currently where we are stopped.
	err := thread.Step()
	if err != nil {
		return err
	}

	return syscall.PtraceCont(thread.Id, 0)
}

// Steps through thread of execution.
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
		regs.SetPC(bp.Addr)
		err = syscall.PtraceSetRegs(thread.Id, regs)
		if err != nil {
			return err
		}

		// Restore breakpoint now that we have passed it.
		defer func() {
			_, err = thread.Process.Break(uintptr(bp.Addr))
		}()
	}

	err = syscall.PtraceSingleStep(thread.Id)
	if err != nil {
		return fmt.Errorf("step failed: ", err.Error())
	}

	_, _, err = wait(thread.Process, thread.Id, 0)
	if err != nil {
		return fmt.Errorf("step failed: ", err.Error())
	}

	return nil
}

// Steps through thread of execution.
func (thread *ThreadContext) Next() (err error) {
	pc, err := thread.CurrentPC()
	if err != nil {
		return err
	}

	if _, ok := thread.Process.BreakPoints[pc-1]; ok {
		// Decrement the PC to be before
		// the breakpoint instruction.
		pc--
	}

	_, l, _ := thread.Process.GoSymTable.PCToLine(pc)
	fde, err := thread.Process.FrameEntries.FDEForPC(pc)
	if err != nil {
		return err
	}

	step := func() (uint64, error) {
		err = thread.Step()
		if err != nil {
			return 0, fmt.Errorf("next stepping failed: ", err.Error())
		}

		return thread.CurrentPC()
	}

	ret := thread.Process.ReturnAddressFromOffset(fde.ReturnAddressOffset(pc))
	for {
		pc, err = step()
		if err != nil {
			return err
		}

		if !fde.Cover(pc) && pc != ret {
			thread.continueToReturnAddress(pc, fde)
			if err != nil {
				if ierr, ok := err.(InvalidAddressError); ok {
					return ierr
				}
			}

			pc, _ = thread.CurrentPC()
		}

		_, nl, _ := thread.Process.GoSymTable.PCToLine(pc)
		if nl != l {
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
		addr := thread.Process.ReturnAddressFromOffset(0)
		bp, err := thread.Process.Break(uintptr(addr))
		if err != nil {
			if _, ok := err.(BreakPointExistsError); !ok {
				return err
			}
		}

		err = thread.Continue()
		if err != nil {
			return err
		}

		err = thread.wait()
		if err != nil {
			return err
		}

		err = thread.clearTempBreakpoint(bp.Addr)
		if err != nil {
			return err
		}

		pc, _ = thread.CurrentPC()
	}

	return nil
}

func (thread *ThreadContext) clearTempBreakpoint(pc uint64) error {
	if bp, ok := thread.Process.BreakPoints[pc]; ok {
		regs, err := thread.Registers()
		if err != nil {
			return err
		}

		// Reset program counter to our restored instruction.
		bp, err = thread.Process.Clear(bp.Addr)
		if err != nil {
			return err
		}

		regs.SetPC(bp.Addr)
		return syscall.PtraceSetRegs(thread.Id, regs)
	}

	return nil
}

func (thread *ThreadContext) wait() error {
	var status syscall.WaitStatus
	_, err := syscall.Wait4(thread.Id, &status, 0, nil)
	if err != nil {
		if status.Exited() {
			delete(thread.Process.Threads, thread.Id)
			return ProcessExitedError{thread.Id}
		}
		return err
	}

	return nil
}

func threadIds(pid int) []int {
	var threads []int
	dir, err := os.Open(fmt.Sprintf("/proc/%d/task", pid))
	if err != nil {
		panic(err)
	}
	defer dir.Close()

	names, err := dir.Readdirnames(0)
	if err != nil {
		panic(err)
	}

	for _, strid := range names {
		tid, err := strconv.Atoi(strid)
		if err != nil {
			panic(err)
		}

		threads = append(threads, tid)
	}

	return threads
}
