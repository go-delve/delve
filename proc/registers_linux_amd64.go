package proc

import sys "golang.org/x/sys/unix"

type Regs struct {
	regs *sys.PtraceRegs
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

func (r *Regs) SetPC(thread *ThreadContext, pc uint64) error {
	r.regs.SetPC(pc)
	return sys.PtraceSetRegs(thread.Id, r.regs)
}

func registers(thread *ThreadContext) (Registers, error) {
	var regs sys.PtraceRegs
	err := sys.PtraceGetRegs(thread.Id, &regs)
	if err != nil {
		return nil, err
	}
	return &Regs{&regs}, nil
}
