package proctl

import (
	"syscall"
	"unsafe"

	sys "golang.org/x/sys/unix"
)

func PtraceDetach(tid, sig int) error {
	_, _, err := sys.Syscall6(sys.SYS_PTRACE, sys.PTRACE_DETACH, uintptr(tid), 1, uintptr(sig), 0, 0)
	if err != syscall.Errno(0) {
		return err
	}
	return nil
}

func PtraceCont(tid, sig int) error {
	return sys.PtraceCont(tid, sig)
}

func PtraceSingleStep(tid int) error {
	return sys.PtraceSingleStep(tid)
}

func PtracePokeUser(tid int, off, addr uintptr) error {
	_, _, err := sys.Syscall6(sys.SYS_PTRACE, sys.PTRACE_POKEUSR, uintptr(tid), uintptr(off), uintptr(addr), 0, 0)
	if err != syscall.Errno(0) {
		return err
	}
	return nil
}

func PtracePeekUser(tid int, off uintptr) (uintptr, error) {
	var val uintptr
	_, _, err := syscall.Syscall6(syscall.SYS_PTRACE, syscall.PTRACE_PEEKUSR, uintptr(tid), uintptr(off), uintptr(unsafe.Pointer(&val)), 0, 0)
	if err != syscall.Errno(0) {
		return 0, err
	}
	return val, nil
}
