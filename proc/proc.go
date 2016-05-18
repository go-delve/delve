package proc

import (
	"debug/gosym"
	"encoding/binary"
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"log"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/debug/dwarf"

	pdwarf "github.com/derekparker/delve/pkg/dwarf"
	"github.com/derekparker/delve/pkg/dwarf/reader"
)

// ResumeMode indicates how we should resume the
// debugged process.
type ResumeMode int

const (
	// Step a single CPU instruction.
	ModeStepInstruction = ResumeMode(0)
	// Step to next source line, stepping into functions.
	ModeStep = iota
	// Resume until breakpoint/signal/manual stop.
	ModeResume
)

// Process represents a process we are debugging.
type Process struct {
	// Process Pid
	Pid int
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
	// Dwarf is the dwarf debugging information for this process.
	Dwarf *pdwarf.Dwarf

	allGCache []*G
	os        *OSProcessDetails
	arch      Arch
	halt      bool
	exited    bool
}

type WaitStatus struct {
	exitstatus int
	exited     bool
	signaled   bool
	signal     syscall.Signal
}

func (ws *WaitStatus) Exited() bool {
	return ws.exited
}

func (ws *WaitStatus) ExitStatus() int {
	return ws.exitstatus
}

func (ws *WaitStatus) Signaled() bool {
	return ws.signaled
}

func (ws *WaitStatus) Signal() syscall.Signal {
	return ws.signal
}

func (ws *WaitStatus) Trap() bool {
	return ws.signal == syscall.SIGTRAP
}

