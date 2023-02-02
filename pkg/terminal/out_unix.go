//go:build linux || darwin || freebsd
// +build linux darwin freebsd

package terminal

import (
	"syscall"
	"unsafe"
)

type winSize struct {
	row, col       uint16
	xpixel, ypixel uint16
}

func (w *pagingWriter) getWindowSize() {
	var ws winSize
	ok, _, _ := syscall.Syscall(syscall.SYS_IOCTL, uintptr(syscall.Stdout), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws)))
	if int(ok) < 0 {
		w.mode = pagingWriterNormal
		return
	}
	w.lines = int(ws.row)
	w.columns = int(ws.col)
}
