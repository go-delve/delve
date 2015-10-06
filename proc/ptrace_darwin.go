package proc

import sys "golang.org/x/sys/unix"

func PtraceDetach(tid, sig int) error {
	return ptrace(sys.PT_DETACH, tid, 1, uintptr(sig))
}

func PtraceCont(tid, sig int) error {
	return ptrace(sys.PTRACE_CONT, tid, 1, 0)
}

func PtraceSingleStep(tid int) error {
	return ptrace(sys.PT_STEP, tid, 1, 0)
}

func ptrace(request, pid int, addr uintptr, data uintptr) (err error) {
	_, _, err = sys.Syscall6(sys.SYS_PTRACE, uintptr(request), uintptr(pid), uintptr(addr), uintptr(data), 0, 0)
	return
}
