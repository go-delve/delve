package native

import (
	"fmt"
	"go/ast"
	"runtime"
	"sync"

	"github.com/derekparker/delve/pkg/proc"
)

// Process represents all of the information the debugger
// is holding onto regarding the process we are debugging.
type Process struct {
	bi  proc.BinaryInfo
	pid int // Process Pid

	// Breakpoint table, holds information on breakpoints.
	// Maps instruction address to Breakpoint struct.
	breakpoints proc.BreakpointMap

	// List of threads mapped as such: pid -> *Thread
	threads map[int]*Thread

	// Active thread
	currentThread *Thread

	// Goroutine that will be used by default to set breakpoint, eval variables, etc...
	// Normally selectedGoroutine is currentThread.GetG, it will not be only if SwitchGoroutine is called with a goroutine that isn't attached to a thread
	selectedGoroutine *proc.G

	allGCache           []*proc.G
	os                  *OSProcessDetails
	firstStart          bool
	haltMu              sync.Mutex
	halt                bool
	resumeChan          chan<- struct{}
	exited              bool
	ptraceChan          chan func()
	ptraceDoneChan      chan interface{}
	childProcess        bool // this process was launched, not attached to
	manualStopRequested bool
}

// New returns an initialized Process struct. Before returning,
// it will also launch a goroutine in order to handle ptrace(2)
// functions. For more information, see the documentation on
// `handlePtraceFuncs`.
func New(pid int) *Process {
	dbp := &Process{
		pid:            pid,
		threads:        make(map[int]*Thread),
		breakpoints:    proc.NewBreakpointMap(),
		firstStart:     true,
		os:             new(OSProcessDetails),
		ptraceChan:     make(chan func()),
		ptraceDoneChan: make(chan interface{}),
		bi:             proc.NewBinaryInfo(runtime.GOOS, runtime.GOARCH),
	}
	go dbp.handlePtraceFuncs()
	return dbp
}

func (dbp *Process) BinInfo() *proc.BinaryInfo {
	return &dbp.bi
}

func (dbp *Process) Recorded() (bool, string)                { return false, "" }
func (dbp *Process) Restart(string) error                    { return proc.NotRecordedErr }
func (dbp *Process) Direction(proc.Direction) error          { return proc.NotRecordedErr }
func (dbp *Process) When() (string, error)                   { return "", nil }
func (dbp *Process) Checkpoint(string) (int, error)          { return -1, proc.NotRecordedErr }
func (dbp *Process) Checkpoints() ([]proc.Checkpoint, error) { return nil, proc.NotRecordedErr }
func (dbp *Process) ClearCheckpoint(int) error               { return proc.NotRecordedErr }

// Detach from the process being debugged, optionally killing it.
func (dbp *Process) Detach(kill bool) (err error) {
	if dbp.exited {
		return nil
	}
	if kill && dbp.childProcess {
		err := dbp.kill()
		if err != nil {
			return err
		}
		dbp.bi.Close()
		return nil
	}
	if !kill {
		// Clean up any breakpoints we've set.
		for _, bp := range dbp.breakpoints.M {
			if bp != nil {
				_, err := dbp.ClearBreakpoint(bp.Addr)
				if err != nil {
					return err
				}
			}
		}
	}
	dbp.execPtraceFunc(func() {
		err = dbp.detach(kill)
		if err != nil {
			return
		}
		if kill {
			err = killProcess(dbp.pid)
		}
	})
	dbp.bi.Close()
	return
}

// Exited returns whether the debugged
// process has exited.
func (dbp *Process) Exited() bool {
	return dbp.exited
}

func (dbp *Process) ResumeNotify(ch chan<- struct{}) {
	dbp.resumeChan = ch
}

func (dbp *Process) Pid() int {
	return dbp.pid
}

func (dbp *Process) SelectedGoroutine() *proc.G {
	return dbp.selectedGoroutine
}

func (dbp *Process) ThreadList() []proc.Thread {
	r := make([]proc.Thread, 0, len(dbp.threads))
	for _, v := range dbp.threads {
		r = append(r, v)
	}
	return r
}

func (dbp *Process) FindThread(threadID int) (proc.Thread, bool) {
	th, ok := dbp.threads[threadID]
	return th, ok
}

func (dbp *Process) CurrentThread() proc.Thread {
	return dbp.currentThread
}

