package native

import (
	"errors"
	"fmt"
	"unsafe"

	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/amd64util"
	"github.com/go-delve/delve/pkg/proc/winutil"
)

func newContext() *winutil.AMD64CONTEXT {
	return winutil.NewAMD64CONTEXT()
}

func registers(t *nativeThread) (proc.Registers, error) {
	context := newContext()

	context.SetFlags(_CONTEXT_ALL)
	err := t.getContext(context)
	if err != nil {
		return nil, err
	}

	var threadInfo _THREAD_BASIC_INFORMATION
	status := _NtQueryInformationThread(t.os.hThread, _ThreadBasicInformation, uintptr(unsafe.Pointer(&threadInfo)), uint32(unsafe.Sizeof(threadInfo)), nil)
	if !_NT_SUCCESS(status) {
		return nil, fmt.Errorf("NtQueryInformationThread failed: it returns 0x%x", status)
	}

	return winutil.NewAMD64Registers(context, uint64(threadInfo.TebBaseAddress)), nil
}

func (t *nativeThread) setContext(context *winutil.AMD64CONTEXT) error {
	return _SetThreadContext(t.os.hThread, context)
}

func (t *nativeThread) getContext(context *winutil.AMD64CONTEXT) error {
	return _GetThreadContext(t.os.hThread, context)
}

func (t *nativeThread) restoreRegisters(savedRegs proc.Registers) error {
	return t.setContext(savedRegs.(*winutil.AMD64Registers).Context)
}

func (t *nativeThread) withDebugRegisters(f func(*amd64util.DebugRegisters) error) error {
	if !enableHardwareBreakpoints {
		return errors.New("hardware breakpoints not supported")
	}

	context := winutil.NewAMD64CONTEXT()
	context.ContextFlags = _CONTEXT_DEBUG_REGISTERS

	err := t.getContext(context)
	if err != nil {
		return err
	}

	drs := amd64util.NewDebugRegisters(&context.Dr0, &context.Dr1, &context.Dr2, &context.Dr3, &context.Dr6, &context.Dr7)

	err = f(drs)
	if err != nil {
		return err
	}

	if drs.Dirty {
		return t.setContext(context)
	}

	return nil
}
