package native

import (
	"errors"
	"syscall"

	sys "golang.org/x/sys/windows"
)

const enableHardwareBreakpoints = false // see https://github.com/go-delve/delve/issues/2768

// waitStatus is a synonym for the platform-specific WaitStatus
type waitStatus sys.WaitStatus

// osSpecificDetails holds information specific to the Windows
// operating system / kernel.
type osSpecificDetails struct {
	hThread            syscall.Handle
	dbgUiRemoteBreakIn bool // whether thread is an auxiliary DbgUiRemoteBreakIn thread created by Windows
	delayErr           error
	setbp              bool
}

func (procgrp *processGroup) singleStep(t *nativeThread) error {
	context := newContext()
	context.SetFlags(_CONTEXT_ALL)

	// Set the processor TRAP flag
	err := t.getContext(context)
	if err != nil {
		return err
	}

	context.SetTrap(true)

	err = t.setContext(context)
	if err != nil {
		return err
	}

	suspendcnt := 0

	// If a thread simultaneously hits a breakpoint and is suspended by the Go
	// runtime it will have a suspend count greater than 1 and to actually take
	// a single step we have to resume it multiple times here.
	// We keep a counter of how many times it was suspended so that after
	// single-stepping we can re-suspend it the correct number of times.
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
		var tid int
		t.dbp.execPtraceFunc(func() {
			tid, err = procgrp.waitForDebugEvent(waitBlocking | waitSuspendNewThreads)
		})
		if err != nil {
			return err
		}

		ep := procgrp.procForThread(tid)

		if ep.pid == t.dbp.pid && tid == t.ID {
			break
		}

		ep.execPtraceFunc(func() {
			err = _ContinueDebugEvent(uint32(ep.pid), uint32(ep.os.breakThread), _DBG_CONTINUE)
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
	err = t.getContext(context)
	if err != nil {
		return err
	}

	context.SetTrap(false)

	return t.setContext(context)
}

func (t *nativeThread) WriteMemory(addr uint64, data []byte) (int, error) {
	if ok, err := t.dbp.Valid(); !ok {
		return 0, err
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
	if ok, err := t.dbp.Valid(); !ok {
		return 0, err
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

// SoftExc returns true if this thread received a software exception during the last resume.
func (t *nativeThread) SoftExc() bool {
	return t.os.setbp
}
