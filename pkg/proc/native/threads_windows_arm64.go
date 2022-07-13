package native

import (
	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/winutil"
)

func newContext() *winutil.ARM64CONTEXT {
	return winutil.NewARM64CONTEXT()
}

func newRegisters(context *winutil.ARM64CONTEXT, TebBaseAddress uint64) *winutil.ARM64Registers {
	return winutil.NewARM64Registers(context, TebBaseAddress)
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
