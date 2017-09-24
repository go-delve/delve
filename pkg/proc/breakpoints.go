package proc

import (
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"reflect"
)

// Breakpoint represents a breakpoint. Stores information on the break
// point including the byte of data that originally was stored at that
// address.
type Breakpoint struct {
	// File & line information for printing.
	FunctionName string
	File         string
	Line         int

	Addr         uint64         // Address breakpoint is set for.
	OriginalData []byte         // If software breakpoint, the data we replace with breakpoint instruction.
	Name         string         // User defined name of the breakpoint
	ID           int            // Monotonically increasing ID.
	Kind         BreakpointKind // Whether this is an internal breakpoint (for next'ing or stepping).

	// Breakpoint information
	Tracepoint    bool     // Tracepoint flag
	Goroutine     bool     // Retrieve goroutine information
	Stacktrace    int      // Number of stack frames to retrieve
	Variables     []string // Variables to evaluate
	LoadArgs      *LoadConfig
	LoadLocals    *LoadConfig
	HitCount      map[int]uint64 // Number of times a breakpoint has been reached in a certain goroutine
	TotalHitCount uint64         // Number of times a breakpoint has been reached

	// DeferReturns: when kind == NextDeferBreakpoint this breakpoint
	// will also check if the caller is runtime.gopanic or if the return
	// address is in the DeferReturns array.
	// Next uses NextDeferBreakpoints for the breakpoint it sets on the
	// deferred function, DeferReturns is populated with the
	// addresses of calls to runtime.deferreturn in the current
	// function. This insures that the breakpoint on the deferred
	// function only triggers on panic or on the defer call to
	// the function, not when the function is called directly
	DeferReturns []uint64
	// Cond: if not nil the breakpoint will be triggered only if evaluating Cond returns true
	Cond ast.Expr
}

// Breakpoint Kind determines the behavior of delve when the
// breakpoint is reached.
type BreakpointKind int

const (
	// UserBreakpoint is a user set breakpoint
	UserBreakpoint BreakpointKind = iota
	// NextBreakpoint is a breakpoint set by Next, Continue
	// will stop on it and delete it
	NextBreakpoint
	// NextDeferBreakpoint is a breakpoint set by Next on the
	// first deferred function. In addition to checking their condition
	// breakpoints of this kind will also check that the function has been
	// called by runtime.gopanic or through runtime.deferreturn.
	NextDeferBreakpoint
	// StepBreakpoint is a breakpoint set by Step on a CALL instruction,
	// Continue will set a new breakpoint (of NextBreakpoint kind) on the
	// destination of CALL, delete this breakpoint and then continue again
	StepBreakpoint
)

func (bp *Breakpoint) String() string {
	return fmt.Sprintf("Breakpoint %d at %#v %s:%d (%d)", bp.ID, bp.Addr, bp.File, bp.Line, bp.TotalHitCount)
}

// BreakpointExistsError is returned when trying to set a breakpoint at
// an address that already has a breakpoint set for it.
type BreakpointExistsError struct {
	File string
	Line int
	Addr uint64
}

func (bpe BreakpointExistsError) Error() string {
	return fmt.Sprintf("Breakpoint exists at %s:%d at %x", bpe.File, bpe.Line, bpe.Addr)
}

// InvalidAddressError represents the result of
// attempting to set a breakpoint at an invalid address.
type InvalidAddressError struct {
	Address uint64
}

func (iae InvalidAddressError) Error() string {
	return fmt.Sprintf("Invalid address %#v\n", iae.Address)
}

// CheckCondition evaluates bp's condition on thread.
func (bp *Breakpoint) CheckCondition(thread Thread) (bool, error) {
	if bp.Cond == nil {
		return true, nil
	}
	if bp.Kind == NextDeferBreakpoint {
		frames, err := ThreadStacktrace(thread, 2)
		if err == nil {
			ispanic := len(frames) >= 3 && frames[2].Current.Fn != nil && frames[2].Current.Fn.Name == "runtime.gopanic"
			isdeferreturn := false
			if len(frames) >= 1 {
				for _, pc := range bp.DeferReturns {
					if frames[0].Ret == pc {
						isdeferreturn = true
						break
					}
				}
			}
			if !ispanic && !isdeferreturn {
				return false, nil
			}
		}
	}
	return evalBreakpointCondition(thread, bp.Cond)
}

