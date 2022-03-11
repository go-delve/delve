package native

import (
	"syscall"

	sys "golang.org/x/sys/unix"
)

// ptraceAttach executes the sys.PtraceAttach call.
func ptraceAttach(pid int) error {
	return sys.PtraceAttach(pid)
}

// ptraceDetach calls ptrace(PTRACE_DETACH).
func ptraceDetach(tid, sig int) error {
	_, _, err := sys.Syscall6(sys.SYS_PTRACE, sys.PTRACE_DETACH, uintptr(tid), 1, uintptr(sig), 0, 0)
	if err != syscall.Errno(0) {
		return err
	}
	return nil
}

// ptraceCont executes ptrace PTRACE_CONT
func ptraceCont(tid, sig int) error {
	return sys.PtraceCont(tid, sig)
}

// ptraceSingleStep executes ptrace PTRACE_SINGLESTEP
func ptraceSingleStep(pid, sig int) error {
	_, _, e1 := sys.Syscall6(sys.SYS_PTRACE, uintptr(sys.PTRACE_SINGLESTEP), uintptr(pid), uintptr(0), uintptr(sig), 0, 0)
	if e1 != 0 {
		return e1
	}
	return nil
}

// remoteIovec is like golang.org/x/sys/unix.Iovec but uses uintptr for the
// base field instead of *byte so that we can use it with addresses that
// belong to the target process.
type remoteIovec struct {
	base uintptr
	len  uintptr
}
