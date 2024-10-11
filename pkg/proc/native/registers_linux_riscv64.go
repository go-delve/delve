package native

import (
	"debug/elf"
	"encoding/binary"
	"syscall"
	"unsafe"

	sys "golang.org/x/sys/unix"

	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/linutil"
)

// Defined in asm/ptrace.h
const (
	_RISCV64_GREGS_SIZE  = 32 * 8
	_RISCV64_FPREGS_SIZE = (32 * 8) + 4 + 4 // Add 4 bytes to align with 8 due to sys.Iovec uses uint64 as len
)

func ptraceGetGRegs(pid int, regs *linutil.RISCV64PtraceRegs) (err error) {
	iov := sys.Iovec{Base: (*byte)(unsafe.Pointer(regs)), Len: _RISCV64_GREGS_SIZE}

	_, _, err = syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_GETREGSET, uintptr(pid),
		uintptr(elf.NT_PRSTATUS), uintptr(unsafe.Pointer(&iov)), 0, 0)
	if err == syscall.Errno(0) {
		err = nil
	}

	return
}

func ptraceSetGRegs(pid int, regs *linutil.RISCV64PtraceRegs) (err error) {
	iov := sys.Iovec{Base: (*byte)(unsafe.Pointer(regs)), Len: _RISCV64_GREGS_SIZE}

	_, _, err = syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_SETREGSET, uintptr(pid), uintptr(elf.NT_PRSTATUS), uintptr(unsafe.Pointer(&iov)), 0, 0)
	if err == syscall.Errno(0) {
		err = nil
	}

	return
}

func ptraceGetFpRegset(tid int, fpregs *linutil.RISCV64PtraceFpRegs) (err error) {
	fprBytes := make([]byte, _RISCV64_FPREGS_SIZE)
	iov := sys.Iovec{Base: &fprBytes[0], Len: uint64(_RISCV64_FPREGS_SIZE)}

	_, _, err = syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_GETREGSET, uintptr(tid), uintptr(elf.NT_FPREGSET), uintptr(unsafe.Pointer(&iov)), 0, 0)
	if err != syscall.Errno(0) {
		if err == syscall.ENODEV {
			err = nil
		}
		return
	} else {
		err = nil
	}

	fpregs.Fregs = fprBytes[:iov.Len-8]
	fpregs.Fcsr = binary.LittleEndian.Uint32(fprBytes[iov.Len-8 : iov.Len-4])

	return
}

func (thread *nativeThread) setPC(pc uint64) error {
	ir, err := registers(thread)
	if err != nil {
		return err
	}

	r := ir.(*linutil.RISCV64Registers)
	r.Regs.Pc = pc
	thread.dbp.execPtraceFunc(func() { err = ptraceSetGRegs(thread.ID, r.Regs) })

	return err
}

func (thread *nativeThread) setSP(sp uint64) (err error) {
	var ir proc.Registers

	ir, err = registers(thread)
	if err != nil {
		return err
	}

	r := ir.(*linutil.RISCV64Registers)
	r.Regs.Sp = sp
	thread.dbp.execPtraceFunc(func() { err = ptraceSetGRegs(thread.ID, r.Regs) })

	return nil
}

func (thread *nativeThread) SetReg(regNum uint64, reg *op.DwarfRegister) error {
	ir, err := registers(thread)
	if err != nil {
		return err
	}
	r := ir.(*linutil.RISCV64Registers)
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
		regs linutil.RISCV64PtraceRegs
		err  error
	)

	thread.dbp.execPtraceFunc(func() { err = ptraceGetGRegs(thread.ID, &regs) })
	if err != nil {
		return nil, err
	}

	var tp_tls uint64
	if thread.dbp.iscgo {
		tp_tls = regs.Tp
	}
	r := linutil.NewRISCV64Registers(&regs, thread.dbp.iscgo, tp_tls,
		func(r *linutil.RISCV64Registers) error {
			var floatLoadError error
			r.Fpregs, r.Fpregset, floatLoadError = thread.fpRegisters()
			return floatLoadError
		})

	return r, nil
}
