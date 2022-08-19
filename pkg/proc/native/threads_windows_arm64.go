package native

import (
	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/winutil"
)

func newContext() *winutil.ARM64CONTEXT {
	return winutil.NewARM64CONTEXT()
}

func registers(t *nativeThread) (proc.Registers, error) {
	context := newContext()

	context.SetFlags(_CONTEXT_ALL)
	err := t.getContext(context)
	if err != nil {
		return nil, err
	}

	return winutil.NewARM64Registers(context, t.dbp.iscgo), nil
}

func newRegisters(context *winutil.ARM64CONTEXT, TebBaseAddress uint64, iscgo bool) *winutil.ARM64Registers {
	return winutil.NewARM64Registers(context, iscgo)
}

func (t *nativeThread) setContext(context *winutil.ARM64CONTEXT) error {
	return _SetThreadContext(t.os.hThread, context)
}

func (t *nativeThread) getContext(context *winutil.ARM64CONTEXT) error {
	return _GetThreadContext(t.os.hThread, context)
}

func (t *nativeThread) restoreRegisters(savedRegs proc.Registers) error {
	return t.setContext(savedRegs.(*winutil.ARM64Registers).Context)
}
