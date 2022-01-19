package native

import (
	"fmt"

	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/linutil"
)

func (t *nativeThread) fpRegisters() ([]proc.Register, []byte, error) {
	var regs []proc.Register
	var fpregs linutil.PPC64LEPtraceFpRegs
	var err error

	t.dbp.execPtraceFunc(func() { fpregs.Fp, err = ptraceGetFpRegset(t.ID) })
	regs = fpregs.Decode()
	if err != nil {
		err = fmt.Errorf("could not get floating point registers: %v", err.Error())
	}
	return regs, fpregs.Fp, err
}

func (t *nativeThread) restoreRegisters(savedRegs proc.Registers) error {
	panic("Unimplemented restoreRegisters method in threads_linux_ppc64le.go")
}
