package proc

import (
	"debug/gosym"
	"encoding/binary"
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/derekparker/delve/dwarf/debug/dwarf"
	"github.com/derekparker/delve/dwarf/frame"
	"github.com/derekparker/delve/dwarf/line"
	"github.com/derekparker/delve/dwarf/reader"
)

// Process represents all of the information the debugger
// is holding onto regarding the process we are debugging.
type Process struct {
	Pid     int         // Process Pid
	Process *os.Process // Pointer to process struct for the actual process we are debugging

	// Breakpoint table, holds information on breakpoints.
	// Maps instruction address to Breakpoint struct.
	Breakpoints map[uint64]*Breakpoint

	// List of threads mapped as such: pid -> *Thread
	Threads map[int]*Thread

	// Active thread
	CurrentThread *Thread

	// Goroutine that will be used by default to set breakpoint, eval variables, etc...
	// Normally SelectedGoroutine is CurrentThread.GetG, it will not be only if SwitchGoroutine is called with a goroutine that isn't attached to a thread
	SelectedGoroutine *G

	// Maps package names to package paths, needed to lookup types inside DWARF info
	packageMap map[string]string

	allGCache               []*G
	dwarf                   *dwarf.Data
	goSymTable              *gosym.Table
	frameEntries            frame.FrameDescriptionEntries
	lineInfo                line.DebugLines
	os                      *OSProcessDetails
	arch                    Arch
	breakpointIDCounter     int
	tempBreakpointIDCounter int
	firstStart              bool
	halt                    bool
	exited                  bool
	ptraceChan              chan func()
	ptraceDoneChan          chan interface{}
	types                   map[string]dwarf.Offset

	loadModuleDataOnce sync.Once
	moduleData         []moduleData
}

// New returns an initialized Process struct. Before returning,
// it will also launch a goroutine in order to handle ptrace(2)
// functions. For more information, see the documentation on
// `handlePtraceFuncs`.
func New(pid int) *Process {
	dbp := &Process{
		Pid:            pid,
		Threads:        make(map[int]*Thread),
		Breakpoints:    make(map[uint64]*Breakpoint),
		firstStart:     true,
		os:             new(OSProcessDetails),
		ptraceChan:     make(chan func()),
		ptraceDoneChan: make(chan interface{}),
	}
	go dbp.handlePtraceFuncs()
	return dbp
}

// ProcessExitedError indicates that the process has exited and contains both
// process id and exit status.
type ProcessExitedError struct {
	Pid    int
	Status int
}

func (pe ProcessExitedError) Error() string {
	return fmt.Sprintf("Process %d has exited with status %d", pe.Pid, pe.Status)
}

// Detach from the process being debugged, optionally killing it.
func (dbp *Process) Detach(kill bool) (err error) {
	if dbp.Running() {
		if err = dbp.Halt(); err != nil {
			return
		}
	}
	if !kill {
		// Clean up any breakpoints we've set.
		for _, bp := range dbp.Breakpoints {
			if bp != nil {
				_, err := dbp.ClearBreakpoint(bp.Addr)
				if err != nil {
					return err
				}
			}
		}
	}
	dbp.execPtraceFunc(func() {
		err = PtraceDetach(dbp.Pid, 0)
		if err != nil {
			return
		}
		if kill {
			err = killProcess(dbp.Pid)
		}
	})
	return
}

// Exited returns whether the debugged
// process has exited.
func (dbp *Process) Exited() bool {
	return dbp.exited
}

// Running returns whether the debugged
// process is currently executing.
func (dbp *Process) Running() bool {
	for _, th := range dbp.Threads {
		if th.running {
			return true
		}
	}
	return false
}

// LoadInformation finds the executable and then uses it
// to parse the following information:
// * Dwarf .debug_frame section
// * Dwarf .debug_line section
// * Go symbol table.
func (dbp *Process) LoadInformation(path string) error {
	var wg sync.WaitGroup

	exe, err := dbp.findExecutable(path)
	if err != nil {
		return err
	}

	wg.Add(5)
	go dbp.loadProcessInformation(&wg)
	go dbp.parseDebugFrame(exe, &wg)
	go dbp.obtainGoSymbols(exe, &wg)
	go dbp.parseDebugLineInfo(exe, &wg)
	go dbp.loadTypeMap(&wg)
	wg.Wait()

	return nil
}

