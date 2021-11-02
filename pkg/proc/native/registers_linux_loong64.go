//go:build linux && loong64
// +build linux,loong64

package native

import (
	"debug/elf"
	"fmt"
	"syscall"
	"unsafe"

	sys "golang.org/x/sys/unix"

	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/regnum"
	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/linutil"
)

const (
	// golang/sys : unix/ztypes_linux_loong64.go
	// 32 GPR * 8 + ERA * 8, refer to definition of kernel
	_LOONG64_GREGS_SIZE = (32 * 8) + (1 * 8)

	// In fact, the total number of bytes is 268(32 Fpr * 8 + 1 Fcc * 8 + 1 Fcsr +4),
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
func ptraceGetFpRegset(tid int) (fpregset []byte, err error) {
	fpregs := make([]byte, _LOONG64_FPREGS_SIZE)
	iov := sys.Iovec{Base: &fpregs[0], Len: uint64(_LOONG64_FPREGS_SIZE)}

	_, _, err = syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_SETREGSET, uintptr(tid),
		uintptr(elf.NT_FPREGSET), uintptr(unsafe.Pointer(&iov)), 0, 0)
	if err != syscall.Errno(0) {
		if err == syscall.ENODEV {
			err = nil
		}
		return
	} else {
		err = nil
	}

	fpregset = fpregs[:iov.Len-16]

	return fpregset, err
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

// SetSP sets SP to the value specified by 'sp'
func (thread *nativeThread) setSP(sp uint64) (err error) {
	var ir proc.Registers

	ir, err = registers(thread)
	if err != nil {
		return err
	}

	r := ir.(*linutil.LOONG64Registers)
	r.Regs.Regs[3] = sp
	thread.dbp.execPtraceFunc(func() { err = ptraceSetGRegs(thread.ID, r.Regs) })

	return nil
}

func (thread *nativeThread) SetReg(regNum uint64, reg *op.DwarfRegister) error {
	ir, err := registers(thread)
	if err != nil {
		return err
	}
	r := ir.(*linutil.LOONG64Registers)

	switch regNum {
	case regnum.LOONG64_FP:
		r.Regs.Regs[regnum.LOONG64_FP] = reg.Uint64Val

	case regnum.LOONG64_LR:
		r.Regs.Regs[regnum.LOONG64_LR] = reg.Uint64Val

	case regnum.LOONG64_SP:
		r.Regs.Regs[regnum.LOONG64_SP] = reg.Uint64Val

	case regnum.LOONG64_PC:
		r.Regs.Era = reg.Uint64Val

	default:
		//TODO: when the register calling convention is adopted by
		// Go on loong64 this should be implemented.
		return fmt.Errorf("changing register %d not implemented", regNum)
	}

	thread.dbp.execPtraceFunc(func() { err = ptraceSetGRegs(thread.ID, r.Regs) })

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

	tp_tls := regs.Regs[2]
	r := linutil.NewLOONG64Registers(&regs, thread.dbp.iscgo, tp_tls,
		func(r *linutil.LOONG64Registers) error {
			var floatLoadError error
			r.Fpregs, r.Fpregset, floatLoadError = thread.fpRegisters()
			return floatLoadError
		})

	return r, nil
}
