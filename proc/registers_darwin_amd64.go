package proc

// #include "threads_darwin.h"
import "C"
import "fmt"

type Regs struct {
	pc, sp, cx uint64
}

func (r *Regs) String() string {
	return fmt.Sprintf("pc = 0x%x, sp = 0x%x, cx = 0x%x",
		r.PC(), r.SP(), r.CX())
}

func (r *Regs) PC() uint64 {
	return r.pc
}

func (r *Regs) SP() uint64 {
	return r.sp
}

func (r *Regs) CX() uint64 {
	return r.cx
}

func (r *Regs) SetPC(thread *Thread, pc uint64) error {
	kret := C.set_pc(thread.os.thread_act, C.uint64_t(pc))
	if kret != C.KERN_SUCCESS {
		return fmt.Errorf("could not set pc")
	}
	return nil
}

func registers(thread *Thread) (Registers, error) {
	var state C.x86_thread_state64_t
	kret := C.get_registers(C.mach_port_name_t(thread.os.thread_act), &state)
	if kret != C.KERN_SUCCESS {
		return nil, fmt.Errorf("could not get registers")
	}
	regs := &Regs{pc: uint64(state.__rip), sp: uint64(state.__rsp), cx: uint64(state.__rcx)}
	return regs, nil
}
