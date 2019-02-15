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
func PtraceDetach(pid, sig int) error {
	_, _, err := sys.Syscall6(sys.SYS_PTRACE, sys.PTRACE_DETACH, uintptr(pid), 1, uintptr(sig), 0, 0)
	if err != syscall.Errno(0) {
		return err
	}
	return nil
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

// Get the lwpid_t of the thread that caused wpid's process to stop, if any.
// Return also the full pl_flags variable, which indicates why the process
// stopped.
func ptraceGetLwpInfo(wpid int) (tid int, flags int, si_code int, err error) {
	var info C.struct_ptrace_lwpinfo
	_, err = C.ptrace_lwp_info(C.int(wpid), &info)

	/*
		if (info.pl_flags & sys.PL_FLAG_SI != 0) {
			 bp_addr := uintptr(info.pl_siginfo.si_addr) - 1
			if (info.pl_siginfo.si_code == sys.TRAP_BRKPT) {
				fmt.Printf("****************************************************** BREAKPOINT hit at: %#v (tid: %v)\n", bp_addr, info.pl_lwpid);
			} else {
				fmt.Printf("****************************************************** TRAP hit at: %#v (tid: %v)\n", bp_addr, info.pl_lwpid);
			}
		}
		fmt.Printf("\t<------------ptraceGetLwpInfo ret: lwpid: %v, pl_flags: %#v, si_code: %#v, err: %v \n",
			info.pl_lwpid, int(info.pl_flags), int(info.pl_siginfo.si_code), err)
	*/

	return int(info.pl_lwpid), int(info.pl_flags), int(info.pl_siginfo.si_code), err
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
	n = len(data)
	_, err = C.ptrace_read(C.int(id), C.uintptr_t(addr), unsafe.Pointer(&data[0]), C.ssize_t(n))
	if err == nil {
		return int(n), err
	} else {
		return 0, err
	}
}

// id may be a PID or an LWPID
func ptraceWriteData(id int, addr uintptr, data []byte) (n int, err error) {
	n = len(data)
	_, err = C.ptrace_write(C.int(id), C.uintptr_t(addr), unsafe.Pointer(&data[0]), C.ssize_t(n))
	if err == nil {
		return int(n), err
	} else {
		return 0, err
	}
}
