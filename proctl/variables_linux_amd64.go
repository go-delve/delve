package proctl

import (
	"syscall"

	"github.com/derekparker/delve/dwarf/op"
)

func calculateOffset(cfaOffset int64, instructions []byte, regs *syscall.PtraceRegs) (int64, error) {
	offset, err := op.ExecuteStackProgram(cfaOffset, instructions)
	if err != nil {
		return 0, err
	}
	return int64(regs.Rsp) + offset, nil
}