// FindFileLocation returns the PC for a given file:line.
// Assumes that `file` is normailzed to lower case and '/' on Windows.
func (dbp *Process) FindFileLocation(fileName string, lineno int) (uint64, error) {
	pc, fn, err := dbp.goSymTable.LineToPC(fileName, lineno)
	if err != nil {
		return 0, err
	}
	if fn.Entry == pc {
		pc, _ = dbp.FirstPCAfterPrologue(fn, true)
	}
	return pc, nil
}

// FindFunctionLocation finds address of a function's line
// If firstLine == true is passed FindFunctionLocation will attempt to find the first line of the function
// If lineOffset is passed FindFunctionLocation will return the address of that line
// Pass lineOffset == 0 and firstLine == false if you want the address for the function's entry point
// Note that setting breakpoints at that address will cause surprising behavior:
// https://github.com/derekparker/delve/issues/170
func (dbp *Process) FindFunctionLocation(funcName string, firstLine bool, lineOffset int) (uint64, error) {
	origfn := dbp.goSymTable.LookupFunc(funcName)
	if origfn == nil {
		return 0, fmt.Errorf("Could not find function %s\n", funcName)
	}

	if firstLine {
		return dbp.FirstPCAfterPrologue(origfn, false)
	} else if lineOffset > 0 {
		filename, lineno, _ := dbp.goSymTable.PCToLine(origfn.Entry)
		breakAddr, _, err := dbp.goSymTable.LineToPC(filename, lineno+lineOffset)
		return breakAddr, err
	}

	return origfn.Entry, nil
}

// CurrentLocation returns the location of the current thread.
func (dbp *Process) CurrentLocation() (*Location, error) {
	return dbp.CurrentThread.Location()
}

// RequestManualStop sets the `halt` flag and
// sends SIGSTOP to all threads.
func (dbp *Process) RequestManualStop() error {
	if dbp.exited {
		return &ProcessExitedError{}
	}
	dbp.halt = true
	return dbp.requestManualStop()
}

// SetBreakpoint sets a breakpoint at addr, and stores it in the process wide
// break point table. Setting a break point must be thread specific due to
// ptrace actions needing the thread to be in a signal-delivery-stop.
func (dbp *Process) SetBreakpoint(addr uint64) (*Breakpoint, error) {
	if dbp.exited {
		return nil, &ProcessExitedError{}
	}
	return dbp.setBreakpoint(dbp.CurrentThread.ID, addr, false)
}

// SetTempBreakpoint sets a temp breakpoint. Used during 'next' operations.
func (dbp *Process) SetTempBreakpoint(addr uint64, cond ast.Expr) (*Breakpoint, error) {
	bp, err := dbp.setBreakpoint(dbp.CurrentThread.ID, addr, true)
	if err != nil {
		return nil, err
	}
	bp.Cond = cond
	return bp, nil
}

// ClearBreakpoint clears the breakpoint at addr.
func (dbp *Process) ClearBreakpoint(addr uint64) (*Breakpoint, error) {
	if dbp.exited {
		return nil, &ProcessExitedError{}
	}
	bp, ok := dbp.FindBreakpoint(addr)
	if !ok {
		return nil, NoBreakpointError{addr: addr}
	}

	if _, err := bp.Clear(dbp.CurrentThread); err != nil {
		return nil, err
	}

	delete(dbp.Breakpoints, addr)

	return bp, nil
}

// Status returns the status of the current main thread context.
func (dbp *Process) Status() *WaitStatus {
	return dbp.CurrentThread.Status
}

// Next continues execution until the next source line.
func (dbp *Process) Next() (err error) {
	if dbp.exited {
		return &ProcessExitedError{}
	}
	for i := range dbp.Breakpoints {
		if dbp.Breakpoints[i].Temp {
			return fmt.Errorf("next while nexting")
		}
	}

	// Set breakpoints for any goroutine that is currently
	// blocked trying to read from a channel. This is so that
	// if control flow switches to that goroutine, we end up
	// somewhere useful instead of in runtime code.
	if _, err = dbp.setChanRecvBreakpoints(); err != nil {
		return
	}

	if err = dbp.setNextBreakpoints(); err != nil {
		switch err.(type) {
		case ThreadBlockedError, NoReturnAddr: // Noop
		default:
			dbp.ClearTempBreakpoints()
			return
		}
	}

	return dbp.Continue()
}

