package native

import (
	"encoding/binary"
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

// PtraceGetRegset returns floating point registers of the specified thread
// using PTRACE.
// See amd64_linux_fetch_inferior_registers in gdb/amd64-linux-nat.c.html
// and amd64_supply_xsave in gdb/amd64-tdep.c.html
// and Section 13.1 (and following) of Intel® 64 and IA-32 Architectures Software Developer’s Manual, Volume 1: Basic Architecture
func PtraceGetRegset(tid int) (regset PtraceXsave, err error) {
	_, _, err = syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_GETFPREGS, uintptr(tid), uintptr(0), uintptr(unsafe.Pointer(&regset.PtraceFpRegs)), 0, 0)
	if err == syscall.Errno(0) {
		err = nil
	}

	var xstateargs [_X86_XSTATE_MAX_SIZE]byte
	iov := sys.Iovec{Base: &xstateargs[0], Len: _X86_XSTATE_MAX_SIZE}
	_, _, err = syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_GETREGSET, uintptr(tid), _NT_X86_XSTATE, uintptr(unsafe.Pointer(&iov)), 0, 0)
	if err != syscall.Errno(0) {
		return
	} else {
		err = nil
	}

	if _XSAVE_HEADER_START+_XSAVE_HEADER_LEN >= iov.Len {
		return
	}
	xsaveheader := xstateargs[_XSAVE_HEADER_START : _XSAVE_HEADER_START+_XSAVE_HEADER_LEN]
	xstate_bv := binary.LittleEndian.Uint64(xsaveheader[0:8])
	xcomp_bv := binary.LittleEndian.Uint64(xsaveheader[8:16])

	if xcomp_bv&(1<<63) != 0 {
		// compact format not supported
		return
	}

	if xstate_bv&(1<<2) == 0 {
		// AVX state not present
		return
	}

	avxstate := xstateargs[_XSAVE_EXTENDED_REGION_START:iov.Len]
	regset.AvxState = true
	copy(regset.YmmSpace[:], avxstate[:len(regset.YmmSpace)])

	return
}
