package native

import (
	"fmt"
	"github.com/go-delve/delve/pkg/proc"
)

func (t *nativeThread) restoreRegisters(savedRegs proc.Registers) error {
	return fmt.Errorf("restore regs not supported on i386")
}

func (t *nativeThread) writeHardwareBreakpoint(addr uint64, wtype proc.WatchType, idx uint8) error {
	return proc.ErrHWBreakUnsupported
}

func (t *nativeThread) clearHardwareBreakpoint(addr uint64, wtype proc.WatchType, idx uint8) error {
	return proc.ErrHWBreakUnsupported
}

func (t *nativeThread) findHardwareBreakpoint() (*proc.Breakpoint, error) {
	return nil, nil
}