func (dbp *Process) setChanRecvBreakpoints() (int, error) {
	var count int
	allg, err := dbp.GoroutinesInfo()
	if err != nil {
		return 0, err
	}

	for _, g := range allg {
		if g.ChanRecvBlocked() {
			ret, err := g.chanRecvReturnAddr(dbp)
			if err != nil {
				if _, ok := err.(NullAddrError); ok {
					continue
				}
				return 0, err
			}
			if _, err = dbp.SetTempBreakpoint(ret, nil); err != nil {
				if _, ok := err.(BreakpointExistsError); ok {
					// Ignore duplicate breakpoints in case if multiple
					// goroutines wait on the same channel
					continue
				}
				return 0, err
			}
			count++
		}
	}
	return count, nil
}

// Continue continues execution of the debugged
// process. It will continue until it hits a breakpoint
// or is otherwise stopped.
func (dbp *Process) Continue() error {
	if dbp.exited {
		return &ProcessExitedError{}
	}
	for {
		if err := dbp.resume(); err != nil {
			return err
		}

		var trapthread *Thread
		var err error

		dbp.run(func() error {
			trapthread, err = dbp.trapWait(-1)
			return nil
		})
		if err != nil {
			return err
		}
		if err := dbp.Halt(); err != nil {
			return dbp.exitGuard(err)
		}
		if err := dbp.setCurrentBreakpoints(trapthread); err != nil {
			return err
		}
		if err := dbp.pickCurrentThread(trapthread); err != nil {
			return err
		}

		switch {
		case dbp.CurrentThread.CurrentBreakpoint == nil:
			// runtime.Breakpoint or manual stop
			if dbp.CurrentThread.onRuntimeBreakpoint() {
				for i := 0; i < 2; i++ {
					if err = dbp.CurrentThread.StepInstruction(); err != nil {
						return err
					}
				}
			}
			return dbp.conditionErrors()
		case dbp.CurrentThread.onTriggeredTempBreakpoint():
			err := dbp.ClearTempBreakpoints()
			if err != nil {
				return err
			}
			return dbp.conditionErrors()
		case dbp.CurrentThread.onTriggeredBreakpoint():
			onNextGoroutine, err := dbp.CurrentThread.onNextGoroutine()
			if err != nil {
				return err
			}
			if onNextGoroutine {
				err := dbp.ClearTempBreakpoints()
				if err != nil {
					return err
				}
			}
			return dbp.conditionErrors()
		default:
			// not a manual stop, not on runtime.Breakpoint, not on a breakpoint, just repeat
		}
	}
}

func (dbp *Process) conditionErrors() error {
	var condErr error
	for _, th := range dbp.Threads {
		if th.CurrentBreakpoint != nil && th.BreakpointConditionError != nil {
			if condErr == nil {
				condErr = th.BreakpointConditionError
			} else {
				return fmt.Errorf("multiple errors evaluating conditions")
			}
		}
	}
	return condErr
}

// pick a new dbp.CurrentThread, with the following priority:
// 	- a thread with onTriggeredTempBreakpoint() == true
// 	- a thread with onTriggeredBreakpoint() == true (prioritizing trapthread)
// 	- trapthread
func (dbp *Process) pickCurrentThread(trapthread *Thread) error {
	for _, th := range dbp.Threads {
		if th.onTriggeredTempBreakpoint() {
			return dbp.SwitchThread(th.ID)
		}
	}
	if trapthread.onTriggeredBreakpoint() {
		return dbp.SwitchThread(trapthread.ID)
	}
	for _, th := range dbp.Threads {
		if th.onTriggeredBreakpoint() {
			return dbp.SwitchThread(th.ID)
		}
	}
	return dbp.SwitchThread(trapthread.ID)
}

// Step will continue until another source line is reached.
// Will step into functions.
func (dbp *Process) Step() (err error) {
	if dbp.SelectedGoroutine != nil && dbp.SelectedGoroutine.thread == nil {
		// Step called on parked goroutine
		return dbp.stepToPC(dbp.SelectedGoroutine.PC)
	}
	fn := func() error {
		var nloc *Location
		th := dbp.CurrentThread
		loc, err := th.Location()
		if err != nil {
			return err
		}
		for {
			pc, err := dbp.CurrentThread.PC()
			if err != nil {
				return err
			}
			text, err := dbp.CurrentThread.Disassemble(pc, pc+maxInstructionLength, true)
			if err == nil && len(text) > 0 && text[0].IsCall() && text[0].DestLoc != nil && text[0].DestLoc.Fn != nil {
				return dbp.StepInto(text[0].DestLoc.Fn)
			}

			err = dbp.CurrentThread.StepInstruction()
			if err != nil {
				return err
			}
			nloc, err = th.Location()
			if err != nil {
				return err
			}
			if nloc.File != loc.File {
				return nil
			}
			if nloc.File == loc.File && nloc.Line != loc.Line {
				return nil
			}
		}
	}
	return dbp.run(fn)
}

