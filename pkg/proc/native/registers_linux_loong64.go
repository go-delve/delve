//go:build linux && loong64

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

const (
	// Refer to the definition of struct user_pt_regs in the kernel file ptrace.h
	_LOONG64_GREGS_SIZE = (32 * 8) + 8 + 8 + 8 + (10 * 8)

	// In fact, the total number of bytes is 268(32 fpr * 8 + 1 Fcc * 8 + 1 Fcsr * 4),
	// but since the Len defined in sys.Iovec is uint64,the total numbel of bytes must
	// be an integral multiple of 8,so add 4 bytes
	_LOONG64_FPREGS_SIZE = (32 * 8) + (1 * 8) + (1 * 4) + 4
)

func ptraceGetGRegs(pid int, regs *linutil.LOONG64PtraceRegs) (err error) {
	iov := sys.Iovec{Base: (*byte)(unsafe.Pointer(regs)), Len: _LOONG64_GREGS_SIZE}

	_, _, err = syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_GETREGSET, uintptr(pid),
		uintptr(elf.NT_PRSTATUS), uintptr(unsafe.Pointer(&iov)), 0, 0)
	if err == syscall.Errno(0) {
		err = nil
	}

	return
}

func ptraceSetGRegs(pid int, regs *linutil.LOONG64PtraceRegs) (err error) {
	iov := sys.Iovec{Base: (*byte)(unsafe.Pointer(regs)), Len: _LOONG64_GREGS_SIZE}

	_, _, err = syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_SETREGSET, uintptr(pid),
		uintptr(elf.NT_PRSTATUS), uintptr(unsafe.Pointer(&iov)), 0, 0)
	if err == syscall.Errno(0) {
		err = nil
	}

	return
}

// ptraceGetFpRegset returns floating point registers of
// the specified thread using PTRACE.
func ptraceGetFpRegset(tid int, fpregs *linutil.LOONG64PtraceFpRegs) (err error) {
	fprBytes := make([]byte, _LOONG64_FPREGS_SIZE)
	iov := sys.Iovec{Base: &fprBytes[0], Len: uint64(_LOONG64_FPREGS_SIZE)}

	_, _, err = syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_GETREGSET, uintptr(tid),
		uintptr(elf.NT_FPREGSET), uintptr(unsafe.Pointer(&iov)), 0, 0)
	if err != syscall.Errno(0) {
		if err == syscall.ENODEV {
			err = nil
		}
		return
	} else {
		err = nil
	}

	fpregs.Fregs = fprBytes[:iov.Len-16]
	fpregs.Fcc = binary.LittleEndian.Uint64(fprBytes[iov.Len-16 : iov.Len-8])
	fpregs.Fcsr = binary.LittleEndian.Uint32(fprBytes[iov.Len-8 : iov.Len-4])

	return
}

// SetPC sets PC to the value specified by 'pc'.
func (thread *nativeThread) setPC(pc uint64) error {
	ir, err := registers(thread)
	if err != nil {
		return err
	}

	r := ir.(*linutil.LOONG64Registers)
	r.Regs.Era = pc
	thread.dbp.execPtraceFunc(func() { err = ptraceSetGRegs(thread.ID, r.Regs) })

	return err
}

func (thread *nativeThread) SetReg(regNum uint64, reg *op.DwarfRegister) error {
	ir, err := registers(thread)
	if err != nil {
		return err
	}
	r := ir.(*linutil.LOONG64Registers)
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
		regs linutil.LOONG64PtraceRegs
		err  error
	)

	thread.dbp.execPtraceFunc(func() { err = ptraceGetGRegs(thread.ID, &regs) })
	if err != nil {
		return nil, err
	}

	var tp_tls uint64
	if thread.dbp.iscgo {
		tp_tls = regs.Regs[2]
	}
	r := linutil.NewLOONG64Registers(&regs, thread.dbp.iscgo, tp_tls,
		func(r *linutil.LOONG64Registers) error {
			var floatLoadError error
			r.Fpregs, r.Fpregset, floatLoadError = thread.fpRegisters()
			return floatLoadError
		})

	return r, nil
}
