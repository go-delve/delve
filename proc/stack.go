package proc

import (
	"encoding/binary"
)

// Takes an offset from RSP and returns the address of the
// instruction the currect function is going to return to.
func (thread *Thread) ReturnAddress() (uint64, error) {
	locations, err := thread.Stacktrace(1)
	if err != nil {
		return 0, err
	}
	return locations[0].PC, nil
}

// Returns the stack trace for thread
// Note that it doesn't include the current frame and the locations in the array are return addresses not call addresses
func (thread *Thread) Stacktrace(depth int) ([]Location, error) {
	regs, err := thread.Registers()
	if err != nil {
		return nil, err
	}
	locations, err := thread.dbp.stacktrace(regs.PC(), regs.SP(), depth)
	if err != nil {
		return nil, err
	}
	return locations, nil
}

// Returns the stack trace for a goroutine
// Note that it doesn't include the current frame and the locations in the array are return addresses not call addresses
func (dbp *Process) GoroutineStacktrace(g *G, depth int) ([]Location, error) {
	if g.thread != nil {
		return g.thread.Stacktrace(depth)
	}
	return dbp.stacktrace(g.PC, g.SP, depth)
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
		data      = make([]byte, dbp.arch.PtrSize())
		btoffset  int64
		locations []Location
		retaddr   uintptr
	)
	for i := int64(0); i < int64(depth); i++ {
		fde, err := dbp.frameEntries.FDEForPC(ret)
		if err != nil {
			return nil, err
		}
		btoffset += fde.ReturnAddressOffset(ret)
		retaddr = uintptr(int64(sp) + btoffset + (i * int64(dbp.arch.PtrSize())))
		if retaddr == 0 {
			return nil, NullAddrError{}
		}
		_, err = readMemory(dbp.CurrentThread, retaddr, data)
		if err != nil {
			return nil, err
		}
		ret = binary.LittleEndian.Uint64(data)
		if ret <= 0 {
			break
		}
		f, l, fn := dbp.goSymTable.PCToLine(ret)
		locations = append(locations, Location{PC: ret, File: f, Line: l, Fn: fn})
		if fn != nil && fn.Name == "runtime.goexit" {
			break
		}

	}
	return locations, nil
}
