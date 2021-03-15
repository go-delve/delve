package native

import (
	"fmt"

	sys "golang.org/x/sys/unix"

	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/regnum"
	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/amd64util"
	"github.com/go-delve/delve/pkg/proc/linutil"
)

// setPC sets EIP to the value specified by 'pc'.
func (thread *nativeThread) setPC(pc uint64) error {
	ir, err := registers(thread)
	if err != nil {
		return err
	}
	r := ir.(*linutil.I386Registers)
	r.Regs.Eip = int32(pc)
	thread.dbp.execPtraceFunc(func() { err = sys.PtraceSetRegs(thread.ID, (*sys.PtraceRegs)(r.Regs)) })
	return err
}

func (thread *nativeThread) SetReg(regNum uint64, reg *op.DwarfRegister) error {
	ir, err := registers(thread)
	if err != nil {
		return err
	}
	r := ir.(*linutil.I386Registers)
	switch regNum {
	case regnum.I386_Eip:
		r.Regs.Eip = int32(reg.Uint64Val)
	case regnum.I386_Esp:
		r.Regs.Esp = int32(reg.Uint64Val)
	case regnum.I386_Edx:
		r.Regs.Edx = int32(reg.Uint64Val)
	default:
		//TODO(aarzilli): when the register calling convention is adopted by Go on
		// i386 this should be implemented.
		return fmt.Errorf("changing register %d not implemented", regNum)
	}
	thread.dbp.execPtraceFunc(func() { err = sys.PtraceSetRegs(thread.ID, (*sys.PtraceRegs)(r.Regs)) })
	return err
}

func registers(thread *nativeThread) (proc.Registers, error) {
	var (
		regs linutil.I386PtraceRegs
		err  error
	)
	thread.dbp.execPtraceFunc(func() { err = sys.PtraceGetRegs(thread.ID, (*sys.PtraceRegs)(&regs)) })
	if err != nil {
		return nil, err
	}
	r := linutil.NewI386Registers(&regs, func(r *linutil.I386Registers) error {
		var fpregset amd64util.AMD64Xstate
		var floatLoadError error
		r.Fpregs, fpregset, floatLoadError = thread.fpRegisters()
		r.Fpregset = &fpregset
		return floatLoadError
	})
	thread.dbp.execPtraceFunc(func() {
		tls, _ := ptraceGetTls(regs.Xgs, thread.ThreadID())
		r.Tls = uint64(tls)
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
