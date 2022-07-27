package native

import (
	"fmt"
	"unsafe"

	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/proc"
)

// SetPC sets the RIP register to the value specified by `pc`.
func (thread *nativeThread) setPC(pc uint64) error {
	context := newContext()
	context.SetFlags(_CONTEXT_ALL)

	err := thread.getContext(context)
	if err != nil {
		return err
	}

	context.SetPC(pc)

	return thread.setContext(context)
}

// SetReg changes the value of the specified register.
func (thread *nativeThread) SetReg(regNum uint64, reg *op.DwarfRegister) error {
	context := newContext()
	context.SetFlags(_CONTEXT_ALL)
	err := thread.getContext(context)
	if err != nil {
		return err
	}

	err = context.SetReg(regNum, reg)
	if err != nil {
		return err
	}

	return thread.setContext(context)
}

func registers(thread *nativeThread) (proc.Registers, error) {
	context := newContext()

	context.SetFlags(_CONTEXT_ALL)
	err := thread.getContext(context)
	if err != nil {
		return nil, err
	}

	var threadInfo _THREAD_BASIC_INFORMATION
	status := _NtQueryInformationThread(thread.os.hThread, _ThreadBasicInformation, uintptr(unsafe.Pointer(&threadInfo)), uint32(unsafe.Sizeof(threadInfo)), nil)
	if !_NT_SUCCESS(status) {
		return nil, fmt.Errorf("NtQueryInformationThread failed: it returns 0x%x", status)
	}

	return newRegisters(context, uint64(threadInfo.TebBaseAddress)), nil
}
