package proc

import (
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"reflect"
)

const (
	// UnrecoveredPanic is the name given to the unrecovered panic breakpoint.
	UnrecoveredPanic = "unrecovered-panic"

	// FatalThrow is the name given to the breakpoint triggered when the target
	// process dies because of a fatal runtime error.
	FatalThrow = "runtime-fatal-throw"

	unrecoveredPanicID = -1
	fatalThrowID       = -2
)

// Breakpoint represents a physical breakpoint. Stores information on the break
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
	LogicalID    int    // ID of the logical breakpoint that owns this physical breakpoint

	WatchExpr    string
	WatchType    WatchType
	HWBreakIndex uint8 // hardware breakpoint index

	// Kind describes whether this is an internal breakpoint (for next'ing or
	// stepping).
	// A single breakpoint can be both a UserBreakpoint and some kind of
	// internal breakpoint, but it can not be two different kinds of internal
	// breakpoint.
	Kind BreakpointKind

	// Breakpoint information
	Tracepoint    bool // Tracepoint flag
	TraceReturn   bool
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
	// function. This ensures that the breakpoint on the deferred
	// function only triggers on panic or on the defer call to
	// the function, not when the function is called directly
	DeferReturns []uint64
	// Cond: if not nil the breakpoint will be triggered only if evaluating Cond returns true
	Cond ast.Expr
	// internalCond is the same as Cond but used for the condition of internal breakpoints
	internalCond ast.Expr
	// HitCond: if not nil the breakpoint will be triggered only if the evaluated HitCond returns
	// true with the TotalHitCount.
	HitCond *struct {
		Op  token.Token
		Val int
	}

	// ReturnInfo describes how to collect return variables when this
	// breakpoint is hit as a return breakpoint.
	returnInfo *returnBreakpointInfo
}

// BreakpointKind determines the behavior of delve when the
// breakpoint is reached.
type BreakpointKind uint16

