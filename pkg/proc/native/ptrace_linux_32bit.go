// +build linux,386 linux,arm

package native

import (
	sys "golang.org/x/sys/unix"
	"syscall"
	"unsafe"
)

// processVmRead calls process_vm_readv
func processVmRead(tid int, addr uintptr, data []byte) (int, error) {
	len_iov := uint32(len(data))
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

// processVmWrite calls process_vm_writev
func processVmWrite(tid int, addr uintptr, data []byte) (int, error) {
	len_iov := uint32(len(data))
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
