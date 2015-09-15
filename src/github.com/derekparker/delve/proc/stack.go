package proc

import (
	"encoding/binary"
	"fmt"
)

type NoReturnAddr struct {
	fn string
}

func (nra NoReturnAddr) Error() string {
	return fmt.Sprintf("could not find return address for %s", nra.fn)
}

type Stackframe struct {
	Location
	CFA int64
	Ret uint64
}

// Takes an offset from RSP and returns the address of the
// instruction the current function is going to return to.
func (thread *Thread) ReturnAddress() (uint64, error) {
	locations, err := thread.Stacktrace(2)
	if err != nil {
		return 0, err
	}
	if len(locations) < 2 {
		return 0, NoReturnAddr{locations[0].Fn.BaseName()}
	}
	return locations[1].PC, nil
}

// Returns the stack trace for thread.
// Note the locations in the array are return addresses not call addresses.
func (thread *Thread) Stacktrace(depth int) ([]Stackframe, error) {
	regs, err := thread.Registers()
	if err != nil {
		return nil, err
	}
	return thread.dbp.stacktrace(regs.PC(), regs.SP(), depth)
}

// Returns the stack trace for a goroutine.
// Note the locations in the array are return addresses not call addresses.
func (dbp *Process) GoroutineStacktrace(g *G, depth int) ([]Stackframe, error) {
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

func (dbp *Process) frameInfo(pc, sp uint64) (Stackframe, error) {
	f, l, fn := dbp.PCToLine(pc)
	fde, err := dbp.frameEntries.FDEForPC(pc)
	if err != nil {
		return Stackframe{}, err
	}
	spoffset, retoffset := fde.ReturnAddressOffset(pc)
	cfa := int64(sp) + spoffset

	retaddr := uintptr(cfa + retoffset)
	if retaddr == 0 {
		return Stackframe{}, NullAddrError{}
	}
	data, err := dbp.CurrentThread.readMemory(retaddr, dbp.arch.PtrSize())
	if err != nil {
		return Stackframe{}, err
	}
	return Stackframe{Location: Location{PC: pc, File: f, Line: l, Fn: fn}, CFA: cfa, Ret: binary.LittleEndian.Uint64(data)}, nil
}

func (dbp *Process) stacktrace(pc, sp uint64, depth int) ([]Stackframe, error) {
	frames := make([]Stackframe, 0, depth+1)

	for i := 0; i < depth+1; i++ {
		frame, err := dbp.frameInfo(pc, sp)
		if err != nil {
			return nil, err
		}
		if frame.Fn == nil {
			break
		}
		frames = append(frames, frame)
		if frame.Ret <= 0 {
			break
		}
		// Look for "top of stack" functions.
		if frame.Fn.Name == "runtime.goexit" || frame.Fn.Name == "runtime.rt0_go" {
			break
		}

		pc = frame.Ret
		sp = uint64(frame.CFA)
	}
	return frames, nil
}
