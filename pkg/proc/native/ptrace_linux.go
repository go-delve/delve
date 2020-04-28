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
