package native

import (
	"fmt"
	"go/ast"
	"runtime"
	"sync"

	"github.com/go-delve/delve/pkg/proc"
)

// Process represents all of the information the debugger
// is holding onto regarding the process we are debugging.
type Process struct {
	bi *proc.BinaryInfo

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

	common              proc.CommonProcess
	os                  *OSProcessDetails
	firstStart          bool
	stopMu              sync.Mutex
	resumeChan          chan<- struct{}
	ptraceChan          chan func()
	ptraceDoneChan      chan interface{}
	childProcess        bool // this process was launched, not attached to
	manualStopRequested bool

	exited, detached bool
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

// BinInfo will return the binary info struct associated with this process.
func (dbp *Process) BinInfo() *proc.BinaryInfo {
	return dbp.bi
}

// Recorded always returns false for the native proc backend.
func (dbp *Process) Recorded() (bool, string) { return false, "" }

// Restart will always return an error in the native proc backend, only for
// recorded traces.
func (dbp *Process) Restart(string) error { return proc.ErrNotRecorded }

// Direction will always return an error in the native proc backend, only for
// recorded traces.
func (dbp *Process) Direction(proc.Direction) error { return proc.ErrNotRecorded }

// When will always return an empty string and nil, not supported on native proc backend.
func (dbp *Process) When() (string, error) { return "", nil }

// Checkpoint will always return an error on the native proc backend,
// only supported for recorded traces.
func (dbp *Process) Checkpoint(string) (int, error) { return -1, proc.ErrNotRecorded }

// Checkpoints will always return an error on the native proc backend,
// only supported for recorded traces.
func (dbp *Process) Checkpoints() ([]proc.Checkpoint, error) { return nil, proc.ErrNotRecorded }

// ClearCheckpoint will always return an error on the native proc backend,
// only supported in recorded traces.
func (dbp *Process) ClearCheckpoint(int) error { return proc.ErrNotRecorded }

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
	dbp.detached = true
	dbp.postExit()
	return
}

// Valid returns whether the process is still attached to and
// has not exited.
func (dbp *Process) Valid() (bool, error) {
	if dbp.detached {
		return false, &proc.ProcessDetachedError{}
	}
	if dbp.exited {
		return false, &proc.ErrProcessExited{Pid: dbp.Pid()}
	}
	return true, nil
}

// ResumeNotify specifies a channel that will be closed the next time
// ContinueOnce finishes resuming the target.
func (dbp *Process) ResumeNotify(ch chan<- struct{}) {
	dbp.resumeChan = ch
}

// Pid returns the process ID.
func (dbp *Process) Pid() int {
	return dbp.pid
}

// SelectedGoroutine returns the current selected,
// active goroutine.
func (dbp *Process) SelectedGoroutine() *proc.G {
	return dbp.selectedGoroutine
}

// ThreadList returns a list of threads in the process.
func (dbp *Process) ThreadList() []proc.Thread {
	r := make([]proc.Thread, 0, len(dbp.threads))
	for _, v := range dbp.threads {
		r = append(r, v)
	}
	return r
}

// FindThread attempts to find the thread with the specified ID.
func (dbp *Process) FindThread(threadID int) (proc.Thread, bool) {
	th, ok := dbp.threads[threadID]
	return th, ok
}

// CurrentThread returns the current selected, active thread.
func (dbp *Process) CurrentThread() proc.Thread {
	return dbp.currentThread
}

// Breakpoints returns a list of breakpoints currently set.
func (dbp *Process) Breakpoints() *proc.BreakpointMap {
	return &dbp.breakpoints
}

// RequestManualStop sets the `halt` flag and
// sends SIGSTOP to all threads.
func (dbp *Process) RequestManualStop() error {
	if dbp.exited {
		return &proc.ErrProcessExited{Pid: dbp.Pid()}
	}
	dbp.stopMu.Lock()
	defer dbp.stopMu.Unlock()
	dbp.manualStopRequested = true
	return dbp.requestManualStop()
}

// CheckAndClearManualStopRequest checks if a manual stop has
// been requested, and then clears that state.
func (dbp *Process) CheckAndClearManualStopRequest() bool {
	dbp.stopMu.Lock()
	defer dbp.stopMu.Unlock()

	msr := dbp.manualStopRequested
	dbp.manualStopRequested = false

	return msr
}

func (dbp *Process) writeBreakpoint(addr uint64) (string, int, *proc.Function, []byte, error) {
	f, l, fn := dbp.bi.PCToLine(uint64(addr))

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
		return nil, &proc.ErrProcessExited{Pid: dbp.Pid()}
	}
	return dbp.breakpoints.Clear(addr, dbp.currentThread.ClearBreakpoint)
}

// ContinueOnce will continue the target until it stops.
// This could be the result of a breakpoint or signal.
func (dbp *Process) ContinueOnce() (proc.Thread, error) {
	if dbp.exited {
		return nil, &proc.ErrProcessExited{Pid: dbp.Pid()}
	}

	if err := dbp.resume(); err != nil {
		return nil, err
	}

	dbp.common.ClearAllGCache()
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
	if err := dbp.stop(trapthread); err != nil {
		return nil, err
	}
	return trapthread, err
}

// StepInstruction will continue the current thread for exactly
// one instruction. This method affects only the thread
// associated with the selected goroutine. All other
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
	dbp.common.ClearAllGCache()
	if dbp.exited {
		return &proc.ErrProcessExited{Pid: dbp.Pid()}
	}
	thread.CurrentBreakpoint.Clear()
	err = thread.StepInstruction()
	if err != nil {
		return err
	}
	err = thread.SetCurrentBreakpoint(true)
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
		return &proc.ErrProcessExited{Pid: dbp.Pid()}
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
		return &proc.ErrProcessExited{Pid: dbp.Pid()}
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

// FindBreakpoint finds the breakpoint for the given pc.
func (dbp *Process) FindBreakpoint(pc uint64, adjustPC bool) (*proc.Breakpoint, bool) {
	if adjustPC {
		// Check to see if address is past the breakpoint, (i.e. breakpoint was hit).
		if bp, ok := dbp.breakpoints.M[pc-uint64(dbp.bi.Arch.BreakpointSize())]; ok {
			return bp, true
		}
	}
	// Directly use addr to lookup breakpoint.
	if bp, ok := dbp.breakpoints.M[pc]; ok {
		return bp, true
	}
	return nil, false
}

// initialize will ensure that all relevant information is loaded
// so the process is ready to be debugged.
func (dbp *Process) initialize(path string, debugInfoDirs []string) error {
	if err := initialize(dbp); err != nil {
		return err
	}
	if err := dbp.updateThreadList(); err != nil {
		return err
	}
	return proc.PostInitializationSetup(dbp, path, debugInfoDirs, dbp.writeBreakpoint)
}

// SetSelectedGoroutine will set internally the goroutine that should be
// the default for any command executed, the goroutine being actively
// followed.
func (dbp *Process) SetSelectedGoroutine(g *proc.G) {
	dbp.selectedGoroutine = g
}

// ClearInternalBreakpoints will clear all non-user set breakpoints. These
// breakpoints are set for internal operations such as 'next'.
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

// Common returns common information across Process
// implementations
func (dbp *Process) Common() *proc.CommonProcess {
	return &dbp.common
}
