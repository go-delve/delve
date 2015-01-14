package proctl

// #include "threads_darwin.h"
import "C"

type Regs struct {
	pc, sp uint64
}

func (r *Regs) PC() uint64 {
	return r.pc
}

func (r *Regs) SP() uint64 {
	return r.sp
}

func (r *Regs) SetPC(thread *ThreadContext, pc uint64) error {
	C.set_pc(thread.os.thread_act, C.uint64_t(pc))
	return nil
}

func registers(thread *ThreadContext) (Registers, error) {
	state := C.get_registers(C.mach_port_name_t(thread.os.thread_act))
	regs := &Regs{pc: uint64(state.__rip), sp: uint64(state.__rsp)}
	return regs, nil
}
