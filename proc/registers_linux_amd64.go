package proc

import "fmt"
import sys "golang.org/x/sys/unix"

type Regs struct {
	regs *sys.PtraceRegs
}

func (r *Regs) String() string {
	return fmt.Sprintf("pc = 0x%x, sp = 0x%x, cx = 0x%x",
		r.PC(), r.SP(), r.CX())
}

func (r *Regs) PC() uint64 {
	return r.regs.PC()
}

func (r *Regs) SP() uint64 {
	return r.regs.Rsp
}

func (r *Regs) CX() uint64 {
	return r.regs.Rcx
}

func (r *Regs) SetPC(thread *Thread, pc uint64) (err error) {
	r.regs.SetPC(pc)
	thread.dbp.execPtraceFunc(func() { err = sys.PtraceSetRegs(thread.Id, r.regs) })
	return
}

func registers(thread *Thread) (Registers, error) {
	var (
		regs sys.PtraceRegs
		err  error
	)
	thread.dbp.execPtraceFunc(func() { err = sys.PtraceGetRegs(thread.Id, &regs) })
	if err != nil {
		return nil, err
	}
	return &Regs{&regs}, nil
}
