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
		if err := clearHardwareBreakpoint(bp.reg, thread.Id); err != nil {
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

// Returns whether or not a breakpoint has been set for the given address.
func (dbp *DebuggedProcess) BreakpointExists(addr uint64) bool {
	for _, bp := range dbp.arch.HardwareBreakpoints() {
		// TODO(darwin)
		if runtime.GOOS == "darwin" {
			break
		}
		if bp != nil && bp.Addr == addr {
			return true
		}
	}
	_, ok := dbp.Breakpoints[addr]
	return ok
}

func (dbp *DebuggedProcess) newBreakpoint(fn, f string, l int, addr uint64, data []byte, temp bool) *Breakpoint {
	var id int
	if temp {
		dbp.tempBreakpointIDCounter++
		id = dbp.tempBreakpointIDCounter
	} else {
		dbp.breakpointIDCounter++
		id = dbp.breakpointIDCounter
	}
	return &Breakpoint{
		FunctionName: fn,
		File:         f,
		Line:         l,
		Addr:         addr,
		OriginalData: data,
		ID:           id,
		Temp:         temp,
	}
}

func (dbp *DebuggedProcess) newHardwareBreakpoint(fn, f string, l int, addr uint64, data []byte, temp bool, reg int) *Breakpoint {
	bp := dbp.newBreakpoint(fn, f, l, addr, data, temp)
	bp.hardware = true
	bp.reg = reg
	return bp
}

func (dbp *DebuggedProcess) setBreakpoint(tid int, addr uint64, temp bool) (*Breakpoint, error) {
	var f, l, fn = dbp.goSymTable.PCToLine(uint64(addr))
	if fn == nil {
		return nil, InvalidAddressError{address: addr}
	}
	if dbp.BreakpointExists(addr) {
		return nil, BreakpointExistsError{f, l, addr}
	}
	// Try and set a hardware breakpoint.
	for i, v := range dbp.arch.HardwareBreakpoints() {
		// TODO(darwin)
		if runtime.GOOS == "darwin" {
			break
		}
		if v == nil {
			for t, _ := range dbp.Threads {
				if err := setHardwareBreakpoint(i, t, addr); err != nil {
					return nil, fmt.Errorf("could not set hardware breakpoint on thread %d: %s", t, err)
				}
			}
			dbp.arch.HardwareBreakpoints()[i] = dbp.newHardwareBreakpoint(fn.Name, f, l, addr, nil, temp, i)
			return dbp.arch.HardwareBreakpoints()[i], nil
		}
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
	dbp.Breakpoints[addr] = dbp.newBreakpoint(fn.Name, f, l, addr, originalData, temp)
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
	// Check for hardware breakpoint
	for i, bp := range dbp.arch.HardwareBreakpoints() {
		if bp == nil {
			continue
		}
		if bp.Addr == addr {
			_, err := bp.Clear(thread)
			if err != nil {
				return nil, err
			}
			dbp.arch.HardwareBreakpoints()[i] = nil
			return bp, nil
		}
	}
	// Check for software breakpoint
	if bp, ok := dbp.Breakpoints[addr]; ok {
		if _, err := bp.Clear(thread); err != nil {
			return nil, err
		}
		delete(dbp.Breakpoints, addr)
		return bp, nil
	}
	return nil, NoBreakpointError{addr: addr}
}
