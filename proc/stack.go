package proc

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// NoReturnAddr is returned when return address
// could not be found during stack tracae.
type NoReturnAddr struct {
	fn string
}

func (nra NoReturnAddr) Error() string {
	return fmt.Sprintf("could not find return address for %s", nra.fn)
}

// Stackframe represents a frame in a system stack.
type Stackframe struct {
	// Address the function above this one on the call stack will return to.
	Current Location
	// Address of the call instruction for the function above on the call stack.
	Call Location
	CFA  int64
	Ret  uint64
}

// Scope returns a new EvalScope using this frame.
func (frame *Stackframe) Scope(thread *Thread) *EvalScope {
	return &EvalScope{Thread: thread, PC: frame.Current.PC, CFA: frame.CFA}
}

// ReturnAddress returns the return address of the function
// this thread is executing.
func (t *Thread) ReturnAddress() (uint64, error) {
	locations, err := t.Stacktrace(2)
	if err != nil {
		return 0, err
	}
	if len(locations) < 2 {
		return 0, NoReturnAddr{locations[0].Current.Fn.BaseName()}
	}
	return locations[1].Current.PC, nil
}

// Stacktrace returns the stack trace for thread.
// Note the locations in the array are return addresses not call addresses.
func (t *Thread) Stacktrace(depth int) ([]Stackframe, error) {
	regs, err := t.Registers()
	if err != nil {
		return nil, err
	}
	return t.dbp.stacktrace(regs.PC(), regs.SP(), depth)
}

// GoroutineStacktrace returns the stack trace for a goroutine.
// Note the locations in the array are return addresses not call addresses.
func (dbp *Process) GoroutineStacktrace(g *G, depth int) ([]Stackframe, error) {
	if g.thread != nil {
		return g.thread.Stacktrace(depth)
	}
	locs, err := dbp.stacktrace(g.PC, g.SP, depth)
	return locs, err
}

// GoroutineLocation returns the location of the given
// goroutine.
func (dbp *Process) GoroutineLocation(g *G) *Location {
	f, l, fn := dbp.PCToLine(g.PC)
	return &Location{PC: g.PC, File: f, Line: l, Fn: fn}
}

// NullAddrError is an error for a null address.
type NullAddrError struct{}

func (n NullAddrError) Error() string {
	return "NULL address"
}

// StackIterator holds information
// required to iterate and walk the program
// stack.
type StackIterator struct {
	pc, sp uint64
	top    bool
	atend  bool
	frame  Stackframe
	dbp    *Process
	err    error
}

func newStackIterator(dbp *Process, pc, sp uint64) *StackIterator {
	return &StackIterator{pc: pc, sp: sp, top: true, dbp: dbp, err: nil, atend: false}
}

// Next points the iterator to the next stack frame.
func (it *StackIterator) Next() bool {
	if it.err != nil || it.atend {
		return false
	}
	it.frame, it.err = it.dbp.frameInfo(it.pc, it.sp, it.top)
	if it.err != nil {
		return false
	}

	if it.frame.Current.Fn == nil {
		return false
	}

	if it.frame.Ret <= 0 {
		it.atend = true
		return true
	}
	// Look for "top of stack" functions.
	if it.frame.Current.Fn.Name == "runtime.goexit" || it.frame.Current.Fn.Name == "runtime.rt0_go" {
		it.atend = true
		return true
	}

	it.top = false
	it.pc = it.frame.Ret
	it.sp = uint64(it.frame.CFA)
	return true
}

// Frame returns the frame the iterator is pointing at.
func (it *StackIterator) Frame() Stackframe {
	if it.err != nil {
		panic(it.err)
	}
	return it.frame
}

// Err returns the error encountered during stack iteration.
func (it *StackIterator) Err() error {
	return it.err
}

func (dbp *Process) frameInfo(pc, sp uint64, top bool) (Stackframe, error) {
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
	r := Stackframe{Current: Location{PC: pc, File: f, Line: l, Fn: fn}, CFA: cfa, Ret: binary.LittleEndian.Uint64(data)}
	if !top {
		r.Call.File, r.Call.Line, r.Call.Fn = dbp.PCToLine(pc - 1)
		r.Call.PC, _, _ = dbp.goSymTable.LineToPC(r.Call.File, r.Call.Line)
	} else {
		r.Call = r.Current
	}
	return r, nil
}

func (dbp *Process) stacktrace(pc, sp uint64, depth int) ([]Stackframe, error) {
	if depth < 0 {
		return nil, errors.New("negative maximum stack depth")
	}
	frames := make([]Stackframe, 0, depth+1)
	it := newStackIterator(dbp, pc, sp)
	for it.Next() {
		frames = append(frames, it.Frame())
		if len(frames) >= depth+1 {
			break
		}
	}
	if err := it.Err(); err != nil {
		return nil, err
	}
	return frames, nil
}