// New returns an initialized Process struct.
func New(pid int) *Process {
	return &Process{
		Pid:         pid,
		Threads:     make(map[int]*Thread),
		Breakpoints: make(map[uint64]*Breakpoint),
		os:          new(OSProcessDetails),
	}
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
	err = PtraceDetach(dbp.Pid, 0)
	if err != nil {
		return
	}
	if kill {
		err = killProcess(dbp.Pid)
	}
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

func (dbp *Process) Mourn() (int, error) {
	dbp.exited = true
	_, ws, err := dbp.wait(dbp.Pid, 0)
	if err != nil {
		if err != syscall.ECHILD {
			return 0, err
		}
		return 0, nil
	}
	sig := syscall.Signal(ws.Signal())
	if !ws.Exited() && sig != syscall.SIGKILL {
		return 0, fmt.Errorf("process did not exit")
	}
	return ws.ExitStatus(), nil
}

// LoadInformation finds the executable and then uses it
// to parse the following information:
// * Dwarf .debug_frame section
// * Dwarf .debug_line section
// * Go symbol table.
func (dbp *Process) LoadInformation(path string) error {
	path, err := dbp.findExecutable(path)
	if err != nil {
		return err
	}
	d, err := pdwarf.Parse(path)
	if err != nil {
		return err
	}
	dbp.Dwarf = d
	dbp.loadProcessInformation()
	return nil
}

// FindFileLocation returns the PC for a given file:line.
// Assumes that `file` is normailzed to lower case and '/' on Windows.
func (dbp *Process) FindFileLocation(fileName string, lineno int) (uint64, error) {
	pc, fn, err := dbp.Dwarf.LineToPC(fileName, lineno)
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
	origfn := dbp.Dwarf.LookupFunc(funcName)
	if origfn == nil {
		return 0, fmt.Errorf("Could not find function %s\n", funcName)
	}

	if firstLine {
		return dbp.FirstPCAfterPrologue(origfn, false)
	} else if lineOffset > 0 {
		filename, lineno, _ := dbp.Dwarf.PCToLine(origfn.Entry)
		breakAddr, _, err := dbp.Dwarf.LineToPC(filename, lineno+lineOffset)
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
	return requestManualStop(dbp)
}

// SetBreakpoint sets a breakpoint at addr, and stores it in the process wide
// break point table. Setting a break point must be thread specific due to
// ptrace actions needing the thread to be in a signal-delivery-stop.
func (dbp *Process) SetBreakpoint(addr uint64) (*Breakpoint, error) {
	return setBreakpoint(dbp, dbp.CurrentThread, addr, false, dbp.arch.BreakpointInstruction())
}

// SetTempBreakpoint sets a temp breakpoint. Used during 'next' operations.
func (dbp *Process) SetTempBreakpoint(addr uint64) (*Breakpoint, error) {
	return setBreakpoint(dbp, dbp.CurrentThread, addr, true, dbp.arch.BreakpointInstruction())
}

func setBreakpoint(dbp *Process, mem memoryReadWriter, addr uint64, temp bool, instr []byte) (*Breakpoint, error) {
	if dbp.exited {
		return nil, &ProcessExitedError{}
	}
	if bp, ok := dbp.FindBreakpoint(addr); ok {
		return nil, BreakpointExistsError{bp.File, bp.Line, bp.Addr}
	}
	f, l, fn := dbp.Dwarf.PCToLine(uint64(addr))
	if fn == nil {
		return nil, InvalidAddressError{address: addr}
	}
	loc := &Location{Fn: fn, File: f, Line: l, PC: addr}
	bp, err := createAndWriteBreakpoint(dbp.CurrentThread, loc, temp, instr)
	if err != nil {
		return nil, err
	}
	dbp.Breakpoints[addr] = bp
	return bp, nil
}

// ClearBreakpoint clears the breakpoint at addr.
func (dbp *Process) ClearBreakpoint(addr uint64) (*Breakpoint, error) {
	log.Printf("clear breakpoint at: %#v\n", addr)
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

	// Restore any thread that was stopped on this
	// breakpoint to the correct instruction.
	for _, th := range dbp.Threads {
		if tbp := th.CurrentBreakpoint; tbp != nil {
			if tbp.Addr == bp.Addr {
				th.CurrentBreakpoint = nil
				th.BreakpointConditionMet = false
				th.BreakpointConditionError = nil
				err := th.SetPC(bp.Addr)
				if err != nil {
					return nil, err
				}
			}
		}
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
	log.Println("nexting")
	if dbp.exited {
		return &ProcessExitedError{}
	}
	for i := range dbp.Breakpoints {
		if dbp.Breakpoints[i].Temp {
			return fmt.Errorf("next while nexting")
		}
	}

	// Get the goroutine for the current thread. We will
	// use it later in order to ensure we are on the same
	// goroutine.
	g, err := dbp.CurrentThread.GetG()
	if err != nil {
		return err
	}

	// Set breakpoints for any goroutine that is currently
	// blocked trying to read from a channel. This is so that
	// if control flow switches to that goroutine, we end up
	// somewhere useful instead of in runtime code.
	if _, err = setChanRecvBreakpoints(dbp); err != nil {
		return
	}

	var goroutineExiting bool
	if err = dbp.CurrentThread.setNextBreakpoints(); err != nil {
		switch t := err.(type) {
		case ThreadBlockedError, NoReturnAddr: // Noop
		case GoroutineExitingError:
			goroutineExiting = t.goid == g.ID
		default:
			dbp.ClearTempBreakpoints()
			return
		}
	}

	if !goroutineExiting {
		for i := range dbp.Breakpoints {
			if dbp.Breakpoints[i].Temp {
				dbp.Breakpoints[i].Cond = &ast.BinaryExpr{
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
		}
	}

	return dbp.Continue()
}

// Continue continues execution of the debugged
// process. It will continue until it hits a breakpoint
// or is otherwise stopped.
func (dbp *Process) Continue() error {
	return resume(dbp, ModeResume)
}

func resume(p *Process, mode ResumeMode) error {
	log.Printf("resume called on process %d with mode %d\n", p.Pid, mode)
	if p.exited {
		return &ProcessExitedError{}
	}
	// Clear state.
	p.allGCache = nil
	// Resume process.
	switch mode {
	case ModeStepInstruction:
		log.Println("begin step instruction")
		if p.SelectedGoroutine == nil {
			return errors.New("cannot single step: no selected goroutine")
		}
		if p.SelectedGoroutine.thread == nil {
			return fmt.Errorf("cannot single step: no thread associated with goroutine %d", p.SelectedGoroutine.ID)
		}
		return p.SelectedGoroutine.thread.StepInstruction()
	case ModeStep:
		log.Println("begin step")
		th := p.CurrentThread
		if fn, ok := atFunctionCall(th); ok {
			log.Printf("stepping into function: %s", fn.Name)
			return p.StepInto(fn)
		}
		return p.Next()
	case ModeResume:
		log.Println("begin resume")
		if err := p.resume(); err != nil {
			log.Printf("resume error: %v\n", err)
			return err
		}
	default:
		// Programmer error, safe to panic here.
		panic("invalid mode passed to resume")
	}
	status, trapthread, err := p.Wait()
	if err != nil {
		log.Printf("Wait error: %v\n", err)
		return err
	}
	if status.Exited() {
		return ProcessExitedError{Pid: p.Pid, Status: status.ExitStatus()}
	}
	// Make sure process is fully stopped.
	if err := p.Halt(); err != nil {
		log.Printf("Halt error: %v\n", err)
		return err
	}
	if err := p.setCurrentBreakpoints(); err != nil {
		log.Printf("setCurrentBreakpoints error: %v\n", err)
		return err
	}
	if err := p.pickCurrentThread(trapthread); err != nil {
		log.Printf("pickCurrentThread error: %v\n", err)
		return err
	}
	switch {
	case status.Trap():
		log.Println("handling SIGTRAP")
		err := handleSigTrap(p)
		switch err.(type) {
		case BreakpointConditionNotMetError:
			// Do not stop for a breakpoint whose condition has not been met.
			return resume(p, mode)
		default:
			return err
		}
	case status.Signaled():
		log.Printf("got signal: %v\n", status.Signal())
		// TODO(derekparker) alert users of signals.
		if err := trapthread.ContinueWithSignal(int(status.Signal())); err != nil {
			// TODO(derekparker) should go through regular resume flow.
			if err == syscall.ESRCH {
				exitcode, err := p.Mourn()
				if err != nil {
					return err
				}
				return ProcessExitedError{Pid: p.Pid, Status: exitcode}
			}
			return err
		}
		return resume(p, mode)
	default:
		panic("unhandled stop event")
	}
	return nil
}

func atFunctionCall(th *Thread) (*gosym.Func, bool) {
	log.Println("begin step")
	pc, err := th.PC()
	if err != nil {
		return nil, false
	}
	text, err := th.Disassemble(pc, pc+maxInstructionLength, true)
	if err != nil {
		return nil, false
	}
	loc := text[0].Loc
	for _, txt := range text {
		if txt.Loc.Line != loc.Line {
			break
		}
		if txt.IsCall() && txt.DestLoc != nil && txt.DestLoc.Fn != nil {
			return txt.DestLoc.Fn, true
		}
	}
	return nil, false
}

func handleSigTrap(p *Process) error {
	log.Println("checking breakpoint conditions")
	switch {
	case p.CurrentThread.CurrentBreakpoint == nil:
		log.Printf("no current breakpoint, halt=%v", p.halt)
		if p.halt {
			p.halt = false
		}
		// runtime.Breakpoint or manual stop
		if p.CurrentThread.onRuntimeBreakpoint() {
			log.Println("on runtime breakpoint")
			for i := 0; i < 2; i++ {
				if err := p.CurrentThread.StepInstruction(); err != nil {
					return err
				}
			}
		}
		return p.conditionErrors()
	case p.CurrentThread.onTriggeredTempBreakpoint():
		log.Println("on triggered temp breakpoint")
		if err := p.ClearTempBreakpoints(); err != nil {
			return err
		}
		return p.conditionErrors()
	case p.CurrentThread.onTriggeredBreakpoint():
		log.Println("on triggered breakpoint")
		onNextGoroutine, err := p.CurrentThread.onNextGoroutine()
		if err != nil {
			return err
		}
		if onNextGoroutine {
			if err := p.ClearTempBreakpoints(); err != nil {
				return err
			}
		}
		return p.conditionErrors()
	}
	return BreakpointConditionNotMetError{}
}

type BreakpointConditionNotMetError struct{}

func (b BreakpointConditionNotMetError) Error() string {
	return "breakpoint condition not met"
}

func (dbp *Process) Wait() (*WaitStatus, *Thread, error) {
	return dbp.trapWait(-dbp.Pid)
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
	if trapthread == nil {
		panic("pickCurrentThread got nil thread")
	}
	for _, th := range dbp.Threads {
		if th.onTriggeredTempBreakpoint() {
			log.Printf("thread %d triggered temp breakpoint\n", th.ID)
			return dbp.SwitchThread(th.ID)
		}
	}
	if trapthread.onTriggeredBreakpoint() {
		log.Printf("thread %d triggered breakpoint\n", trapthread.ID)
		return dbp.SwitchThread(trapthread.ID)
	}
	for _, th := range dbp.Threads {
		if th.onTriggeredBreakpoint() {
			log.Printf("thread %d triggered breakpoint\n", th.ID)
			return dbp.SwitchThread(th.ID)
		}
	}
	log.Printf("switching to default thread %d\n", trapthread.ID)
	return dbp.SwitchThread(trapthread.ID)
}

// Step will continue until another source line is reached.
// Will step into functions.
func (dbp *Process) Step() (err error) {
	return resume(dbp, ModeStep)
}

// StepInto sets a temp breakpoint after the prologue of fn and calls Continue
func (dbp *Process) StepInto(fn *gosym.Func) error {
	for i := range dbp.Breakpoints {
		if dbp.Breakpoints[i].Temp {
			return fmt.Errorf("next while nexting")
		}
	}
	pc, _ := dbp.FirstPCAfterPrologue(fn, false)
	if _, err := dbp.SetTempBreakpoint(pc); err != nil {
		return err
	}
	return dbp.Continue()
}

// StepInstruction will continue the current thread for exactly
// one instruction. This method affects only the thread
// asssociated with the selected goroutine. All other
// threads will remain stopped.
func (dbp *Process) StepInstruction() (err error) {
	return resume(dbp, ModeStepInstruction)
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
	return dbp.Dwarf.Reader()
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
		err = PtraceAttach(dbp.Pid)
		if err != nil {
			return nil, err
		}
		_, _, err = dbp.wait(dbp.Pid, 0)
		if err != nil {
			return nil, err
		}
	}

	err := dbp.LoadInformation(path)
	if err != nil {
		return nil, err
	}

	switch runtime.GOARCH {
	case "amd64":
		dbp.arch = AMD64Arch()
	}

	if err := updateThreadList(dbp); err != nil {
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
			breakpointIDCounter--
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
	return nil
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

func (dbp *Process) setCurrentBreakpoints() error {
	for _, th := range dbp.Threads {
		err := th.SetCurrentBreakpoint()
		if err != nil {
			return err
		}
	}
	return nil
}

func setChanRecvBreakpoints(dbp *Process) (int, error) {
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
			if _, err = dbp.SetTempBreakpoint(ret); err != nil {
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
