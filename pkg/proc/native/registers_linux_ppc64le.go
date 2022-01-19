package native

import (
	"debug/elf"
	"syscall"
	"unsafe"

	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/regnum"
	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/linutil"
	sys "golang.org/x/sys/unix"
)

const (
	_PPC64LE_GPREGS_SIZE = 44 * 8
	_PPC64LE_FPREGS_SIZE = 33*8 + 8
)

func ptraceGetGRegs(pid int, regs *linutil.PPC64LEPtraceRegs) (err error) {
	sys.PtraceGetRegs(pid, (*sys.PtraceRegs)(regs))
	if err == syscall.Errno(0) {
		err = nil
	}
	return
}

func ptraceSetGRegs(pid int, regs *linutil.PPC64LEPtraceRegs) (err error) {
	sys.PtraceSetRegs(pid, (*sys.PtraceRegs)(regs))
	if err == syscall.Errno(0) {
		err = nil
	}
	return
}

func ptraceGetFpRegset(tid int) (fpregset []byte, err error) {
	var ppc64leFpregs [_PPC64LE_FPREGS_SIZE]byte
	iov := sys.Iovec{Base: &ppc64leFpregs[0], Len: _PPC64LE_FPREGS_SIZE}
	_, _, err = syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_GETREGSET, uintptr(tid), uintptr(elf.NT_FPREGSET), uintptr(unsafe.Pointer(&iov)), 0, 0)
	if err != syscall.Errno(0) {
		if err == syscall.ENODEV {
			err = nil
		}
		return
	} else {
		err = nil
	}

	fpregset = ppc64leFpregs[:iov.Len-8]
	return fpregset, err
}

// SetPC sets PC to the value specified by 'pc'.
func (t *nativeThread) setPC(pc uint64) error {
	ir, err := registers(t)
	if err != nil {
		return err
	}
	r := ir.(*linutil.PPC64LERegisters)
	r.Regs.Nip = pc
	t.dbp.execPtraceFunc(func() { err = ptraceSetGRegs(t.ID, r.Regs) })
	return err
}

// SetReg changes the value of the specified register.
func (t *nativeThread) SetReg(regNum uint64, reg *op.DwarfRegister) error {
	ir, err := registers(t)
	if err != nil {
		return err
	}
	r := ir.(*linutil.PPC64LERegisters)

	switch regNum {
	case regnum.PPC64LE_PC:
		r.Regs.Nip = reg.Uint64Val
	case regnum.PPC64LE_SP:
		r.Regs.Gpr[1] = reg.Uint64Val
	case regnum.PPC64LE_LR:
		r.Regs.Link = reg.Uint64Val
	default:
		panic("SetReg")
	}

	t.dbp.execPtraceFunc(func() { err = ptraceSetGRegs(t.ID, r.Regs) })
	return err
}

func registers(thread *nativeThread) (proc.Registers, error) {
	var (
		regs linutil.PPC64LEPtraceRegs
		err  error
	)

	thread.dbp.execPtraceFunc(func() { err = ptraceGetGRegs(thread.ID, &regs) })
	if err != nil {
		return nil, err
	}
	r := linutil.NewPPC64LERegisters(&regs, func(r *linutil.PPC64LERegisters) error {
		var floatLoadError error
		r.Fpregs, r.Fpregset, floatLoadError = thread.fpRegisters()
		return floatLoadError
	})
	return r, nil
}
