package native

import (
	"debug/elf"
	"fmt"
	"syscall"
	"unsafe"

	sys "golang.org/x/sys/unix"

	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/linutil"
)

const (
	AARCH64_GREGS_SIZE  = 34 * 8
	AARCH64_FPREGS_SIZE = 32*16 + 8
)

func ptraceGetGRegs(pid int, regs *linutil.ARM64PtraceRegs) (err error) {
	iov := sys.Iovec{Base: (*byte)(unsafe.Pointer(regs)), Len: AARCH64_GREGS_SIZE}
	_, _, err = syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_GETREGSET, uintptr(pid), uintptr(elf.NT_PRSTATUS), uintptr(unsafe.Pointer(&iov)), 0, 0)
	if err == syscall.Errno(0) {
		err = nil
	}
	return
}

func ptraceSetGRegs(pid int, regs *linutil.ARM64PtraceRegs) (err error) {
	iov := sys.Iovec{Base: (*byte)(unsafe.Pointer(regs)), Len: AARCH64_GREGS_SIZE}
	_, _, err = syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_SETREGSET, uintptr(pid), uintptr(elf.NT_PRSTATUS), uintptr(unsafe.Pointer(&iov)), 0, 0)
	if err == syscall.Errno(0) {
		err = nil
	}
	return
}

// PtraceGetFpRegset returns floating point registers of the specified thread
// using PTRACE.
func PtraceGetFpRegset(tid int) (fpregset []byte, err error) {
	var arm64_fpregs [AARCH64_FPREGS_SIZE]byte
	iov := sys.Iovec{Base: &arm64_fpregs[0], Len: AARCH64_FPREGS_SIZE}
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

// SetPC sets PC to the value specified by 'pc'.
func (thread *Thread) SetPC(pc uint64) error {
	ir, err := registers(thread, false)
	if err != nil {
		return err
	}
	r := ir.(*linutil.ARM64Registers)
	r.Regs.Pc = pc
	thread.dbp.execPtraceFunc(func() { err = ptraceSetGRegs(thread.ID, r.Regs) })
	return err
}

// SetSP sets RSP to the value specified by 'sp'
func (thread *Thread) SetSP(sp uint64) (err error) {
	var ir proc.Registers
	ir, err = registers(thread, false)
	if err != nil {
		return err
	}
	r := ir.(*linutil.ARM64Registers)
	r.Regs.Sp = sp
	thread.dbp.execPtraceFunc(func() { err = ptraceSetGRegs(thread.ID, r.Regs) })
	return
}

func (thread *Thread) SetDX(dx uint64) (err error) {
	return fmt.Errorf("not supported")
}

func registers(thread *Thread, floatingPoint bool) (proc.Registers, error) {
	var (
		regs linutil.ARM64PtraceRegs
		err  error
	)
	thread.dbp.execPtraceFunc(func() { err = ptraceGetGRegs(thread.ID, &regs) })
	if err != nil {
		return nil, err
	}
	r := &linutil.ARM64Registers{&regs, nil, nil}
	if floatingPoint {
		r.Fpregs, r.Fpregset, err = thread.fpRegisters()
		if err != nil {
			return nil, err
		}
	}
	return r, nil
}
