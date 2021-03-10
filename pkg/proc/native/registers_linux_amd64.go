package native

import (
	"fmt"
	"syscall"
	"unsafe"

	sys "golang.org/x/sys/unix"

	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/amd64util"
	"github.com/go-delve/delve/pkg/proc/linutil"
)

// SetPC sets RIP to the value specified by 'pc'.
func (thread *nativeThread) setPC(pc uint64) error {
	ir, err := registers(thread)
	if err != nil {
		return err
	}
	r := ir.(*linutil.AMD64Registers)
	r.Regs.Rip = pc
	thread.dbp.execPtraceFunc(func() { err = sys.PtraceSetRegs(thread.ID, (*sys.PtraceRegs)(r.Regs)) })
	return err
}

// SetReg changes the value of the specified register.
func (thread *nativeThread) SetReg(regNum uint64, reg *op.DwarfRegister) error {
	ir, err := registers(thread)
	if err != nil {
		return err
	}
	r := ir.(*linutil.AMD64Registers)
	fpchanged, err := r.SetReg(regNum, reg)
	if err != nil {
		return err
	}
	thread.dbp.execPtraceFunc(func() {
		err = sys.PtraceSetRegs(thread.ID, (*sys.PtraceRegs)(r.Regs))
		if err != nil {
			return
		}
		if fpchanged && r.Fpregset != nil && r.Fpregset.Xsave != nil {
			iov := sys.Iovec{Base: &r.Fpregset.Xsave[0], Len: uint64(len(r.Fpregset.Xsave))}
			_, _, err = syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_SETREGSET, uintptr(thread.ID), _NT_X86_XSTATE, uintptr(unsafe.Pointer(&iov)), 0, 0)
			if err == syscall.Errno(0) {
				err = nil
			}
		}
	})
	return err
}

func registers(thread *nativeThread) (proc.Registers, error) {
	var (
		regs linutil.AMD64PtraceRegs
		err  error
	)
	thread.dbp.execPtraceFunc(func() { err = sys.PtraceGetRegs(thread.ID, (*sys.PtraceRegs)(&regs)) })
	if err != nil {
		return nil, err
	}
	r := linutil.NewAMD64Registers(&regs, func(r *linutil.AMD64Registers) error {
		var fpregset amd64util.AMD64Xstate
		var floatLoadError error
		r.Fpregs, fpregset, floatLoadError = thread.fpRegisters()
		r.Fpregset = &fpregset
		return floatLoadError
	})
	return r, nil
}

const _NT_X86_XSTATE = 0x202

func (thread *nativeThread) fpRegisters() (regs []proc.Register, fpregs amd64util.AMD64Xstate, err error) {
	thread.dbp.execPtraceFunc(func() { fpregs, err = ptraceGetRegset(thread.ID) })
	regs = fpregs.Decode()
	if err != nil {
		err = fmt.Errorf("could not get floating point registers: %v", err.Error())
	}
	return
}
