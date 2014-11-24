package proctl

import (
	"bytes"
	"encoding/binary"
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
		return nil, err
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

// Sets a software breakpoint at addr, and stores it in the process wide
// break point table. Setting a break point must be thread specific due to
// ptrace actions needing the thread to be in a signal-delivery-stop in order
// to initiate any ptrace command. Otherwise, it really doesn't matter
// as we're only dealing with threads.
func (thread *ThreadContext) Break(addr uintptr) (*BreakPoint, error) {
	var (
		int3         = []byte{0xCC}
		f, l, fn     = thread.Process.GoSymTable.PCToLine(uint64(addr))
		originalData = make([]byte, 1)
	)

	if fn == nil {
		return nil, InvalidAddressError{address: addr}
	}

	_, err := syscall.PtracePeekData(thread.Id, addr, originalData)
	if err != nil {
		fmt.Println("PEEK ERR")
		return nil, err
	}

	if bytes.Equal(originalData, int3) {
		return nil, BreakPointExistsError{f, l, addr}
	}

	_, err = syscall.PtracePokeData(thread.Id, addr, int3)
	if err != nil {
		fmt.Println("POKE ERR")
		return nil, err
	}

	breakpoint := &BreakPoint{
		FunctionName: fn.Name,
		File:         f,
		Line:         l,
		Addr:         uint64(addr),
		OriginalData: originalData,
	}

	thread.Process.BreakPoints[uint64(addr)] = breakpoint

	return breakpoint, nil
}

// Clears a software breakpoint, and removes it from the process level
// break point table.
func (thread *ThreadContext) Clear(pc uint64) (*BreakPoint, error) {
	bp, ok := thread.Process.BreakPoints[pc]
	if !ok {
		return nil, fmt.Errorf("No breakpoint currently set for %#v", pc)
	}

	if _, err := syscall.PtracePokeData(thread.Id, uintptr(bp.Addr), bp.OriginalData); err != nil {
		return nil, err
	}

	delete(thread.Process.BreakPoints, pc)

	return bp, nil
}

func (thread *ThreadContext) Continue() error {
	// Check whether we are stopped at a breakpoint, and
	// if so, single step over it before continuing.
	regs, err := thread.Registers()
	if err != nil {
		return err
	}

	if _, ok := thread.Process.BreakPoints[regs.PC()-1]; ok {
		err := thread.Step()
		if err != nil {
			return err
		}
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
			_, err = thread.Break(uintptr(bp.Addr))
		}()
	}

	err = syscall.PtraceSingleStep(thread.Id)
	if err != nil {
		return fmt.Errorf("step failed: %s", err.Error())
	}

	_, _, err = timeoutWait(thread, 0)
	if err != nil {
		return err
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
		pc-- // Decrement PC to account for BreakPoint
	}

	_, l, _ := thread.Process.GoSymTable.PCToLine(pc)
	fde, err := thread.Process.FrameEntries.FDEForPC(pc)
	if err != nil {
		return err
	}

	step := func() (uint64, error) {
		err = thread.Step()
		if err != nil {
			return 0, err
		}

		return thread.CurrentPC()
	}

	ret := thread.ReturnAddressFromOffset(fde.ReturnAddressOffset(pc))
	for {
		pc, err = step()
		if err != nil {
			return err
		}

		if !fde.Cover(pc) && pc != ret {
			err := thread.continueToReturnAddress(pc, fde)
			if err != nil {
				if _, ok := err.(InvalidAddressError); !ok {
					return err
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
		addr := thread.ReturnAddressFromOffset(0)
		bp, err := thread.Break(uintptr(addr))
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
			wpid, _, err := trapWait(thread.Process, -1, 0)
			if err != nil {
				return err
			}
			if wpid != thread.Id {
				thread = thread.Process.Threads[wpid]
			}
			pc, _ = thread.CurrentPC()
			if (pc - 1) == bp.Addr {
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

	retaddr := int64(regs.Rsp) + offset
	data := make([]byte, 8)
	syscall.PtracePeekText(thread.Id, uintptr(retaddr), data)
	return binary.LittleEndian.Uint64(data)
}

func (thread *ThreadContext) clearTempBreakpoint(pc uint64) error {
	if bp, ok := thread.Process.BreakPoints[pc]; ok {
		_, err := thread.Clear(bp.Addr)
		if err != nil {
			return err
		}

		// Reset program counter to our restored instruction.
		regs, err := thread.Registers()
		if err != nil {
			return err
		}

		regs.SetPC(bp.Addr)
		return syscall.PtraceSetRegs(thread.Id, regs)
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

		if tid != pid {
			threads = append(threads, tid)
		}
	}

	return threads
}
