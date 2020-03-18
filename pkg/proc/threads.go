package proc

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"reflect"

	"github.com/go-delve/delve/pkg/dwarf/godwarf"
)

// Thread represents a thread.
type Thread interface {
	MemoryReadWriter
	Location() (*Location, error)
	// Breakpoint will return the breakpoint that this thread is stopped at or
	// nil if the thread is not stopped at any breakpoint.
	Breakpoint() *BreakpointState
	ThreadID() int

	// Registers returns the CPU registers of this thread. The contents of the
	// variable returned may or may not change to reflect the new CPU status
	// when the thread is resumed or the registers are changed by calling
	// SetPC/SetSP/etc.
	// To insure that the the returned variable won't change call the Copy
	// method of Registers.
	Registers(floatingPoint bool) (Registers, error)

	// RestoreRegisters restores saved registers
	RestoreRegisters(Registers) error
	Arch() Arch
	BinInfo() *BinaryInfo
	StepInstruction() error
	// Blocked returns true if the thread is blocked
	Blocked() bool
	// SetCurrentBreakpoint updates the current breakpoint of this thread, if adjustPC is true also checks for breakpoints that were just hit (this should only be passed true after a thread resume)
	SetCurrentBreakpoint(adjustPC bool) error
	// Common returns the CommonThread structure for this thread
	Common() *CommonThread

	SetPC(uint64) error
	SetSP(uint64) error
	SetDX(uint64) error
}

// Location represents the location of a thread.
// Holds information on the current instruction
// address, the source file:line, and the function.
type Location struct {
	PC   uint64
	File string
	Line int
	Fn   *Function
}

// ErrThreadBlocked is returned when the thread
// is blocked in the scheduler.
type ErrThreadBlocked struct{}

func (tbe ErrThreadBlocked) Error() string {
	return "thread blocked"
}

// CommonThread contains fields used by this package, common to all
// implementations of the Thread interface.
type CommonThread struct {
	returnValues []*Variable
	g            *G // cached g for this thread
}

// ReturnValues reads the return values from the function executing on
// this thread using the provided LoadConfig.
func (t *CommonThread) ReturnValues(cfg LoadConfig) []*Variable {
	loadValues(t.returnValues, cfg)
	return t.returnValues
}

// topframe returns the two topmost frames of g, or thread if g is nil.
func topframe(g *G, thread Thread) (Stackframe, Stackframe, error) {
	var frames []Stackframe
	var err error

	if g == nil {
		if thread.Blocked() {
			return Stackframe{}, Stackframe{}, ErrThreadBlocked{}
		}
		frames, err = ThreadStacktrace(thread, 1)
	} else {
		frames, err = g.Stacktrace(1, StacktraceReadDefers)
	}
	if err != nil {
		return Stackframe{}, Stackframe{}, err
	}
	switch len(frames) {
	case 0:
		return Stackframe{}, Stackframe{}, errors.New("empty stack trace")
	case 1:
		return frames[0], Stackframe{}, nil
	default:
		return frames[0], frames[1], nil
	}
}

// ErrNoSourceForPC is returned when the given address
// does not correspond with a source file location.
type ErrNoSourceForPC struct {
	pc uint64
}

func (err *ErrNoSourceForPC) Error() string {
	return fmt.Sprintf("no source for PC %#x", err.pc)
}

func getGVariable(thread Thread) (*Variable, error) {
	regs, err := thread.Registers(false)
	if err != nil {
		return nil, err
	}

	gaddr, hasgaddr := regs.GAddr()
	if !hasgaddr {
		var err error
		gaddr, err = readUintRaw(thread, uintptr(regs.TLS()+thread.BinInfo().GStructOffset()), int64(thread.BinInfo().Arch.PtrSize()))
		if err != nil {
			return nil, err
		}
	}

	return newGVariable(thread, uintptr(gaddr), thread.Arch().DerefTLS())
}

func newGVariable(thread Thread, gaddr uintptr, deref bool) (*Variable, error) {
	typ, err := thread.BinInfo().findType("runtime.g")
	if err != nil {
		return nil, err
	}

	name := ""

	if deref {
		typ = &godwarf.PtrType{
			CommonType: godwarf.CommonType{
				ByteSize:    int64(thread.Arch().PtrSize()),
				Name:        "",
				ReflectKind: reflect.Ptr,
				Offset:      0,
			},
			Type: typ,
		}
	} else {
		name = "runtime.curg"
	}

	return newVariableFromThread(thread, name, gaddr, typ), nil
}