func (dbp *Process) Breakpoints() *proc.BreakpointMap {
	return &dbp.breakpoints
}

// LoadInformation finds the executable and then uses it
// to parse the following information:
// * Dwarf .debug_frame section
// * Dwarf .debug_line section
// * Go symbol table.
func (dbp *Process) LoadInformation(path string) error {
	var wg sync.WaitGroup

	path = findExecutable(path, dbp.pid)

	wg.Add(1)
	go dbp.loadProcessInformation(&wg)
	err := dbp.bi.LoadBinaryInfo(path, &wg)
	wg.Wait()
	if err == nil {
		err = dbp.bi.LoadError()
	}

	return err
}

// RequestManualStop sets the `halt` flag and
// sends SIGSTOP to all threads.
func (dbp *Process) RequestManualStop() error {
	if dbp.exited {
		return &proc.ProcessExitedError{Pid: dbp.Pid()}
	}
	dbp.haltMu.Lock()
	defer dbp.haltMu.Unlock()
	dbp.manualStopRequested = true
	dbp.halt = true
	return dbp.requestManualStop()
}

func (dbp *Process) CheckAndClearManualStopRequest() bool {
	dbp.haltMu.Lock()
	msr := dbp.manualStopRequested
	dbp.manualStopRequested = false
	dbp.haltMu.Unlock()
	return msr
}

func (dbp *Process) writeBreakpoint(addr uint64) (string, int, *proc.Function, []byte, error) {
	f, l, fn := dbp.bi.PCToLine(uint64(addr))
	if fn == nil {
		return "", 0, nil, nil, proc.InvalidAddressError{Address: addr}
	}

	originalData := make([]byte, dbp.bi.Arch.BreakpointSize())
	_, err := dbp.currentThread.ReadMemory(originalData, uintptr(addr))
	if err != nil {
		return "", 0, nil, nil, err
	}
	if err := dbp.writeSoftwareBreakpoint(dbp.currentThread, addr); err != nil {
		return "", 0, nil, nil, err
	}

	return f, l, fn, originalData, nil
}

// SetBreakpoint sets a breakpoint at addr, and stores it in the process wide
// break point table.
func (dbp *Process) SetBreakpoint(addr uint64, kind proc.BreakpointKind, cond ast.Expr) (*proc.Breakpoint, error) {
	return dbp.breakpoints.Set(addr, kind, cond, dbp.writeBreakpoint)
}

// ClearBreakpoint clears the breakpoint at addr.
func (dbp *Process) ClearBreakpoint(addr uint64) (*proc.Breakpoint, error) {
	if dbp.exited {
		return nil, &proc.ProcessExitedError{Pid: dbp.Pid()}
	}
	return dbp.breakpoints.Clear(addr, dbp.currentThread.ClearBreakpoint)
}

func (dbp *Process) ContinueOnce() (proc.Thread, error) {
	if dbp.exited {
		return nil, &proc.ProcessExitedError{Pid: dbp.Pid()}
	}

	if err := dbp.resume(); err != nil {
		return nil, err
	}

	dbp.allGCache = nil
	for _, th := range dbp.threads {
		th.CurrentBreakpoint.Clear()
	}

	if dbp.resumeChan != nil {
		close(dbp.resumeChan)
		dbp.resumeChan = nil
	}

	trapthread, err := dbp.trapWait(-1)
	if err != nil {
		return nil, err
	}
	if err := dbp.Halt(); err != nil {
		return nil, dbp.exitGuard(err)
	}
	if err := dbp.setCurrentBreakpoints(trapthread); err != nil {
		return nil, err
	}
	return trapthread, err
}

// StepInstruction will continue the current thread for exactly
// one instruction. This method affects only the thread
// asssociated with the selected goroutine. All other
// threads will remain stopped.
func (dbp *Process) StepInstruction() (err error) {
	thread := dbp.currentThread
	if dbp.selectedGoroutine != nil {
		if dbp.selectedGoroutine.Thread == nil {
			// Step called on parked goroutine
			if _, err := dbp.SetBreakpoint(dbp.selectedGoroutine.PC, proc.NextBreakpoint, proc.SameGoroutineCondition(dbp.selectedGoroutine)); err != nil {
				return err
			}
			return proc.Continue(dbp)
		}
		thread = dbp.selectedGoroutine.Thread.(*Thread)
	}
	dbp.allGCache = nil
	if dbp.exited {
		return &proc.ProcessExitedError{Pid: dbp.Pid()}
	}
	thread.CurrentBreakpoint.Clear()
	err = thread.StepInstruction()
	if err != nil {
		return err
	}
	err = thread.SetCurrentBreakpoint()
	if err != nil {
		return err
	}
	if g, _ := proc.GetG(thread); g != nil {
		dbp.selectedGoroutine = g
	}
	return nil
}

