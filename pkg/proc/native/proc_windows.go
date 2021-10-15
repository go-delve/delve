package native

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	sys "golang.org/x/sys/windows"

	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/internal/ebpf"
	"github.com/go-delve/delve/pkg/proc/winutil"
)

// osProcessDetails holds Windows specific information.
type osProcessDetails struct {
	hProcess    syscall.Handle
	breakThread int
	entryPoint  uint64
	running     bool
}

func (os *osProcessDetails) Close() {}

// Launch creates and begins debugging a new process.
func Launch(cmd []string, wd string, flags proc.LaunchFlags, _ []string, _ string, redirects [3]string) (*proc.Target, error) {
	argv0Go, err := filepath.Abs(cmd[0])
	if err != nil {
		return nil, err
	}

	env := proc.DisableAsyncPreemptEnv()

	stdin, stdout, stderr, closefn, err := openRedirects(redirects, true)
	if err != nil {
		return nil, err
	}

	var p *os.Process
	dbp := newProcess(0)
	dbp.execPtraceFunc(func() {
		attr := &os.ProcAttr{
			Dir:   wd,
			Files: []*os.File{stdin, stdout, stderr},
			Sys: &syscall.SysProcAttr{
				CreationFlags: _DEBUG_ONLY_THIS_PROCESS,
			},
			Env: env,
		}
		p, err = os.StartProcess(argv0Go, cmd, attr)
	})
	closefn()
	if err != nil {
		return nil, err
	}
	defer p.Release()

	dbp.pid = p.Pid
	dbp.childProcess = true

	tgt, err := dbp.initialize(argv0Go, []string{})
	if err != nil {
		dbp.Detach(true)
		return nil, err
	}
	return tgt, nil
}

func initialize(dbp *nativeProcess) error {
	// It should not actually be possible for the
	// call to waitForDebugEvent to fail, since Windows
	// will always fire a CREATE_PROCESS_DEBUG_EVENT event
	// immediately after launching under DEBUG_ONLY_THIS_PROCESS.
	// Attaching with DebugActiveProcess has similar effect.
	var err error
	var tid, exitCode int
	dbp.execPtraceFunc(func() {
		tid, exitCode, err = dbp.waitForDebugEvent(waitBlocking)
	})
	if err != nil {
		return err
	}
	if tid == 0 {
		dbp.postExit()
		return proc.ErrProcessExited{Pid: dbp.pid, Status: exitCode}
	}
	// Suspend all threads so that the call to _ContinueDebugEvent will
	// not resume the target.
	for _, thread := range dbp.threads {
		if !thread.os.dbgUiRemoteBreakIn {
			_, err := _SuspendThread(thread.os.hThread)
			if err != nil {
				return err
			}
		}
	}

	dbp.execPtraceFunc(func() {
		err = _ContinueDebugEvent(uint32(dbp.pid), uint32(dbp.os.breakThread), _DBG_CONTINUE)
	})
	return err
}

// findExePath searches for process pid, and returns its executable path.
func findExePath(pid int) (string, error) {
	// Original code suggested different approach (see below).
	// Maybe it could be useful in the future.
	//
	// Find executable path from PID/handle on Windows:
	// https://msdn.microsoft.com/en-us/library/aa366789(VS.85).aspx

	p, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer syscall.CloseHandle(p)

	n := uint32(128)
	for {
		buf := make([]uint16, int(n))
		err = _QueryFullProcessImageName(p, 0, &buf[0], &n)
		switch err {
		case syscall.ERROR_INSUFFICIENT_BUFFER:
			// try bigger buffer
			n *= 2
			// but stop if it gets too big
			if n > 10000 {
				return "", err
			}
		case nil:
			return syscall.UTF16ToString(buf[:n]), nil
		default:
			return "", err
		}
	}
}

// Attach to an existing process with the given PID.
func Attach(pid int, _ []string) (*proc.Target, error) {
	dbp := newProcess(pid)
	var err error
	dbp.execPtraceFunc(func() {
		// TODO: Probably should have SeDebugPrivilege before starting here.
		err = _DebugActiveProcess(uint32(pid))
	})
	if err != nil {
		return nil, err
	}
	exepath, err := findExePath(pid)
	if err != nil {
		return nil, err
	}
	tgt, err := dbp.initialize(exepath, []string{})
	if err != nil {
		dbp.Detach(true)
		return nil, err
	}
	return tgt, nil
}

