//go:build (linux && amd64) || (linux && arm64) || (linux && ppc64le)
// +build linux,amd64 linux,arm64 linux,ppc64le

package native

import (
	"syscall"
	"unsafe"

	sys "golang.org/x/sys/unix"
)

// processVmRead calls process_vm_readv
func processVmRead(tid int, addr uintptr, data []byte) (int, error) {
	len_iov := uint64(len(data))
	local_iov := sys.Iovec{Base: &data[0], Len: len_iov}
	remote_iov := remoteIovec{base: addr, len: uintptr(len_iov)}
	n, _, err := syscall.Syscall6(sys.SYS_PROCESS_VM_READV, uintptr(tid), uintptr(unsafe.Pointer(&local_iov)), 1, uintptr(unsafe.Pointer(&remote_iov)), 1, 0)
	if err != syscall.Errno(0) {
		return 0, err
	}
	return int(n), nil
}

// processVmWrite calls process_vm_writev
func processVmWrite(tid int, addr uintptr, data []byte) (int, error) {
	len_iov := uint64(len(data))
	local_iov := sys.Iovec{Base: &data[0], Len: len_iov}
	remote_iov := remoteIovec{base: addr, len: uintptr(len_iov)}
	n, _, err := syscall.Syscall6(sys.SYS_PROCESS_VM_WRITEV, uintptr(tid), uintptr(unsafe.Pointer(&local_iov)), 1, uintptr(unsafe.Pointer(&remote_iov)), 1, 0)
	if err != syscall.Errno(0) {
		return 0, err
	}
	return int(n), nil
}
