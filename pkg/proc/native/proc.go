package native

import (
	"os"
	"runtime"

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

	// Thread used to read and write memory
	memthread *nativeThread

	os           *osProcessDetails
	firstStart   bool
	ptraceThread *ptraceThread
	childProcess bool // this process was launched, not attached to
	followExec   bool // automatically attach to new processes

	// Controlling terminal file descriptor for
	// this process.
	ctty *os.File

	iscgo bool

	exited, detached bool
}

// newProcess returns an initialized Process struct. Before returning,
// it will also launch a goroutine in order to handle ptrace(2)
// functions. For more information, see the documentation on
// `handlePtraceFuncs`.
func newProcess(pid int) *nativeProcess {
	dbp := &nativeProcess{
		pid:          pid,
		threads:      make(map[int]*nativeThread),
		breakpoints:  proc.NewBreakpointMap(),
		firstStart:   true,
		os:           new(osProcessDetails),
		ptraceThread: newPtraceThread(),
		bi:           proc.NewBinaryInfo(runtime.GOOS, runtime.GOARCH),
	}
	return dbp
}

// newChildProcess is like newProcess but uses the same ptrace thread as dbp.
func newChildProcess(dbp *nativeProcess, pid int) *nativeProcess {
	return &nativeProcess{
		pid:          pid,
		threads:      make(map[int]*nativeThread),
		breakpoints:  proc.NewBreakpointMap(),
		firstStart:   true,
		os:           new(osProcessDetails),
		ptraceThread: dbp.ptraceThread.acquire(),
		bi:           proc.NewBinaryInfo(runtime.GOOS, runtime.GOARCH),
	}
}

// BinInfo will return the binary info struct associated with this process.
func (dbp *nativeProcess) BinInfo() *proc.BinaryInfo {
	return dbp.bi
}

// StartCallInjection notifies the backend that we are about to inject a function call.
func (dbp *nativeProcess) StartCallInjection() (func(), error) { return func() {}, nil }

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
		return false, proc.ErrProcessExited{Pid: dbp.pid}
	}
	return true, nil
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

// Memory returns the process memory.
func (dbp *nativeProcess) Memory() proc.MemoryReadWriter {
	return dbp.memthread
}

// Breakpoints returns a list of breakpoints currently set.
func (dbp *nativeProcess) Breakpoints() *proc.BreakpointMap {
	return &dbp.breakpoints
}

// RequestManualStop sets the `manualStopRequested` flag and
// sends SIGSTOP to all threads.
func (dbp *nativeProcess) RequestManualStop(cctx *proc.ContinueOnceContext) error {
	if dbp.exited {
		return proc.ErrProcessExited{Pid: dbp.pid}
	}
	return dbp.requestManualStop()
}

func (dbp *nativeProcess) WriteBreakpoint(bp *proc.Breakpoint) error {
	if bp.WatchType != 0 {
		for _, thread := range dbp.threads {
			err := thread.writeHardwareBreakpoint(bp.Addr, bp.WatchType, bp.HWBreakIndex)
			if err != nil {
				return err
			}
		}
		return nil
	}

	bp.OriginalData = make([]byte, dbp.bi.Arch.BreakpointSize())
	_, err := dbp.memthread.ReadMemory(bp.OriginalData, bp.Addr)
	if err != nil {
		return err
	}
	return dbp.writeSoftwareBreakpoint(dbp.memthread, bp.Addr)
}

func (dbp *nativeProcess) EraseBreakpoint(bp *proc.Breakpoint) error {
	if bp.WatchType != 0 {
		for _, thread := range dbp.threads {
			err := thread.clearHardwareBreakpoint(bp.Addr, bp.WatchType, bp.HWBreakIndex)
			if err != nil {
				return err
			}
		}
		return nil
	}

	return dbp.memthread.clearSoftwareBreakpoint(bp)
}

type processGroup struct {
	procs     []*nativeProcess
	addTarget proc.AddTargetFunc
}

func (procgrp *processGroup) numValid() int {
	n := 0
	for _, p := range procgrp.procs {
		if ok, _ := p.Valid(); ok {
			n++
		}
	}
	return n
}

func (procgrp *processGroup) procForThread(tid int) *nativeProcess {
	for _, p := range procgrp.procs {
		if p.threads[tid] != nil {
			return p
		}
	}
	return nil
}

func (procgrp *processGroup) add(p *nativeProcess, pid int, currentThread proc.Thread, path string, stopReason proc.StopReason, cmdline string) (*proc.Target, error) {
	tgt, err := procgrp.addTarget(p, pid, currentThread, path, stopReason, cmdline)
	if err != nil {
		return nil, err
	}
	if tgt != nil {
		procgrp.procs = append(procgrp.procs, p)
	}
	return tgt, nil
}

