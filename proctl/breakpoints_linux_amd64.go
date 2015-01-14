package proctl

/*
#include <stddef.h>
#include <sys/user.h>
#include <sys/debugreg.h>

// Exposes C macro `offsetof` which is needed for getting
// the offset of the debug register we want, and passing
// that offset to PTRACE_POKE_USER.
int offset(int reg) {
	return offsetof(struct user, u_debugreg[reg]);
}
*/
import "C"

import (
	"fmt"
	"syscall"
)

// Represents a single breakpoint. Stores information on the break
// point including the byte of data that originally was stored at that
// address.
type BreakPoint struct {
	FunctionName string
	File         string
	Line         int
	Addr         uint64
	OriginalData []byte
	ID           int
	temp         bool
}

type BreakPointExistsError struct {
	file string
	line int
	addr uint64
}

func (bpe BreakPointExistsError) Error() string {
	return fmt.Sprintf("Breakpoint exists at %s:%d at %x", bpe.file, bpe.line, bpe.addr)
}

func PtracePokeUser(tid int, off, addr uintptr) error {
	_, _, err := syscall.Syscall6(syscall.SYS_PTRACE, syscall.PTRACE_POKEUSR, uintptr(tid), uintptr(off), uintptr(addr), 0, 0)
	if err != syscall.Errno(0) {
		return err
	}
	return nil
}

func (dbp *DebuggedProcess) BreakpointExists(addr uint64) bool {
	for _, bp := range dbp.HWBreakPoints {
		if bp != nil && bp.Addr == addr {
			return true
		}
	}
	if _, ok := dbp.BreakPoints[addr]; ok {
		return true
	}
	return false
}

func (dbp *DebuggedProcess) setBreakpoint(tid int, addr uint64) (*BreakPoint, error) {
	var f, l, fn = dbp.GoSymTable.PCToLine(uint64(addr))
	if fn == nil {
		return nil, InvalidAddressError{address: addr}
	}
	if dbp.BreakpointExists(addr) {
		return nil, BreakPointExistsError{f, l, addr}
	}
	// Try and set a hardware breakpoint.
	for i, v := range dbp.HWBreakPoints {
		if v == nil {
			if err := setHardwareBreakpoint(i, tid, addr); err != nil {
				return nil, fmt.Errorf("could not set hardware breakpoint")
			}
			dbp.HWBreakPoints[i] = dbp.newBreakpoint(fn.Name, f, l, addr, nil)
			return dbp.HWBreakPoints[i], nil
		}
	}
	// Fall back to software breakpoint. 0xCC is INT 3, software
	// breakpoint trap interrupt.
	originalData := make([]byte, 1)
	if _, err := readMemory(tid, uintptr(addr), originalData); err != nil {
		return nil, err
	}
	_, err := writeMemory(tid, uintptr(addr), []byte{0xCC})
	if err != nil {
		return nil, err
	}
	dbp.BreakPoints[addr] = dbp.newBreakpoint(fn.Name, f, l, addr, originalData)
	return dbp.BreakPoints[addr], nil
}

func (dbp *DebuggedProcess) clearBreakpoint(tid int, addr uint64) (*BreakPoint, error) {
	// Check for hardware breakpoint
	for i, bp := range dbp.HWBreakPoints {
		if bp.Addr == addr {
			dbp.HWBreakPoints[i] = nil
			if err := clearHardwareBreakpoint(i, tid); err != nil {
				return nil, err
			}
			return bp, nil
		}
	}
	// Check for software breakpoint
	if bp, ok := dbp.BreakPoints[addr]; ok {
		if _, err := writeMemory(tid, uintptr(bp.Addr), bp.OriginalData); err != nil {
			return nil, fmt.Errorf("could not clear breakpoint %s", err)
		}
		delete(dbp.BreakPoints, addr)
		return bp, nil
	}
	return nil, fmt.Errorf("No breakpoint currently set for %#v", addr)
}

func (dbp *DebuggedProcess) newBreakpoint(fn, f string, l int, addr uint64, data []byte) *BreakPoint {
	dbp.breakpointIDCounter++
	return &BreakPoint{
		FunctionName: fn,
		File:         f,
		Line:         l,
		Addr:         addr,
		OriginalData: data,
		ID:           dbp.breakpointIDCounter,
	}
}

// Sets a hardware breakpoint by setting the contents of the
// debug register `reg` with the address of the instruction
// that we want to break at. There are only 4 debug registers
// DR0-DR3. Debug register 7 is the control register.
func setHardwareBreakpoint(reg, tid int, addr uint64) error {
	if reg < 0 || reg > 7 {
		return fmt.Errorf("invalid register value")
	}

	var (
		off     = uintptr(C.offset(C.int(reg)))
		dr7     = uintptr(0x1 | C.DR_RW_EXECUTE | C.DR_LEN_8)
		dr7addr = uintptr(C.offset(C.DR_CONTROL))
	)

	// Set the debug register `reg` with the address of the
	// instruction we want to trigger a debug exception.
	if err := PtracePokeUser(tid, off, uintptr(addr)); err != nil {
		return err
	}
	// Set the debug control register. This
	// instructs the cpu to raise a debug
	// exception when hitting the address of
	// an instruction stored in dr0-dr3.
	return PtracePokeUser(tid, dr7addr, dr7)
}