// kill kills the process.
func (dbp *nativeProcess) kill() error {
	if dbp.exited {
		return nil
	}

	p, err := os.FindProcess(dbp.pid)
	if err != nil {
		return err
	}
	defer p.Release()

	// TODO: Should not have to ignore failures here,
	// but some tests appear to Kill twice causing
	// this to fail on second attempt.
	_ = syscall.TerminateProcess(dbp.os.hProcess, 1)

	dbp.execPtraceFunc(func() {
		dbp.waitForDebugEvent(waitBlocking | waitDontHandleExceptions)
	})

	p.Wait()

	dbp.postExit()
	return nil
}

func (dbp *nativeProcess) requestManualStop() error {
	if !dbp.os.running {
		return nil
	}
	dbp.os.running = false
	return _DebugBreakProcess(dbp.os.hProcess)
}

func (dbp *nativeProcess) updateThreadList() error {
	// We ignore this request since threads are being
	// tracked as they are created/killed in waitForDebugEvent.
	return nil
}

func (dbp *nativeProcess) addThread(hThread syscall.Handle, threadID int, attach, suspendNewThreads bool, dbgUiRemoteBreakIn bool) (*nativeThread, error) {
	if thread, ok := dbp.threads[threadID]; ok {
		return thread, nil
	}
	thread := &nativeThread{
		ID:  threadID,
		dbp: dbp,
		os:  new(osSpecificDetails),
	}
	thread.os.dbgUiRemoteBreakIn = dbgUiRemoteBreakIn
	thread.os.hThread = hThread
	dbp.threads[threadID] = thread
	if dbp.memthread == nil {
		dbp.memthread = dbp.threads[threadID]
	}
	if suspendNewThreads && !dbgUiRemoteBreakIn {
		_, err := _SuspendThread(thread.os.hThread)
		if err != nil {
			return nil, err
		}
	}

	for _, bp := range dbp.Breakpoints().M {
		if bp.WatchType != 0 {
			err := thread.writeHardwareBreakpoint(bp.Addr, bp.WatchType, bp.HWBreakIndex)
			if err != nil {
				return nil, err
			}
		}
	}

	return thread, nil
}

func findExecutable(path string, pid int) string {
	return path
}

type waitForDebugEventFlags int

const (
	waitBlocking waitForDebugEventFlags = 1 << iota
	waitSuspendNewThreads
	waitDontHandleExceptions
)

const _MS_VC_EXCEPTION = 0x406D1388 // part of VisualC protocol to set thread names

