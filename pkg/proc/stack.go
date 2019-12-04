package proc

import (
	"debug/dwarf"
	"errors"
	"fmt"
	"go/constant"

	"github.com/go-delve/delve/pkg/dwarf/op"
)

// This code is partly adapted from runtime.gentraceback in
// $GOROOT/src/runtime/traceback.go

// Stackframe represents a frame in a system stack.
//
// Each stack frame has two locations Current and Call.
//
// For the topmost stackframe Current and Call are the same location.
//
// For stackframes after the first Current is the location corresponding to
// the return address and Call is the location of the CALL instruction that
// was last executed on the frame. Note however that Call.PC is always equal
// to Current.PC, because finding the correct value for Call.PC would
// require disassembling each function in the stacktrace.
//
// For synthetic stackframes generated for inlined function calls Current.Fn
// is the function containing the inlining and Call.Fn in the inlined
// function.
type Stackframe struct {
	Current, Call Location

	// Frame registers.
	Regs op.DwarfRegisters
	// High address of the stack.
	stackHi uint64
	// Return address for this stack frame (as read from the stack frame itself).
	Ret uint64
	// Address to the memory location containing the return address
	addrret uint64
	// Err is set if an error occurred during stacktrace
	Err error
	// SystemStack is true if this frame belongs to a system stack.
	SystemStack bool
	// Inlined is true if this frame is actually an inlined call.
	Inlined bool
	// Bottom is true if this is the bottom of the stack
	Bottom bool

	// lastpc is a memory address guaranteed to belong to the last instruction
	// executed in this stack frame.
	// For the topmost stack frame this will be the same as Current.PC and
	// Call.PC, for other stack frames it will usually be Current.PC-1, but
	// could be different when inlined calls are involved in the stacktrace.
	// Note that this address isn't guaranteed to belong to the start of an
	// instruction and, for this reason, should not be propagated outside of
	// pkg/proc.
	// Use this value to determine active lexical scopes for the stackframe.
	lastpc uint64

	// TopmostDefer is the defer that would be at the top of the stack when a
	// panic unwind would get to this call frame, in other words it's the first
	// deferred function that will  be called if the runtime unwinds past this
	// call frame.
	TopmostDefer *Defer

	// Defers is the list of functions deferred by this stack frame (so far).
	Defers []*Defer
}

// FrameOffset returns the address of the stack frame, absolute for system
// stack frames or as an offset from stackhi for goroutine stacks (a
// negative value).
func (frame *Stackframe) FrameOffset() int64 {
	if frame.SystemStack {
		return frame.Regs.CFA
	}
	return frame.Regs.CFA - int64(frame.stackHi)
}

// FramePointerOffset returns the value of the frame pointer, absolute for
// system stack frames or as an offset from stackhi for goroutine stacks (a
// negative value).
func (frame *Stackframe) FramePointerOffset() int64 {
	if frame.SystemStack {
		return int64(frame.Regs.BP())
	}
	return int64(frame.Regs.BP()) - int64(frame.stackHi)
}

// ThreadStacktrace returns the stack trace for thread.
// Note the locations in the array are return addresses not call addresses.
func ThreadStacktrace(thread Thread, depth int) ([]Stackframe, error) {
	g, _ := GetG(thread)
	if g == nil {
		regs, err := thread.Registers(true)
		if err != nil {
			return nil, err
		}
		so := thread.BinInfo().PCToImage(regs.PC())
		it := newStackIterator(thread.BinInfo(), thread, thread.BinInfo().Arch.RegistersToDwarfRegisters(so.StaticBase, regs), 0, nil, -1, nil, 0)
		return it.Stacktrace(depth)
	}
	return g.Stacktrace(depth, 0)
}

func (g *G) stackIterator(opts StacktraceOptions) (stackIterator, error) {
	stkbar, err := g.stkbar()
	if err != nil {
		return nil, err
	}

	if g.Thread != nil {
		regs, err := g.Thread.Registers(true)
		if err != nil {
			return nil, err
		}
		so := g.variable.bi.PCToImage(regs.PC())
		return newStackIterator(
			g.variable.bi, g.Thread,
			g.variable.bi.Arch.RegistersToDwarfRegisters(so.StaticBase, regs),
			g.stackhi, stkbar, g.stkbarPos, g, opts), nil
	}
	so := g.variable.bi.PCToImage(g.PC)
	return newStackIterator(
		g.variable.bi, g.variable.mem,
		g.variable.bi.Arch.AddrAndStackRegsToDwarfRegisters(so.StaticBase, g.PC, g.SP, g.BP, g.LR),
		g.stackhi, stkbar, g.stkbarPos, g, opts), nil
}