// StepInto sets a temp breakpoint after the prologue of fn and calls Continue
func (dbp *Process) StepInto(fn *gosym.Func) error {
	for i := range dbp.Breakpoints {
		if dbp.Breakpoints[i].Temp {
			return fmt.Errorf("next while nexting")
		}
	}
	pc, _ := dbp.FirstPCAfterPrologue(fn, false)
	return dbp.stepToPC(pc)
}

func (dbp *Process) stepToPC(pc uint64) error {
	if _, err := dbp.SetTempBreakpoint(pc, sameGoroutineCondition(dbp.SelectedGoroutine)); err != nil {
		return err
	}
	return dbp.Continue()
}

// Returns an expression that evaluates to true when the current goroutine is g
func sameGoroutineCondition(g *G) ast.Expr {
	if g == nil {
		return nil
	}
	return &ast.BinaryExpr{
		Op: token.EQL,
		X: &ast.SelectorExpr{
			X: &ast.SelectorExpr{
				X:   &ast.Ident{Name: "runtime"},
				Sel: &ast.Ident{Name: "curg"},
			},
			Sel: &ast.Ident{Name: "goid"},
		},
		Y: &ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(g.ID)},
	}
}

// StepInstruction will continue the current thread for exactly
// one instruction. This method affects only the thread
// asssociated with the selected goroutine. All other
// threads will remain stopped.
func (dbp *Process) StepInstruction() (err error) {
	if dbp.SelectedGoroutine == nil {
		return errors.New("cannot single step: no selected goroutine")
	}
	if dbp.SelectedGoroutine.thread == nil {
		return fmt.Errorf("cannot single step: no thread associated with goroutine %d", dbp.SelectedGoroutine.ID)
	}
	return dbp.run(dbp.SelectedGoroutine.thread.StepInstruction)
}

// SwitchThread changes from current thread to the thread specified by `tid`.
func (dbp *Process) SwitchThread(tid int) error {
	if dbp.exited {
		return &ProcessExitedError{}
	}
	if th, ok := dbp.Threads[tid]; ok {
		dbp.CurrentThread = th
		dbp.SelectedGoroutine, _ = dbp.CurrentThread.GetG()
		return nil
	}
	return fmt.Errorf("thread %d does not exist", tid)
}

// SwitchGoroutine changes from current thread to the thread
// running the specified goroutine.
func (dbp *Process) SwitchGoroutine(gid int) error {
	if dbp.exited {
		return &ProcessExitedError{}
	}
	g, err := dbp.FindGoroutine(gid)
	if err != nil {
		return err
	}
	if g == nil {
		// user specified -1 and SelectedGoroutine is nil
		return nil
	}
	if g.thread != nil {
		return dbp.SwitchThread(g.thread.ID)
	}
	dbp.SelectedGoroutine = g
	return nil
}

// GoroutinesInfo returns an array of G structures representing the information
// Delve cares about from the internal runtime G structure.
func (dbp *Process) GoroutinesInfo() ([]*G, error) {
	if dbp.exited {
		return nil, &ProcessExitedError{}
	}
	if dbp.allGCache != nil {
		return dbp.allGCache, nil
	}

	var (
		threadg = map[int]*Thread{}
		allg    []*G
		rdr     = dbp.DwarfReader()
	)

	for i := range dbp.Threads {
		if dbp.Threads[i].blocked() {
			continue
		}
		g, _ := dbp.Threads[i].GetG()
		if g != nil {
			threadg[g.ID] = dbp.Threads[i]
		}
	}

	addr, err := rdr.AddrFor("runtime.allglen")
	if err != nil {
		return nil, err
	}
	allglenBytes, err := dbp.CurrentThread.readMemory(uintptr(addr), 8)
	if err != nil {
		return nil, err
	}
	allglen := binary.LittleEndian.Uint64(allglenBytes)

	rdr.Seek(0)
	allgentryaddr, err := rdr.AddrFor("runtime.allgs")
	if err != nil {
		// try old name (pre Go 1.6)
		allgentryaddr, err = rdr.AddrFor("runtime.allg")
		if err != nil {
			return nil, err
		}
	}
	faddr, err := dbp.CurrentThread.readMemory(uintptr(allgentryaddr), dbp.arch.PtrSize())
	allgptr := binary.LittleEndian.Uint64(faddr)

	for i := uint64(0); i < allglen; i++ {
		gvar, err := dbp.CurrentThread.newGVariable(uintptr(allgptr+(i*uint64(dbp.arch.PtrSize()))), true)
		if err != nil {
			return nil, err
		}
		g, err := gvar.parseG()
		if err != nil {
			return nil, err
		}
		if thread, allocated := threadg[g.ID]; allocated {
			loc, err := thread.Location()
			if err != nil {
				return nil, err
			}
			g.thread = thread
			// Prefer actual thread location information.
			g.CurrentLoc = *loc
		}
		if g.Status != Gdead {
			allg = append(allg, g)
		}
	}
	dbp.allGCache = allg
	return allg, nil
}

