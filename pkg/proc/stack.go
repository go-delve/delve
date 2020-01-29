package proc

import (
	"debug/dwarf"
	"errors"
	"fmt"
	"go/constant"

	"github.com/go-delve/delve/pkg/dwarf/frame"
	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/reader"
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
		return it.stacktrace(depth)
	}
	return g.Stacktrace(depth, 0)
}

func (g *G) stackIterator(opts StacktraceOptions) (*stackIterator, error) {
	stkbar, err := g.stkbar()
	if err != nil {
		return nil, err
	}

	bi := g.variable.bi
	if g.Thread != nil {
		regs, err := g.Thread.Registers(true)
		if err != nil {
			return nil, err
		}
		so := bi.PCToImage(regs.PC())
		return newStackIterator(
			bi, g.Thread,
			bi.Arch.RegistersToDwarfRegisters(so.StaticBase, regs),
			g.stackhi, stkbar, g.stkbarPos, g, opts), nil
	}
	so := g.variable.bi.PCToImage(g.PC)
	return newStackIterator(
		bi, g.variable.mem,
		bi.Arch.AddrAndStackRegsToDwarfRegisters(so.StaticBase, g.PC, g.SP, g.BP, g.LR),
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
	frames, err := it.stacktrace(depth)
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

// stackIterator holds information
// required to iterate and walk the program
// stack.
type stackIterator struct {
	pc    uint64
	top   bool
	atend bool
	frame Stackframe
	bi    *BinaryInfo
	mem   MemoryReadWriter
	err   error

	stackhi        uint64
	systemstack    bool
	stackBarrierPC uint64
	stkbar         []savedLR

	// regs is the register set for the current frame
	regs op.DwarfRegisters

	g                  *G     // the goroutine being stacktraced, nil if we are stacktracing a goroutine-less thread
	g0_sched_sp        uint64 // value of g0.sched.sp (see comments around its use)
	g0_sched_sp_loaded bool   // g0_sched_sp was loaded from g0

	opts StacktraceOptions
}

type savedLR struct {
	ptr uint64
	val uint64
}

func newStackIterator(bi *BinaryInfo, mem MemoryReadWriter, regs op.DwarfRegisters, stackhi uint64, stkbar []savedLR, stkbarPos int, g *G, opts StacktraceOptions) *stackIterator {
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
				return &stackIterator{err: fmt.Errorf("failed to unwind through stackBarrier at SP %x", regs.SP())}
			}
		}
		stkbar = stkbar[stkbarPos:]
	}
	systemstack := true
	if g != nil {
		systemstack = g.SystemStack
	}
	return &stackIterator{pc: regs.PC(), regs: regs, top: true, bi: bi, mem: mem, err: nil, atend: false, stackhi: stackhi, stackBarrierPC: stackBarrierPC, stkbar: stkbar, systemstack: systemstack, g: g, opts: opts}
}

// Next points the iterator to the next stack frame.
func (it *stackIterator) Next() bool {
	if it.err != nil || it.atend {
		return false
	}

	callFrameRegs, ret, retaddr := it.advanceRegs()
	it.frame = it.newStackframe(ret, retaddr)

	if it.stkbar != nil && it.frame.Ret == it.stackBarrierPC && it.frame.addrret == it.stkbar[0].ptr {
		// Skip stack barrier frames
		it.frame.Ret = it.stkbar[0].val
		it.stkbar = it.stkbar[1:]
	}

	if it.opts&StacktraceSimple == 0 {
		if it.bi.Arch.SwitchStack(it, &callFrameRegs) {
			return true
		}
	}

	if it.frame.Ret <= 0 {
		it.atend = true
		return true
	}

	it.top = false
	it.pc = it.frame.Ret
	it.regs = callFrameRegs
	return true
}

func (it *stackIterator) switchToGoroutineStack() {
	it.systemstack = false
	it.top = false
	it.pc = it.g.PC
	it.regs.Reg(it.regs.SPRegNum).Uint64Val = it.g.SP
	it.regs.Reg(it.regs.BPRegNum).Uint64Val = it.g.BP
	if _, ok := it.bi.Arch.(*ARM64); ok {
		it.regs.Reg(it.regs.LRRegNum).Uint64Val = it.g.LR
	}
}

// Frame returns the frame the iterator is pointing at.
func (it *stackIterator) Frame() Stackframe {
	it.frame.Bottom = it.atend
	return it.frame
}

// Err returns the error encountered during stack iteration.
func (it *stackIterator) Err() error {
	return it.err
}

// frameBase calculates the frame base pseudo-register for DWARF for fn and
// the current frame.
func (it *stackIterator) frameBase(fn *Function) int64 {
	dwarfTree, err := fn.cu.image.getDwarfTree(fn.offset)
	if err != nil {
		return 0
	}
	fb, _, _, _ := it.bi.Location(dwarfTree.Entry, dwarf.AttrFrameBase, it.pc, it.regs)
	return fb
}

