package native

// #cgo LDFLAGS: -lutil
//#include <sys/types.h>
//#include <sys/ptrace.h>
//
// #include <stdlib.h>
//
import "C"

import (
	"unsafe"

	sys "golang.org/x/sys/unix"
)

// ptraceAttach executes the sys.PtraceAttach call.
// pid must be a PID, not a LWPID
func ptraceAttach(pid int) error {
	return sys.PtraceAttach(pid)
}

// ptraceDetach calls ptrace(PTRACE_DETACH).
func ptraceDetach(pid int) error {
	return sys.PtraceDetach(pid)
}

// ptraceCont executes ptrace PTRACE_CONT
// id may be a PID or an LWPID
func ptraceCont(id, sig int) error {
	return sys.PtraceCont(id, sig)
}

// ptraceSingleStep executes ptrace PTRACE_SINGLE_STEP.
// id may be a PID or an LWPID
func ptraceSingleStep(id int) error {
	return sys.PtraceSingleStep(id)
}

// Get a list of the thread ids of a process
func ptraceGetLwpList(pid int) (tids []int32) {
	numLWPS := C.ptrace(C.PT_GETNUMLWPS, C.pid_t(pid), C.caddr_t(unsafe.Pointer(uintptr(0))), C.int(0))
	if numLWPS < 0 {
		panic("PT_GETNUMLWPS failed")
	}
	tids = make([]int32, numLWPS)
	n := C.ptrace(C.PT_GETLWPLIST, C.pid_t(pid), C.caddr_t(unsafe.Pointer(&tids[0])), C.int(numLWPS))
	if n < 0 {
		panic("PT_GETLWPLIST failed")
	}
	return tids[0:n]
}

// Get info of the thread that caused wpid's process to stop.
func ptraceGetLwpInfo(wpid int) (info sys.PtraceLwpInfoStruct, err error) {
	err = sys.PtraceLwpInfo(wpid, uintptr(unsafe.Pointer(&info)))
	return info, err
}

// id may be a PID or an LWPID
func ptraceReadData(id int, addr uintptr, data []byte) (n int, err error) {
	return sys.PtraceIO(sys.PIOD_READ_D, id, addr, data, len(data))
}

// id may be a PID or an LWPID
func ptraceWriteData(id int, addr uintptr, data []byte) (n int, err error) {
	return sys.PtraceIO(sys.PIOD_WRITE_D, id, addr, data, len(data))
}
