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
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/derekparker/delve/pkg/dwarf/frame"
	"github.com/derekparker/delve/pkg/dwarf/line"
	"github.com/derekparker/delve/pkg/dwarf/reader"
	"golang.org/x/debug/dwarf"
)

// Process represents all of the information the debugger
// is holding onto regarding the process we are debugging.
type Process struct {
	pid          int         // Process Pid
	Process      *os.Process // Pointer to process struct for the actual process we are debugging
	lastModified time.Time   // Time the executable of this process was last modified

	// Breakpoint table, holds information on breakpoints.
	// Maps instruction address to Breakpoint struct.
	breakpoints map[uint64]*Breakpoint

	// List of threads mapped as such: pid -> *Thread
	threads map[int]*Thread

	// Active thread
	currentThread *Thread

	// Goroutine that will be used by default to set breakpoint, eval variables, etc...
	// Normally selectedGoroutine is currentThread.GetG, it will not be only if SwitchGoroutine is called with a goroutine that isn't attached to a thread
	selectedGoroutine *G

	// Maps package names to package paths, needed to lookup types inside DWARF info
	packageMap map[string]string

	allGCache                   []*G
	dwarf                       *dwarf.Data
	goSymTable                  *gosym.Table
	frameEntries                frame.FrameDescriptionEntries
	lineInfo                    line.DebugLines
	os                          *OSProcessDetails
	arch                        Arch
	breakpointIDCounter         int
	internalBreakpointIDCounter int
	firstStart                  bool
	halt                        bool
	exited                      bool
	ptraceChan                  chan func()
	ptraceDoneChan              chan interface{}
	types                       map[string]dwarf.Offset
	functions                   []functionDebugInfo

	loadModuleDataOnce sync.Once
	moduleData         []moduleData
	nameOfRuntimeType  map[uintptr]nameOfRuntimeTypeEntry
}

type functionDebugInfo struct {
	lowpc, highpc uint64
	offset        dwarf.Offset
}

var NotExecutableErr = errors.New("not an executable file")

