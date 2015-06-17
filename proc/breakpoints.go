package proc

import (
	"fmt"
	"runtime"
)

// Represents a single breakpoint. Stores information on the break
// point including the byte of data that originally was stored at that
// address.
type Breakpoint struct {
	// File & line information for printing.
	FunctionName string
	File         string
	Line         int

	Addr         uint64 // Address breakpoint is set for.
	OriginalData []byte // If software breakpoint, the data we replace with breakpoint instruction.
	ID           int    // Monotonically increasing ID.
	Temp         bool   // Whether this is a temp breakpoint (for next'ing).
	hardware     bool   // Breakpoint using CPU debug registers.
	reg          int    // If hardware breakpoint, what debug register it belongs to.
}

func (bp *Breakpoint) String() string {
	return fmt.Sprintf("Breakpoint %d at %#v %s:%d", bp.ID, bp.Addr, bp.File, bp.Line)
}

// Clear this breakpoint appropriately depending on whether it is a
// hardware or software breakpoint.
func (bp *Breakpoint) Clear(thread *Thread) (*Breakpoint, error) {
	if bp.hardware {
		if err := thread.dbp.clearHardwareBreakpoint(bp.reg, thread.Id); err != nil {
			return nil, err
		}
		return bp, nil
	}
	if _, err := writeMemory(thread, uintptr(bp.Addr), bp.OriginalData); err != nil {
		return nil, fmt.Errorf("could not clear breakpoint %s", err)
	}
	return bp, nil
}

// Returned when trying to set a breakpoint at
// an address that already has a breakpoint set for it.
type BreakpointExistsError struct {
	file string
	line int
	addr uint64
}

func (bpe BreakpointExistsError) Error() string {
	return fmt.Sprintf("Breakpoint exists at %s:%d at %x", bpe.file, bpe.line, bpe.addr)
}

// InvalidAddressError represents the result of
// attempting to set a breakpoint at an invalid address.
type InvalidAddressError struct {
	address uint64
}

func (iae InvalidAddressError) Error() string {
	return fmt.Sprintf("Invalid address %#v\n", iae.address)
}

func (dbp *DebuggedProcess) setBreakpoint(tid int, addr uint64, temp bool) (*Breakpoint, error) {
	if bp, ok := dbp.FindBreakpoint(addr); ok {
		return nil, BreakpointExistsError{bp.File, bp.Line, bp.Addr}
	}

	f, l, fn := dbp.goSymTable.PCToLine(uint64(addr))
	if fn == nil {
		return nil, InvalidAddressError{address: addr}
	}

	var id int
	if temp {
		dbp.tempBreakpointIDCounter++
		id = dbp.tempBreakpointIDCounter
	} else {
		dbp.breakpointIDCounter++
		id = dbp.breakpointIDCounter
	}

	// Try and set a hardware breakpoint.
	for i, used := range dbp.arch.HardwareBreakpointUsage() {
		if runtime.GOOS == "darwin" { // TODO(dp): Implement hardware breakpoints on OSX.
			break
		}
		if used {
			continue
		}
		for t, _ := range dbp.Threads {
			if err := dbp.setHardwareBreakpoint(i, t, addr); err != nil {
				return nil, fmt.Errorf("could not set hardware breakpoint on thread %d: %s", t, err)
			}
		}
		dbp.arch.SetHardwareBreakpointUsage(i, true)
		dbp.Breakpoints[addr] = &Breakpoint{
			FunctionName: fn.Name,
			File:         f,
			Line:         l,
			Addr:         addr,
			ID:           id,
			Temp:         temp,
			hardware:     true,
			reg:          i,
		}

		return dbp.Breakpoints[addr], nil
	}

	// Fall back to software breakpoint. 0xCC is INT 3 trap interrupt.
	thread := dbp.Threads[tid]
	originalData := make([]byte, dbp.arch.BreakpointSize())
	if _, err := readMemory(thread, uintptr(addr), originalData); err != nil {
		return nil, err
	}
	if _, err := writeMemory(thread, uintptr(addr), dbp.arch.BreakpointInstruction()); err != nil {
		return nil, err
	}
	dbp.Breakpoints[addr] = &Breakpoint{
		FunctionName: fn.Name,
		File:         f,
		Line:         l,
		Addr:         addr,
		OriginalData: originalData,
		ID:           id,
		Temp:         temp,
	}

	return dbp.Breakpoints[addr], nil
}

// Error thrown when trying to clear a breakpoint that does not exist.
type NoBreakpointError struct {
	addr uint64
}

func (nbp NoBreakpointError) Error() string {
	return fmt.Sprintf("no breakpoint at %#v", nbp.addr)
}

func (dbp *DebuggedProcess) clearBreakpoint(tid int, addr uint64) (*Breakpoint, error) {
	thread := dbp.Threads[tid]
	if bp, ok := dbp.Breakpoints[addr]; ok {
		if _, err := bp.Clear(thread); err != nil {
			return nil, err
		}
		if bp.hardware {
			dbp.arch.SetHardwareBreakpointUsage(bp.reg, false)
		}
		delete(dbp.Breakpoints, addr)
		return bp, nil
	}
	return nil, NoBreakpointError{addr: addr}
}
