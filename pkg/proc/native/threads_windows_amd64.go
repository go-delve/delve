package native

import (
	"errors"

	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/amd64util"
	"github.com/go-delve/delve/pkg/proc/winutil"
)

func newContext() *winutil.AMD64CONTEXT {
	return winutil.NewAMD64CONTEXT()
}

func newRegisters(context *winutil.AMD64CONTEXT, TebBaseAddress uint64) *winutil.AMD64Registers {
	return winutil.NewAMD64Registers(context, TebBaseAddress)
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
