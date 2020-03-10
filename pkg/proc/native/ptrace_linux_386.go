package native

import "C"

import (
	"fmt"
	"syscall"
	"unsafe"

	sys "golang.org/x/sys/unix"

	"github.com/go-delve/delve/pkg/proc/linutil"
)

// PtraceGetRegset returns floating point registers of the specified thread
// using PTRACE.
// See i386_linux_fetch_inferior_registers in gdb/i386-linux-nat.c.html
// and i386_supply_xsave in gdb/i386-tdep.c.html
// and Section 13.1 (and following) of Intel® 64 and IA-32 Architectures Software Developer’s Manual, Volume 1: Basic Architecture
func PtraceGetRegset(tid int) (regset linutil.I386Xstate, err error) {
	_, _, err = syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_GETFPREGS, uintptr(tid), uintptr(0), uintptr(unsafe.Pointer(&regset.I386PtraceFpRegs)), 0, 0)
	if err == syscall.Errno(0) || err == syscall.ENODEV {
		// ignore ENODEV, it just means this CPU doesn't have X87 registers (??)
		err = nil
	}

	var xstateargs [_X86_XSTATE_MAX_SIZE]byte
	iov := sys.Iovec{Base: &xstateargs[0], Len: _X86_XSTATE_MAX_SIZE}
	_, _, err = syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_GETREGSET, uintptr(tid), _NT_X86_XSTATE, uintptr(unsafe.Pointer(&iov)), 0, 0)
	if err != syscall.Errno(0) {
		if err == syscall.ENODEV || err == syscall.EIO {
			// ignore ENODEV, it just means this CPU or kernel doesn't support XSTATE, see https://github.com/go-delve/delve/issues/1022
			// also ignore EIO, it means that we are running on an old kernel (pre 2.6.34) and PTRACE_GETREGSET is not implemented
			err = nil
		}
		return
	} else {
		err = nil
	}

	regset.Xsave = xstateargs[:iov.Len]
	err = linutil.I386XstateRead(regset.Xsave, false, &regset)
	return
}

// PtraceGetTls return the addr of tls by PTRACE_GET_THREAD_AREA for specify thread.
// See http://man7.org/linux/man-pages/man2/ptrace.2.html for detail about PTRACE_GET_THREAD_AREA.
// struct user_desc at https://golang.org/src/runtime/sys_linux_386.s
// type UserDesc struct {
// 	 EntryNumber uint32
//	 BaseAddr    uint32
//	 Limit       uint32
//	 Flag        uint32
// }
func PtraceGetTls(gs int32, tid int) (uint32, error) {
	ud := [4]uint32{}

	// Gs usually is 0x33
	_, _, err := syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_GET_THREAD_AREA, uintptr(tid), uintptr(gs>>3), uintptr(unsafe.Pointer(&ud)), 0, 0)
	if err == syscall.ENODEV || err == syscall.EIO {
		return 0, fmt.Errorf("%s", err)
	}

	return uint32(ud[1]), nil
}

// ProcessVmRead calls process_vm_readv
func ProcessVmRead(tid int, addr uintptr, data []byte) (int, error) {
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

// ProcessVmWrite calls process_vm_writev
func ProcessVmWrite(tid int, addr uintptr, data []byte) (int, error) {
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