// New returns an initialized Process struct. Before returning,
// it will also launch a goroutine in order to handle ptrace(2)
// functions. For more information, see the documentation on
// `handlePtraceFuncs`.
func New(pid int) *Process {
	dbp := &Process{
		pid:               pid,
		threads:           make(map[int]*Thread),
		breakpoints:       make(map[uint64]*Breakpoint),
		firstStart:        true,
		os:                new(OSProcessDetails),
		ptraceChan:        make(chan func()),
		ptraceDoneChan:    make(chan interface{}),
		nameOfRuntimeType: make(map[uintptr]nameOfRuntimeTypeEntry),
	}
	// TODO: find better way to determine proc arch (perhaps use executable file info)
	switch runtime.GOARCH {
	case "amd64":
		dbp.arch = AMD64Arch()
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
		for _, bp := range dbp.breakpoints {
			if bp != nil {
				_, err := dbp.ClearBreakpoint(bp.Addr)
				if err != nil {
					return err
				}
			}
		}
	}
	dbp.execPtraceFunc(func() {
		err = PtraceDetach(dbp.pid, 0)
		if err != nil {
			return
		}
		if kill {
			err = killProcess(dbp.pid)
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
	for _, th := range dbp.threads {
		if th.running {
			return true
		}
	}
	return false
}

func (dbp *Process) LastModified() time.Time {
	return dbp.lastModified
}

func (dbp *Process) Pid() int {
	return dbp.pid
}

func (dbp *Process) SelectedGoroutine() *G {
	return dbp.selectedGoroutine
}

func (dbp *Process) Threads() map[int]*Thread {
	return dbp.threads
}

func (dbp *Process) CurrentThread() *Thread {
	return dbp.currentThread
}

func (dbp *Process) Breakpoints() map[uint64]*Breakpoint {
	return dbp.breakpoints
}

// LoadInformation finds the executable and then uses it
// to parse the following information:
// * Dwarf .debug_frame section
// * Dwarf .debug_line section
// * Go symbol table.
func (dbp *Process) LoadInformation(path string) error {
	var wg sync.WaitGroup

	exe, path, err := dbp.findExecutable(path)
	if err != nil {
		return err
	}
	fi, err := os.Stat(path)
	if err == nil {
		dbp.lastModified = fi.ModTime()
	}

	wg.Add(5)
	go dbp.loadProcessInformation(&wg)
	go dbp.parseDebugFrame(exe, &wg)
	go dbp.obtainGoSymbols(exe, &wg)
	go dbp.parseDebugLineInfo(exe, &wg)
	go dbp.loadDebugInfoMaps(&wg)
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
	return dbp.currentThread.Location()
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
func (dbp *Process) SetBreakpoint(addr uint64, kind BreakpointKind, cond ast.Expr) (*Breakpoint, error) {
	tid := dbp.currentThread.ID

	if bp, ok := dbp.FindBreakpoint(addr); ok {
		return nil, BreakpointExistsError{bp.File, bp.Line, bp.Addr}
	}

	f, l, fn := dbp.goSymTable.PCToLine(uint64(addr))
	if fn == nil {
		return nil, InvalidAddressError{address: addr}
	}

	newBreakpoint := &Breakpoint{
		FunctionName: fn.Name,
		File:         f,
		Line:         l,
		Addr:         addr,
		Kind:         kind,
		Cond:         cond,
		HitCount:     map[int]uint64{},
	}

	if kind != UserBreakpoint {
		dbp.internalBreakpointIDCounter++
		newBreakpoint.ID = dbp.internalBreakpointIDCounter
	} else {
		dbp.breakpointIDCounter++
		newBreakpoint.ID = dbp.breakpointIDCounter
	}

	thread := dbp.threads[tid]
	originalData, err := thread.readMemory(uintptr(addr), dbp.arch.BreakpointSize())
	if err != nil {
		return nil, err
	}
	if err := dbp.writeSoftwareBreakpoint(thread, addr); err != nil {
		return nil, err
	}
	newBreakpoint.OriginalData = originalData
	dbp.breakpoints[addr] = newBreakpoint

	return newBreakpoint, nil
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

	if _, err := bp.Clear(dbp.currentThread); err != nil {
		return nil, err
	}

	delete(dbp.breakpoints, addr)

	return bp, nil
}

// Status returns the status of the current main thread context.
func (dbp *Process) Status() *WaitStatus {
	return dbp.currentThread.Status
}

// Next continues execution until the next source line.
func (dbp *Process) Next() (err error) {
	if dbp.exited {
		return &ProcessExitedError{}
	}
	for i := range dbp.breakpoints {
		if dbp.breakpoints[i].Internal() {
			return fmt.Errorf("next while nexting")
		}
	}

	if err = dbp.next(false); err != nil {
		switch err.(type) {
		case ThreadBlockedError, NoReturnAddr: // Noop
		default:
			dbp.ClearInternalBreakpoints()
			return
		}
	}

	return dbp.Continue()
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

		dbp.allGCache = nil
		for _, th := range dbp.threads {
			th.clearBreakpointState()
		}

		trapthread, err := dbp.trapWait(-1)
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
		case dbp.currentThread.CurrentBreakpoint == nil:
			// runtime.Breakpoint or manual stop
			if dbp.currentThread.onRuntimeBreakpoint() {
				// Single-step current thread until we exit runtime.breakpoint and
				// runtime.Breakpoint.
				// On go < 1.8 it was sufficient to single-step twice on go1.8 a change
				// to the compiler requires 4 steps.
				for {
					if err = dbp.currentThread.StepInstruction(); err != nil {
						return err
					}
					loc, err := dbp.currentThread.Location()
					if err != nil || loc.Fn == nil || (loc.Fn.Name != "runtime.breakpoint" && loc.Fn.Name != "runtime.Breakpoint") {
						break
					}
				}
			}
			return dbp.conditionErrors()
		case dbp.currentThread.onTriggeredInternalBreakpoint():
			if dbp.currentThread.CurrentBreakpoint.Kind == StepBreakpoint {
				// See description of proc.(*Process).next for the meaning of StepBreakpoints
				if err := dbp.conditionErrors(); err != nil {
					return err
				}
				pc, err := dbp.currentThread.PC()
				if err != nil {
					return err
				}
				text, err := dbp.currentThread.Disassemble(pc, pc+maxInstructionLength, true)
				if err != nil {
					return err
				}
				// here we either set a breakpoint into the destination of the CALL
				// instruction or we determined that the called function is hidden,
				// either way we need to resume execution
				if err = dbp.setStepIntoBreakpoint(text, sameGoroutineCondition(dbp.selectedGoroutine)); err != nil {
					return err
				}
			} else {
				if err := dbp.ClearInternalBreakpoints(); err != nil {
					return err
				}
				return dbp.conditionErrors()
			}
		case dbp.currentThread.onTriggeredBreakpoint():
			onNextGoroutine, err := dbp.currentThread.onNextGoroutine()
			if err != nil {
				return err
			}
			if onNextGoroutine {
				err := dbp.ClearInternalBreakpoints()
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
	for _, th := range dbp.threads {
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

// pick a new dbp.currentThread, with the following priority:
// 	- a thread with onTriggeredInternalBreakpoint() == true
// 	- a thread with onTriggeredBreakpoint() == true (prioritizing trapthread)
// 	- trapthread
func (dbp *Process) pickCurrentThread(trapthread *Thread) error {
	for _, th := range dbp.threads {
		if th.onTriggeredInternalBreakpoint() {
			return dbp.SwitchThread(th.ID)
		}
	}
	if trapthread.onTriggeredBreakpoint() {
		return dbp.SwitchThread(trapthread.ID)
	}
	for _, th := range dbp.threads {
		if th.onTriggeredBreakpoint() {
			return dbp.SwitchThread(th.ID)
		}
	}
	return dbp.SwitchThread(trapthread.ID)
}

// Step will continue until another source line is reached.
// Will step into functions.
func (dbp *Process) Step() (err error) {
	if dbp.exited {
		return &ProcessExitedError{}
	}
	for i := range dbp.breakpoints {
		if dbp.breakpoints[i].Internal() {
			return fmt.Errorf("next while nexting")
		}
	}

	if err = dbp.next(true); err != nil {
		switch err.(type) {
		case ThreadBlockedError, NoReturnAddr: // Noop
		default:
			dbp.ClearInternalBreakpoints()
			return
		}
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
	if dbp.selectedGoroutine == nil {
		return errors.New("cannot single step: no selected goroutine")
	}
	if dbp.selectedGoroutine.thread == nil {
		// Step called on parked goroutine
		if _, err := dbp.SetBreakpoint(dbp.selectedGoroutine.PC, NextBreakpoint, sameGoroutineCondition(dbp.selectedGoroutine)); err != nil {
			return err
		}
		return dbp.Continue()
	}
	dbp.allGCache = nil
	if dbp.exited {
		return &ProcessExitedError{}
	}
	dbp.selectedGoroutine.thread.clearBreakpointState()
	err = dbp.selectedGoroutine.thread.StepInstruction()
	if err != nil {
		return err
	}
	return dbp.selectedGoroutine.thread.SetCurrentBreakpoint()
}

// StepOut will continue until the current goroutine exits the
// function currently being executed or a deferred function is executed
func (dbp *Process) StepOut() error {
	cond := sameGoroutineCondition(dbp.selectedGoroutine)

	topframe, err := topframe(dbp.selectedGoroutine, dbp.currentThread)
	if err != nil {
		return err
	}

	pcs := []uint64{}

	var deferpc uint64 = 0
	if filepath.Ext(topframe.Current.File) == ".go" {
		if dbp.selectedGoroutine != nil {
			deferPCEntry := dbp.selectedGoroutine.DeferPC()
			if deferPCEntry != 0 {
				_, _, deferfn := dbp.goSymTable.PCToLine(deferPCEntry)
				deferpc, err = dbp.FirstPCAfterPrologue(deferfn, false)
				if err != nil {
					return err
				}
				pcs = append(pcs, deferpc)
			}
		}
	}

	if topframe.Ret == 0 && deferpc == 0 {
		return errors.New("nothing to stepout to")
	}

	if deferpc != 0 && deferpc != topframe.Current.PC {
		bp, err := dbp.SetBreakpoint(deferpc, NextDeferBreakpoint, cond)
		if err != nil {
			if _, ok := err.(BreakpointExistsError); !ok {
				dbp.ClearInternalBreakpoints()
				return err
			}
		}
		if bp != nil {
			// For StepOut we do not want to step into the deferred function
			// when it's called by runtime.deferreturn so we do not populate
			// DeferReturns.
			bp.DeferReturns = []uint64{}
		}
	}

	if topframe.Ret != 0 {
		if err := dbp.setInternalBreakpoints(topframe.Current.PC, []uint64{topframe.Ret}, NextBreakpoint, cond); err != nil {
			return err
		}
	}

	return dbp.Continue()
}

// SwitchThread changes from current thread to the thread specified by `tid`.
func (dbp *Process) SwitchThread(tid int) error {
	if dbp.exited {
		return &ProcessExitedError{}
	}
	if th, ok := dbp.threads[tid]; ok {
		dbp.currentThread = th
		dbp.selectedGoroutine, _ = dbp.currentThread.GetG()
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
		// user specified -1 and selectedGoroutine is nil
		return nil
	}
	if g.thread != nil {
		return dbp.SwitchThread(g.thread.ID)
	}
	dbp.selectedGoroutine = g
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

	for i := range dbp.threads {
		if dbp.threads[i].blocked() {
			continue
		}
		g, _ := dbp.threads[i].GetG()
		if g != nil {
			threadg[g.ID] = dbp.threads[i]
		}
	}

	addr, err := rdr.AddrFor("runtime.allglen")
	if err != nil {
		return nil, err
	}
	allglenBytes, err := dbp.currentThread.readMemory(uintptr(addr), 8)
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
	faddr, err := dbp.currentThread.readMemory(uintptr(allgentryaddr), dbp.arch.PtrSize())
	allgptr := binary.LittleEndian.Uint64(faddr)

	for i := uint64(0); i < allglen; i++ {
		gvar, err := dbp.currentThread.newGVariable(uintptr(allgptr+(i*uint64(dbp.arch.PtrSize()))), true)
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

func (g *G) Thread() *Thread {
	return g.thread
}

// Halt stops all threads.
func (dbp *Process) Halt() (err error) {
	if dbp.exited {
		return &ProcessExitedError{}
	}
	for _, th := range dbp.threads {
		if err := th.Halt(); err != nil {
			return err
		}
	}
	return nil
}

// Registers obtains register values from the
// "current" thread of the traced process.
func (dbp *Process) Registers() (Registers, error) {
	return dbp.currentThread.Registers(false)
}

// PC returns the PC of the current thread.
func (dbp *Process) PC() (uint64, error) {
	return dbp.currentThread.PC()
}

// CurrentBreakpoint returns the breakpoint the current thread
// is stopped at.
func (dbp *Process) CurrentBreakpoint() *Breakpoint {
	return dbp.currentThread.CurrentBreakpoint
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
	for _, bp := range dbp.breakpoints {
		if bp.ID == id {
			return bp, true
		}
	}
	return nil, false
}

// FindBreakpoint finds the breakpoint for the given pc.
func (dbp *Process) FindBreakpoint(pc uint64) (*Breakpoint, bool) {
	// Check to see if address is past the breakpoint, (i.e. breakpoint was hit).
	if bp, ok := dbp.breakpoints[pc-uint64(dbp.arch.BreakpointSize())]; ok {
		return bp, true
	}
	// Directly use addr to lookup breakpoint.
	if bp, ok := dbp.breakpoints[pc]; ok {
		return bp, true
	}
	return nil, false
}

// Returns a new Process struct.
func initializeDebugProcess(dbp *Process, path string, attach bool) (*Process, error) {
	if attach {
		var err error
		dbp.execPtraceFunc(func() { err = PtraceAttach(dbp.pid) })
		if err != nil {
			return nil, err
		}
		_, _, err = dbp.wait(dbp.pid, 0)
		if err != nil {
			return nil, err
		}
	}

	proc, err := os.FindProcess(dbp.pid)
	if err != nil {
		return nil, err
	}

	dbp.Process = proc
	err = dbp.LoadInformation(path)
	if err != nil {
		return nil, err
	}

	if err := dbp.updateThreadList(); err != nil {
		return nil, err
	}

	ver, isextld, err := dbp.getGoInformation()
	if err != nil {
		return nil, err
	}

	dbp.arch.SetGStructOffset(ver, isextld)
	// selectedGoroutine can not be set correctly by the call to updateThreadList
	// because without calling SetGStructOffset we can not read the G struct of currentThread
	// but without calling updateThreadList we can not examine memory to determine
	// the offset of g struct inside TLS
	dbp.selectedGoroutine, _ = dbp.currentThread.GetG()

	panicpc, err := dbp.FindFunctionLocation("runtime.startpanic", true, 0)
	if err == nil {
		bp, err := dbp.SetBreakpoint(panicpc, UserBreakpoint, nil)
		if err == nil {
			bp.Name = "unrecovered-panic"
			bp.ID = -1
			dbp.breakpointIDCounter--
		}
	}

	return dbp, nil
}

func (dbp *Process) ClearInternalBreakpoints() error {
	for _, bp := range dbp.breakpoints {
		if !bp.Internal() {
			continue
		}
		if _, err := dbp.ClearBreakpoint(bp.Addr); err != nil {
			return err
		}
	}
	for i := range dbp.threads {
		if dbp.threads[i].CurrentBreakpoint != nil && dbp.threads[i].CurrentBreakpoint.Internal() {
			dbp.threads[i].CurrentBreakpoint = nil
		}
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
		return dbp.selectedGoroutine, nil
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
		return dbp.currentThread.Scope()
	}

	var out EvalScope

	if g.thread == nil {
		out.Thread = dbp.currentThread
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
