package native

import (
	"debug/elf"
	"syscall"
	"unsafe"

	sys "golang.org/x/sys/unix"

	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/linutil"
)

const (
	_AARCH64_GREGS_SIZE  = 34 * 8
	_AARCH64_FPREGS_SIZE = 32*16 + 8
	_NT_ARM_TLS          = 0x401 // used in PTRACE_GETREGSET on ARM64 to retrieve the value of TPIDR_EL0, see source/include/uapi/linux/elf.h and source/arch/arm64/kernel/ptrace.c
)

func ptraceGetGRegs(pid int, regs *linutil.ARM64PtraceRegs) (err error) {
	iov := sys.Iovec{Base: (*byte)(unsafe.Pointer(regs)), Len: _AARCH64_GREGS_SIZE}
	_, _, err = syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_GETREGSET, uintptr(pid), uintptr(elf.NT_PRSTATUS), uintptr(unsafe.Pointer(&iov)), 0, 0)
	if err == syscall.Errno(0) {
		err = nil
	}
	return
}

func ptraceGetTpidr_el0(pid int, tpidr_el0 *uint64) (err error) {
	iov := sys.Iovec{Base: (*byte)(unsafe.Pointer(tpidr_el0)), Len: uint64(unsafe.Sizeof(*tpidr_el0))}
	_, _, err = syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_GETREGSET, uintptr(pid), uintptr(_NT_ARM_TLS), uintptr(unsafe.Pointer(&iov)), 0, 0)
	if err == syscall.Errno(0) {
		err = nil
	}
	return
}

func ptraceSetGRegs(pid int, regs *linutil.ARM64PtraceRegs) (err error) {
	iov := sys.Iovec{Base: (*byte)(unsafe.Pointer(regs)), Len: _AARCH64_GREGS_SIZE}
	_, _, err = syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_SETREGSET, uintptr(pid), uintptr(elf.NT_PRSTATUS), uintptr(unsafe.Pointer(&iov)), 0, 0)
	if err == syscall.Errno(0) {
		err = nil
	}
	return
}

// ptraceGetFpRegset returns floating point registers of the specified thread
// using PTRACE.
func ptraceGetFpRegset(tid int) (fpregset []byte, err error) {
	var arm64_fpregs [_AARCH64_FPREGS_SIZE]byte
	iov := sys.Iovec{Base: &arm64_fpregs[0], Len: _AARCH64_FPREGS_SIZE}
	_, _, err = syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_GETREGSET, uintptr(tid), uintptr(elf.NT_FPREGSET), uintptr(unsafe.Pointer(&iov)), 0, 0)
	if err != syscall.Errno(0) {
		if err == syscall.ENODEV {
			err = nil
		}
		return
	} else {
		err = nil
	}

	fpregset = arm64_fpregs[:iov.Len-8]
	return fpregset, err
}

// setPC sets PC to the value specified by 'pc'.
func (thread *nativeThread) setPC(pc uint64) error {
	ir, err := registers(thread)
	if err != nil {
		return err
	}
	r := ir.(*linutil.ARM64Registers)
	r.Regs.Pc = pc
	thread.dbp.execPtraceFunc(func() { err = ptraceSetGRegs(thread.ID, r.Regs) })
	return err
}

func (thread *nativeThread) SetReg(regNum uint64, reg *op.DwarfRegister) error {
	ir, err := registers(thread)
	if err != nil {
		return err
	}
	r := ir.(*linutil.ARM64Registers)
	fpchanged, err := r.SetReg(regNum, reg)
	if err != nil {
		return err
	}

	thread.dbp.execPtraceFunc(func() {
		err = ptraceSetGRegs(thread.ID, r.Regs)
		if err != syscall.Errno(0) && err != nil {
			return
		}
		if fpchanged && r.Fpregset != nil {
			iov := sys.Iovec{Base: &r.Fpregset[0], Len: uint64(len(r.Fpregset))}
			_, _, err = syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_SETREGSET, uintptr(thread.ID), uintptr(elf.NT_FPREGSET), uintptr(unsafe.Pointer(&iov)), 0, 0)
		}
	})
	if err == syscall.Errno(0) {
		err = nil
	}
	return err
}

func registers(thread *nativeThread) (proc.Registers, error) {
	var (
		regs linutil.ARM64PtraceRegs
		err  error
	)
	thread.dbp.execPtraceFunc(func() { err = ptraceGetGRegs(thread.ID, &regs) })
	if err != nil {
		return nil, err
	}
	var tpidr_el0 uint64
	if thread.dbp.iscgo {
		thread.dbp.execPtraceFunc(func() { err = ptraceGetTpidr_el0(thread.ID, &tpidr_el0) })
		if err != nil {
			return nil, err
		}
	}
	r := linutil.NewARM64Registers(&regs, thread.dbp.iscgo, tpidr_el0, func(r *linutil.ARM64Registers) error {
		var floatLoadError error
		r.Fpregs, r.Fpregset, floatLoadError = thread.fpRegisters()
		return floatLoadError
	})
	return r, nil
}
