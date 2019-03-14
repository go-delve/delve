package native

// #cgo LDFLAGS: -lutil
//#include <sys/types.h>
//#include <sys/ptrace.h>
//
// #include <stdlib.h>
// #include "ptrace_freebsd_amd64.h"
import "C"

import (
	"syscall"
	"unsafe"

	sys "golang.org/x/sys/unix"

	"github.com/go-delve/delve/pkg/proc/fbsdutil"
)

// PtraceAttach executes the sys.PtraceAttach call.
// pid must be a PID, not a LWPID
func PtraceAttach(pid int) error {
	return sys.PtraceAttach(pid)
}

// PtraceDetach calls ptrace(PTRACE_DETACH).
func PtraceDetach(pid int) error {
	return sys.PtraceDetach(pid)
}

// PtraceCont executes ptrace PTRACE_CONT
// id may be a PID or an LWPID
func PtraceCont(id, sig int) error {
	return sys.PtraceCont(id, sig)
}

// PtraceSingleStep executes ptrace PTRACE_SINGLE_STEP.
// id may be a PID or an LWPID
func PtraceSingleStep(id int) error {
	return sys.PtraceSingleStep(id)
}

// Get a list of the thread ids of a process
func PtraceGetLwpList(pid int) (tids []int32) {
	num_lwps, _ := C.ptrace_get_num_lwps(C.int(pid))
	tids = make([]int32, num_lwps)
	n, _ := C.ptrace_get_lwp_list(C.int(pid), (*C.int)(unsafe.Pointer(&tids[0])), C.size_t(num_lwps))
	return tids[0:n]
}

// Get info of the thread that caused wpid's process to stop.
func ptraceGetLwpInfo(wpid int) (info sys.PtraceLwpInfoStruct, err error) {
	err = sys.PtraceLwpInfo(wpid, uintptr(unsafe.Pointer(&info)))
	return info, err
}

func PtraceGetRegset(id int) (regset fbsdutil.AMD64Xstate, err error) {
	_, _, err = syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_GETFPREGS, uintptr(id), uintptr(unsafe.Pointer(&regset.AMD64PtraceFpRegs)), 0, 0, 0)
	if err == syscall.Errno(0) || err == syscall.ENODEV {
		var xsave_len C.size_t
		xsave, _ := C.ptrace_get_xsave(C.int(id), &xsave_len)
		defer C.free(unsafe.Pointer(xsave))
		if xsave != nil {
			xsave_sl := C.GoBytes(unsafe.Pointer(xsave), C.int(xsave_len))
			err = fbsdutil.AMD64XstateRead(xsave_sl, false, &regset)
		}
	}
	return
}

// id may be a PID or an LWPID
func ptraceReadData(id int, addr uintptr, data []byte) (n int, err error) {
	return sys.PtraceIo(sys.PIOD_READ_D, id, addr, data, len(data))
}

// id may be a PID or an LWPID
func ptraceWriteData(id int, addr uintptr, data []byte) (n int, err error) {
	return sys.PtraceIo(sys.PIOD_WRITE_D, id, addr, data, len(data))
}
