package native

import (
	"errors"
	"syscall"

	sys "golang.org/x/sys/windows"

	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/winutil"
)

// waitStatus is a synonym for the platform-specific WaitStatus
type waitStatus sys.WaitStatus

// osSpecificDetails holds information specific to the Windows
// operating system / kernel.
type osSpecificDetails struct {
	hThread            syscall.Handle
	dbgUiRemoteBreakIn bool // whether thread is an auxiliary DbgUiRemoteBreakIn thread created by Windows
	delayErr           error
}

func (t *nativeThread) singleStep() error {
	context := winutil.NewCONTEXT()
	context.ContextFlags = _CONTEXT_ALL

	// Set the processor TRAP flag
	err := _GetThreadContext(t.os.hThread, context)
	if err != nil {
		return err
	}

	context.EFlags |= 0x100

	err = _SetThreadContext(t.os.hThread, context)
	if err != nil {
		return err
	}

	suspendcnt := 0

	// If a thread simultaneously hits a breakpoint and is suspended by the Go
	// runtime it will have a suspend count greater than 1 and to actually take
	// a single step we have to resume it multiple times here.
	// We keep a counter of how many times it was suspended so that after
	// single-stepping we can re-suspend it the corrent number of times.
	for {
		n, err := _ResumeThread(t.os.hThread)
		if err != nil {
			return err
		}
		suspendcnt++
		if n == 1 {
			break
		}
	}

	for {
		var tid, exitCode int
		t.dbp.execPtraceFunc(func() {
			tid, exitCode, err = t.dbp.waitForDebugEvent(waitBlocking | waitSuspendNewThreads)
		})
		if err != nil {
			return err
		}
		if tid == 0 {
			t.dbp.postExit()
			return proc.ErrProcessExited{Pid: t.dbp.pid, Status: exitCode}
		}

		if t.dbp.os.breakThread == t.ID {
			break
		}

		t.dbp.execPtraceFunc(func() {
			err = _ContinueDebugEvent(uint32(t.dbp.pid), uint32(t.dbp.os.breakThread), _DBG_CONTINUE)
		})
	}

	for i := 0; i < suspendcnt; i++ {
		if !t.os.dbgUiRemoteBreakIn {
			_, err = _SuspendThread(t.os.hThread)
			if err != nil {
				return err
			}
		}
	}

	t.dbp.execPtraceFunc(func() {
		err = _ContinueDebugEvent(uint32(t.dbp.pid), uint32(t.ID), _DBG_CONTINUE)
	})
	if err != nil {
		return err
	}

	// Unset the processor TRAP flag
	err = _GetThreadContext(t.os.hThread, context)
	if err != nil {
		return err
	}

	context.EFlags &= ^uint32(0x100)

	return _SetThreadContext(t.os.hThread, context)
}

func (t *nativeThread) resume() error {
	var err error
	t.dbp.execPtraceFunc(func() {
		//TODO: Note that we are ignoring the thread we were asked to continue and are continuing the
		//thread that we last broke on.
		err = _ContinueDebugEvent(uint32(t.dbp.pid), uint32(t.ID), _DBG_CONTINUE)
	})
	return err
}

// Stopped returns whether the thread is stopped at the operating system
// level. On windows this always returns true.
func (t *nativeThread) Stopped() bool {
	return true
}

func (t *nativeThread) WriteMemory(addr uint64, data []byte) (int, error) {
	if t.dbp.exited {
		return 0, proc.ErrProcessExited{Pid: t.dbp.pid}
	}
	if len(data) == 0 {
		return 0, nil
	}
	var count uintptr
	err := _WriteProcessMemory(t.dbp.os.hProcess, uintptr(addr), &data[0], uintptr(len(data)), &count)
	if err != nil {
		return 0, err
	}
	return int(count), nil
}

var ErrShortRead = errors.New("short read")

func (t *nativeThread) ReadMemory(buf []byte, addr uint64) (int, error) {
	if t.dbp.exited {
		return 0, proc.ErrProcessExited{Pid: t.dbp.pid}
	}
	if len(buf) == 0 {
		return 0, nil
	}
	var count uintptr
	err := _ReadProcessMemory(t.dbp.os.hProcess, uintptr(addr), &buf[0], uintptr(len(buf)), &count)
	if err == nil && count != uintptr(len(buf)) {
		err = ErrShortRead
	}
	return int(count), err
}

func (t *nativeThread) restoreRegisters(savedRegs proc.Registers) error {
	return _SetThreadContext(t.os.hThread, savedRegs.(*winutil.AMD64Registers).Context)
}

func (t *nativeThread) writeHardwareBreakpoint(addr uint64, wtype proc.WatchType, idx uint8) error {
	return proc.ErrHWBreakUnsupported
}

func (t *nativeThread) clearHardwareBreakpoint(addr uint64, wtype proc.WatchType, idx uint8) error {
	return proc.ErrHWBreakUnsupported
}

func (t *nativeThread) findHardwareBreakpoint() (*proc.Breakpoint, error) {
	return nil, nil
}
