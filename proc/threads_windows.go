package proc

// #include <windows.h>
import "C"
import (
	"fmt"
	"unsafe"

	sys "golang.org/x/sys/windows"
)

// WaitStatus is a synonym for the platform-specific WaitStatus
type WaitStatus sys.WaitStatus

// OSSpecificDetails holds information specific to the Windows
// operating system / kernel.
type OSSpecificDetails struct {
	hThread sys.Handle
}

func (t *Thread) halt() (err error) {
	// Ignore the request to halt. On Windows, all threads are halted
	// on return from WaitForDebugEvent.
	return nil

	// TODO - This may not be correct in all usages of dbp.Halt.  There
	// are some callers who use dbp.Halt() to stop the process when it is not
	// already broken on a debug event.
}

func (t *Thread) singleStep() error {
	var context C.CONTEXT
	context.ContextFlags = C.CONTEXT_ALL

	// Set the processor TRAP flag
	res := C.GetThreadContext(C.HANDLE(t.os.hThread), &context)
	if res == C.FALSE {
		return fmt.Errorf("could not GetThreadContext")
	}

	context.EFlags |= 0x100

	res = C.SetThreadContext(C.HANDLE(t.os.hThread), &context)
	if res == C.FALSE {
		return fmt.Errorf("could not SetThreadContext")
	}

	// Suspend all threads except this one
	for _, thread := range t.dbp.Threads {
		if thread.ID == t.ID {
			continue
		}
		res := C.SuspendThread(C.HANDLE(thread.os.hThread))
		if res == C.DWORD(0xFFFFFFFF) {
			return fmt.Errorf("could not suspend thread: %d", thread.ID)
		}
	}

	// Continue and wait for the step to complete
	t.dbp.execPtraceFunc(func() {
		res = C.ContinueDebugEvent(C.DWORD(t.dbp.Pid), C.DWORD(t.ID), C.DBG_CONTINUE)
	})
	if res == C.FALSE {
		return fmt.Errorf("could not ContinueDebugEvent.")
	}
	_, err := t.dbp.trapWait(0)
	if err != nil {
		return err
	}

	// Resume all threads except this one
	for _, thread := range t.dbp.Threads {
		if thread.ID == t.ID {
			continue
		}
		res := C.ResumeThread(C.HANDLE(thread.os.hThread))
		if res == C.DWORD(0xFFFFFFFF) {
			return fmt.Errorf("ould not resume thread: %d", thread.ID)
		}
	}

	// Unset the processor TRAP flag
	res = C.GetThreadContext(C.HANDLE(t.os.hThread), &context)
	if res == C.FALSE {
		return fmt.Errorf("could not GetThreadContext")
	}

	context.EFlags &= ^C.DWORD(0x100)

	res = C.SetThreadContext(C.HANDLE(t.os.hThread), &context)
	if res == C.FALSE {
		return fmt.Errorf("could not SetThreadContext")
	}

	return nil
}

func (t *Thread) resume() error {
	t.running = true
	var res C.WINBOOL
	t.dbp.execPtraceFunc(func() {
		//TODO: Note that we are ignoring the thread we were asked to continue and are continuing the
		//thread that we last broke on.
		res = C.ContinueDebugEvent(C.DWORD(t.dbp.Pid), C.DWORD(t.ID), C.DBG_CONTINUE)
	})
	if res == C.FALSE {
		return fmt.Errorf("could not ContinueDebugEvent.")
	}
	return nil
}

func (t *Thread) blocked() bool {
	// TODO: Probably incorrect - what are the runtime functions that
	// indicate blocking on Windows?
	pc, err := t.PC()
	if err != nil {
		return false
	}
	fn := t.dbp.goSymTable.PCToFunc(pc)
	if fn == nil {
		return false
	}
	switch fn.Name {
	case "runtime.kevent", "runtime.usleep":
		return true
	default:
		return false
	}
}

func (t *Thread) stopped() bool {
	// TODO: We are assuming that threads are always stopped
	// during command exection.
	return true
}

func (t *Thread) writeMemory(addr uintptr, data []byte) (int, error) {
	var (
		vmData = C.LPCVOID(unsafe.Pointer(&data[0]))
		vmAddr = C.LPVOID(addr)
		length = C.SIZE_T(len(data))
		count  C.SIZE_T
	)
	ret := C.WriteProcessMemory(C.HANDLE(t.dbp.os.hProcess), vmAddr, vmData, length, &count)
	if ret == C.FALSE {
		return int(count), fmt.Errorf("could not write memory")
	}
	return int(count), nil
}

func (t *Thread) readMemory(addr uintptr, size int) ([]byte, error) {
	if size == 0 {
		return nil, nil
	}
	var (
		buf    = make([]byte, size)
		vmData = C.LPVOID(unsafe.Pointer(&buf[0]))
		vmAddr = C.LPCVOID(addr)
		length = C.SIZE_T(size)
		count  C.SIZE_T
	)
	ret := C.ReadProcessMemory(C.HANDLE(t.dbp.os.hProcess), vmAddr, vmData, length, &count)
	if ret == C.FALSE {
		return nil, fmt.Errorf("could not read memory")
	}
	return buf, nil
}
