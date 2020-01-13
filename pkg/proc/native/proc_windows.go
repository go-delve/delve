package native

import (
	"debug/pe"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"

	sys "golang.org/x/sys/windows"

	"github.com/go-delve/delve/pkg/proc"
)

// OSProcessDetails holds Windows specific information.
type OSProcessDetails struct {
	hProcess    syscall.Handle
	breakThread int
	entryPoint  uint64
}

func openExecutablePathPE(path string) (*pe.File, io.Closer, error) {
	f, err := os.OpenFile(path, 0, os.ModePerm)
	if err != nil {
		return nil, nil, err
	}
	peFile, err := pe.NewFile(f)
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return peFile, f, nil
}

// Launch creates and begins debugging a new process.
func Launch(cmd []string, wd string, foreground bool, _ []string) (*Process, error) {
	argv0Go, err := filepath.Abs(cmd[0])
	if err != nil {
		return nil, err
	}

	// Make sure the binary exists and is an executable file
	if filepath.Base(cmd[0]) == cmd[0] {
		if _, err := exec.LookPath(cmd[0]); err != nil {
			return nil, err
		}
	}

	_, closer, err := openExecutablePathPE(argv0Go)
	if err != nil {
		return nil, proc.ErrNotExecutable
	}
	closer.Close()

	var p *os.Process
	dbp := New(0)
	dbp.common = proc.NewCommonProcess(true)
	dbp.execPtraceFunc(func() {
		attr := &os.ProcAttr{
			Dir:   wd,
			Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
			Sys: &syscall.SysProcAttr{
				CreationFlags: _DEBUG_ONLY_THIS_PROCESS,
			},
		}
		p, err = os.StartProcess(argv0Go, cmd, attr)
	})
	if err != nil {
		return nil, err
	}
	defer p.Release()

	dbp.pid = p.Pid
	dbp.childProcess = true

	if err = dbp.initialize(argv0Go, []string{}); err != nil {
		dbp.Detach(true)
		return nil, err
	}
	return dbp, nil
}

func initialize(dbp *Process) error {
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
		_, err := _SuspendThread(thread.os.hThread)
		if err != nil {
			return err
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
func Attach(pid int, _ []string) (*Process, error) {
	dbp := New(pid)
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
	if err = dbp.initialize(exepath, []string{}); err != nil {
		dbp.Detach(true)
		return nil, err
	}
	return dbp, nil
}

// kill kills the process.
func (dbp *Process) kill() error {
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

func (dbp *Process) requestManualStop() error {
	return _DebugBreakProcess(dbp.os.hProcess)
}

func (dbp *Process) updateThreadList() error {
	// We ignore this request since threads are being
	// tracked as they are created/killed in waitForDebugEvent.
	return nil
}

func (dbp *Process) addThread(hThread syscall.Handle, threadID int, attach, suspendNewThreads bool) (*Thread, error) {
	if thread, ok := dbp.threads[threadID]; ok {
		return thread, nil
	}
	thread := &Thread{
		ID:  threadID,
		dbp: dbp,
		os:  new(OSSpecificDetails),
	}
	thread.os.hThread = hThread
	dbp.threads[threadID] = thread
	if dbp.currentThread == nil {
		dbp.SwitchThread(thread.ID)
	}
	if suspendNewThreads {
		_, err := _SuspendThread(thread.os.hThread)
		if err != nil {
			return nil, err
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

func (dbp *Process) waitForDebugEvent(flags waitForDebugEventFlags) (threadID, exitCode int, err error) {
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
			_, err = dbp.addThread(debugInfo.Thread, int(debugEvent.ThreadId), false, flags&waitSuspendNewThreads != 0)
			if err != nil {
				return 0, 0, err
			}
			break
		case _CREATE_THREAD_DEBUG_EVENT:
			debugInfo := (*_CREATE_THREAD_DEBUG_INFO)(unionPtr)
			_, err = dbp.addThread(debugInfo.Thread, int(debugEvent.ThreadId), false, flags&waitSuspendNewThreads != 0)
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
					if _, err := thread.ReadMemory(data, exception.ExceptionRecord.ExceptionAddress); err == nil {
						instr := dbp.bi.Arch.BreakpointInstruction()
						for i := range instr {
							if data[i] != instr[i] {
								atbp = false
								break
							}
						}
					}
					if !atbp {
						thread.SetPC(uint64(exception.ExceptionRecord.ExceptionAddress))
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

func (dbp *Process) trapWait(pid int) (*Thread, error) {
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

func (dbp *Process) wait(pid, options int) (int, *sys.WaitStatus, error) {
	return 0, nil, fmt.Errorf("not implemented: wait")
}

func (dbp *Process) exitGuard(err error) error {
	return err
}

func (dbp *Process) resume() error {
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

	return nil
}

// stop stops all running threads threads and sets breakpoints
func (dbp *Process) stop(trapthread *Thread) (err error) {
	if dbp.exited {
		return &proc.ErrProcessExited{Pid: dbp.Pid()}
	}

	// While the debug event that stopped the target was being propagated
	// other target threads could generate other debug events.
	// After this function we need to know about all the threads
	// stopped on a breakpoint. To do that we first suspend all target
	// threads and then repeatedly call _ContinueDebugEvent followed by
	// waitForDebugEvent in non-blocking mode.
	// We need to explicitly call SuspendThread because otherwise the
	// call to _ContinueDebugEvent will resume execution of some of the
	// target threads.

	err = trapthread.SetCurrentBreakpoint(true)
	if err != nil {
		return err
	}

	for _, thread := range dbp.threads {
		_, err := _SuspendThread(thread.os.hThread)
		if err != nil {
			return err
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
			return err
		}
		if tid == 0 {
			break
		}
		err = dbp.threads[tid].SetCurrentBreakpoint(true)
		if err != nil {
			return err
		}
	}

	return nil
}

func (dbp *Process) detach(kill bool) error {
	if !kill {
		for _, thread := range dbp.threads {
			_, err := _ResumeThread(thread.os.hThread)
			if err != nil {
				return err
			}
		}
	}
	return _DebugActiveProcessStop(uint32(dbp.pid))
}

func (dbp *Process) EntryPoint() (uint64, error) {
	return dbp.os.entryPoint, nil
}

func killProcess(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	defer p.Release()

	return p.Kill()
}
