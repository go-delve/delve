package proc

// #include "threads_darwin.h"
import "C"
import (
	"fmt"
	"unsafe"
)

type OSSpecificDetails struct {
	thread_act C.thread_act_t
	registers  C.x86_thread_state64_t
}

func (t *Thread) Halt() error {
	var kret C.kern_return_t
	kret = C.thread_suspend(t.os.thread_act)
	if kret != C.KERN_SUCCESS {
		return fmt.Errorf("could not suspend thread %d", t.Id)
	}
	t.running = false
	return nil
}

func (t *Thread) singleStep() error {
	kret := C.single_step(t.os.thread_act)
	if kret != C.KERN_SUCCESS {
		return fmt.Errorf("could not single step")
	}
	t.dbp.trapWait(0)
	kret = C.clear_trap_flag(t.os.thread_act)
	if kret != C.KERN_SUCCESS {
		return fmt.Errorf("could not clear CPU trap flag")
	}
	return nil
}

func (t *Thread) resume() error {
	t.running = true
	// TODO(dp) set flag for ptrace stops
	var err error
	t.dbp.execPtraceFunc(func() { err = PtraceCont(t.dbp.Pid, 0) })
	if err == nil {
		return nil
	}
	kret := C.resume_thread(t.os.thread_act)
	if kret != C.KERN_SUCCESS {
		return fmt.Errorf("could not continue thread")
	}
	return nil
}

func (t *Thread) blocked() bool {
	// TODO(dp) cache the func pc to remove this lookup
	pc, _ := t.PC()
	fn := t.dbp.goSymTable.PCToFunc(pc)
	if fn == nil {
		return false
	}
	switch fn.Name {
	case "runtime.kevent", "runtime.mach_semaphore_wait", "runtime.usleep":
		return true
	default:
		return false
	}
}

func writeMemory(thread *Thread, addr uintptr, data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	var (
		vm_data = unsafe.Pointer(&data[0])
		vm_addr = C.mach_vm_address_t(addr)
		length  = C.mach_msg_type_number_t(len(data))
	)
	if ret := C.write_memory(thread.dbp.os.task, vm_addr, vm_data, length); ret < 0 {
		return 0, fmt.Errorf("could not write memory")
	}
	return len(data), nil
}

func readMemory(thread *Thread, addr uintptr, data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	var (
		vm_data = unsafe.Pointer(&data[0])
		vm_addr = C.mach_vm_address_t(addr)
		length  = C.mach_msg_type_number_t(len(data))
	)

	ret := C.read_memory(thread.dbp.os.task, vm_addr, vm_data, length)
	if ret < 0 {
		return 0, fmt.Errorf("could not read memory")
	}
	return len(data), nil
}