// Halt stops all threads.
func (dbp *Process) Halt() (err error) {
	if dbp.exited {
		return &ProcessExitedError{}
	}
	for _, th := range dbp.Threads {
		if err := th.Halt(); err != nil {
			return err
		}
	}
	return nil
}

// Registers obtains register values from the
// "current" thread of the traced process.
func (dbp *Process) Registers() (Registers, error) {
	return dbp.CurrentThread.Registers()
}

// PC returns the PC of the current thread.
func (dbp *Process) PC() (uint64, error) {
	return dbp.CurrentThread.PC()
}

// CurrentBreakpoint returns the breakpoint the current thread
// is stopped at.
func (dbp *Process) CurrentBreakpoint() *Breakpoint {
	return dbp.CurrentThread.CurrentBreakpoint
}

// DwarfReader returns a reader for the dwarf data
func (dbp *Process) DwarfReader() *reader.Reader {
	return reader.New(dbp.dwarf)
}

// Sources returns list of source files that comprise the debugged binary.
func (dbp *Process) Sources() map[string]*gosym.Obj {
	return dbp.goSymTable.Files
}

// Funcs returns list of functions present in the debugged program.
func (dbp *Process) Funcs() []gosym.Func {
	return dbp.goSymTable.Funcs
}

// Types returns list of types present in the debugged program.
func (dbp *Process) Types() ([]string, error) {
	types := make([]string, 0, len(dbp.types))
	for k := range dbp.types {
		types = append(types, k)
	}
	return types, nil
}

// PCToLine converts an instruction address to a file/line/function.
func (dbp *Process) PCToLine(pc uint64) (string, int, *gosym.Func) {
	return dbp.goSymTable.PCToLine(pc)
}

// FindBreakpointByID finds the breakpoint for the given ID.
func (dbp *Process) FindBreakpointByID(id int) (*Breakpoint, bool) {
	for _, bp := range dbp.Breakpoints {
		if bp.ID == id {
			return bp, true
		}
	}
	return nil, false
}

// FindBreakpoint finds the breakpoint for the given pc.
func (dbp *Process) FindBreakpoint(pc uint64) (*Breakpoint, bool) {
	// Check to see if address is past the breakpoint, (i.e. breakpoint was hit).
	if bp, ok := dbp.Breakpoints[pc-uint64(dbp.arch.BreakpointSize())]; ok {
		return bp, true
	}
	// Directly use addr to lookup breakpoint.
	if bp, ok := dbp.Breakpoints[pc]; ok {
		return bp, true
	}
	return nil, false
}

// Returns a new Process struct.
func initializeDebugProcess(dbp *Process, path string, attach bool) (*Process, error) {
	if attach {
		var err error
		dbp.execPtraceFunc(func() { err = PtraceAttach(dbp.Pid) })
		if err != nil {
			return nil, err
		}
		_, _, err = dbp.wait(dbp.Pid, 0)
		if err != nil {
			return nil, err
		}
	}

	proc, err := os.FindProcess(dbp.Pid)
	if err != nil {
		return nil, err
	}

	dbp.Process = proc
	err = dbp.LoadInformation(path)
	if err != nil {
		return nil, err
	}

	switch runtime.GOARCH {
	case "amd64":
		dbp.arch = AMD64Arch()
	}

	if err := dbp.updateThreadList(); err != nil {
		return nil, err
	}

	ver, isextld, err := dbp.getGoInformation()
	if err != nil {
		return nil, err
	}

	dbp.arch.SetGStructOffset(ver, isextld)
	// SelectedGoroutine can not be set correctly by the call to updateThreadList
	// because without calling SetGStructOffset we can not read the G struct of CurrentThread
	// but without calling updateThreadList we can not examine memory to determine
	// the offset of g struct inside TLS
	dbp.SelectedGoroutine, _ = dbp.CurrentThread.GetG()

	panicpc, err := dbp.FindFunctionLocation("runtime.startpanic", true, 0)
	if err == nil {
		bp, err := dbp.SetBreakpoint(panicpc)
		if err == nil {
			bp.Name = "unrecovered-panic"
			bp.ID = -1
			dbp.breakpointIDCounter--
		}
	}

	return dbp, nil
}

