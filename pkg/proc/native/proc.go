package native

import (
	"os"
	"runtime"
	"sync"

	"github.com/go-delve/delve/pkg/proc"
)

// Process represents all of the information the debugger
// is holding onto regarding the process we are debugging.
type nativeProcess struct {
	bi *proc.BinaryInfo

	pid int // Process Pid

	// Breakpoint table, holds information on breakpoints.
	// Maps instruction address to Breakpoint struct.
	breakpoints proc.BreakpointMap

	// List of threads mapped as such: pid -> *Thread
	threads map[int]*nativeThread

	// Active thread
	currentThread *nativeThread

	os                  *osProcessDetails
	firstStart          bool
	stopMu              sync.Mutex
	resumeChan          chan<- struct{}
	ptraceChan          chan func()
	ptraceDoneChan      chan interface{}
	childProcess        bool // this process was launched, not attached to
	manualStopRequested bool

	// Controlling terminal file descriptor for
	// this process.
	ctty *os.File

	insidePtraceThread bool // currently executing inside the ptrace thread

	exited, detached bool
}

var _ proc.ProcessInternal = &nativeProcess{}

// newProcess returns an initialized Process struct. Before returning,
// it will also launch a goroutine in order to handle ptrace(2)
// functions. For more information, see the documentation on
// `handlePtraceFuncs`.
func newProcess(pid int) *nativeProcess {
	dbp := &nativeProcess{
		pid:            pid,
		threads:        make(map[int]*nativeThread),
		breakpoints:    proc.NewBreakpointMap(),
		firstStart:     true,
		os:             new(osProcessDetails),
		ptraceChan:     make(chan func()),
		ptraceDoneChan: make(chan interface{}),
		bi:             proc.NewBinaryInfo(runtime.GOOS, runtime.GOARCH),
	}
	go dbp.handlePtraceFuncs()
	return dbp
}

// BinInfo will return the binary info struct associated with this process.
func (dbp *nativeProcess) BinInfo() *proc.BinaryInfo {
	return dbp.bi
}

// Recorded always returns false for the native proc backend.
func (dbp *nativeProcess) Recorded() (bool, string) { return false, "" }

// Restart will always return an error in the native proc backend, only for
// recorded traces.
func (dbp *nativeProcess) Restart(string) error { return proc.ErrNotRecorded }

// ChangeDirection will always return an error in the native proc backend, only for
// recorded traces.
func (dbp *nativeProcess) ChangeDirection(dir proc.Direction) error {
	if dir != proc.Forward {
		return proc.ErrNotRecorded
	}
	return nil
}

// GetDirection will always return Forward.
func (p *nativeProcess) GetDirection() proc.Direction { return proc.Forward }

// When will always return an empty string and nil, not supported on native proc backend.
func (dbp *nativeProcess) When() (string, error) { return "", nil }

// Checkpoint will always return an error on the native proc backend,
// only supported for recorded traces.
func (dbp *nativeProcess) Checkpoint(string) (int, error) { return -1, proc.ErrNotRecorded }

// Checkpoints will always return an error on the native proc backend,
// only supported for recorded traces.
func (dbp *nativeProcess) Checkpoints() ([]proc.Checkpoint, error) { return nil, proc.ErrNotRecorded }

// ClearCheckpoint will always return an error on the native proc backend,
// only supported in recorded traces.
func (dbp *nativeProcess) ClearCheckpoint(int) error { return proc.ErrNotRecorded }

