//go:build darwin && macnative
// +build darwin,macnative

package native

import sys "golang.org/x/sys/unix"

// ptraceAttach executes the sys.PtraceAttach call.
func ptraceAttach(pid int) error {
	return sys.PtraceAttach(pid)
}

// ptraceDetach executes the PT_DETACH ptrace call.
func ptraceDetach(tid, sig int) error {
	return ptrace(sys.PT_DETACH, tid, 1, uintptr(sig))
}

// ptraceCont executes the PTRACE_CONT ptrace call.
func ptraceCont(tid, sig int) error {
	return ptrace(sys.PTRACE_CONT, tid, 1, 0)
}

// ptraceSingleStep returns PT_STEP ptrace call.
func ptraceSingleStep(tid int) error {
	return ptrace(sys.PT_STEP, tid, 1, 0)
}

func ptrace(request, pid int, addr uintptr, data uintptr) (err error) {
	_, _, err = sys.Syscall6(sys.SYS_PTRACE, uintptr(request), uintptr(pid), uintptr(addr), uintptr(data), 0, 0)
	return
}
