package proc

import (
	"debug/dwarf"
	"debug/gosym"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	sys "golang.org/x/sys/unix"

	"github.com/derekparker/delve/dwarf/frame"
	"github.com/derekparker/delve/dwarf/line"
	"github.com/derekparker/delve/dwarf/reader"
	"github.com/derekparker/delve/source"
)

// Process represents all of the information the debugger
// is holding onto regarding the process we are debugging.
type Process struct {
	Pid     int         // Process Pid
	Process *os.Process // Pointer to process struct for the actual process we are debugging

	// Breakpoint table, hold information on software / hardware breakpoints.
	// Maps instruction address to Breakpoint struct.
	Breakpoints map[uint64]*Breakpoint

	// List of threads mapped as such: pid -> *Thread
	Threads map[int]*Thread

	// Active thread. This is the default thread used for setting breakpoints, evaluating variables, etc..
	CurrentThread *Thread

	dwarf                   *dwarf.Data
	goSymTable              *gosym.Table
	frameEntries            frame.FrameDescriptionEntries
	lineInfo                line.DebugLines
	firstStart              bool
	os                      *OSProcessDetails
	arch                    Arch
	ast                     *source.Searcher
	breakpointIDCounter     int
	tempBreakpointIDCounter int
	halt                    bool
	exited                  bool
	ptraceChan              chan func()
	ptraceDoneChan          chan interface{}
}

func New(pid int) *Process {
	dbp := &Process{
		Pid:            pid,
		Threads:        make(map[int]*Thread),
		Breakpoints:    make(map[uint64]*Breakpoint),
		firstStart:     true,
		os:             new(OSProcessDetails),
		ast:            source.New(),
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
		var sig int
		if kill {
			sig = int(sys.SIGINT)
		}
		err = PtraceDetach(dbp.Pid, sig)
	})
	return
}

// Returns whether or not Delve thinks the debugged
// process has exited.
func (dbp *Process) Exited() bool {
	return dbp.exited
}

// Returns whether or not Delve thinks the debugged
// process is currently executing.
func (dbp *Process) Running() bool {
	for _, th := range dbp.Threads {
		if th.running {
			return true
		}
	}
	return false
}

// Finds the executable and then uses it
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

	wg.Add(3)
	go dbp.parseDebugFrame(exe, &wg)
	go dbp.obtainGoSymbols(exe, &wg)
	go dbp.parseDebugLineInfo(exe, &wg)
	wg.Wait()

	return nil
}

func (dbp *Process) FindFileLocation(fileName string, lineno int) (uint64, error) {
	pc, _, err := dbp.goSymTable.LineToPC(fileName, lineno)
	if err != nil {
		return 0, err
	}
	return pc, nil
}

// Finds address of a function's line
// If firstLine == true is passed FindFunctionLocation will attempt to find the first line of the function
// If lineOffset is passed FindFunctionLocation will return the address of that line
// Pass lineOffset == 0 and firstLine == false if you want the address for the function's entry point
// Note that setting breakpoints at that address will cause surprising behavior:
// https://github.com/derekparker/delve/issues/170
func (dbp *Process) FindFunctionLocation(funcName string, firstLine bool, lineOffset int) (uint64, error) {
	fn := dbp.goSymTable.LookupFunc(funcName)
	if fn == nil {
		return 0, fmt.Errorf("Could not find function %s\n", funcName)
	}

	if firstLine {
		filename, lineno, _ := dbp.goSymTable.PCToLine(fn.Entry)
		if filepath.Ext(filename) != ".go" {
			return fn.Entry, nil
		}

		lines, err := dbp.ast.NextLines(filename, lineno)
		if err != nil {
			return 0, err
		}

		if len(lines) > 0 {
			linePC, _, err := dbp.goSymTable.LineToPC(filename, lines[0])
			return linePC, err
		} else {
			return fn.Entry, nil
		}
	} else if lineOffset > 0 {
		filename, lineno, _ := dbp.goSymTable.PCToLine(fn.Entry)
		breakAddr, _, err := dbp.goSymTable.LineToPC(filename, lineno+lineOffset)
		return breakAddr, err
	}

	return fn.Entry, nil
}

