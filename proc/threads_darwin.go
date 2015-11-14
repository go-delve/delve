package proc

// #include "threads_darwin.h"
// #include "proc_darwin.h"
import "C"
import (
	"fmt"
	"unsafe"
)

type OSSpecificDetails struct {
	thread_act C.thread_act_t
	registers  C.x86_thread_state64_t
}

var ErrContinueThread = fmt.Errorf("could not continue thread")

func (t *Thread) halt() (err error) {
	kret := C.thread_suspend(t.os.thread_act)
	if kret != C.KERN_SUCCESS {
		errStr := C.GoString(C.mach_error_string(C.mach_error_t(kret)))
		err = fmt.Errorf("could not suspend thread %d %s", t.Id, errStr)
		return
	}
	return
}

func (t *Thread) singleStep() error {
	kret := C.single_step(t.os.thread_act)
	if kret != C.KERN_SUCCESS {
		return fmt.Errorf("could not single step")
	}
	for {
		port := C.mach_port_wait(t.dbp.os.portSet, C.int(0))
		if port == C.mach_port_t(t.Id) {
			break
		}
	}

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
		return ErrContinueThread
	}
	return nil
}

func (thread *Thread) blocked() bool {
	// TODO(dp) cache the func pc to remove this lookup
	pc, err := thread.PC()
	if err != nil {
		return false
	}
	fn := thread.dbp.goSymTable.PCToFunc(pc)
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

func (thread *Thread) stopped() bool {
	return C.thread_blocked(thread.os.thread_act) > C.int(0)
}

func (thread *Thread) writeMemory(addr uintptr, data []byte) (int, error) {
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

func (thread *Thread) readMemory(addr uintptr, size int) ([]byte, error) {
	if size == 0 {
		return nil, nil
	}
	var (
		buf     = make([]byte, size)
		vm_data = unsafe.Pointer(&buf[0])
		vm_addr = C.mach_vm_address_t(addr)
		length  = C.mach_msg_type_number_t(size)
	)

	ret := C.read_memory(thread.dbp.os.task, vm_addr, vm_data, length)
	if ret < 0 {
		return nil, fmt.Errorf("could not read memory")
	}
	return buf, nil
}