type StacktraceOptions uint16

const (
	// StacktraceReadDefers requests a stacktrace decorated with deferred calls
	// for each frame.
	StacktraceReadDefers StacktraceOptions = 1 << iota

	// StacktraceSimple requests a stacktrace where no stack switches will be
	// attempted.
	StacktraceSimple

	// StacktraceG requests a stacktrace starting with the register
	// values saved in the runtime.g structure.
	StacktraceG
)

// Stacktrace returns the stack trace for a goroutine.
// Note the locations in the array are return addresses not call addresses.
func (g *G) Stacktrace(depth int, opts StacktraceOptions) ([]Stackframe, error) {
	it, err := g.stackIterator(opts)
	if err != nil {
		return nil, err
	}
	frames, err := it.Stacktrace(depth)
	if err != nil {
		return nil, err
	}
	if opts&StacktraceReadDefers != 0 {
		g.readDefers(frames)
	}
	return frames, nil
}

// NullAddrError is an error for a null address.
type NullAddrError struct{}

func (n NullAddrError) Error() string {
	return "NULL address"
}

type stackIterator interface {
	Next() bool
	Frame() Stackframe
	Err() error
	Stacktrace(depth int) ([]Stackframe, error)
}

type savedLR struct {
	ptr uint64
	val uint64
}

func newStackIterator(bi *BinaryInfo, mem MemoryReadWriter, regs op.DwarfRegisters, stackhi uint64, stkbar []savedLR, stkbarPos int, g *G, opts StacktraceOptions) stackIterator {
	stackBarrierFunc := bi.LookupFunc["runtime.stackBarrier"] // stack barriers were removed in Go 1.9
	var stackBarrierPC uint64
	if stackBarrierFunc != nil && stkbar != nil {
		stackBarrierPC = stackBarrierFunc.Entry
		fn := bi.PCToFunc(regs.PC())
		if fn != nil && fn.Name == "runtime.stackBarrier" {
			// We caught the goroutine as it's executing the stack barrier, we must
			// determine whether or not g.stackPos has already been incremented or not.
			if len(stkbar) > 0 && stkbar[stkbarPos].ptr < regs.SP() {
				// runtime.stackBarrier has not incremented stkbarPos.
			} else if stkbarPos > 0 && stkbar[stkbarPos-1].ptr < regs.SP() {
				// runtime.stackBarrier has incremented stkbarPos.
				stkbarPos--
			} else {
				switch bi.Arch.(type) {
				case *AMD64:
					return &amd64Stack{err: fmt.Errorf("failed to unwind through stackBarrier at SP %x", regs.SP())}
				case *ARM64:
					return &arm64Stack{err: fmt.Errorf("failed to unwind through stackBarrier at SP %x", regs.SP())}
				default:
					return nil
				}
			}
		}
		stkbar = stkbar[stkbarPos:]
	}
	var g0_sched_sp uint64
	systemstack := true
	if g != nil {
		systemstack = g.SystemStack
		g0var, _ := g.variable.fieldVariable("m").structMember("g0")
		if g0var != nil {
			g0, _ := g0var.parseG()
			if g0 != nil {
				g0_sched_sp = g0.SP
			}
		}
	}
	switch bi.Arch.(type) {
	case *AMD64:
		return &amd64Stack{pc: regs.PC(), regs: regs, top: true, bi: bi, mem: mem, err: nil, atend: false, stackhi: stackhi, stackBarrierPC: stackBarrierPC, stkbar: stkbar, systemstack: systemstack, g: g, g0_sched_sp: g0_sched_sp, opts: opts}
	case *ARM64:
		return &arm64Stack{pc: regs.PC(), regs: regs, top: true, bi: bi, mem: mem, err: nil, atend: false, stackhi: stackhi, stackBarrierPC: stackBarrierPC, stkbar: stkbar, systemstack: systemstack, g: g, g0_sched_sp: g0_sched_sp, opts: opts}
	default:
		return nil
	}
}

// Defer represents one deferred call
type Defer struct {
	DeferredPC uint64 // Value of field _defer.fn.fn, the deferred function
	DeferPC    uint64 // PC address of instruction that added this defer
	SP         uint64 // Value of SP register when this function was deferred (this field gets adjusted when the stack is moved to match the new stack space)
	link       *Defer // Next deferred function
	argSz      int64

	variable   *Variable
	Unreadable error
}

