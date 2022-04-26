package native

import (
	"fmt"

	sys "golang.org/x/sys/unix"

	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/regnum"
	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/amd64util"
	"github.com/go-delve/delve/pkg/proc/fbsdutil"
)

// SetPC sets RIP to the value specified by 'pc'.
func (thread *nativeThread) setPC(pc uint64) error {
	ir, err := registers(thread)
	if err != nil {
		return err
	}
	r := ir.(*fbsdutil.AMD64Registers)
	r.Regs.Rip = int64(pc)
	thread.dbp.execPtraceFunc(func() { err = sys.PtraceSetRegs(thread.ID, (*sys.Reg)(r.Regs)) })
	return err
}

// SetReg changes the value of the specified register.
func (thread *nativeThread) SetReg(regNum uint64, reg *op.DwarfRegister) (err error) {
	ir, err := registers(thread)
	if err != nil {
		return err
	}
	r := ir.(*fbsdutil.AMD64Registers)
	switch regNum {
	case regnum.AMD64_Rax:
		r.Regs.Rax = int64(reg.Uint64Val)
	case regnum.AMD64_Rbx:
		r.Regs.Rbx = int64(reg.Uint64Val)
	case regnum.AMD64_Rcx:
		r.Regs.Rcx = int64(reg.Uint64Val)
	case regnum.AMD64_Rdx:
		r.Regs.Rdx = int64(reg.Uint64Val)
	case regnum.AMD64_Rsi:
		r.Regs.Rsi = int64(reg.Uint64Val)
	case regnum.AMD64_Rdi:
		r.Regs.Rdi = int64(reg.Uint64Val)
	case regnum.AMD64_Rbp:
		r.Regs.Rbp = int64(reg.Uint64Val)
	case regnum.AMD64_Rsp:
		r.Regs.Rsp = int64(reg.Uint64Val)
	case regnum.AMD64_R8:
		r.Regs.R8 = int64(reg.Uint64Val)
	case regnum.AMD64_R9:
		r.Regs.R9 = int64(reg.Uint64Val)
	case regnum.AMD64_R10:
		r.Regs.R10 = int64(reg.Uint64Val)
	case regnum.AMD64_R11:
		r.Regs.R11 = int64(reg.Uint64Val)
	case regnum.AMD64_R12:
		r.Regs.R12 = int64(reg.Uint64Val)
	case regnum.AMD64_R13:
		r.Regs.R13 = int64(reg.Uint64Val)
	case regnum.AMD64_R14:
		r.Regs.R14 = int64(reg.Uint64Val)
	case regnum.AMD64_R15:
		r.Regs.R15 = int64(reg.Uint64Val)
	case regnum.AMD64_Rip:
		r.Regs.Rip = int64(reg.Uint64Val)
	default:
		return fmt.Errorf("changing register %d not implemented", regNum)
	}
	thread.dbp.execPtraceFunc(func() { err = sys.PtraceSetRegs(thread.ID, (*sys.Reg)(r.Regs)) })
	return
}

func registers(thread *nativeThread) (proc.Registers, error) {
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
	r := fbsdutil.NewAMD64Registers(&regs, uint64(fsbase), func(r *fbsdutil.AMD64Registers) error {
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
	if err != nil {
		err = fmt.Errorf("could not get floating point registers: %v", err.Error())
	}
	regs = fpregs.Decode()
	return
}