func (dbp *nativeProcess) waitForDebugEvent(flags waitForDebugEventFlags) (threadID, exitCode int, err error) {
	var debugEvent _DEBUG_EVENT
	shouldExit := false
	for {
		continueStatus := uint32(_DBG_CONTINUE)
		var milliseconds uint32 = 0
		if flags&waitBlocking != 0 {
			milliseconds = syscall.INFINITE
		}
		// Wait for a debug event...
		err := _WaitForDebugEvent(&debugEvent, milliseconds)
		if err != nil {
			return 0, 0, err
		}

		// ... handle each event kind ...
		unionPtr := unsafe.Pointer(&debugEvent.U[0])
		switch debugEvent.DebugEventCode {
		case _CREATE_PROCESS_DEBUG_EVENT:
			debugInfo := (*_CREATE_PROCESS_DEBUG_INFO)(unionPtr)
			hFile := debugInfo.File
			if hFile != 0 && hFile != syscall.InvalidHandle {
				err = syscall.CloseHandle(hFile)
				if err != nil {
					return 0, 0, err
				}
			}
			dbp.os.entryPoint = uint64(debugInfo.BaseOfImage)
			dbp.os.hProcess = debugInfo.Process
			_, err = dbp.addThread(debugInfo.Thread, int(debugEvent.ThreadId), false,
				flags&waitSuspendNewThreads != 0, debugInfo.StartAddress == dbgUiRemoteBreakin.Addr())
			if err != nil {
				return 0, 0, err
			}
			break
		case _CREATE_THREAD_DEBUG_EVENT:
			debugInfo := (*_CREATE_THREAD_DEBUG_INFO)(unionPtr)
			_, err = dbp.addThread(debugInfo.Thread, int(debugEvent.ThreadId), false,
				flags&waitSuspendNewThreads != 0, debugInfo.StartAddress == dbgUiRemoteBreakin.Addr())
			if err != nil {
				return 0, 0, err
			}
			break
		case _EXIT_THREAD_DEBUG_EVENT:
			delete(dbp.threads, int(debugEvent.ThreadId))
			break
		case _OUTPUT_DEBUG_STRING_EVENT:
			//TODO: Handle debug output strings
			break
		case _LOAD_DLL_DEBUG_EVENT:
			debugInfo := (*_LOAD_DLL_DEBUG_INFO)(unionPtr)
			hFile := debugInfo.File
			if hFile != 0 && hFile != syscall.InvalidHandle {
				err = syscall.CloseHandle(hFile)
				if err != nil {
					return 0, 0, err
				}
			}
			break
		case _UNLOAD_DLL_DEBUG_EVENT:
			break
		case _RIP_EVENT:
			break
		case _EXCEPTION_DEBUG_EVENT:
			if flags&waitDontHandleExceptions != 0 {
				continueStatus = _DBG_EXCEPTION_NOT_HANDLED
				break
			}
			exception := (*_EXCEPTION_DEBUG_INFO)(unionPtr)
			tid := int(debugEvent.ThreadId)

			switch code := exception.ExceptionRecord.ExceptionCode; code {
			case _EXCEPTION_BREAKPOINT:

				// check if the exception address really is a breakpoint instruction, if
				// it isn't we already removed that breakpoint and we can't deal with
				// this exception anymore.
				atbp := true
				if thread, found := dbp.threads[tid]; found {
					data := make([]byte, dbp.bi.Arch.BreakpointSize())
					if _, err := thread.ReadMemory(data, uint64(exception.ExceptionRecord.ExceptionAddress)); err == nil {
						instr := dbp.bi.Arch.BreakpointInstruction()
						for i := range instr {
							if data[i] != instr[i] {
								atbp = false
								break
							}
						}
					}
					if !atbp {
						thread.setPC(uint64(exception.ExceptionRecord.ExceptionAddress))
					}
				}

				if atbp {
					dbp.os.breakThread = tid
					return tid, 0, nil
				} else {
					continueStatus = _DBG_CONTINUE
				}
			case _EXCEPTION_SINGLE_STEP:
				dbp.os.breakThread = tid
				return tid, 0, nil
			case _MS_VC_EXCEPTION:
				// This exception is sent to set the thread name in VisualC, we should
				// mask it or it might crash the program.
				continueStatus = _DBG_CONTINUE
			default:
				continueStatus = _DBG_EXCEPTION_NOT_HANDLED
			}
		case _EXIT_PROCESS_DEBUG_EVENT:
			debugInfo := (*_EXIT_PROCESS_DEBUG_INFO)(unionPtr)
			exitCode = int(debugInfo.ExitCode)
			shouldExit = true
		default:
			return 0, 0, fmt.Errorf("unknown debug event code: %d", debugEvent.DebugEventCode)
		}

		// .. and then continue unless we received an event that indicated we should break into debugger.
		err = _ContinueDebugEvent(debugEvent.ProcessId, debugEvent.ThreadId, continueStatus)
		if err != nil {
			return 0, 0, err
		}

		if shouldExit {
			return 0, exitCode, nil
		}
	}
}

func (dbp *nativeProcess) trapWait(pid int) (*nativeThread, error) {
	var err error
	var tid, exitCode int
	dbp.execPtraceFunc(func() {
		tid, exitCode, err = dbp.waitForDebugEvent(waitBlocking)
	})
	if err != nil {
		return nil, err
	}
	if tid == 0 {
		dbp.postExit()
		return nil, proc.ErrProcessExited{Pid: dbp.pid, Status: exitCode}
	}
	th := dbp.threads[tid]
	return th, nil
}

func (dbp *nativeProcess) wait(pid, options int) (int, *sys.WaitStatus, error) {
	return 0, nil, fmt.Errorf("not implemented: wait")
}

func (dbp *nativeProcess) exitGuard(err error) error {
	return err
}