// readDefers decorates the frames with the function deferred at each stack frame.
func (g *G) readDefers(frames []Stackframe) {
	curdefer := g.Defer()
	i := 0

	// scan simultaneously frames and the curdefer linked list, assigning
	// defers to their associated frames.
	for {
		if curdefer == nil || i >= len(frames) {
			return
		}
		if curdefer.Unreadable != nil {
			// Current defer is unreadable, stick it into the first available frame
			// (so that it can be reported to the user) and exit
			frames[i].Defers = append(frames[i].Defers, curdefer)
			return
		}
		if frames[i].Err != nil {
			return
		}

		if frames[i].TopmostDefer == nil {
			frames[i].TopmostDefer = curdefer
		}

		if frames[i].SystemStack || curdefer.SP >= uint64(frames[i].Regs.CFA) {
			// frames[i].Regs.CFA is the value that SP had before the function of
			// frames[i] was called.
			// This means that when curdefer.SP == frames[i].Regs.CFA then curdefer
			// was added by the previous frame.
			//
			// curdefer.SP < frames[i].Regs.CFA means curdefer was added by a
			// function further down the stack.
			//
			// SystemStack frames live on a different physical stack and can't be
			// compared with deferred frames.
			i++
		} else {
			frames[i].Defers = append(frames[i].Defers, curdefer)
			curdefer = curdefer.Next()
		}
	}
}

func (d *Defer) load() {
	d.variable.loadValue(LoadConfig{false, 1, 0, 0, -1, 0})
	if d.variable.Unreadable != nil {
		d.Unreadable = d.variable.Unreadable
		return
	}

	fnvar := d.variable.fieldVariable("fn").maybeDereference()
	if fnvar.Addr != 0 {
		fnvar = fnvar.loadFieldNamed("fn")
		if fnvar.Unreadable == nil {
			d.DeferredPC, _ = constant.Uint64Val(fnvar.Value)
		}
	}

	d.DeferPC, _ = constant.Uint64Val(d.variable.fieldVariable("pc").Value)
	d.SP, _ = constant.Uint64Val(d.variable.fieldVariable("sp").Value)
	d.argSz, _ = constant.Int64Val(d.variable.fieldVariable("siz").Value)

	linkvar := d.variable.fieldVariable("link").maybeDereference()
	if linkvar.Addr != 0 {
		d.link = &Defer{variable: linkvar}
	}
}

// errSPDecreased is used when (*Defer).Next detects a corrupted linked
// list, specifically when after followin a link pointer the value of SP
// decreases rather than increasing or staying the same (the defer list is a
// FIFO list, nodes further down the list have been added by function calls
// further down the call stack and therefore the SP should always increase).
var errSPDecreased = errors.New("corrupted defer list: SP decreased")

// Next returns the next defer in the linked list
func (d *Defer) Next() *Defer {
	if d.link == nil {
		return nil
	}
	d.link.load()
	if d.link.SP < d.SP {
		d.link.Unreadable = errSPDecreased
	}
	return d.link
}

// EvalScope returns an EvalScope relative to the argument frame of this deferred call.
// The argument frame of a deferred call is stored in memory immediately
// after the deferred header.
func (d *Defer) EvalScope(thread Thread) (*EvalScope, error) {
	scope, err := GoroutineScope(thread)
	if err != nil {
		return nil, fmt.Errorf("could not get scope: %v", err)
	}

	bi := thread.BinInfo()
	scope.PC = d.DeferredPC
	scope.File, scope.Line, scope.Fn = bi.PCToLine(d.DeferredPC)

	if scope.Fn == nil {
		return nil, fmt.Errorf("could not find function at %#x", d.DeferredPC)
	}

	// The arguments are stored immediately after the defer header struct, i.e.
	// addr+sizeof(_defer). Since CFA in go is always the address of the first
	// argument, that's what we use for the value of CFA.
	// For SP we use CFA minus the size of one pointer because that would be
	// the space occupied by pushing the return address on the stack during the
	// CALL.
	scope.Regs.CFA = (int64(d.variable.Addr) + d.variable.RealType.Common().ByteSize)
	scope.Regs.Regs[scope.Regs.SPRegNum].Uint64Val = uint64(scope.Regs.CFA - int64(bi.Arch.PtrSize()))

	rdr := scope.Fn.cu.image.dwarfReader
	rdr.Seek(scope.Fn.offset)
	e, err := rdr.Next()
	if err != nil {
		return nil, fmt.Errorf("could not read DWARF function entry: %v", err)
	}
	scope.Regs.FrameBase, _, _, _ = bi.Location(e, dwarf.AttrFrameBase, scope.PC, scope.Regs)
	scope.Mem = cacheMemory(scope.Mem, uintptr(scope.Regs.CFA), int(d.argSz))

	return scope, nil
}
