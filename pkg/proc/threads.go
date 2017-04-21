package proc

import (
	"debug/gosym"
	"encoding/binary"
	"errors"
	"fmt"
	"go/ast"
	"path/filepath"
	"reflect"
	"strings"

	"golang.org/x/debug/dwarf"
)

// IThread represents a thread.
type IThread interface {
	MemoryReadWriter
	Location() (*Location, error)
	// Breakpoint will return the breakpoint that this thread is stopped at or
	// nil if the thread is not stopped at any breakpoint.
	// Active will be true if the thread is stopped at a breakpoint and the
	// breakpoint's condition is met.
	// If there was an error evaluating the breakpoint's condition it will be
	// returned as condErr
	Breakpoint() (breakpoint *Breakpoint, active bool, condErr error)
	ThreadID() int
	Registers(floatingPoint bool) (Registers, error)
	Arch() Arch
	BinInfo() *BinaryInfo
	StepInstruction() error
	// Blocked returns true if the thread is blocked
	Blocked() bool
}

// Location represents the location of a thread.
// Holds information on the current instruction
// address, the source file:line, and the function.
type Location struct {
	PC   uint64
	File string
	Line int
	Fn   *gosym.Func
}

// ThreadBlockedError is returned when the thread
// is blocked in the scheduler.
type ThreadBlockedError struct{}

func (tbe ThreadBlockedError) Error() string {
	return ""
}

// returns topmost frame of g or thread if g is nil
func topframe(g *G, thread IThread) (Stackframe, error) {
	var frames []Stackframe
	var err error

	if g == nil {
		if thread.Blocked() {
			return Stackframe{}, ThreadBlockedError{}
		}
		frames, err = ThreadStacktrace(thread, 0)
	} else {
		frames, err = g.Stacktrace(0)
	}
	if err != nil {
		return Stackframe{}, err
	}
	if len(frames) < 1 {
		return Stackframe{}, errors.New("empty stack trace")
	}
	return frames[0], nil
}

// Set breakpoints at every line, and the return address. Also look for
// a deferred function and set a breakpoint there too.
// If stepInto is true it will also set breakpoints inside all
// functions called on the current source line, for non-absolute CALLs
// a breakpoint of kind StepBreakpoint is set on the CALL instruction,
// Continue will take care of setting a breakpoint to the destination
// once the CALL is reached.
func next(dbp Continuable, stepInto bool) error {
	selg := dbp.SelectedGoroutine()
	curthread := dbp.CurrentThread()
	topframe, err := topframe(selg, curthread)
	if err != nil {
		return err
	}

	success := false
	defer func() {
		if !success {
			dbp.ClearInternalBreakpoints()
		}
	}()

	csource := filepath.Ext(topframe.Current.File) != ".go"
	var thread MemoryReadWriter = curthread
	var regs Registers
	if selg != nil && selg.Thread != nil {
		thread = selg.Thread
		regs, err = selg.Thread.Registers(false)
		if err != nil {
			return err
		}
	}

	text, err := disassemble(thread, regs, dbp.Breakpoints(), dbp.BinInfo(), topframe.FDE.Begin(), topframe.FDE.End())
	if err != nil && stepInto {
		return err
	}

	for i := range text {
		if text[i].Inst == nil {
			fmt.Printf("error at instruction %d\n", i)
		}
	}

	cond := SameGoroutineCondition(selg)

	if stepInto {
		for _, instr := range text {
			if instr.Loc.File != topframe.Current.File || instr.Loc.Line != topframe.Current.Line || !instr.IsCall() {
				continue
			}

			if instr.DestLoc != nil && instr.DestLoc.Fn != nil {
				if err := setStepIntoBreakpoint(dbp, []AsmInstruction{instr}, cond); err != nil {
					return err
				}
			} else {
				// Non-absolute call instruction, set a StepBreakpoint here
				if _, err := dbp.SetBreakpoint(instr.Loc.PC, StepBreakpoint, cond); err != nil {
					if _, ok := err.(BreakpointExistsError); !ok {
						return err
					}
				}
			}
		}
	}

	if !csource {
		deferreturns := []uint64{}

		// Find all runtime.deferreturn locations in the function
		// See documentation of Breakpoint.DeferCond for why this is necessary
		for _, instr := range text {
			if instr.IsCall() && instr.DestLoc != nil && instr.DestLoc.Fn != nil && instr.DestLoc.Fn.Name == "runtime.deferreturn" {
				deferreturns = append(deferreturns, instr.Loc.PC)
			}
		}

		// Set breakpoint on the most recently deferred function (if any)
		var deferpc uint64 = 0
		if selg != nil {
			deferPCEntry := selg.DeferPC()
			if deferPCEntry != 0 {
				_, _, deferfn := dbp.BinInfo().PCToLine(deferPCEntry)
				var err error
				deferpc, err = dbp.FirstPCAfterPrologue(deferfn, false)
				if err != nil {
					return err
				}
			}
		}
		if deferpc != 0 && deferpc != topframe.Current.PC {
			bp, err := dbp.SetBreakpoint(deferpc, NextDeferBreakpoint, cond)
			if err != nil {
				if _, ok := err.(BreakpointExistsError); !ok {
					return err
				}
			}
			if bp != nil {
				bp.DeferReturns = deferreturns
			}
		}
	}

	// Add breakpoints on all the lines in the current function
	pcs, err := dbp.BinInfo().lineInfo.AllPCsBetween(topframe.FDE.Begin(), topframe.FDE.End()-1, topframe.Current.File)
	if err != nil {
		return err
	}

	if !csource {
		var covered bool
		for i := range pcs {
			if topframe.FDE.Cover(pcs[i]) {
				covered = true
				break
			}
		}

		if !covered {
			fn := dbp.BinInfo().goSymTable.PCToFunc(topframe.Ret)
			if selg != nil && fn != nil && fn.Name == "runtime.goexit" {
				return nil
			}
		}
	}

	// Add a breakpoint on the return address for the current frame
	pcs = append(pcs, topframe.Ret)
	success = true
	return setInternalBreakpoints(dbp, topframe.Current.PC, pcs, NextBreakpoint, cond)
}