func evalBreakpointCondition(thread Thread, cond ast.Expr) (bool, error) {
	scope, err := GoroutineScope(thread)
	if err != nil {
		return true, err
	}
	v, err := scope.evalAST(cond)
	if err != nil {
		return true, fmt.Errorf("error evaluating expression: %v", err)
	}
	if v.Unreadable != nil {
		return true, fmt.Errorf("condition expression unreadable: %v", v.Unreadable)
	}
	if v.Kind != reflect.Bool {
		return true, errors.New("condition expression not boolean")
	}
	return constant.BoolVal(v.Value), nil
}

// Internal returns true for breakpoints not set directly by the user.
func (bp *Breakpoint) Internal() bool {
	return bp.Kind != UserBreakpoint
}

// NoBreakpointError is returned when trying to
// clear a breakpoint that does not exist.
type NoBreakpointError struct {
	Addr uint64
}

func (nbp NoBreakpointError) Error() string {
	return fmt.Sprintf("no breakpoint at %#v", nbp.Addr)
}

type BreakpointMap struct {
	M map[uint64]*Breakpoint

	breakpointIDCounter         int
	internalBreakpointIDCounter int
}

func NewBreakpointMap() BreakpointMap {
	return BreakpointMap{
		M: make(map[uint64]*Breakpoint),
	}
}

func (bpmap *BreakpointMap) ResetBreakpointIDCounter() {
	bpmap.breakpointIDCounter = 0
}

type writeBreakpointFn func(addr uint64) (file string, line int, fn *Function, originalData []byte, err error)
type clearBreakpointFn func(*Breakpoint) error

// Set creates a breakpoint at addr calling writeBreakpoint. Do not call this
// function, call proc.Process.SetBreakpoint instead, this function exists
// to implement proc.Process.SetBreakpoint.
func (bpmap *BreakpointMap) Set(addr uint64, kind BreakpointKind, cond ast.Expr, writeBreakpoint writeBreakpointFn) (*Breakpoint, error) {
	if bp, ok := bpmap.M[addr]; ok {
		return bp, BreakpointExistsError{bp.File, bp.Line, bp.Addr}
	}

	f, l, fn, originalData, err := writeBreakpoint(addr)
	if err != nil {
		return nil, err
	}

	newBreakpoint := &Breakpoint{
		FunctionName: fn.Name,
		File:         f,
		Line:         l,
		Addr:         addr,
		Kind:         kind,
		Cond:         cond,
		OriginalData: originalData,
		HitCount:     map[int]uint64{},
	}

	if kind != UserBreakpoint {
		bpmap.internalBreakpointIDCounter++
		newBreakpoint.ID = bpmap.internalBreakpointIDCounter
	} else {
		bpmap.breakpointIDCounter++
		newBreakpoint.ID = bpmap.breakpointIDCounter
	}

	bpmap.M[addr] = newBreakpoint

	return newBreakpoint, nil
}

// SetWithID creates a breakpoint at addr, with the specified ID.
func (bpmap *BreakpointMap) SetWithID(id int, addr uint64, writeBreakpoint writeBreakpointFn) (*Breakpoint, error) {
	bp, err := bpmap.Set(addr, UserBreakpoint, nil, writeBreakpoint)
	if err == nil {
		bp.ID = id
		bpmap.breakpointIDCounter--
	}
	return bp, err
}

// Clear clears the breakpoint at addr.
// Do not call this function call proc.Process.ClearBreakpoint instead.
func (bpmap *BreakpointMap) Clear(addr uint64, clearBreakpoint clearBreakpointFn) (*Breakpoint, error) {
	bp, ok := bpmap.M[addr]
	if !ok {
		return nil, NoBreakpointError{Addr: addr}
	}

	if err := clearBreakpoint(bp); err != nil {
		return nil, err
	}

	delete(bpmap.M, addr)

	return bp, nil
}

// ClearInternalBreakpoints removes all internal breakpoints from the map,
// calling clearBreakpoint on each one.
// Do not call this function, call proc.Process.ClearInternalBreakpoints
// instead, this function is used to implement that.
func (bpmap *BreakpointMap) ClearInternalBreakpoints(clearBreakpoint clearBreakpointFn) error {
	for addr, bp := range bpmap.M {
		if !bp.Internal() {
			continue
		}
		if err := clearBreakpoint(bp); err != nil {
			return err
		}
		delete(bpmap.M, addr)
	}
	return nil
}