func (procgrp *processGroup) ContinueOnce(cctx *proc.ContinueOnceContext) (proc.Thread, proc.StopReason, error) {
	if len(procgrp.procs) != 1 && runtime.GOOS != "linux" {
		panic("not implemented")
	}
	if procgrp.numValid() == 0 {
		return nil, proc.StopExited, proc.ErrProcessExited{Pid: procgrp.procs[0].pid}
	}

	for {
		for _, dbp := range procgrp.procs {
			if dbp.exited {
				continue
			}
			if err := dbp.resume(); err != nil {
				return nil, proc.StopUnknown, err
			}
			for _, th := range dbp.threads {
				th.CurrentBreakpoint.Clear()
			}
		}

		if cctx.ResumeChan != nil {
			close(cctx.ResumeChan)
			cctx.ResumeChan = nil
		}

		trapthread, err := trapWait(procgrp, -1)
		if err != nil {
			return nil, proc.StopUnknown, err
		}
		trapthread, err = procgrp.stop(cctx, trapthread)
		if err != nil {
			return nil, proc.StopUnknown, err
		}
		if trapthread != nil {
			dbp := procgrp.procForThread(trapthread.ID)
			dbp.memthread = trapthread
			// refresh memthread for every other process
			for _, p2 := range procgrp.procs {
				if p2.exited || p2 == dbp {
					continue
				}
				for _, th := range p2.threads {
					p2.memthread = th
					if th.SoftExc() {
						break
					}
				}
			}
			return trapthread, proc.StopUnknown, nil
		}
	}
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

func (dbp *nativeProcess) initializeBasic() (string, error) {
	cmdline, err := initialize(dbp)
	if err != nil {
		return "", err
	}
	if err := dbp.updateThreadList(); err != nil {
		return "", err
	}
	return cmdline, nil
}

// initialize will ensure that all relevant information is loaded
// so the process is ready to be debugged.
func (dbp *nativeProcess) initialize(path string, debugInfoDirs []string) (*proc.TargetGroup, error) {
	cmdline, err := dbp.initializeBasic()
	if err != nil {
		return nil, err
	}
	stopReason := proc.StopLaunched
	if !dbp.childProcess {
		stopReason = proc.StopAttached
	}
	procgrp := &processGroup{}
	grp, addTarget := proc.NewGroup(procgrp, proc.NewTargetGroupConfig{
		DebugInfoDirs: debugInfoDirs,

		// We disable asyncpreempt for the following reasons:
		//  - on Windows asyncpreempt is incompatible with debuggers, see:
		//    https://github.com/golang/go/issues/36494
		//  - on linux/arm64 asyncpreempt can sometimes restart a sequence of
		//    instructions, if the sequence happens to contain a breakpoint it will
		//    look like the breakpoint was hit twice when it was "logically" only
		//    executed once.
		//    See: https://go-review.googlesource.com/c/go/+/208126
		DisableAsyncPreempt: runtime.GOOS == "windows" || (runtime.GOOS == "linux" && runtime.GOARCH == "arm64"),

		StopReason: stopReason,
		CanDump:    runtime.GOOS == "linux" || runtime.GOOS == "freebsd" || (runtime.GOOS == "windows" && runtime.GOARCH == "amd64"),
	})
	procgrp.addTarget = addTarget
	tgt, err := procgrp.add(dbp, dbp.pid, dbp.memthread, path, stopReason, cmdline)
	if err != nil {
		return nil, err
	}
	if dbp.bi.Arch.Name == "arm64" {
		dbp.iscgo = tgt.IsCgo()
	}
	return grp, nil
}

func (pt *ptraceThread) handlePtraceFuncs() {
	// We must ensure here that we are running on the same thread during
	// while invoking the ptrace(2) syscall. This is due to the fact that ptrace(2) expects
	// all commands after PTRACE_ATTACH to come from the same thread.
	runtime.LockOSThread()

	// Leaving the OS thread locked currently leads to segfaults in the
	// Go runtime while running on FreeBSD and OpenBSD:
	//   https://github.com/golang/go/issues/52394
	if runtime.GOOS == "freebsd" || runtime.GOOS == "openbsd" {
		defer runtime.UnlockOSThread()
	}

	for fn := range pt.ptraceChan {
		fn()
		pt.ptraceDoneChan <- nil
	}
}

func (dbp *nativeProcess) execPtraceFunc(fn func()) {
	dbp.ptraceThread.ptraceChan <- fn
	<-dbp.ptraceThread.ptraceDoneChan
}

func (dbp *nativeProcess) postExit() {
	dbp.exited = true
	dbp.ptraceThread.release()
	dbp.bi.Close()
	if dbp.ctty != nil {
		dbp.ctty.Close()
	}
	dbp.os.Close()
}

func (dbp *nativeProcess) writeSoftwareBreakpoint(thread *nativeThread, addr uint64) error {
	_, err := thread.WriteMemory(addr, dbp.bi.Arch.BreakpointInstruction())
	return err
}

func openRedirects(redirects [3]string, foreground bool) (stdin, stdout, stderr *os.File, closefn func(), err error) {
	toclose := []*os.File{}

	if redirects[0] != "" {
		stdin, err = os.Open(redirects[0])
		if err != nil {
			return nil, nil, nil, nil, err
		}
		toclose = append(toclose, stdin)
	} else if foreground {
		stdin = os.Stdin
	}

	create := func(path string, dflt *os.File) *os.File {
		if path == "" {
			return dflt
		}
		var f *os.File
		f, err = os.Create(path)
		if f != nil {
			toclose = append(toclose, f)
		}
		return f
	}

	stdout = create(redirects[1], os.Stdout)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	stderr = create(redirects[2], os.Stderr)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	closefn = func() {
		for _, f := range toclose {
			_ = f.Close()
		}
	}

	return stdin, stdout, stderr, closefn, nil
}

type ptraceThread struct {
	ptraceRefCnt   int
	ptraceChan     chan func()
	ptraceDoneChan chan interface{}
}

func newPtraceThread() *ptraceThread {
	pt := &ptraceThread{
		ptraceChan:     make(chan func()),
		ptraceDoneChan: make(chan interface{}),
		ptraceRefCnt:   1,
	}
	go pt.handlePtraceFuncs()
	return pt
}

func (pt *ptraceThread) acquire() *ptraceThread {
	pt.ptraceRefCnt++
	return pt
}

func (pt *ptraceThread) release() {
	pt.ptraceRefCnt--
	if pt.ptraceRefCnt == 0 {
		close(pt.ptraceChan)
		close(pt.ptraceDoneChan)
	}
}