func (dbp *nativeProcess) resume() error {
	for _, thread := range dbp.threads {
		if thread.CurrentBreakpoint.Breakpoint != nil {
			if err := thread.StepInstruction(); err != nil {
				return err
			}
			thread.CurrentBreakpoint.Clear()
		}
	}

	for _, thread := range dbp.threads {
		_, err := _ResumeThread(thread.os.hThread)
		if err != nil {
			return err
		}
	}
	dbp.os.running = true

	return nil
}

// stop stops all running threads threads and sets breakpoints
func (dbp *nativeProcess) stop(trapthread *nativeThread) (*nativeThread, error) {
	if dbp.exited {
		return nil, proc.ErrProcessExited{Pid: dbp.Pid()}
	}

	dbp.os.running = false

	// While the debug event that stopped the target was being propagated
	// other target threads could generate other debug events.
	// After this function we need to know about all the threads
	// stopped on a breakpoint. To do that we first suspend all target
	// threads and then repeatedly call _ContinueDebugEvent followed by
	// waitForDebugEvent in non-blocking mode.
	// We need to explicitly call SuspendThread because otherwise the
	// call to _ContinueDebugEvent will resume execution of some of the
	// target threads.

	err := trapthread.SetCurrentBreakpoint(true)
	if err != nil {
		return nil, err
	}

	context := winutil.NewCONTEXT()

	for _, thread := range dbp.threads {
		thread.os.delayErr = nil
		if !thread.os.dbgUiRemoteBreakIn {
			// Wait before reporting the error, the thread could be removed when we
			// call waitForDebugEvent in the next loop.
			_, thread.os.delayErr = _SuspendThread(thread.os.hThread)
			if thread.os.delayErr == nil {
				// This call will block until the thread has stopped.
				_ = _GetThreadContext(thread.os.hThread, context)
			}
		}
	}

	for {
		var err error
		var tid int
		dbp.execPtraceFunc(func() {
			err = _ContinueDebugEvent(uint32(dbp.pid), uint32(dbp.os.breakThread), _DBG_CONTINUE)
			if err == nil {
				tid, _, _ = dbp.waitForDebugEvent(waitSuspendNewThreads)
			}
		})
		if err != nil {
			return nil, err
		}
		if tid == 0 {
			break
		}
		err = dbp.threads[tid].SetCurrentBreakpoint(true)
		if err != nil {
			return nil, err
		}
	}

	// Check if trapthread still exist, if the process is dying it could have
	// been removed while we were stopping the other threads.
	trapthreadFound := false
	for _, thread := range dbp.threads {
		if thread.ID == trapthread.ID {
			trapthreadFound = true
		}
		if thread.os.delayErr != nil && thread.os.delayErr != syscall.Errno(0x5) {
			// Do not report Access is denied error, it is caused by the thread
			// having already died but we haven't been notified about it yet.
			return nil, thread.os.delayErr
		}
	}

	if !trapthreadFound {
		wasDbgUiRemoteBreakIn := trapthread.os.dbgUiRemoteBreakIn
		// trapthread exited during stop, pick another one
		trapthread = nil
		for _, thread := range dbp.threads {
			if thread.CurrentBreakpoint.Breakpoint != nil && thread.os.delayErr == nil {
				trapthread = thread
				break
			}
		}
		if trapthread == nil && wasDbgUiRemoteBreakIn {
			// If this was triggered by a manual stop request we should stop
			// regardless, pick a thread.
			for _, thread := range dbp.threads {
				return thread, nil
			}
		}
	}

	return trapthread, nil
}

func (dbp *nativeProcess) detach(kill bool) error {
	if !kill {
		//TODO(aarzilli): when debug.Target exist Detach should be moved to
		// debug.Target and the call to RestoreAsyncPreempt should be moved there.
		for _, thread := range dbp.threads {
			_, err := _ResumeThread(thread.os.hThread)
			if err != nil {
				return err
			}
		}
	}
	return _DebugActiveProcessStop(uint32(dbp.pid))
}

func (dbp *nativeProcess) EntryPoint() (uint64, error) {
	return dbp.os.entryPoint, nil
}

func (dbp *nativeProcess) SupportsBPF() bool {
	return false
}

func (dbp *nativeProcess) SetUProbe(fnName string, goidOffset int64, args []ebpf.UProbeArgMap) error {
	return nil
}

func (dbp *nativeProcess) GetBufferedTracepoints() []ebpf.RawUProbeParams {
	return nil
}

func killProcess(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	defer p.Release()

	return p.Kill()
}