func setStepIntoBreakpoint(dbp Continuable, text []AsmInstruction, cond ast.Expr) error {
	if len(text) <= 0 {
		return nil
	}

	instr := text[0]

	if instr.DestLoc == nil || instr.DestLoc.Fn == nil {
		return nil
	}

	fn := instr.DestLoc.Fn

	// Ensure PC and Entry match, otherwise StepInto is likely to set
	// its breakpoint before DestLoc.PC and hence run too far ahead.
	// Calls to runtime.duffzero and duffcopy have this problem.
	if fn.Entry != instr.DestLoc.PC {
		return nil
	}

	// Skip unexported runtime functions
	if strings.HasPrefix(fn.Name, "runtime.") && !isExportedRuntime(fn.Name) {
		return nil
	}

	//TODO(aarzilli): if we want to let users hide functions
	// or entire packages from being stepped into with 'step'
	// those extra checks should be done here.

	// Set a breakpoint after the function's prologue
	pc, _ := dbp.FirstPCAfterPrologue(fn, false)
	if _, err := dbp.SetBreakpoint(pc, NextBreakpoint, cond); err != nil {
		if _, ok := err.(BreakpointExistsError); !ok {
			return err
		}
	}

	return nil
}

// setInternalBreakpoints sets a breakpoint to all addresses specified in pcs
// skipping over curpc and curpc-1
func setInternalBreakpoints(dbp Continuable, curpc uint64, pcs []uint64, kind BreakpointKind, cond ast.Expr) error {
	for i := range pcs {
		if pcs[i] == curpc || pcs[i] == curpc-1 {
			continue
		}
		if _, err := dbp.SetBreakpoint(pcs[i], kind, cond); err != nil {
			if _, ok := err.(BreakpointExistsError); !ok {
				dbp.ClearInternalBreakpoints()
				return err
			}
		}
	}
	return nil
}

func getGVariable(thread IThread) (*Variable, error) {
	arch := thread.Arch()
	regs, err := thread.Registers(false)
	if err != nil {
		return nil, err
	}

	if arch.GStructOffset() == 0 {
		// GetG was called through SwitchThread / updateThreadList during initialization
		// thread.dbp.arch isn't setup yet (it needs a current thread to read global variables from)
		return nil, fmt.Errorf("g struct offset not initialized")
	}

	gaddr, hasgaddr := regs.GAddr()
	if !hasgaddr {
		gaddrbs := make([]byte, arch.PtrSize())
		_, err := thread.ReadMemory(gaddrbs, uintptr(regs.TLS()+arch.GStructOffset()))
		if err != nil {
			return nil, err
		}
		gaddr = binary.LittleEndian.Uint64(gaddrbs)
	}

	return newGVariable(thread, uintptr(gaddr), arch.DerefTLS())
}

func newGVariable(thread IThread, gaddr uintptr, deref bool) (*Variable, error) {
	typ, err := thread.BinInfo().findType("runtime.g")
	if err != nil {
		return nil, err
	}

	name := ""

	if deref {
		typ = &dwarf.PtrType{dwarf.CommonType{int64(thread.Arch().PtrSize()), "", reflect.Ptr, 0}, typ}
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
func GetG(thread IThread) (g *G, err error) {
	gaddr, err := getGVariable(thread)
	if err != nil {
		return nil, err
	}

	g, err = gaddr.parseG()
	if err == nil {
		g.Thread = thread
		if loc, err := thread.Location(); err == nil {
			g.CurrentLoc = *loc
		}
	}
	return
}

// ThreadScope returns an EvalScope for this thread.
func ThreadScope(thread IThread) (*EvalScope, error) {
	locations, err := ThreadStacktrace(thread, 0)
	if err != nil {
		return nil, err
	}
	if len(locations) < 1 {
		return nil, errors.New("could not decode first frame")
	}
	return &EvalScope{locations[0].Current.PC, locations[0].CFA, thread, nil, thread.BinInfo()}, nil
}

// GoroutineScope returns an EvalScope for the goroutine running on this thread.
func GoroutineScope(thread IThread) (*EvalScope, error) {
	locations, err := ThreadStacktrace(thread, 0)
	if err != nil {
		return nil, err
	}
	if len(locations) < 1 {
		return nil, errors.New("could not decode first frame")
	}
	gvar, err := getGVariable(thread)
	if err != nil {
		return nil, err
	}
	return &EvalScope{locations[0].Current.PC, locations[0].CFA, thread, gvar, thread.BinInfo()}, nil
}

func onRuntimeBreakpoint(thread IThread) bool {
	loc, err := thread.Location()
	if err != nil {
		return false
	}
	return loc.Fn != nil && loc.Fn.Name == "runtime.breakpoint"
}

// onNextGorutine returns true if this thread is on the goroutine requested by the current 'next' command
func onNextGoroutine(thread IThread, breakpoints map[uint64]*Breakpoint) (bool, error) {
	var bp *Breakpoint
	for i := range breakpoints {
		if breakpoints[i].Internal() {
			bp = breakpoints[i]
			break
		}
	}
	if bp == nil {
		return false, nil
	}
	if bp.Kind == NextDeferBreakpoint {
		// we just want to check the condition on the goroutine id here
		bp.Kind = NextBreakpoint
		defer func() {
			bp.Kind = NextDeferBreakpoint
		}()
	}
	return bp.CheckCondition(thread)
}
