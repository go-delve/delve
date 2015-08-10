package proc

import (
	"encoding/binary"
	"fmt"
)

// Takes an offset from RSP and returns the address of the
// instruction the current function is going to return to.
func (thread *Thread) ReturnAddress() (uint64, error) {
	locations, err := thread.Stacktrace(2)
	if err != nil {
		return 0, err
	}
	if len(locations) < 2 {
		return 0, fmt.Errorf("could not find return address for %s", locations[0].Fn.BaseName())
	}
	return locations[1].PC, nil
}

// Returns the stack trace for thread.
// Note the locations in the array are return addresses not call addresses.
func (thread *Thread) Stacktrace(depth int) ([]Location, error) {
	regs, err := thread.Registers()
	if err != nil {
		return nil, err
	}
	return thread.dbp.stacktrace(regs.PC(), regs.SP(), depth)
}

// Returns the stack trace for a goroutine.
// Note the locations in the array are return addresses not call addresses.
func (dbp *Process) GoroutineStacktrace(g *G, depth int) ([]Location, error) {
	if g.thread != nil {
		return g.thread.Stacktrace(depth)
	}
	locs, err := dbp.stacktrace(g.PC, g.SP, depth)
	return locs, err
}

func (dbp *Process) GoroutineLocation(g *G) *Location {
	f, l, fn := dbp.PCToLine(g.PC)
	return &Location{PC: g.PC, File: f, Line: l, Fn: fn}
}

type NullAddrError struct{}

func (n NullAddrError) Error() string {
	return "NULL address"
}

func (dbp *Process) stacktrace(pc, sp uint64, depth int) ([]Location, error) {
	var (
		ret       = pc
		btoffset  int64
		locations []Location
		retaddr   uintptr
	)
	f, l, fn := dbp.PCToLine(pc)
	locations = append(locations, Location{PC: pc, File: f, Line: l, Fn: fn})
	for i := 0; i < depth; i++ {
		fde, err := dbp.frameEntries.FDEForPC(ret)
		if err != nil {
			return nil, err
		}
		btoffset += fde.ReturnAddressOffset(ret)
		retaddr = uintptr(int64(sp) + btoffset + int64(i*dbp.arch.PtrSize()))
		if retaddr == 0 {
			return nil, NullAddrError{}
		}
		data, err := dbp.CurrentThread.readMemory(retaddr, dbp.arch.PtrSize())
		if err != nil {
			return nil, err
		}
		ret = binary.LittleEndian.Uint64(data)
		if ret <= 0 {
			break
		}
		f, l, fn = dbp.goSymTable.PCToLine(ret)
		if fn == nil {
			break
		}
		locations = append(locations, Location{PC: ret, File: f, Line: l, Fn: fn})
		// Look for "top of stack" functions.
		if fn.Name == "runtime.rt0_go" {
			break
		}
	}
	return locations, nil
}
