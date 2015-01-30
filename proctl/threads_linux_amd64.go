package proctl

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

func (r *Regs) SetPC(tid int, pc uint64) error {
	r.regs.SetPC(pc)
	return sys.PtraceSetRegs(tid, r.regs)
}

func registers(tid int) (Registers, error) {
	var regs sys.PtraceRegs
	err := sys.PtraceGetRegs(tid, &regs)
	if err != nil {
		return nil, err
	}
	return &Regs{&regs}, nil
}

func writeMemory(tid int, addr uintptr, data []byte) (int, error) {
	return sys.PtracePokeData(tid, addr, data)
}

func readMemory(tid int, addr uintptr, data []byte) (int, error) {
	return sys.PtracePeekData(tid, addr, data)
}

func clearHardwareBreakpoint(reg, tid int) error {
	return setHardwareBreakpoint(reg, tid, 0)
}