// SwitchThread changes from current thread to the thread specified by `tid`.
func (dbp *Process) SwitchThread(tid int) error {
	if dbp.exited {
		return &proc.ProcessExitedError{Pid: dbp.Pid()}
	}
	if th, ok := dbp.threads[tid]; ok {
		dbp.currentThread = th
		dbp.selectedGoroutine, _ = proc.GetG(dbp.currentThread)
		return nil
	}
	return fmt.Errorf("thread %d does not exist", tid)
}

// SwitchGoroutine changes from current thread to the thread
// running the specified goroutine.
func (dbp *Process) SwitchGoroutine(gid int) error {
	if dbp.exited {
		return &proc.ProcessExitedError{Pid: dbp.Pid()}
	}
	g, err := proc.FindGoroutine(dbp, gid)
	if err != nil {
		return err
	}
	if g == nil {
		// user specified -1 and selectedGoroutine is nil
		return nil
	}
	if g.Thread != nil {
		return dbp.SwitchThread(g.Thread.ThreadID())
	}
	dbp.selectedGoroutine = g
	return nil
}

// Halt stops all threads.
func (dbp *Process) Halt() (err error) {
	if dbp.exited {
		return &proc.ProcessExitedError{Pid: dbp.Pid()}
	}
	for _, th := range dbp.threads {
		if err := th.Halt(); err != nil {
			return err
		}
	}
	return nil
}

// FindBreakpoint finds the breakpoint for the given pc.
func (dbp *Process) FindBreakpoint(pc uint64) (*proc.Breakpoint, bool) {
	// Check to see if address is past the breakpoint, (i.e. breakpoint was hit).
	if bp, ok := dbp.breakpoints.M[pc-uint64(dbp.bi.Arch.BreakpointSize())]; ok {
		return bp, true
	}
	// Directly use addr to lookup breakpoint.
	if bp, ok := dbp.breakpoints.M[pc]; ok {
		return bp, true
	}
	return nil, false
}

// Returns a new Process struct.
func initializeDebugProcess(dbp *Process, path string) (*Process, error) {
	err := dbp.LoadInformation(path)
	if err != nil {
		return dbp, err
	}

	if err := dbp.updateThreadList(); err != nil {
		return dbp, err
	}

	// selectedGoroutine can not be set correctly by the call to updateThreadList
	// because without calling SetGStructOffset we can not read the G struct of currentThread
	// but without calling updateThreadList we can not examine memory to determine
	// the offset of g struct inside TLS
	dbp.selectedGoroutine, _ = proc.GetG(dbp.currentThread)

	panicpc, err := proc.FindFunctionLocation(dbp, "runtime.startpanic", true, 0)
	if err == nil {
		bp, err := dbp.breakpoints.SetWithID(-1, panicpc, dbp.writeBreakpoint)
		if err == nil {
			bp.Name = proc.UnrecoveredPanic
			bp.Variables = []string{"runtime.curg._panic.arg"}
		}
	}

	return dbp, nil
}

func (dbp *Process) ClearInternalBreakpoints() error {
	return dbp.breakpoints.ClearInternalBreakpoints(func(bp *proc.Breakpoint) error {
		if err := dbp.currentThread.ClearBreakpoint(bp); err != nil {
			return err
		}
		for _, thread := range dbp.threads {
			if thread.CurrentBreakpoint.Breakpoint == bp {
				thread.CurrentBreakpoint.Clear()
			}
		}
		return nil
	})
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

func (dbp *Process) postExit() {
	dbp.exited = true
	close(dbp.ptraceChan)
	close(dbp.ptraceDoneChan)
	dbp.bi.Close()
}

func (dbp *Process) writeSoftwareBreakpoint(thread *Thread, addr uint64) error {
	_, err := thread.WriteMemory(uintptr(addr), dbp.bi.Arch.BreakpointInstruction())
	return err
}

func (dbp *Process) AllGCache() *[]*proc.G {
	return &dbp.allGCache
}
