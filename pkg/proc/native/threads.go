package native

import (
	"fmt"

	"github.com/go-delve/delve/pkg/proc"
)

// Thread represents a single thread in the traced process
// ID represents the thread id or port, Process holds a reference to the
// Process struct that contains info on the process as
// a whole, and Status represents the last result of a `wait` call
// on this thread.
type Thread struct {
	ID                int                  // Thread ID or mach port
	Status            *WaitStatus          // Status returned from last wait call
	CurrentBreakpoint proc.BreakpointState // Breakpoint thread is currently stopped at

	dbp            *Process
	singleStepping bool
	os             *OSSpecificDetails
	common         proc.CommonThread
}

// Continue the execution of this thread.
//
// If we are currently at a breakpoint, we'll clear it
// first and then resume execution. Thread will continue until
// it hits a breakpoint or is signaled.
func (t *Thread) Continue() error {
	pc, err := t.PC()
	if err != nil {
		return err
	}
	// Check whether we are stopped at a breakpoint, and
	// if so, single step over it before continuing.
	if _, ok := t.dbp.FindBreakpoint(pc, false); ok {
		if err := t.StepInstruction(); err != nil {
			return err
		}
	}
	return t.resume()
}

// StepInstruction steps a single instruction.
//
// Executes exactly one instruction and then returns.
// If the thread is at a breakpoint, we first clear it,
// execute the instruction, and then replace the breakpoint.
// Otherwise we simply execute the next instruction.
func (t *Thread) StepInstruction() (err error) {
	t.singleStepping = true
	defer func() {
		t.singleStepping = false
	}()
	pc, err := t.PC()
	if err != nil {
		return err
	}

	bp, ok := t.dbp.FindBreakpoint(pc, false)
	if ok {
		// Clear the breakpoint so that we can continue execution.
		err = t.ClearBreakpoint(bp)
		if err != nil {
			return err
		}

		// Restore breakpoint now that we have passed it.
		defer func() {
			err = t.dbp.writeSoftwareBreakpoint(t, bp.Addr)
		}()
	}

	err = t.singleStep()
	if err != nil {
		if _, exited := err.(proc.ErrProcessExited); exited {
			return err
		}
		return fmt.Errorf("step failed: %s", err.Error())
	}
	return nil
}

// Location returns the threads location, including the file:line
// of the corresponding source code, the function we're in
// and the current instruction address.
func (t *Thread) Location() (*proc.Location, error) {
	pc, err := t.PC()
	if err != nil {
		return nil, err
	}
	f, l, fn := t.dbp.bi.PCToLine(pc)
	return &proc.Location{PC: pc, File: f, Line: l, Fn: fn}, nil
}

// Arch returns the architecture the binary is
// compiled for and executing on.
func (t *Thread) Arch() proc.Arch {
	return t.dbp.bi.Arch
}

// BinInfo returns information on the binary.
func (t *Thread) BinInfo() *proc.BinaryInfo {
	return t.dbp.bi
}

// Common returns information common across Process
// implementations.
func (t *Thread) Common() *proc.CommonThread {
	return &t.common
}

// SetCurrentBreakpoint sets the current breakpoint that this
// thread is stopped at as CurrentBreakpoint on the thread struct.
func (t *Thread) SetCurrentBreakpoint(adjustPC bool) error {
	t.CurrentBreakpoint.Clear()
	pc, err := t.PC()
	if err != nil {
		return err
	}

	// If the breakpoint instruction does not change the value
	// of PC after being executed we should look for breakpoints
	// with bp.Addr == PC and there is no need to call SetPC
	// after finding one.
	adjustPC = adjustPC && t.Arch().BreakInstrMovesPC()

	if bp, ok := t.dbp.FindBreakpoint(pc, adjustPC); ok {
		if adjustPC {
			if err = t.SetPC(bp.Addr); err != nil {
				return err
			}
		}
		t.CurrentBreakpoint = bp.CheckCondition(t)
		if t.CurrentBreakpoint.Breakpoint != nil && t.CurrentBreakpoint.Active {
			if g, err := proc.GetG(t); err == nil {
				t.CurrentBreakpoint.HitCount[g.ID]++
			}
			t.CurrentBreakpoint.TotalHitCount++
		}
	}
	return nil
}

// Breakpoint returns the current breakpoint that is active
// on this thread.
func (t *Thread) Breakpoint() proc.BreakpointState {
	return t.CurrentBreakpoint
}

// ThreadID returns the ID of this thread.
func (t *Thread) ThreadID() int {
	return t.ID
}

// ClearBreakpoint clears the specified breakpoint.
func (t *Thread) ClearBreakpoint(bp *proc.Breakpoint) error {
	if _, err := t.WriteMemory(uintptr(bp.Addr), bp.OriginalData); err != nil {
		return fmt.Errorf("could not clear breakpoint %s", err)
	}
	return nil
}

// Registers obtains register values from the debugged process.
func (t *Thread) Registers(floatingPoint bool) (proc.Registers, error) {
	return registers(t, floatingPoint)
}

// RestoreRegisters will set the value of the CPU registers to those
// passed in via 'savedRegs'.
func (t *Thread) RestoreRegisters(savedRegs proc.Registers) error {
	return t.restoreRegisters(savedRegs)
}

// PC returns the current program counter value for this thread.
func (t *Thread) PC() (uint64, error) {
	regs, err := t.Registers(false)
	if err != nil {
		return 0, err
	}
	return regs.PC(), nil
}
