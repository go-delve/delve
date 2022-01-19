//go:build (linux && 386) || (darwin && arm64) || (windows && arm64) || (linux && ppc64le)
// +build linux,386 darwin,arm64 windows,arm64 linux,ppc64le

package native

import (
	"errors"

	"github.com/go-delve/delve/pkg/proc"
)

func (t *nativeThread) findHardwareBreakpoint() (*proc.Breakpoint, error) {
	return nil, errors.New("hardware breakpoints not supported")
}

func (t *nativeThread) writeHardwareBreakpoint(addr uint64, wtype proc.WatchType, idx uint8) error {
	return errors.New("hardware breakpoints not supported")
}

func (t *nativeThread) clearHardwareBreakpoint(addr uint64, wtype proc.WatchType, idx uint8) error {
	return errors.New("hardware breakpoints not supported")
}
