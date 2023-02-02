package terminal

import (
	"syscall"
	"unsafe"
)

var (
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procGetStdHandle               = kernel32.NewProc("GetStdHandle")
	procGetConsoleScreenBufferInfo = kernel32.NewProc("GetConsoleScreenBufferInfo")
)

type coord struct {
	x, y int16
}

type smallRect struct {
	left, top, right, bottom int16
}

type consoleScreenBufferInfo struct {
	dwSize              coord
	dwCursorPosition    coord
	wAttributes         int16
	srWindow            smallRect
	dwMaximumWindowSize coord
}

func (w *pagingWriter) getWindowSize() {
	hout, _, err := procGetStdHandle.Call(uintptr(uint32(-12 & 0xFFFFFFFF))) // stdout handle
	if err != syscall.Errno(0) {
		w.mode = pagingWriterNormal
		return
	}
	var sbi consoleScreenBufferInfo
	_, _, err = procGetConsoleScreenBufferInfo.Call(uintptr(hout), uintptr(unsafe.Pointer(&sbi)))
	if err != syscall.Errno(0) {
		w.mode = pagingWriterNormal
		return
	}
	w.columns = int(sbi.srWindow.right - sbi.srWindow.left + 1)
	w.lines = int(sbi.srWindow.bottom - sbi.srWindow.top + 1)
}