// GetG returns information on the G (goroutine) that is executing on this thread.
//
// The G structure for a thread is stored in thread local storage. Here we simply
// calculate the address and read and parse the G struct.
//
// We cannot simply use the allg linked list in order to find the M that represents
// the given OS thread and follow its G pointer because on Darwin mach ports are not
// universal, so our port for this thread would not map to the `id` attribute of the M
// structure. Also, when linked against libc, Go prefers the libc version of clone as
// opposed to the runtime version. This has the consequence of not setting M.id for
// any thread, regardless of OS.
//
// In order to get around all this craziness, we read the address of the G structure for
// the current thread from the thread local storage area.
func GetG(thread Thread) (*G, error) {
	if thread.Common().g != nil {
		return thread.Common().g, nil
	}
	if loc, _ := thread.Location(); loc != nil && loc.Fn != nil && loc.Fn.Name == "runtime.clone" {
		// When threads are executing runtime.clone the value of TLS is unreliable.
		return nil, nil
	}
	gaddr, err := getGVariable(thread)
	if err != nil {
		return nil, err
	}

	g, err := gaddr.parseG()
	if err != nil {
		return nil, err
	}
	if g.ID == 0 {
		// The runtime uses a special goroutine with ID == 0 to mark that the
		// current goroutine is executing on the system stack (sometimes also
		// referred to as the g0 stack or scheduler stack, I'm not sure if there's
		// actually any difference between those).
		// For our purposes it's better if we always return the real goroutine
		// since the rest of the code assumes the goroutine ID is univocal.
		// The real 'current goroutine' is stored in g0.m.curg
		mvar, err := g.variable.structMember("m")
		if err != nil {
			return nil, err
		}
		curgvar, err := mvar.structMember("curg")
		if err != nil {
			return nil, err
		}
		g, err = curgvar.parseG()
		if err != nil {
			if _, ok := err.(ErrNoGoroutine); ok {
				err = ErrNoGoroutine{thread.ThreadID()}
			}
			return nil, err
		}
		g.SystemStack = true
	}
	g.Thread = thread
	if loc, err := thread.Location(); err == nil {
		g.CurrentLoc = *loc
	}
	thread.Common().g = g
	return g, nil
}

// ThreadScope returns an EvalScope for this thread.
func ThreadScope(thread Thread) (*EvalScope, error) {
	locations, err := ThreadStacktrace(thread, 1)
	if err != nil {
		return nil, err
	}
	if len(locations) < 1 {
		return nil, errors.New("could not decode first frame")
	}
	return FrameToScope(thread.BinInfo(), thread, nil, locations...), nil
}

// GoroutineScope returns an EvalScope for the goroutine running on this thread.
func GoroutineScope(thread Thread) (*EvalScope, error) {
	locations, err := ThreadStacktrace(thread, 1)
	if err != nil {
		return nil, err
	}
	if len(locations) < 1 {
		return nil, errors.New("could not decode first frame")
	}
	g, err := GetG(thread)
	if err != nil {
		return nil, err
	}
	return FrameToScope(thread.BinInfo(), thread, g, locations...), nil
}

// onNextGoroutine returns true if this thread is on the goroutine requested by the current 'next' command
func onNextGoroutine(thread Thread, breakpoints *BreakpointMap) (bool, error) {
	var bp *Breakpoint
	for i := range breakpoints.M {
		if breakpoints.M[i].Kind != UserBreakpoint && breakpoints.M[i].internalCond != nil {
			bp = breakpoints.M[i]
			break
		}
	}
	if bp == nil {
		return false, nil
	}
	// Internal breakpoint conditions can take multiple different forms:
	// Step into breakpoints:
	//   runtime.curg.goid == X
	// Next or StepOut breakpoints:
	//   runtime.curg.goid == X && runtime.frameoff == Y
	// Breakpoints that can be hit either by stepping on a line in the same
	// function or by returning from the function:
	//   runtime.curg.goid == X && (runtime.frameoff == Y || runtime.frameoff == Z)
	// Here we are only interested in testing the runtime.curg.goid clause.
	w := onNextGoroutineWalker{thread: thread}
	ast.Walk(&w, bp.internalCond)
	return w.ret, w.err
}

type onNextGoroutineWalker struct {
	thread Thread
	ret    bool
	err    error
}

func (w *onNextGoroutineWalker) Visit(n ast.Node) ast.Visitor {
	if binx, isbin := n.(*ast.BinaryExpr); isbin && binx.Op == token.EQL && exprToString(binx.X) == "runtime.curg.goid" {
		w.ret, w.err = evalBreakpointCondition(w.thread, n.(ast.Expr))
		return nil
	}
	return w
}
