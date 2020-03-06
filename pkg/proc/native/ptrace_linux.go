package native

import (
	"syscall"
	"unsafe"

	sys "golang.org/x/sys/unix"
)

// PtraceAttach executes the sys.PtraceAttach call.
func PtraceAttach(pid int) error {
	return sys.PtraceAttach(pid)
}

// PtraceDetach calls ptrace(PTRACE_DETACH).
func PtraceDetach(tid, sig int) error {
	_, _, err := sys.Syscall6(sys.SYS_PTRACE, sys.PTRACE_DETACH, uintptr(tid), 1, uintptr(sig), 0, 0)
	if err != syscall.Errno(0) {
		return err
	}
	return nil
}

// PtraceCont executes ptrace PTRACE_CONT
func PtraceCont(tid, sig int) error {
	return sys.PtraceCont(tid, sig)
}

// PtraceSingleStep executes ptrace PTRACE_SINGLE_STEP.
func PtraceSingleStep(tid int) error {
	return sys.PtraceSingleStep(tid)
}

// PtracePokeUser execute ptrace PTRACE_POKE_USER.
func PtracePokeUser(tid int, off, addr uintptr) error {
	_, _, err := sys.Syscall6(sys.SYS_PTRACE, sys.PTRACE_POKEUSR, uintptr(tid), uintptr(off), uintptr(addr), 0, 0)
	if err != syscall.Errno(0) {
		return err
	}
	return nil
}

// PtracePeekUser execute ptrace PTRACE_PEEK_USER.
func PtracePeekUser(tid int, off uintptr) (uintptr, error) {
	var val uintptr
	_, _, err := syscall.Syscall6(syscall.SYS_PTRACE, syscall.PTRACE_PEEKUSR, uintptr(tid), uintptr(off), uintptr(unsafe.Pointer(&val)), 0, 0)
	if err != syscall.Errno(0) {
		return 0, err
	}
	return val, nil
}

// ProcessVmRead calls process_vm_readv
func ProcessVmRead(tid int, addr uintptr, data []byte) (int, error) {
	len_iov := uint64(len(data))
	local_iov := sys.Iovec{Base: &data[0], Len: len_iov}
	remote_iov := sys.Iovec{Base: (*byte)(unsafe.Pointer(addr)), Len: len_iov}
	p_local := uintptr(unsafe.Pointer(&local_iov))
	p_remote := uintptr(unsafe.Pointer(&remote_iov))
	n, _, err := syscall.Syscall6(sys.SYS_PROCESS_VM_READV, uintptr(tid), p_local, 1, p_remote, 1, 0)
	if err != syscall.Errno(0) {
		return 0, err
	}
	return int(n), nil
}

// ProcessVmWrite calls process_vm_writev
func ProcessVmWrite(tid int, addr uintptr, data []byte) (int, error) {
	len_iov := uint64(len(data))
	local_iov := sys.Iovec{Base: &data[0], Len: len_iov}
	remote_iov := sys.Iovec{Base: (*byte)(unsafe.Pointer(addr)), Len: len_iov}
	p_local := uintptr(unsafe.Pointer(&local_iov))
	p_remote := uintptr(unsafe.Pointer(&remote_iov))
	n, _, err := syscall.Syscall6(sys.SYS_PROCESS_VM_WRITEV, uintptr(tid), p_local, 1, p_remote, 1, 0)
	if err != syscall.Errno(0) {
		return 0, err
	}
	return int(n), nil
}
