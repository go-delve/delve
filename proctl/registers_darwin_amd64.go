package proctl

// #include "threads_darwin.h"
import "C"
import "errors"

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
	kret := C.set_pc(thread.os.thread_act, C.uint64_t(pc))
	if kret != C.KERN_SUCCESS {
		return errors.New("could not set pc")
	}
	return nil
}

func registers(thread *ThreadContext) (Registers, error) {
	var state C.x86_thread_state64_t
	kret := C.get_registers(C.mach_port_name_t(thread.os.thread_act), &state)
	if kret != C.KERN_SUCCESS {
		return nil, errors.New("could not get registers")
	}
	regs := &Regs{pc: uint64(state.__rip), sp: uint64(state.__rsp)}
	return regs, nil
}
