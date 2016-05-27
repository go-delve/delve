package proc

import (
	"syscall"
	"unsafe"

	sys "golang.org/x/sys/unix"
)

func convertThreadExitErr(err error) error {
	if err == sys.ESRCH || err == syscall.ESRCH {
		return ThreadExitedErr
	}
	return err
}

// PtraceAttach executes the sys.PtraceAttach call.
func PtraceAttach(pid int) error {
	var err error
	execOnPtraceThread(func() { err = convertThreadExitErr(sys.PtraceAttach(pid)) })
	return err
}

// PtraceDetach calls ptrace(PTRACE_DETACH).
func PtraceDetach(tid, sig int) error {
	var err error
	execOnPtraceThread(func() {
		_, _, err = sys.Syscall6(sys.SYS_PTRACE, sys.PTRACE_DETACH, uintptr(tid), 1, uintptr(sig), 0, 0)
	})
	if err != syscall.Errno(0) {
		return err
	}
	return nil
}

// PtraceCont executes ptrace PTRACE_CONT
func PtraceCont(tid, sig int) error {
	var err error
	execOnPtraceThread(func() { err = convertThreadExitErr(sys.PtraceCont(tid, sig)) })
	return err
}

// PtraceSingleStep executes ptrace PTRACE_SINGLE_STEP.
func PtraceSingleStep(tid int) error {
	var err error
	execOnPtraceThread(func() { err = convertThreadExitErr(sys.PtraceSingleStep(tid)) })
	return err
}

// PtracePokeUser execute ptrace PTRACE_POKE_USER.
func PtracePokeUser(tid int, off, addr uintptr) error {
	var err error
	execOnPtraceThread(func() {
		_, _, err = sys.Syscall6(sys.SYS_PTRACE, sys.PTRACE_POKEUSR, uintptr(tid), uintptr(off), uintptr(addr), 0, 0)
	})
	if err != syscall.Errno(0) {
		return convertThreadExitErr(err)
	}
	return nil
}

// PtracePeekUser execute ptrace PTRACE_PEEK_USER.
func PtracePeekUser(tid int, off uintptr) (uintptr, error) {
	var val uintptr
	var err error
	execOnPtraceThread(func() {
		_, _, err = syscall.Syscall6(syscall.SYS_PTRACE, syscall.PTRACE_PEEKUSR, uintptr(tid), uintptr(off), uintptr(unsafe.Pointer(&val)), 0, 0)
	})
	if err != syscall.Errno(0) {
		return 0, convertThreadExitErr(err)
	}
	return val, nil
}
