package native

/*
#include <sys/types.h>
#include <sys/ptrace.h>
*/
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/go-delve/delve/pkg/proc/amd64util"
)

var (
	xsaveLen int
	xsaveErr error
)

func ptraceGetXsaveLen(tid int) (int, error) {
	var info C.struct_ptrace_xstate_info
	ret, err := C.ptrace(C.PT_GETXSTATE_INFO, C.pid_t(tid), C.caddr_t(unsafe.Pointer(&info)), C.int(unsafe.Sizeof(info)))
	if ret == 0 {
		xsaveLen = int(info.xsave_len)
	} else {
		xsaveLen, xsaveErr = -1, err
		return xsaveLen, fmt.Errorf("failed to get xstate info: %v", err)
	}
	return xsaveLen, nil
}

func ptraceXsaveLen(tid int) (int, error) {
	if xsaveLen > 0 {
		return xsaveLen, nil
	}
	if xsaveLen < 0 {
		return xsaveLen, fmt.Errorf("failed to get xstate info: %v", xsaveErr)
	}
	return ptraceGetXsaveLen(tid)
}

// ptraceGetXsave gets the X86 XSAVE data for the given tid.
func ptraceGetXsave(tid int) ([]byte, error) {
	len, err := ptraceXsaveLen(tid)
	if err != nil {
		return nil, err
	}
	xsaveBuf := make([]byte, len)
	ret, err := C.ptrace(C.PT_GETXSTATE, C.pid_t(tid), C.caddr_t(unsafe.Pointer(&xsaveBuf[0])), C.int(len))
	if ret != 0 {
		return nil, fmt.Errorf("failed to get xstate: %v", err)
	}
	return xsaveBuf, nil
}

// ptraceSetXsave sets the X86 XSAVE data for the given tid.
func ptraceSetXsave(tid int, xsaveBuf []byte) error {
	ret, err := C.ptrace(C.PT_SETXSTATE, C.pid_t(tid), C.caddr_t(unsafe.Pointer(&xsaveBuf[0])), C.int(len(xsaveBuf)))
	if ret != 0 {
		return fmt.Errorf("failed to set xstate: %v", err)
	}
	return nil
}

func ptraceGetRegset(id int) (*amd64util.AMD64Xstate, error) {
	var regset amd64util.AMD64Xstate
	ret, err := C.ptrace(C.PT_GETFPREGS, C.pid_t(id), C.caddr_t(unsafe.Pointer(&regset.AMD64PtraceFpRegs)), C.int(0))
	if ret != 0 {
		return nil, fmt.Errorf("failed to get FP registers: %v", err)
	}
	regset.Xsave, err = ptraceGetXsave(id)
	if err != nil {
		return nil, err
	}
	err = amd64util.AMD64XstateRead(regset.Xsave, false, &regset)
	return &regset, err
}

func ptraceSetRegset(id int, regset *amd64util.AMD64Xstate) error {
	ret, err := C.ptrace(C.PT_SETFPREGS, C.pid_t(id), C.caddr_t(unsafe.Pointer(&regset.AMD64PtraceFpRegs)), C.int(0))
	if ret != 0 {
		return fmt.Errorf("failed to set FP registers: %v", err)
	}
	if regset.Xsave != nil {
		return ptraceSetXsave(id, regset.Xsave)
	}
	return nil
}
