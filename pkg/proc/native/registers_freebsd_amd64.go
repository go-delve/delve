package native

import (
	"fmt"

	sys "golang.org/x/sys/unix"

	"github.com/go-delve/delve/pkg/dwarf/op"
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
func (thread *nativeThread) SetReg(regNum uint64, reg *op.DwarfRegister) error {
	ir, err := registers(thread)
	if err != nil {
		return err
	}
	r := ir.(*fbsdutil.AMD64Registers)
	fpchanged, err := r.SetReg(regNum, reg)
	if err != nil {
		return err
	}
	return setRegisters(thread, r, fpchanged)
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
		var floatLoadError error
		r.Fpregs, r.Fpregset, floatLoadError = thread.fpRegisters()
		return floatLoadError
	})
	return r, nil
}

func (thread *nativeThread) fpRegisters() (regs []proc.Register, fpregs *amd64util.AMD64Xstate, err error) {
	thread.dbp.execPtraceFunc(func() { fpregs, err = ptraceGetRegset(thread.ID) })
	if err != nil {
		err = fmt.Errorf("could not get floating point registers: %v", err.Error())
	}
	regs = fpregs.Decode()
	return
}

func setRegisters(thread *nativeThread, r *fbsdutil.AMD64Registers, setFP bool) (err error) {
	thread.dbp.execPtraceFunc(func() {
		err = sys.PtraceSetRegs(thread.ID, (*sys.Reg)(r.Regs))
		if err != nil {
			return
		}
		if setFP && r.Fpregset != nil {
			err = ptraceSetRegset(thread.ID, r.Fpregset)
		}
	})
	return
}