// Sends out a request that the debugged process halt
// execution. Sends SIGSTOP to all threads.
func (dbp *Process) RequestManualStop() error {
	dbp.halt = true
	return dbp.requestManualStop()
}

// Sets a breakpoint at addr, and stores it in the process wide
// break point table. Setting a break point must be thread specific due to
// ptrace actions needing the thread to be in a signal-delivery-stop.
//
// Depending on hardware support, Delve will choose to either
// set a hardware or software breakpoint. Essentially, if the
// hardware supports it, and there are free debug registers, Delve
// will set a hardware breakpoint. Otherwise we fall back to software
// breakpoints, which are a bit more work for us.
func (dbp *Process) SetBreakpoint(addr uint64) (*Breakpoint, error) {
	return dbp.setBreakpoint(dbp.CurrentThread.Id, addr, false)
}

// Sets a temp breakpoint, for the 'next' command.
func (dbp *Process) SetTempBreakpoint(addr uint64) (*Breakpoint, error) {
	return dbp.setBreakpoint(dbp.CurrentThread.Id, addr, true)
}

// Clears a breakpoint.
//
// If it is a hardware assisted breakpoint, iterate through all threads
// and clear the debug register. Otherwise, restore original instruction.
func (dbp *Process) ClearBreakpoint(addr uint64) (*Breakpoint, error) {
	bp, ok := dbp.Breakpoints[addr]
	if !ok {
		return nil, NoBreakpointError{addr: addr}
	}

	for _, thread := range dbp.Threads {
		if _, err := bp.Clear(thread); err != nil {
			return nil, err
		}
		if !bp.hardware {
			break
		}
	}

	if bp.hardware {
		dbp.arch.SetHardwareBreakpointUsage(bp.reg, false)
	}
	delete(dbp.Breakpoints, addr)

	return bp, nil
}

// Returns the status of the current main thread context.
func (dbp *Process) Status() *sys.WaitStatus {
	return dbp.CurrentThread.Status
}

// Step over function calls.
func (dbp *Process) Next() error {
	return dbp.run(dbp.next)
}

func (dbp *Process) next() (err error) {
	defer func() {
		// Always halt process at end of this function.
		herr := dbp.Halt()
		// Make sure we clean up the temp breakpoints.
		cerr := dbp.clearTempBreakpoints()
		// If we already had an error, return it.
		if err != nil {
			return
		}
		if herr != nil {
			err = herr
			return
		}
		if cerr != nil {
			err = cerr
		}
	}()

	// Set breakpoints for any goroutine that is currently
	// blocked trying to read from a channel. This is so that
	// if control flow switches to that goroutine, we end up
	// somewhere useful instead of in runtime code.
	if _, err = dbp.setChanRecvBreakpoints(); err != nil {
		return
	}

	// Get the goroutine for the current thread. We will
	// use it later in order to ensure we are on the same
	// goroutine.
	g, err := dbp.CurrentThread.GetG()
	if err != nil {
		return err
	}

	var goroutineExiting bool
	if err = dbp.CurrentThread.setNextBreakpoints(); err != nil {
		switch t := err.(type) {
		case ThreadBlockedError, NoReturnAddr: // Noop
		case GoroutineExitingError:
			goroutineExiting = t.goid == g.Id
		default:
			return
		}
	}

	for _, th := range dbp.Threads {
		if err = th.Continue(); err != nil {
			return
		}
	}

	for {
		th, err := dbp.trapWait(-1)
		if err != nil {
			return err
		}
		tg, err := th.GetG()
		if err != nil {
			return err
		}
		// Make sure we're on the same goroutine, unless it has exited.
		if tg.Id == g.Id || goroutineExiting {
			// Check to see if the goroutine has switched to another
			// thread, if so make it the current thread.
			if dbp.CurrentThread.Id != th.Id {
				if err = dbp.SwitchThread(th.Id); err != nil {
					return err
				}
			}
			return nil
		}
		// This thread was not running our goroutine.
		// We continue it since our goroutine could
		// potentially be on this threads queue.
		if err = th.Continue(); err != nil {
			return err
		}
	}
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
			if _, err = dbp.SetTempBreakpoint(ret); err != nil {
				return 0, err
			}
			count++
		}
	}
	return count, nil
}