func (dbp *Process) ClearTempBreakpoints() error {
	for _, bp := range dbp.Breakpoints {
		if !bp.Temp {
			continue
		}
		if _, err := dbp.ClearBreakpoint(bp.Addr); err != nil {
			return err
		}
	}
	for i := range dbp.Threads {
		if dbp.Threads[i].CurrentBreakpoint != nil && dbp.Threads[i].CurrentBreakpoint.Temp {
			dbp.Threads[i].CurrentBreakpoint = nil
		}
	}
	return nil
}

func (dbp *Process) run(fn func() error) error {
	dbp.allGCache = nil
	if dbp.exited {
		return fmt.Errorf("process has already exited")
	}
	for _, th := range dbp.Threads {
		th.CurrentBreakpoint = nil
		th.BreakpointConditionMet = false
		th.BreakpointConditionError = nil
	}
	if err := fn(); err != nil {
		return err
	}
	return nil
}

func (dbp *Process) handlePtraceFuncs() {
	// We must ensure here that we are running on the same thread during
	// while invoking the ptrace(2) syscall. This is due to the fact that ptrace(2) expects
	// all commands after PTRACE_ATTACH to come from the same thread.
	runtime.LockOSThread()

	for fn := range dbp.ptraceChan {
		fn()
		dbp.ptraceDoneChan <- nil
	}
}

func (dbp *Process) execPtraceFunc(fn func()) {
	dbp.ptraceChan <- fn
	<-dbp.ptraceDoneChan
}

func (dbp *Process) getGoInformation() (ver GoVersion, isextld bool, err error) {
	vv, err := dbp.EvalPackageVariable("runtime.buildVersion", LoadConfig{true, 0, 64, 0, 0})
	if err != nil {
		err = fmt.Errorf("Could not determine version number: %v\n", err)
		return
	}
	if vv.Unreadable != nil {
		err = fmt.Errorf("Unreadable version number: %v\n", vv.Unreadable)
		return
	}

	ver, ok := ParseVersionString(constant.StringVal(vv.Value))
	if !ok {
		err = fmt.Errorf("Could not parse version number: %v\n", vv.Value)
		return
	}

	rdr := dbp.DwarfReader()
	rdr.Seek(0)
	for entry, err := rdr.NextCompileUnit(); entry != nil; entry, err = rdr.NextCompileUnit() {
		if err != nil {
			return ver, isextld, err
		}
		if prod, ok := entry.Val(dwarf.AttrProducer).(string); ok && (strings.HasPrefix(prod, "GNU AS")) {
			isextld = true
			break
		}
	}
	return
}

// FindGoroutine returns a G struct representing the goroutine
// specified by `gid`.
func (dbp *Process) FindGoroutine(gid int) (*G, error) {
	if gid == -1 {
		return dbp.SelectedGoroutine, nil
	}

	gs, err := dbp.GoroutinesInfo()
	if err != nil {
		return nil, err
	}
	for i := range gs {
		if gs[i].ID == gid {
			return gs[i], nil
		}
	}
	return nil, fmt.Errorf("Unknown goroutine %d", gid)
}

// ConvertEvalScope returns a new EvalScope in the context of the
// specified goroutine ID and stack frame.
func (dbp *Process) ConvertEvalScope(gid, frame int) (*EvalScope, error) {
	if dbp.exited {
		return nil, &ProcessExitedError{}
	}
	g, err := dbp.FindGoroutine(gid)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return dbp.CurrentThread.Scope()
	}

	var out EvalScope

	if g.thread == nil {
		out.Thread = dbp.CurrentThread
	} else {
		out.Thread = g.thread
	}

	locs, err := g.Stacktrace(frame)
	if err != nil {
		return nil, err
	}

	if frame >= len(locs) {
		return nil, fmt.Errorf("Frame %d does not exist in goroutine %d", frame, gid)
	}

	out.PC, out.CFA = locs[frame].Current.PC, locs[frame].CFA

	return &out, nil
}

func (dbp *Process) postExit() {
	dbp.exited = true
	close(dbp.ptraceChan)
	close(dbp.ptraceDoneChan)
}
