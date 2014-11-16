package proctl

import (
	"encoding/binary"
	"syscall"
)

// Takes an offset from RSP and returns the address of the
// instruction the currect function is going to return to.
func (thread *ThreadContext) ReturnAddressFromOffset(offset int64) uint64 {
	regs, err := thread.Registers()
	if err != nil {
		panic("Could not obtain register values")
	}

	retaddr := int64(regs.Rsp) + offset
	data := make([]byte, 8)
	syscall.PtracePeekText(thread.Id, uintptr(retaddr), data)
	return binary.LittleEndian.Uint64(data)
}