// Resume process.
func (dbp *Process) Continue() error {
	for _, thread := range dbp.Threads {
		err := thread.Continue()
		if err != nil {
			return fmt.Errorf("could not continue thread %d %s", thread.Id, err)
		}
	}
	return dbp.run(func() error {
		thread, err := dbp.trapWait(-1)
		if err != nil {
			return err
		}
		if err := dbp.Halt(); err != nil {
			return err
		}
		if dbp.CurrentThread != thread {
			dbp.SwitchThread(thread.Id)
		}
		loc, err := thread.Location()
		if err != nil {
			return err
		}
		// Check to see if we hit a runtime.breakpoint
		if loc.Fn != nil && loc.Fn.Name == "runtime.breakpoint" {
			// Step twice to get back to user code
			for i := 0; i < 2; i++ {
				if err = thread.Step(); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// Single step, will execute a single instruction.
func (dbp *Process) Step() (err error) {
	fn := func() error {
		for _, th := range dbp.Threads {
			if th.blocked() {
				continue
			}
			if err := th.Step(); err != nil {
				return err
			}
		}
		return nil
	}

	return dbp.run(fn)
}

// Change from current thread to the thread specified by `tid`.
func (dbp *Process) SwitchThread(tid int) error {
	if th, ok := dbp.Threads[tid]; ok {
		dbp.CurrentThread = th
		return nil
	}
	return fmt.Errorf("thread %d does not exist", tid)
}

// Returns an array of G structures representing the information
// Delve cares about from the internal runtime G structure.
func (dbp *Process) GoroutinesInfo() ([]*G, error) {
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
			threadg[g.Id] = dbp.Threads[i]
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
	allgentryaddr, err := rdr.AddrFor("runtime.allg")
	if err != nil {
		return nil, err
	}
	faddr, err := dbp.CurrentThread.readMemory(uintptr(allgentryaddr), dbp.arch.PtrSize())
	allgptr := binary.LittleEndian.Uint64(faddr)

	for i := uint64(0); i < allglen; i++ {
		g, err := parseG(dbp.CurrentThread, allgptr+(i*uint64(dbp.arch.PtrSize())), true)
		if err != nil {
			return nil, err
		}
		if thread, allocated := threadg[g.Id]; allocated {
			loc, err := thread.Location()
			if err != nil {
				return nil, err
			}
			g.thread = thread
			// Prefer actual thread location information.
			g.File = loc.File
			g.Line = loc.Line
			g.Func = loc.Fn
		}
		allg = append(allg, g)
	}
	return allg, nil
}

// Stop all threads.
func (dbp *Process) Halt() (err error) {
	for _, th := range dbp.Threads {
		if err := th.Halt(); err != nil {
			return err
		}
	}
	return nil
}

// Obtains register values from what Delve considers to be the current
// thread of the traced process.
func (dbp *Process) Registers() (Registers, error) {
	return dbp.CurrentThread.Registers()
}

// Returns the PC of the current thread.
func (dbp *Process) PC() (uint64, error) {
	return dbp.CurrentThread.PC()
}

// Returns the PC of the current thread.
func (dbp *Process) CurrentBreakpoint() *Breakpoint {
	return dbp.CurrentThread.CurrentBreakpoint
}

// Returns the value of the named symbol.
func (dbp *Process) EvalVariable(name string) (*Variable, error) {
	return dbp.CurrentThread.EvalVariable(name)
}

// Returns a reader for the dwarf data
func (dbp *Process) DwarfReader() *reader.Reader {
	return reader.New(dbp.dwarf)
}

// Returns list of source files that comprise the debugged binary.
func (dbp *Process) Sources() map[string]*gosym.Obj {
	return dbp.goSymTable.Files
}

// Returns list of functions present in the debugged program.
func (dbp *Process) Funcs() []gosym.Func {
	return dbp.goSymTable.Funcs
}

// Converts an instruction address to a file/line/function.
func (dbp *Process) PCToLine(pc uint64) (string, int, *gosym.Func) {
	return dbp.goSymTable.PCToLine(pc)
}

// Finds the breakpoint for the given ID.
func (dbp *Process) FindBreakpointByID(id int) (*Breakpoint, bool) {
	for _, bp := range dbp.Breakpoints {
		if bp.ID == id {
			return bp, true
		}
	}
	return nil, false
}

// Finds the breakpoint for the given pc.
func (dbp *Process) FindBreakpoint(pc uint64) (*Breakpoint, bool) {
	// Check for software breakpoint. PC will be at
	// breakpoint instruction + size of breakpoint.
	if bp, ok := dbp.Breakpoints[pc-uint64(dbp.arch.BreakpointSize())]; ok {
		return bp, true
	}
	// Check for hardware breakpoint. PC will equal
	// the breakpoint address since the CPU will stop
	// the process without executing the instruction at
	// this address.
	if bp, ok := dbp.Breakpoints[pc]; ok {
		return bp, true
	}
	return nil, false
}

// Returns a new Process struct.
func initializeDebugProcess(dbp *Process, path string, attach bool) (*Process, error) {
	if attach {
		var err error
		dbp.execPtraceFunc(func() { err = sys.PtraceAttach(dbp.Pid) })
		if err != nil {
			return nil, err
		}
		_, _, err = wait(dbp.Pid, dbp.Pid, 0)
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

	if err := dbp.updateThreadList(); err != nil {
		return nil, err
	}

	switch runtime.GOARCH {
	case "amd64":
		dbp.arch = AMD64Arch()
	}

	ver, isextld, err := dbp.getGoInformation()
	if err != nil {
		return nil, err
	}

	dbp.arch.SetGStructOffset(ver, isextld)

	return dbp, nil
}

func (dbp *Process) clearTempBreakpoints() error {
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

func (dbp *Process) handleBreakpointOnThread(id int) (*Thread, error) {
	thread, ok := dbp.Threads[id]
	if !ok {
		return nil, fmt.Errorf("could not find thread for %d", id)
	}
	pc, err := thread.PC()
	if err != nil {
		return nil, err
	}
	// Check to see if we have hit a breakpoint.
	if bp, ok := dbp.FindBreakpoint(pc); ok {
		thread.CurrentBreakpoint = bp
		if err = thread.SetPC(bp.Addr); err != nil {
			return nil, err
		}
		return thread, nil
	}
	if dbp.halt {
		return thread, nil
	}
	fn := dbp.goSymTable.PCToFunc(pc)
	if fn != nil && fn.Name == "runtime.breakpoint" {
		for i := 0; i < 2; i++ {
			if err := thread.Step(); err != nil {
				return nil, err
			}
		}
		return thread, nil
	}
	return nil, NoBreakpointError{addr: pc}
}

func (dbp *Process) run(fn func() error) error {
	if dbp.exited {
		return fmt.Errorf("process has already exited")
	}
	for _, th := range dbp.Threads {
		th.CurrentBreakpoint = nil
	}
	if err := fn(); err != nil {
		return err
	}
	return nil
}

func (dbp *Process) handlePtraceFuncs() {
	// We must ensure here that we are running on the same thread during
	// the execution of dbg. This is due to the fact that ptrace(2) expects
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
	vv, err := dbp.CurrentThread.EvalPackageVariable("runtime.buildVersion")
	if err != nil {
		err = fmt.Errorf("Could not determine version number: %v\n", err)
		return
	}

	ver, ok := parseVersionString(vv.Value)
	if !ok {
		err = fmt.Errorf("Could not parse version number: %s\n", vv.Value)
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