const (
	// UserBreakpoint is a user set breakpoint
	UserBreakpoint BreakpointKind = (1 << iota)
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

// WatchType is the watchpoint type
type WatchType uint8

const (
	WatchRead WatchType = 1 << iota
	WatchWrite
)

// Read returns true if the hardware breakpoint should trigger on memory reads.
func (wtype WatchType) Read() bool {
	return wtype&WatchRead != 0
}

// Write returns true if the hardware breakpoint should trigger on memory writes.
func (wtype WatchType) Write() bool {
	return wtype&WatchWrite != 0
}

// Size returns the size in bytes of the hardware breakpoint.
func (wtype WatchType) Size() int {
	return int(wtype >> 4)
}

// withSize returns a new HWBreakType with the size set to the specified value
func (wtype WatchType) withSize(sz uint8) WatchType {
	return WatchType((sz << 4) | uint8(wtype&0xf))
}

var ErrHWBreakUnsupported = errors.New("hardware breakpoints not implemented")

func (bp *Breakpoint) String() string {
	return fmt.Sprintf("Breakpoint %d at %#v %s:%d (%d)", bp.LogicalID, bp.Addr, bp.File, bp.Line, bp.TotalHitCount)
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

type returnBreakpointInfo struct {
	retFrameCond ast.Expr
	fn           *Function
	frameOffset  int64
	spOffset     int64
}

// CheckCondition evaluates bp's condition on thread.
func (bp *Breakpoint) CheckCondition(thread Thread) BreakpointState {
	bpstate := BreakpointState{Breakpoint: bp, Active: false, Internal: false, CondError: nil}
	bpstate.checkCond(thread)
	// Update the breakpoint hit counts.
	if bpstate.Breakpoint != nil && bpstate.Active {
		if g, err := GetG(thread); err == nil {
			bpstate.HitCount[g.ID]++
		}
		bpstate.TotalHitCount++
	}
	bpstate.checkHitCond(thread)
	return bpstate
}

func (bpstate *BreakpointState) checkCond(thread Thread) {
	if bpstate.Cond == nil && bpstate.internalCond == nil {
		bpstate.Active = true
		bpstate.Internal = bpstate.IsInternal()
		return
	}
	nextDeferOk := true
	if bpstate.Kind&NextDeferBreakpoint != 0 {
		var err error
		frames, err := ThreadStacktrace(thread, 2)
		if err == nil {
			nextDeferOk = isPanicCall(frames)
			if !nextDeferOk {
				nextDeferOk, _ = isDeferReturnCall(frames, bpstate.DeferReturns)
			}
		}
	}
	if bpstate.IsInternal() {
		// Check internalCondition if this is also an internal breakpoint
		bpstate.Active, bpstate.CondError = evalBreakpointCondition(thread, bpstate.internalCond)
		bpstate.Active = bpstate.Active && nextDeferOk
		if bpstate.Active || bpstate.CondError != nil {
			bpstate.Internal = true
			return
		}
	}
	if bpstate.IsUser() {
		// Check normal condition if this is also a user breakpoint
		bpstate.Active, bpstate.CondError = evalBreakpointCondition(thread, bpstate.Cond)
	}
}

// checkHitCond evaluates bp's hit condition on thread.
func (bpstate *BreakpointState) checkHitCond(thread Thread) {
	if bpstate.HitCond == nil || !bpstate.Active || bpstate.Internal {
		return
	}
	// Evaluate the breakpoint condition.
	switch bpstate.HitCond.Op {
	case token.EQL:
		bpstate.Active = int(bpstate.TotalHitCount) == bpstate.HitCond.Val
	case token.NEQ:
		bpstate.Active = int(bpstate.TotalHitCount) != bpstate.HitCond.Val
	case token.GTR:
		bpstate.Active = int(bpstate.TotalHitCount) > bpstate.HitCond.Val
	case token.LSS:
		bpstate.Active = int(bpstate.TotalHitCount) < bpstate.HitCond.Val
	case token.GEQ:
		bpstate.Active = int(bpstate.TotalHitCount) >= bpstate.HitCond.Val
	case token.LEQ:
		bpstate.Active = int(bpstate.TotalHitCount) <= bpstate.HitCond.Val
	case token.REM:
		bpstate.Active = int(bpstate.TotalHitCount)%bpstate.HitCond.Val == 0
	}
}

func isPanicCall(frames []Stackframe) bool {
	return len(frames) >= 3 && frames[2].Current.Fn != nil && frames[2].Current.Fn.Name == "runtime.gopanic"
}

func isDeferReturnCall(frames []Stackframe, deferReturns []uint64) (bool, uint64) {
	if len(frames) >= 1 {
		for _, pc := range deferReturns {
			if frames[0].Ret == pc {
				return true, pc
			}
		}
	}
	return false, 0
}

// IsInternal returns true if bp is an internal breakpoint.
// User-set breakpoints can overlap with internal breakpoints, in that case
// both IsUser and IsInternal will be true.
func (bp *Breakpoint) IsInternal() bool {
	return bp.Kind != UserBreakpoint
}

// IsUser returns true if bp is a user-set breakpoint.
// User-set breakpoints can overlap with internal breakpoints, in that case
// both IsUser and IsInternal will be true.
func (bp *Breakpoint) IsUser() bool {
	return bp.Kind&UserBreakpoint != 0
}

func evalBreakpointCondition(thread Thread, cond ast.Expr) (bool, error) {
	if cond == nil {
		return true, nil
	}
	scope, err := GoroutineScope(nil, thread)
	if err != nil {
		scope, err = ThreadScope(nil, thread)
		if err != nil {
			return true, err
		}
	}
	v, err := scope.evalAST(cond)
	if err != nil {
		return true, fmt.Errorf("error evaluating expression: %v", err)
	}
	if v.Kind != reflect.Bool {
		return true, errors.New("condition expression not boolean")
	}
	v.loadValue(loadFullValue)
	if v.Unreadable != nil {
		return true, fmt.Errorf("condition expression unreadable: %v", v.Unreadable)
	}
	return constant.BoolVal(v.Value), nil
}

// NoBreakpointError is returned when trying to
// clear a breakpoint that does not exist.
type NoBreakpointError struct {
	Addr uint64
}

func (nbp NoBreakpointError) Error() string {
	return fmt.Sprintf("no breakpoint at %#v", nbp.Addr)
}

// BreakpointMap represents an (address, breakpoint) map.
type BreakpointMap struct {
	M map[uint64]*Breakpoint

	breakpointIDCounter         int
	internalBreakpointIDCounter int
}

// NewBreakpointMap creates a new BreakpointMap.
func NewBreakpointMap() BreakpointMap {
	return BreakpointMap{
		M: make(map[uint64]*Breakpoint),
	}
}

// SetBreakpoint sets a breakpoint at addr, and stores it in the process wide
// break point table.
func (t *Target) SetBreakpoint(addr uint64, kind BreakpointKind, cond ast.Expr) (*Breakpoint, error) {
	return t.setBreakpointInternal(addr, kind, 0, cond)
}

// SetWatchpoint sets a data breakpoint at addr and stores it in the
// process wide break point table.
func (t *Target) SetWatchpoint(scope *EvalScope, expr string, wtype WatchType, cond ast.Expr) (*Breakpoint, error) {
	if (wtype&WatchWrite == 0) && (wtype&WatchRead == 0) {
		return nil, errors.New("at least one of read and write must be set for watchpoint")
	}

	n, err := parser.ParseExpr(expr)
	if err != nil {
		return nil, err
	}
	xv, err := scope.evalAST(n)
	if err != nil {
		return nil, err
	}
	if xv.Addr == 0 || xv.Flags&VariableFakeAddress != 0 || xv.DwarfType == nil {
		return nil, fmt.Errorf("can not watch %q", expr)
	}
	if xv.Unreadable != nil {
		return nil, fmt.Errorf("expression %q is unreadable: %v", expr, xv.Unreadable)
	}
	if xv.Kind == reflect.UnsafePointer || xv.Kind == reflect.Invalid {
		return nil, fmt.Errorf("can not watch variable of type %s", xv.Kind.String())
	}
	sz := xv.DwarfType.Size()
	if sz <= 0 || sz > int64(t.BinInfo().Arch.PtrSize()) {
		//TODO(aarzilli): it is reasonable to expect to be able to watch string
		//and interface variables and we could support it by watching certain
		//member fields here.
		return nil, fmt.Errorf("can not watch variable of type %s", xv.DwarfType.String())
	}
	if xv.Addr >= scope.g.stack.lo && xv.Addr < scope.g.stack.hi {
		//TODO(aarzilli): support watching stack variables
		return nil, errors.New("can not watch stack allocated variable")
	}

	bp, err := t.setBreakpointInternal(xv.Addr, UserBreakpoint, wtype.withSize(uint8(sz)), cond)
	if bp != nil {
		bp.WatchExpr = expr
	}
	return bp, err
}

func (t *Target) setBreakpointInternal(addr uint64, kind BreakpointKind, wtype WatchType, cond ast.Expr) (*Breakpoint, error) {
	if valid, err := t.Valid(); !valid {
		return nil, err
	}
	bpmap := t.Breakpoints()
	if bp, ok := bpmap.M[addr]; ok {
		// We can overlap one internal breakpoint with one user breakpoint, we
		// need to support this otherwise a conditional breakpoint can mask a
		// breakpoint set by next or step.
		if (kind != UserBreakpoint && bp.Kind != UserBreakpoint) || (kind == UserBreakpoint && bp.IsUser()) {
			return bp, BreakpointExistsError{bp.File, bp.Line, bp.Addr}
		}
		bp.Kind |= kind
		if kind != UserBreakpoint {
			bp.internalCond = cond
		} else {
			bp.Cond = cond
		}
		return bp, nil
	}

	f, l, fn := t.BinInfo().PCToLine(uint64(addr))

	fnName := ""
	if fn != nil {
		fnName = fn.Name
	}

	hwidx := uint8(0)
	if wtype != 0 {
		m := make(map[uint8]bool)
		for _, bp := range bpmap.M {
			if bp.WatchType != 0 {
				m[bp.HWBreakIndex] = true
			}
		}
		for hwidx = 0; true; hwidx++ {
			if !m[hwidx] {
				break
			}
		}
	}

	newBreakpoint := &Breakpoint{
		FunctionName: fnName,
		WatchType:    wtype,
		HWBreakIndex: hwidx,
		File:         f,
		Line:         l,
		Addr:         addr,
		Kind:         kind,
		HitCount:     map[int]uint64{},
	}

	err := t.proc.WriteBreakpoint(newBreakpoint)
	if err != nil {
		return nil, err
	}

	if kind != UserBreakpoint {
		bpmap.internalBreakpointIDCounter++
		newBreakpoint.LogicalID = bpmap.internalBreakpointIDCounter
		newBreakpoint.internalCond = cond
	} else {
		bpmap.breakpointIDCounter++
		newBreakpoint.LogicalID = bpmap.breakpointIDCounter
		newBreakpoint.Cond = cond
	}

	bpmap.M[addr] = newBreakpoint

	return newBreakpoint, nil
}

// SetBreakpointWithID creates a breakpoint at addr, with the specified logical ID.
func (t *Target) SetBreakpointWithID(id int, addr uint64) (*Breakpoint, error) {
	bpmap := t.Breakpoints()
	bp, err := t.SetBreakpoint(addr, UserBreakpoint, nil)
	if err == nil {
		bp.LogicalID = id
		bpmap.breakpointIDCounter--
	}
	return bp, err
}

// ClearBreakpoint clears the breakpoint at addr.
func (t *Target) ClearBreakpoint(addr uint64) (*Breakpoint, error) {
	if valid, err := t.Valid(); !valid {
		return nil, err
	}
	bpmap := t.Breakpoints()
	bp, ok := bpmap.M[addr]
	if !ok {
		return nil, NoBreakpointError{Addr: addr}
	}

	bp.Kind &= ^UserBreakpoint
	bp.Cond = nil
	if bp.Kind != 0 {
		return bp, nil
	}

	if err := t.proc.EraseBreakpoint(bp); err != nil {
		return nil, err
	}

	delete(bpmap.M, addr)

	return bp, nil
}

// ClearInternalBreakpoints removes all internal breakpoints from the map,
// calling clearBreakpoint on each one.
func (t *Target) ClearInternalBreakpoints() error {
	bpmap := t.Breakpoints()
	threads := t.ThreadList()
	for addr, bp := range bpmap.M {
		bp.Kind = bp.Kind & UserBreakpoint
		bp.internalCond = nil
		bp.returnInfo = nil
		if bp.Kind != 0 {
			continue
		}
		if err := t.proc.EraseBreakpoint(bp); err != nil {
			return err
		}
		for _, thread := range threads {
			if thread.Breakpoint().Breakpoint == bp {
				thread.Breakpoint().Clear()
			}
		}
		delete(bpmap.M, addr)
	}
	return nil
}

// HasInternalBreakpoints returns true if bpmap has at least one internal
// breakpoint set.
func (bpmap *BreakpointMap) HasInternalBreakpoints() bool {
	for _, bp := range bpmap.M {
		if bp.IsInternal() {
			return true
		}
	}
	return false
}

// HasHWBreakpoints returns true if there are hardware breakpoints.
func (bpmap *BreakpointMap) HasHWBreakpoints() bool {
	for _, bp := range bpmap.M {
		if bp.WatchType != 0 {
			return true
		}
	}
	return false
}

// BreakpointState describes the state of a breakpoint in a thread.
type BreakpointState struct {
	*Breakpoint
	// Active is true if the breakpoint condition was met.
	Active bool
	// Internal is true if the breakpoint was matched as an internal
	// breakpoint.
	Internal bool
	// CondError contains any error encountered while evaluating the
	// breakpoint's condition.
	CondError error
}

// Clear zeros the struct.
func (bpstate *BreakpointState) Clear() {
	bpstate.Breakpoint = nil
	bpstate.Active = false
	bpstate.Internal = false
	bpstate.CondError = nil
}

func (bpstate *BreakpointState) String() string {
	s := bpstate.Breakpoint.String()
	if bpstate.Active {
		s += " active"
	}
	if bpstate.Internal {
		s += " internal"
	}
	return s
}

func configureReturnBreakpoint(bi *BinaryInfo, bp *Breakpoint, topframe *Stackframe, retFrameCond ast.Expr) {
	if topframe.Current.Fn == nil {
		return
	}
	bp.returnInfo = &returnBreakpointInfo{
		retFrameCond: retFrameCond,
		fn:           topframe.Current.Fn,
		frameOffset:  topframe.FrameOffset(),
		spOffset:     topframe.FrameOffset() - int64(bi.Arch.PtrSize()), // must be the value that SP had at the entry point of the function
	}
}

func (rbpi *returnBreakpointInfo) Collect(t *Target, thread Thread) []*Variable {
	if rbpi == nil {
		return nil
	}

	g, err := GetG(thread)
	if err != nil {
		return returnInfoError("could not get g", err, thread.ProcessMemory())
	}
	scope, err := GoroutineScope(t, thread)
	if err != nil {
		return returnInfoError("could not get scope", err, thread.ProcessMemory())
	}
	v, err := scope.evalAST(rbpi.retFrameCond)
	if err != nil || v.Unreadable != nil || v.Kind != reflect.Bool {
		// This condition was evaluated as part of the breakpoint condition
		// evaluation, if the errors happen they will be reported as part of the
		// condition errors.
		return nil
	}
	if !constant.BoolVal(v.Value) {
		// Breakpoint not hit as a return breakpoint.
		return nil
	}

	oldFrameOffset := rbpi.frameOffset + int64(g.stack.hi)
	oldSP := uint64(rbpi.spOffset + int64(g.stack.hi))
	err = fakeFunctionEntryScope(scope, rbpi.fn, oldFrameOffset, oldSP)
	if err != nil {
		return returnInfoError("could not read function entry", err, thread.ProcessMemory())
	}

	vars, err := scope.Locals()
	if err != nil {
		return returnInfoError("could not evaluate return variables", err, thread.ProcessMemory())
	}
	vars = filterVariables(vars, func(v *Variable) bool {
		return (v.Flags & VariableReturnArgument) != 0
	})

	return vars
}

func returnInfoError(descr string, err error, mem MemoryReadWriter) []*Variable {
	v := newConstant(constant.MakeString(fmt.Sprintf("%s: %v", descr, err.Error())), mem)
	v.Name = "return value read error"
	return []*Variable{v}
}
