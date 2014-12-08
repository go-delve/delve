package proctl

import "syscall"

type Regs struct {
	regs *syscall.PtraceRegs
}

func (r *Regs) PC() uint64 {
	return r.regs.PC()
}

func (r *Regs) SP() uint64 {
	return r.regs.Rsp
}

func (r *Regs) SetPC(tid int, pc uint64) error {
	r.regs.SetPC(pc)
	return syscall.PtraceSetRegs(tid, r.regs)
}

func registers(tid int) (Registers, error) {
	var regs syscall.PtraceRegs
	err := syscall.PtraceGetRegs(tid, &regs)
	if err != nil {
		return nil, err
	}
	return &Regs{&regs}, nil
}

func WriteMemory(tid int, addr uintptr, data []byte) (int, error) {
	return syscall.PtracePokeData(tid, addr, data)
}

func ReadMemory(tid int, addr uintptr, data []byte) (int, error) {
	return syscall.PtracePeekData(tid, addr, data)
}
