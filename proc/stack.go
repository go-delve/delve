package proc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/derekparker/delve/dwarf/frame"
)

// NoReturnAddr is returned when return address
// could not be found during stack trace.
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
	// Start address of the stack frame.
	CFA  int64
	// Description of the stack frame.
	FDE  *frame.FrameDescriptionEntry
	// Return address for this stack frame (as read from the stack frame itself).
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

func (t *Thread) stackIterator() (*stackIterator, error) {
	regs, err := t.Registers()
	if err != nil {
		return nil, err
	}
	return newStackIterator(t.dbp, regs.PC(), regs.SP()), nil
}

// Stacktrace returns the stack trace for thread.
// Note the locations in the array are return addresses not call addresses.
func (t *Thread) Stacktrace(depth int) ([]Stackframe, error) {
	it, err := t.stackIterator()
	if err != nil {
		return nil, err
	}
	return it.stacktrace(depth)
}

func (g *G) stackIterator() (*stackIterator, error) {
	if g.thread != nil {
		return g.thread.stackIterator()
	}
	return newStackIterator(g.dbp, g.PC, g.SP), nil
}

// Stacktrace returns the stack trace for a goroutine.
// Note the locations in the array are return addresses not call addresses.
func (g *G) Stacktrace(depth int) ([]Stackframe, error) {
	it, err := g.stackIterator()
	if err != nil {
		return nil, err
	}
	return it.stacktrace(depth)
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

// stackIterator holds information
// required to iterate and walk the program
// stack.
type stackIterator struct {
	pc, sp uint64
	top    bool
	atend  bool
	frame  Stackframe
	dbp    *Process
	err    error
}

func newStackIterator(dbp *Process, pc, sp uint64) *stackIterator {
	return &stackIterator{pc: pc, sp: sp, top: true, dbp: dbp, err: nil, atend: false}
}

// Next points the iterator to the next stack frame.
func (it *stackIterator) Next() bool {
	if it.err != nil || it.atend {
		return false
	}
	it.frame, it.err = it.dbp.frameInfo(it.pc, it.sp, it.top)
	if it.err != nil {
		if _, nofde := it.err.(*frame.NoFDEForPCError); nofde && !it.top {
			it.frame = Stackframe{Current: Location{PC: it.pc, File: "?", Line: -1}, Call: Location{PC: it.pc, File: "?", Line: -1}, CFA: 0, Ret: 0}
			it.atend = true
			it.err = nil
			return true
		}
		return false
	}

	if it.frame.Current.Fn == nil {
		if it.top {
			it.err = fmt.Errorf("PC not associated to any function")
		}
		return false
	}

	if it.frame.Ret <= 0 {
		it.atend = true
		return true
	}
	// Look for "top of stack" functions.
	if it.frame.Current.Fn.Name == "runtime.goexit" || it.frame.Current.Fn.Name == "runtime.rt0_go" || it.frame.Current.Fn.Name == "runtime.mcall" {
		it.atend = true
		return true
	}

	it.top = false
	it.pc = it.frame.Ret
	it.sp = uint64(it.frame.CFA)
	return true
}

// Frame returns the frame the iterator is pointing at.
func (it *stackIterator) Frame() Stackframe {
	if it.err != nil {
		panic(it.err)
	}
	return it.frame
}

// Err returns the error encountered during stack iteration.
func (it *stackIterator) Err() error {
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
	r := Stackframe{Current: Location{PC: pc, File: f, Line: l, Fn: fn}, CFA: cfa, FDE: fde, Ret: binary.LittleEndian.Uint64(data)}
	if !top {
		r.Call.File, r.Call.Line, r.Call.Fn = dbp.PCToLine(pc - 1)
		r.Call.PC, _, _ = dbp.goSymTable.LineToPC(r.Call.File, r.Call.Line)
	} else {
		r.Call = r.Current
	}
	return r, nil
}

func (it *stackIterator) stacktrace(depth int) ([]Stackframe, error) {
	if depth < 0 {
		return nil, errors.New("negative maximum stack depth")
	}
	frames := make([]Stackframe, 0, depth+1)
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
