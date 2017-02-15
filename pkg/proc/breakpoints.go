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

// ClearBreakpoint clears the specified breakpoint.
func (thread *Thread) ClearBreakpoint(bp *Breakpoint) (*Breakpoint, error) {
	if _, err := thread.writeMemory(uintptr(bp.Addr), bp.OriginalData); err != nil {
		return nil, fmt.Errorf("could not clear breakpoint %s", err)
	}
	return bp, nil
}

// BreakpointExistsError is returned when trying to set a breakpoint at
// an address that already has a breakpoint set for it.
type BreakpointExistsError struct {
	file string
	line int
	addr uint64
}

func (bpe BreakpointExistsError) Error() string {
	return fmt.Sprintf("Breakpoint exists at %s:%d at %x", bpe.file, bpe.line, bpe.addr)
}

// InvalidAddressError represents the result of
// attempting to set a breakpoint at an invalid address.
type InvalidAddressError struct {
	address uint64
}

func (iae InvalidAddressError) Error() string {
	return fmt.Sprintf("Invalid address %#v\n", iae.address)
}

func (dbp *Process) writeSoftwareBreakpoint(thread *Thread, addr uint64) error {
	_, err := thread.writeMemory(uintptr(addr), dbp.bi.arch.BreakpointInstruction())
	return err
}

func (bp *Breakpoint) checkCondition(thread *Thread) (bool, error) {
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
	scope, err := GoroutineScope(thread)
	if err != nil {
		return true, err
	}
	v, err := scope.evalAST(bp.Cond)
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
	addr uint64
}

func (nbp NoBreakpointError) Error() string {
	return fmt.Sprintf("no breakpoint at %#v", nbp.addr)
}
