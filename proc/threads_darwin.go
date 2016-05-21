package proc

// #include "threads_darwin.h"
// #include "proc_darwin.h"
import "C"
import (
	"errors"
	"fmt"
	"strings"
	"unsafe"
)

// OSSpecificDetails holds information specific to the OSX/Darwin
// operating system / kernel.
type OSSpecificDetails struct {
	threadAct C.thread_act_t
	registers C.x86_thread_state64_t
	hdr       C.mach_msg_header_t
	sig       C.int
	msgStop   bool
}

// ErrContinueThread is the error returned when a thread could not
// be continued.
var ErrContinueThread = fmt.Errorf("could not continue thread")

func (t *Thread) halt() error {
	kret := C.thread_suspend(t.os.threadAct)
	if kret != C.KERN_SUCCESS {
		errStr := C.GoString(C.mach_error_string(C.mach_error_t(kret)))
		if threadExited(kret) {
			return ThreadExitedErr
		}
		return fmt.Errorf("could not suspend thread %d %s", t.ID, errStr)
	}
	return nil
}

func (t *Thread) singleStep() error {
	if kret := C.set_single_step_flag(t.os.threadAct); kret != C.KERN_SUCCESS {
		if threadExited(kret) {
			return ThreadExitedErr
		}
		return fmt.Errorf("could not single step")
	}
	C.task_suspend(t.p.os.task)
	for _, th := range t.p.Threads {
		if err := th.halt(); err != nil {
			return err
		}
	}
	if err := t.resume(); err != nil {
		return err
	}
	C.task_resume(t.p.os.task)
	ws, _, err := Wait(t.p)
	if err != nil {
		return err
	}
	if ws.Exited() {
		status, err := Mourn(t.p)
		if err != nil {
			return err
		}
		return ProcessExitedError{Pid: t.p.Pid, Status: status}
	}
	if kret := C.clear_trap_flag(t.os.threadAct); kret != C.KERN_SUCCESS {
		if threadExited(kret) {
			return ThreadExitedErr
		}
		return fmt.Errorf("could not clear CPU trap flag")
	}
	return nil
}

func (t *Thread) resume() error {
	t.running = true
	var kret C.kern_return_t
	if err := t.sendMachReply(); err != nil {
		if threadExited(kret) {
			return ThreadExitedErr
		}
		return ErrContinueThread
	}
	kret = C.resume_thread(t.os.threadAct)
	if kret != C.KERN_SUCCESS {
		if threadExited(kret) {
			return ThreadExitedErr
		}
		return ErrContinueThread
	}
	return nil
}

func threadExited(kret C.kern_return_t) bool {
	errStr := C.GoString(C.mach_error_string(C.mach_error_t(kret)))
	if strings.Contains(errStr, "invalid destination") || strings.Contains(errStr, "terminated") {
		return true
	}
	return false
}

func (t *Thread) resumeWithSig(sig int) error {
	// TODO(derekparker) Maybe keep state around regarding whether we are in
	// a signal stop?
	if err := PtraceThupdate(t.p.Pid, t.os.threadAct, sig); err != nil {
		log.WithError(err).Error("error during PtraceThupdate")
	}
	return t.resume()
}

func (t *Thread) sendMachReply() error {
	var emptyhdr C.mach_msg_header_t
	var kret C.kern_return_t
	if t.os.msgStop {
		t.os.msgStop = false
		kret = C.mach_send_reply(t.os.hdr)
		if kret != C.KERN_SUCCESS {
			if threadExited(kret) {
				return ThreadExitedErr
			}
			return errors.New("could not send mach reply")
		}
		t.os.hdr = emptyhdr
	}
	return nil
}

func (t *Thread) blocked() bool {
	// TODO(dp) cache the func pc to remove this lookup
	pc, err := t.PC()
	if err != nil {
		return false
	}
	fn := t.p.Dwarf.PCToFunc(pc)
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

func (t *Thread) stopped() bool {
	return C.thread_blocked(t.os.threadAct) > C.int(0)
}

func (t *Thread) writeMemory(addr uintptr, data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	var (
		vmData = unsafe.Pointer(&data[0])
		vmAddr = C.mach_vm_address_t(addr)
		length = C.mach_msg_type_number_t(len(data))
	)
	if ret := C.write_memory(t.p.os.task, vmAddr, vmData, length); ret < 0 {
		return 0, fmt.Errorf("could not write memory")
	}
	return len(data), nil
}

func (t *Thread) readMemory(addr uintptr, size int) ([]byte, error) {
	if size == 0 {
		return nil, nil
	}
	var (
		buf    = make([]byte, size)
		vmData = unsafe.Pointer(&buf[0])
		vmAddr = C.mach_vm_address_t(addr)
		length = C.mach_msg_type_number_t(size)
	)

	ret := C.read_memory(t.p.os.task, vmAddr, vmData, length)
	if ret < 0 {
		return nil, fmt.Errorf("could not read memory")
	}
	return buf, nil
}
