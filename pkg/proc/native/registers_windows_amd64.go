package native

import (
	"fmt"
	"unsafe"

	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/winutil"
)

// SetPC sets the RIP register to the value specified by `pc`.
func (thread *nativeThread) SetPC(pc uint64) error {
	context := winutil.NewCONTEXT()
	context.ContextFlags = _CONTEXT_ALL

	err := _GetThreadContext(thread.os.hThread, context)
	if err != nil {
		return err
	}

	context.Rip = pc

	return _SetThreadContext(thread.os.hThread, context)
}

// SetSP sets the RSP register to the value specified by `sp`.
func (thread *nativeThread) SetSP(sp uint64) error {
	context := winutil.NewCONTEXT()
	context.ContextFlags = _CONTEXT_ALL

	err := _GetThreadContext(thread.os.hThread, context)
	if err != nil {
		return err
	}

	context.Rsp = sp

	return _SetThreadContext(thread.os.hThread, context)
}

func (thread *nativeThread) SetDX(dx uint64) error {
	context := winutil.NewCONTEXT()
	context.ContextFlags = _CONTEXT_ALL

	err := _GetThreadContext(thread.os.hThread, context)
	if err != nil {
		return err
	}

	context.Rdx = dx

	return _SetThreadContext(thread.os.hThread, context)
}

func registers(thread *nativeThread) (proc.Registers, error) {
	context := winutil.NewCONTEXT()

	context.ContextFlags = _CONTEXT_ALL
	err := _GetThreadContext(thread.os.hThread, context)
	if err != nil {
		return nil, err
	}

	var threadInfo _THREAD_BASIC_INFORMATION
	status := _NtQueryInformationThread(thread.os.hThread, _ThreadBasicInformation, uintptr(unsafe.Pointer(&threadInfo)), uint32(unsafe.Sizeof(threadInfo)), nil)
	if !_NT_SUCCESS(status) {
		return nil, fmt.Errorf("NtQueryInformationThread failed: it returns 0x%x", status)
	}

	return winutil.NewAMD64Registers(context, uint64(threadInfo.TebBaseAddress)), nil
}
