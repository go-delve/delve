package proc

import (
	"errors"
	"fmt"

	"github.com/derekparker/delve/pkg/dwarf/frame"
)

// This code is partly adaped from runtime.gentraceback in
// $GOROOT/src/runtime/traceback.go

const runtimeStackBarrier = "runtime.stackBarrier"

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
	CFA int64
	// Description of the stack frame.
	FDE *frame.FrameDescriptionEntry
	// Return address for this stack frame (as read from the stack frame itself).
	Ret uint64
	// Address to the memory location containing the return address
	addrret uint64
}

// Stacktrace returns the stack trace for thread.
// Note the locations in the array are return addresses not call addresses.
func ThreadStacktrace(thread IThread, depth int) ([]Stackframe, error) {
	regs, err := thread.Registers(false)
	if err != nil {
		return nil, err
	}
	it := newStackIterator(thread.BinInfo(), thread, regs.PC(), regs.SP(), regs.BP(), nil, -1)
	return it.stacktrace(depth)
}

func (g *G) stackIterator() (*stackIterator, error) {
	stkbar, err := g.stkbar()
	if err != nil {
		return nil, err
	}
	if g.thread != nil {
		regs, err := g.thread.Registers(false)
		if err != nil {
			return nil, err
		}
		return newStackIterator(g.variable.bi, g.thread, regs.PC(), regs.SP(), regs.BP(), stkbar, g.stkbarPos), nil
	}
	return newStackIterator(g.variable.bi, g.variable.mem, g.PC, g.SP, 0, stkbar, g.stkbarPos), nil
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

// NullAddrError is an error for a null address.
type NullAddrError struct{}

func (n NullAddrError) Error() string {
	return "NULL address"
}

// stackIterator holds information
// required to iterate and walk the program
// stack.
type stackIterator struct {
	pc, sp, bp uint64
	top        bool
	atend      bool
	frame      Stackframe
	bi         *BinaryInfo
	mem        memoryReadWriter
	err        error

	stackBarrierPC uint64
	stkbar         []savedLR
}

type savedLR struct {
	ptr uint64
	val uint64
}

func newStackIterator(bi *BinaryInfo, mem memoryReadWriter, pc, sp, bp uint64, stkbar []savedLR, stkbarPos int) *stackIterator {
	stackBarrierFunc := bi.goSymTable.LookupFunc(runtimeStackBarrier) // stack barriers were removed in Go 1.9
	var stackBarrierPC uint64
	if stackBarrierFunc != nil && stkbar != nil {
		stackBarrierPC = stackBarrierFunc.Entry
		fn := bi.goSymTable.PCToFunc(pc)
		if fn != nil && fn.Name == runtimeStackBarrier {
			// We caught the goroutine as it's executing the stack barrier, we must
			// determine whether or not g.stackPos has already been incremented or not.
			if len(stkbar) > 0 && stkbar[stkbarPos].ptr < sp {
				// runtime.stackBarrier has not incremented stkbarPos.
			} else if stkbarPos > 0 && stkbar[stkbarPos-1].ptr < sp {
				// runtime.stackBarrier has incremented stkbarPos.
				stkbarPos--
			} else {
				return &stackIterator{err: fmt.Errorf("failed to unwind through stackBarrier at SP %x", sp)}
			}
		}
		stkbar = stkbar[stkbarPos:]
	}
	return &stackIterator{pc: pc, sp: sp, bp: bp, top: true, bi: bi, mem: mem, err: nil, atend: false, stackBarrierPC: stackBarrierPC, stkbar: stkbar}
}

// Next points the iterator to the next stack frame.
func (it *stackIterator) Next() bool {
	if it.err != nil || it.atend {
		return false
	}
	it.frame, it.err = it.frameInfo(it.pc, it.sp, it.bp, it.top)
	if it.err != nil {
		if _, nofde := it.err.(*frame.NoFDEForPCError); nofde && !it.top {
			it.frame = Stackframe{Current: Location{PC: it.pc, File: "?", Line: -1}, Call: Location{PC: it.pc, File: "?", Line: -1}, CFA: 0, Ret: 0}
			it.atend = true
			it.err = nil
			return true
		}
		return false
	}

	if it.frame.Ret <= 0 {
		it.atend = true
		return true
	}

	if it.stkbar != nil && it.frame.Ret == it.stackBarrierPC && it.frame.addrret == it.stkbar[0].ptr {
		// Skip stack barrier frames
		it.frame.Ret = it.stkbar[0].val
		it.stkbar = it.stkbar[1:]
	}

	// Look for "top of stack" functions.
	if it.frame.Current.Fn != nil && (it.frame.Current.Fn.Name == "runtime.goexit" || it.frame.Current.Fn.Name == "runtime.rt0_go" || it.frame.Current.Fn.Name == "runtime.mcall") {
		it.atend = true
		return true
	}

	it.top = false
	it.pc = it.frame.Ret
	it.sp = uint64(it.frame.CFA)
	it.bp, _ = readUintRaw(it.mem, uintptr(it.bp), int64(it.bi.arch.PtrSize()))
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

func (it *stackIterator) frameInfo(pc, sp, bp uint64, top bool) (Stackframe, error) {
	fde, err := it.bi.frameEntries.FDEForPC(pc)
	if _, nofde := err.(*frame.NoFDEForPCError); nofde {
		if bp == 0 {
			return Stackframe{}, err
		}
		// When no FDE is available attempt to use BP instead
		retaddr := uintptr(int(bp) + it.bi.arch.PtrSize())
		cfa := int64(retaddr) + int64(it.bi.arch.PtrSize())
		return it.newStackframe(pc, cfa, retaddr, nil, top)
	}

	spoffset, retoffset := fde.ReturnAddressOffset(pc)
	cfa := int64(sp) + spoffset

	retaddr := uintptr(cfa + retoffset)
	return it.newStackframe(pc, cfa, retaddr, fde, top)
}

func (it *stackIterator) newStackframe(pc uint64, cfa int64, retaddr uintptr, fde *frame.FrameDescriptionEntry, top bool) (Stackframe, error) {
	if retaddr == 0 {
		return Stackframe{}, NullAddrError{}
	}
	f, l, fn := it.bi.PCToLine(pc)
	ret, err := readUintRaw(it.mem, retaddr, int64(it.bi.arch.PtrSize()))
	if err != nil {
		return Stackframe{}, err
	}
	r := Stackframe{Current: Location{PC: pc, File: f, Line: l, Fn: fn}, CFA: cfa, FDE: fde, Ret: ret, addrret: uint64(retaddr)}
	if !top {
		r.Call.File, r.Call.Line, r.Call.Fn = it.bi.PCToLine(pc - 1)
		r.Call.PC = r.Current.PC
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