// Detach from the process being debugged, optionally killing it.
func (dbp *nativeProcess) Detach(kill bool) (err error) {
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
func (dbp *nativeProcess) Valid() (bool, error) {
	if dbp.detached {
		return false, proc.ErrProcessDetached
	}
	if dbp.exited {
		return false, &proc.ErrProcessExited{Pid: dbp.Pid()}
	}
	return true, nil
}

// ResumeNotify specifies a channel that will be closed the next time
// ContinueOnce finishes resuming the target.
func (dbp *nativeProcess) ResumeNotify(ch chan<- struct{}) {
	dbp.resumeChan = ch
}

// Pid returns the process ID.
func (dbp *nativeProcess) Pid() int {
	return dbp.pid
}

// ThreadList returns a list of threads in the process.
func (dbp *nativeProcess) ThreadList() []proc.Thread {
	r := make([]proc.Thread, 0, len(dbp.threads))
	for _, v := range dbp.threads {
		r = append(r, v)
	}
	return r
}

// FindThread attempts to find the thread with the specified ID.
func (dbp *nativeProcess) FindThread(threadID int) (proc.Thread, bool) {
	th, ok := dbp.threads[threadID]
	return th, ok
}

// CurrentThread returns the current selected, active thread.
func (dbp *nativeProcess) CurrentThread() proc.Thread {
	return dbp.currentThread
}

// SetCurrentThread is used internally by proc.Target to change the current thread.
func (p *nativeProcess) SetCurrentThread(th proc.Thread) {
	p.currentThread = th.(*nativeThread)
}

// Breakpoints returns a list of breakpoints currently set.
func (dbp *nativeProcess) Breakpoints() *proc.BreakpointMap {
	return &dbp.breakpoints
}

// RequestManualStop sets the `halt` flag and
// sends SIGSTOP to all threads.
func (dbp *nativeProcess) RequestManualStop() error {
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
func (dbp *nativeProcess) CheckAndClearManualStopRequest() bool {
	dbp.stopMu.Lock()
	defer dbp.stopMu.Unlock()

	msr := dbp.manualStopRequested
	dbp.manualStopRequested = false

	return msr
}

func (dbp *nativeProcess) WriteBreakpoint(addr uint64) (string, int, *proc.Function, []byte, error) {
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

func (dbp *nativeProcess) EraseBreakpoint(bp *proc.Breakpoint) error {
	return dbp.currentThread.ClearBreakpoint(bp)
}

// ContinueOnce will continue the target until it stops.
// This could be the result of a breakpoint or signal.
func (dbp *nativeProcess) ContinueOnce() (proc.Thread, proc.StopReason, error) {
	if dbp.exited {
		return nil, proc.StopExited, &proc.ErrProcessExited{Pid: dbp.Pid()}
	}

	if err := dbp.resume(); err != nil {
		return nil, proc.StopUnknown, err
	}

	for _, th := range dbp.threads {
		th.CurrentBreakpoint.Clear()
	}

	if dbp.resumeChan != nil {
		close(dbp.resumeChan)
		dbp.resumeChan = nil
	}

	trapthread, err := dbp.trapWait(-1)
	if err != nil {
		return nil, proc.StopUnknown, err
	}
	if err := dbp.stop(trapthread); err != nil {
		return nil, proc.StopUnknown, err
	}
	return trapthread, proc.StopUnknown, err
}

// FindBreakpoint finds the breakpoint for the given pc.
func (dbp *nativeProcess) FindBreakpoint(pc uint64, adjustPC bool) (*proc.Breakpoint, bool) {
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
func (dbp *nativeProcess) initialize(path string, debugInfoDirs []string) (*proc.Target, error) {
	if err := initialize(dbp); err != nil {
		return nil, err
	}
	if err := dbp.updateThreadList(); err != nil {
		return nil, err
	}
	stopReason := proc.StopLaunched
	if !dbp.childProcess {
		stopReason = proc.StopAttached
	}
	return proc.NewTarget(dbp, proc.NewTargetConfig{
		Path:                path,
		DebugInfoDirs:       debugInfoDirs,
		DisableAsyncPreempt: runtime.GOOS == "windows" || runtime.GOOS == "freebsd",
		StopReason:          stopReason})
}

func (dbp *nativeProcess) handlePtraceFuncs() {
	// We must ensure here that we are running on the same thread during
	// while invoking the ptrace(2) syscall. This is due to the fact that ptrace(2) expects
	// all commands after PTRACE_ATTACH to come from the same thread.
	runtime.LockOSThread()

	for fn := range dbp.ptraceChan {
		dbp.insidePtraceThread = true
		fn()
		dbp.insidePtraceThread = false
		dbp.ptraceDoneChan <- nil
	}
	close(dbp.ptraceDoneChan)
}

func (dbp *nativeProcess) execPtraceFunc(fn func()) {
	if dbp.insidePtraceThread {
		fn()
		return
	}
	dbp.ptraceChan <- fn
	<-dbp.ptraceDoneChan
}

// ExecOnMagicThread executes fn on the thread that can make ptrace
// calls (or the equivalent of ptrace on windows). If fn does a lot of calls
// to other methods of Process that need to do ptrace calls this will speed
// it up considerably by avoiding a large number of context switches.
// It is very important that fn does not panic, you must defer a recover
// call here.
//
// This function works by calling fn in the ptrace goroutine and then
// disabling the code that sends ptrace calls to that goroutine, executing
// them directly instead.
// It is very important that only the goroutine that calls this function
// will attempt to execute ptrace calls, otherwise the ptrace calls will be
// executed by a goroutine that can't execute them and return the ESRCH / no
// such process (or access denied on windows).
// For Delve this property is ensured by the debugger package which takes
// care to serialize every call to pkg/proc.
// The only risky thing is function call injection which happens inside a
// different goroutine.
func (dbp *nativeProcess) ExecOnMagicThread(fn func()) {
	dbp.execPtraceFunc(fn)
}

func (dbp *nativeProcess) postExit() {
	dbp.exited = true
	close(dbp.ptraceChan)
	dbp.bi.Close()
	if dbp.ctty != nil {
		dbp.ctty.Close()
	}
}

func (dbp *nativeProcess) writeSoftwareBreakpoint(thread *nativeThread, addr uint64) error {
	_, err := thread.WriteMemory(uintptr(addr), dbp.bi.Arch.BreakpointInstruction())
	return err
}
