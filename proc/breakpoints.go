package proc

import (
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"log"
	"reflect"
)

var (
	breakpointIDCounter     int
	tempBreakpointIDCounter int

	breakpointConditionNotMetError = errors.New("breakpoint condition not met")
)

// Breakpoint represents a breakpoint. Stores information on the break
// point including the byte of data that originally was stored at that
// address.
type Breakpoint struct {
	// File & line information for printing.
	FunctionName string
	File         string
	Line         int

	Addr         uint64 // Address breakpoint is set for.
	OriginalData []byte // If software breakpoint, the data we replace with breakpoint instruction.
	Name         string // User defined name of the breakpoint
	ID           int    // Monotonically increasing ID.
	Temp         bool   // Whether this is a temp breakpoint (for next'ing).

	// Breakpoint information
	Tracepoint    bool     // Tracepoint flag
	Goroutine     bool     // Retrieve goroutine information
	Stacktrace    int      // Number of stack frames to retrieve
	Variables     []string // Variables to evaluate
	LoadArgs      *LoadConfig
	LoadLocals    *LoadConfig
	HitCount      map[int]uint64 // Number of times a breakpoint has been reached in a certain goroutine
	TotalHitCount uint64         // Number of times a breakpoint has been reached

	Cond ast.Expr // When Cond is not nil the breakpoint will be triggered only if evaluating Cond returns true
}

func (bp *Breakpoint) String() string {
	return fmt.Sprintf("Breakpoint %d at %#v %s:%d (%d)", bp.ID, bp.Addr, bp.File, bp.Line, bp.TotalHitCount)
}

// Clear this breakpoint appropriately depending on whether it is a
// hardware or software breakpoint.
func (bp *Breakpoint) Clear(thread *Thread) (*Breakpoint, error) {
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

func createAndWriteBreakpoint(mem memoryReadWriter, loc *Location, temp bool, instr []byte) (*Breakpoint, error) {
	log.Printf("setting breakpoint at %s temp=%v\n", loc.String(), temp)

	newBreakpoint := &Breakpoint{
		FunctionName: loc.Fn.Name,
		File:         loc.File,
		Line:         loc.Line,
		Addr:         loc.PC,
		Temp:         temp,
		Cond:         nil,
		HitCount:     map[int]uint64{},
	}

	if temp {
		tempBreakpointIDCounter++
		newBreakpoint.ID = tempBreakpointIDCounter
	} else {
		breakpointIDCounter++
		newBreakpoint.ID = breakpointIDCounter
	}

	originalData, err := mem.readMemory(uintptr(loc.PC), len(instr))
	if err != nil {
		return nil, err
	}
	if err := writeSoftwareBreakpoint(mem, loc.PC, instr); err != nil {
		return nil, err
	}
	newBreakpoint.OriginalData = originalData
	return newBreakpoint, nil
}

func writeSoftwareBreakpoint(mem memoryReadWriter, addr uint64, instr []byte) error {
	_, err := mem.writeMemory(uintptr(addr), instr)
	return err
}

func (bp *Breakpoint) checkCondition(thread *Thread) (bool, error) {
	if bp.Cond == nil {
		return true, nil
	}
	scope, err := thread.Scope()
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

// NoBreakpointError is returned when trying to
// clear a breakpoint that does not exist.
type NoBreakpointError struct {
	addr uint64
}

func (nbp NoBreakpointError) Error() string {
	return fmt.Sprintf("no breakpoint at %#v", nbp.addr)
}
