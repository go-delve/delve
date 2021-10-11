package native

import (
	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/amd64util"
)

func (t *nativeThread) writeHardwareBreakpoint(addr uint64, wtype proc.WatchType, idx uint8) error {
	return t.withDebugRegisters(func(drs *amd64util.DebugRegisters) error {
		return drs.SetBreakpoint(idx, addr, wtype.Read(), wtype.Write(), wtype.Size())
	})
}

func (t *nativeThread) clearHardwareBreakpoint(addr uint64, wtype proc.WatchType, idx uint8) error {
	return t.withDebugRegisters(func(drs *amd64util.DebugRegisters) error {
		drs.ClearBreakpoint(idx)
		return nil
	})
}

func (t *nativeThread) findHardwareBreakpoint() (*proc.Breakpoint, error) {
	var retbp *proc.Breakpoint
	err := t.withDebugRegisters(func(drs *amd64util.DebugRegisters) error {
		ok, idx := drs.GetActiveBreakpoint()
		if ok {
			for _, bp := range t.dbp.Breakpoints().M {
				if bp.WatchType != 0 && bp.HWBreakIndex == idx {
					retbp = bp
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return retbp, nil
}