func (it *stackIterator) newStackframe(ret, retaddr uint64) Stackframe {
	if retaddr == 0 {
		it.err = NullAddrError{}
		return Stackframe{}
	}
	f, l, fn := it.bi.PCToLine(it.pc)
	if fn == nil {
		f = "?"
		l = -1
	} else {
		it.regs.FrameBase = it.frameBase(fn)
	}
	r := Stackframe{Current: Location{PC: it.pc, File: f, Line: l, Fn: fn}, Regs: it.regs, Ret: ret, addrret: retaddr, stackHi: it.stackhi, SystemStack: it.systemstack, lastpc: it.pc}
	r.Call = r.Current
	if !it.top && r.Current.Fn != nil && it.pc != r.Current.Fn.Entry {
		// if the return address is the entry point of the function that
		// contains it then this is some kind of fake return frame (for example
		// runtime.sigreturn) that didn't actually call the current frame,
		// attempting to get the location of the CALL instruction would just
		// obfuscate what's going on, since there is no CALL instruction.
		switch r.Current.Fn.Name {
		case "runtime.mstart", "runtime.systemstack_switch":
			// these frames are inserted by runtime.systemstack and there is no CALL
			// instruction to look for at pc - 1
		default:
			r.lastpc = it.pc - 1
			r.Call.File, r.Call.Line = r.Current.Fn.cu.lineInfo.PCToLine(r.Current.Fn.Entry, it.pc-1)
		}
	}
	return r
}

func (it *stackIterator) stacktrace(depth int) ([]Stackframe, error) {
	if depth < 0 {
		return nil, errors.New("negative maximum stack depth")
	}
	if it.opts&StacktraceG != 0 && it.g != nil {
		it.switchToGoroutineStack()
		it.top = true
	}
	frames := make([]Stackframe, 0, depth+1)
	for it.Next() {
		frames = it.appendInlineCalls(frames, it.Frame())
		if len(frames) >= depth+1 {
			break
		}
	}
	if err := it.Err(); err != nil {
		if len(frames) == 0 {
			return nil, err
		}
		frames = append(frames, Stackframe{Err: err})
	}
	return frames, nil
}

func (it *stackIterator) appendInlineCalls(frames []Stackframe, frame Stackframe) []Stackframe {
	if frame.Call.Fn == nil {
		return append(frames, frame)
	}
	if frame.Call.Fn.cu.lineInfo == nil {
		return append(frames, frame)
	}

	callpc := frame.Call.PC
	if len(frames) > 0 {
		callpc--
	}

	dwarfTree, err := frame.Call.Fn.cu.image.getDwarfTree(frame.Call.Fn.offset)
	if err != nil {
		return append(frames, frame)
	}

	for _, entry := range reader.InlineStack(dwarfTree, callpc) {
		fnname, okname := entry.Val(dwarf.AttrName).(string)
		fileidx, okfileidx := entry.Val(dwarf.AttrCallFile).(int64)
		line, okline := entry.Val(dwarf.AttrCallLine).(int64)

		if !okname || !okfileidx || !okline {
			break
		}
		if fileidx-1 < 0 || fileidx-1 >= int64(len(frame.Current.Fn.cu.lineInfo.FileNames)) {
			break
		}

		inlfn := &Function{Name: fnname, Entry: frame.Call.Fn.Entry, End: frame.Call.Fn.End, offset: entry.Offset, cu: frame.Call.Fn.cu}
		frames = append(frames, Stackframe{
			Current: frame.Current,
			Call: Location{
				frame.Call.PC,
				frame.Call.File,
				frame.Call.Line,
				inlfn,
			},
			Regs:        frame.Regs,
			stackHi:     frame.stackHi,
			Ret:         frame.Ret,
			addrret:     frame.addrret,
			Err:         frame.Err,
			SystemStack: frame.SystemStack,
			Inlined:     true,
			lastpc:      frame.lastpc,
		})

		frame.Call.File = frame.Current.Fn.cu.lineInfo.FileNames[fileidx-1].Path
		frame.Call.Line = int(line)
	}

	return append(frames, frame)
}

