package native

import (
	"runtime"
	"sync"

	"github.com/go-delve/delve/pkg/proc"
)

// Process represents all of the information the debugger
// is holding onto regarding the process we are debugging.
type Process struct {
	t proc.Process

	pid int // Process Pid

	// List of threads mapped as such: pid -> *Thread
	threads map[int]*Thread

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
		firstStart:     true,
		os:             new(OSProcessDetails),
		ptraceChan:     make(chan func()),
		ptraceDoneChan: make(chan interface{}),
	}
	go dbp.handlePtraceFuncs()
	return dbp
}

func (dbp *Process) SetTarget(p proc.Process) {
	dbp.t = p
}

// BinInfo will return the binary info struct associated with this process.
func (dbp *Process) BinInfo() *proc.BinaryInfo {
	return dbp.t.BinInfo()
}

// Recorded always returns false for the native proc backend.
func (dbp *Process) Recorded() (bool, string) { return false, "" }

// Restart will always return an error in the native proc backend, only for
// recorded traces.
func (dbp *Process) Restart(string) error { return proc.ErrNotRecorded }

// Direction will always return an error in the native proc backend, only for
// recorded traces.
func (dbp *Process) ChangeDirection(proc.Direction) error { return proc.ErrNotRecorded }

func (p *Process) Direction() proc.Direction { return proc.Forward }

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
		dbp.BinInfo().Close()
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
// Resume finishes resuming the target.
func (dbp *Process) ResumeNotify(ch chan<- struct{}) {
	dbp.resumeChan = ch
}

// Pid returns the process ID.
func (dbp *Process) Pid() int {
	return dbp.pid
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

func (dbp *Process) WriteBreakpoint(addr uint64) (string, int, *proc.Function, []byte, error) {
	f, l, fn := dbp.BinInfo().PCToLine(uint64(addr))

	originalData := make([]byte, dbp.BinInfo().Arch.BreakpointSize())
	th := dbp.ThreadList()[0]
	_, err := th.ReadMemory(originalData, uintptr(addr))
	if err != nil {
		return "", 0, nil, nil, err
	}
	if err := dbp.writeSoftwareBreakpoint(th.(*Thread), addr); err != nil {
		return "", 0, nil, nil, err
	}

	return f, l, fn, originalData, nil
}

func (dbp *Process) ClearBreakpointFn(bp *proc.Breakpoint) error {
	th := dbp.ThreadList()[0]
	return th.(*Thread).ClearBreakpoint(bp)
}

// Resume will continue the target until it stops.
// This could be the result of a breakpoint or signal.
func (dbp *Process) Resume() (proc.Thread, error) {
	if dbp.exited {
		return nil, &proc.ErrProcessExited{Pid: dbp.Pid()}
	}

	if err := dbp.resume(); err != nil {
		return nil, err
	}

	dbp.common.ClearAllGCache()

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
	return dbp.t.StepInstruction()
}

func (p *Process) StepInstructionOut(thread proc.Thread, fn1, fn2 string) error {
	return p.t.StepInstructionOut(thread, fn1, fn2)
}

// Initialize will ensure that all relevant information is loaded
// so the process is ready to be debugged.
func (dbp *Process) Initialize() error {
	if err := initialize(dbp); err != nil {
		return err
	}
	return dbp.updateThreadList()
}

func (dbp *Process) ExecutablePath() string {
	return dbp.Common().ExePath
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
	dbp.BinInfo().Close()
}

func (dbp *Process) writeSoftwareBreakpoint(thread *Thread, addr uint64) error {
	_, err := thread.WriteMemory(uintptr(addr), dbp.BinInfo().Arch.BreakpointInstruction())
	return err
}

// Common returns common information across Process
// implementations
func (dbp *Process) Common() *proc.CommonProcess {
	return &dbp.common
}
