package amd64util

import (
	"errors"
	"fmt"
)

// DebugRegisters represents x86 debug registers described in the Intel 64
// and IA-32 Architectures Software Developer's Manual, Vol. 3B, section
// 17.2
type DebugRegisters struct {
	pAddrs     [4]*uint64
	pDR6, pDR7 *uint64
	Dirty      bool
}

func NewDebugRegisters(pDR0, pDR1, pDR2, pDR3, pDR6, pDR7 *uint64) *DebugRegisters {
	return &DebugRegisters{
		pAddrs: [4]*uint64{pDR0, pDR1, pDR2, pDR3},
		pDR6:   pDR6,
		pDR7:   pDR7,
		Dirty:  false,
	}
}

func lenrwBitsOffset(idx uint8) uint8 {
	return 16 + idx*4
}

func enableBitOffset(idx uint8) uint8 {
	return idx * 2
}

func (drs *DebugRegisters) breakpoint(idx uint8) (addr uint64, read, write bool, sz int) {
	enable := *(drs.pDR7) & (1 << enableBitOffset(idx))
	if enable == 0 {
		return 0, false, false, 0
	}

	addr = *(drs.pAddrs[idx])
	lenrw := (*(drs.pDR7) >> lenrwBitsOffset(idx)) & 0xf
	write = (lenrw & 0x1) != 0
	read = (lenrw & 0x2) != 0
	switch lenrw >> 2 {
	case 0x0:
		sz = 1
	case 0x1:
		sz = 2
	case 0x2:
		sz = 8 // sic
	case 0x3:
		sz = 4
	}
	return addr, read, write, sz
}

// SetBreakpoint sets hardware breakpoint at index 'idx' to the specified
// address, read/write flags and size.
// If the breakpoint is already in use but the parameters match it does
// nothing.
func (drs *DebugRegisters) SetBreakpoint(idx uint8, addr uint64, read, write bool, sz int) error {
	if int(idx) >= len(drs.pAddrs) {
		return fmt.Errorf("hardware breakpoints exhausted")
	}
	curaddr, curread, curwrite, cursz := drs.breakpoint(idx)
	if curaddr != 0 {
		if (curaddr != addr) || (curread != read) || (curwrite != write) || (cursz != sz) {
			return fmt.Errorf("hardware breakpoint %d already in use (address %#x)", idx, curaddr)
		}
		// hardware breakpoint already set
		return nil
	}

	if read && !write {
		return errors.New("break on read only not supported")
	}

	*(drs.pAddrs[idx]) = addr
	var lenrw uint64
	if write {
		lenrw |= 0x1
	}
	if read {
		lenrw |= 0x2
	}
	switch sz {
	case 1:
		// already ok
	case 2:
		lenrw |= 0x1 << 2
	case 4:
		lenrw |= 0x3 << 2
	case 8:
		lenrw |= 0x2 << 2
	default:
		return fmt.Errorf("data breakpoint of size %d not supported", sz)
	}
	*(drs.pDR7) &^= (0xf << lenrwBitsOffset(idx)) // clear old settings
	*(drs.pDR7) |= lenrw << lenrwBitsOffset(idx)
	*(drs.pDR7) |= 1 << enableBitOffset(idx) // enable
	drs.Dirty = true
	return nil
}

// ClearBreakpoint disables the hardware breakpoint at index 'idx'. If the
// breakpoint was already disabled it does nothing.
func (drs *DebugRegisters) ClearBreakpoint(idx uint8) {
	if *(drs.pDR7)&(1<<enableBitOffset(idx)) == 0 {
		return
	}
	*(drs.pDR7) &^= (1 << enableBitOffset(idx))
	drs.Dirty = true
}

// GetActiveBreakpoint returns the active hardware breakpoint and resets the
// condition flags.
func (drs *DebugRegisters) GetActiveBreakpoint() (ok bool, idx uint8) {
	for idx := uint8(0); idx < 3; idx++ {
		enable := *(drs.pDR7) & (1 << enableBitOffset(idx))
		if enable == 0 {
			continue
		}
		if *(drs.pDR6)&(1<<idx) != 0 {
			*drs.pDR6 &^= 0xf // it is our responsibility to clear the condition bits
			drs.Dirty = true
			return true, idx
		}
	}
	return false, 0
}