// advanceRegs calculates it.callFrameRegs using it.regs and the frame
// descriptor entry for the current stack frame.
// it.regs.CallFrameCFA is updated.
func (it *stackIterator) advanceRegs() (callFrameRegs op.DwarfRegisters, ret uint64, retaddr uint64) {
	fde, err := it.bi.frameEntries.FDEForPC(it.pc)
	var framectx *frame.FrameContext
	if _, nofde := err.(*frame.ErrNoFDEForPC); nofde {
		framectx = it.bi.Arch.FixFrameUnwindContext(nil, it.pc, it.bi)
	} else {
		framectx = it.bi.Arch.FixFrameUnwindContext(fde.EstablishFrame(it.pc), it.pc, it.bi)
	}

	cfareg, err := it.executeFrameRegRule(0, framectx.CFA, 0)
	if cfareg == nil {
		it.err = fmt.Errorf("CFA becomes undefined at PC %#x", it.pc)
		return op.DwarfRegisters{}, 0, 0
	}
	it.regs.CFA = int64(cfareg.Uint64Val)

	callimage := it.bi.PCToImage(it.pc)

	callFrameRegs = op.DwarfRegisters{StaticBase: callimage.StaticBase, ByteOrder: it.regs.ByteOrder, PCRegNum: it.regs.PCRegNum, SPRegNum: it.regs.SPRegNum, BPRegNum: it.regs.BPRegNum, LRRegNum: it.regs.LRRegNum}

	// According to the standard the compiler should be responsible for emitting
	// rules for the RSP register so that it can then be used to calculate CFA,
	// however neither Go nor GCC do this.
	// In the following line we copy GDB's behaviour by assuming this is
	// implicit.
	// See also the comment in dwarf2_frame_default_init in
	// $GDB_SOURCE/dwarf2-frame.c
	callFrameRegs.AddReg(callFrameRegs.SPRegNum, cfareg)

	for i, regRule := range framectx.Regs {
		reg, err := it.executeFrameRegRule(i, regRule, it.regs.CFA)
		callFrameRegs.AddReg(i, reg)
		if i == framectx.RetAddrReg {
			if reg == nil {
				if err == nil {
					err = fmt.Errorf("Undefined return address at %#x", it.pc)
				}
				it.err = err
			} else {
				ret = reg.Uint64Val
			}
			retaddr = uint64(it.regs.CFA + regRule.Offset)
		}
	}

	if _, ok := it.bi.Arch.(*ARM64); ok {
		if ret == 0 && it.regs.Regs[it.regs.LRRegNum] != nil {
			ret = it.regs.Regs[it.regs.LRRegNum].Uint64Val
		}
	}

	return callFrameRegs, ret, retaddr
}

func (it *stackIterator) executeFrameRegRule(regnum uint64, rule frame.DWRule, cfa int64) (*op.DwarfRegister, error) {
	switch rule.Rule {
	default:
		fallthrough
	case frame.RuleUndefined:
		return nil, nil
	case frame.RuleSameVal:
		if it.regs.Reg(regnum) == nil {
			return nil, nil
		}
		reg := *it.regs.Reg(regnum)
		return &reg, nil
	case frame.RuleOffset:
		return it.readRegisterAt(regnum, uint64(cfa+rule.Offset))
	case frame.RuleValOffset:
		return op.DwarfRegisterFromUint64(uint64(cfa + rule.Offset)), nil
	case frame.RuleRegister:
		return it.regs.Reg(rule.Reg), nil
	case frame.RuleExpression:
		v, _, err := op.ExecuteStackProgram(it.regs, rule.Expression, it.bi.Arch.PtrSize())
		if err != nil {
			return nil, err
		}
		return it.readRegisterAt(regnum, uint64(v))
	case frame.RuleValExpression:
		v, _, err := op.ExecuteStackProgram(it.regs, rule.Expression, it.bi.Arch.PtrSize())
		if err != nil {
			return nil, err
		}
		return op.DwarfRegisterFromUint64(uint64(v)), nil
	case frame.RuleArchitectural:
		return nil, errors.New("architectural frame rules are unsupported")
	case frame.RuleCFA:
		if it.regs.Reg(rule.Reg) == nil {
			return nil, nil
		}
		return op.DwarfRegisterFromUint64(uint64(int64(it.regs.Uint64Val(rule.Reg)) + rule.Offset)), nil
	case frame.RuleFramePointer:
		curReg := it.regs.Reg(rule.Reg)
		if curReg == nil {
			return nil, nil
		}
		if curReg.Uint64Val <= uint64(cfa) {
			return it.readRegisterAt(regnum, curReg.Uint64Val)
		}
		newReg := *curReg
		return &newReg, nil
	}
}

func (it *stackIterator) readRegisterAt(regnum uint64, addr uint64) (*op.DwarfRegister, error) {
	buf := make([]byte, it.bi.Arch.RegSize(regnum))
	_, err := it.mem.ReadMemory(buf, uintptr(addr))
	if err != nil {
		return nil, err
	}
	return op.DwarfRegisterFromBytes(buf), nil
}

func (it *stackIterator) loadG0SchedSP() {
	if it.g0_sched_sp_loaded {
		return
	}
	it.g0_sched_sp_loaded = true
	if it.g != nil {
		mvar, _ := it.g.variable.structMember("m")
		if mvar != nil {
			g0var, _ := mvar.structMember("g0")
			if g0var != nil {
				g0, _ := g0var.parseG()
				if g0 != nil {
					it.g0_sched_sp = g0.SP
				}
			}
		}
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
