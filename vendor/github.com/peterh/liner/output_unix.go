//go:build linux || darwin || openbsd || freebsd || netbsd
// +build linux darwin openbsd freebsd netbsd

package liner

import (
	"syscall"
	"unsafe"
)

func (s *State) getColumns() bool {
	var ws winSize
	ok, _, _ := syscall.Syscall(syscall.SYS_IOCTL, uintptr(syscall.Stdout),
		syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws)))
	if int(ok) < 0 {
		return false
	}
	s.columns = int(ws.col)
	return true
}
