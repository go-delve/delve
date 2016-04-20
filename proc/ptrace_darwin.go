package proc

// #include "proc_darwin.h"
import "C"
import (
	"syscall"

	sys "golang.org/x/sys/unix"
)

// PtraceAttach executes the sys.PtraceAttach call.
func PtraceAttach(pid int) error {
	return sys.PtraceAttach(pid)
}

// PtraceDetach executes the PT_DETACH ptrace call.
func PtraceDetach(tid, sig int) error {
	return ptrace(sys.PT_DETACH, tid, 1, uintptr(sig))
}

// PtraceCont executes the PTRACE_CONT ptrace call.
func PtraceCont(tid, sig int) error {
	return ptrace(sys.PTRACE_CONT, tid, 1, 0)
}

// PtraceThupdate executes the PT_THUPDATE ptrace call.
func PtraceThupdate(pid int, tid C.thread_act_t, sig int) error {
	return ptrace(syscall.PT_THUPDATE, pid, uintptr(tid), uintptr(sig))
}

// PtraceSingleStep returns PT_STEP ptrace call.
func PtraceSingleStep(tid int) error {
	return ptrace(sys.PT_STEP, tid, 1, 0)
}

func ptrace(request, pid int, addr uintptr, data uintptr) error {
	_, _, err := sys.Syscall6(sys.SYS_PTRACE, uintptr(request), uintptr(pid), uintptr(addr), uintptr(data), 0, 0)
	if err != syscall.Errno(0) {
		return err
	}
	return nil
}
