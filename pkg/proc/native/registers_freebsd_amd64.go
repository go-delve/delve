package native

import (
	"fmt"

	sys "golang.org/x/sys/unix"

	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/fbsdutil"
)

// SetPC sets RIP to the value specified by 'pc'.
func (thread *Thread) SetPC(pc uint64) error {
	ir, err := registers(thread, false)
	if err != nil {
		return err
	}
	r := ir.(*fbsdutil.AMD64Registers)
	r.Regs.Rip = int64(pc)
	thread.dbp.execPtraceFunc(func() { err = sys.PtraceSetRegs(thread.ID, (*sys.Reg)(r.Regs)) })
	return err
}

// SetSP sets RSP to the value specified by 'sp'
func (thread *Thread) SetSP(sp uint64) (err error) {
	var ir proc.Registers
	ir, err = registers(thread, false)
	if err != nil {
		return err
	}
	r := ir.(*fbsdutil.AMD64Registers)
	r.Regs.Rsp = int64(sp)
	thread.dbp.execPtraceFunc(func() { err = sys.PtraceSetRegs(thread.ID, (*sys.Reg)(r.Regs)) })
	return
}

func (thread *Thread) SetDX(dx uint64) (err error) {
	var ir proc.Registers
	ir, err = registers(thread, false)
	if err != nil {
		return err
	}
	r := ir.(*fbsdutil.AMD64Registers)
	r.Regs.Rdx = int64(dx)
	thread.dbp.execPtraceFunc(func() { err = sys.PtraceSetRegs(thread.ID, (*sys.Reg)(r.Regs)) })
	return
}

func registers(thread *Thread, floatingPoint bool) (proc.Registers, error) {
	var (
		regs fbsdutil.AMD64PtraceRegs
		err  error
	)
	thread.dbp.execPtraceFunc(func() { err = sys.PtraceGetRegs(thread.ID, (*sys.Reg)(&regs)) })
	if err != nil {
		return nil, err
	}
	var fsbase int64
	thread.dbp.execPtraceFunc(func() { err = sys.PtraceGetFsBase(thread.ID, &fsbase) })
	if err != nil {
		return nil, err
	}
	r := &fbsdutil.AMD64Registers{&regs, nil, nil, uint64(fsbase)}
	if floatingPoint {
		var fpregset fbsdutil.AMD64Xstate
		r.Fpregs, fpregset, err = thread.fpRegisters()
		r.Fpregset = &fpregset
		if err != nil {
			return nil, err
		}
	}
	return r, nil
}

const (
	_X86_XSTATE_MAX_SIZE = 2688
	_NT_X86_XSTATE       = 0x202

	_XSAVE_HEADER_START          = 512
	_XSAVE_HEADER_LEN            = 64
	_XSAVE_EXTENDED_REGION_START = 576
	_XSAVE_SSE_REGION_LEN        = 416
)

func (thread *Thread) fpRegisters() (regs []proc.Register, fpregs fbsdutil.AMD64Xstate, err error) {
	thread.dbp.execPtraceFunc(func() { fpregs, err = PtraceGetRegset(thread.ID) })
	if err != nil {
		err = fmt.Errorf("could not get floating point registers: %v", err.Error())
	}
	regs = fpregs.Decode()
	return
}
