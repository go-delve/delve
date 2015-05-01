package proctl

import "errors"

// TODO(darwin)
func setHardwareBreakpoint(reg, tid int, addr uint64) error {
	return errors.New("not implemented on darwin")
}

// TODO(darwin)
func clearHardwareBreakpoint(reg, tid int) error {
	return errors.New("not implemented on darwin")
}
