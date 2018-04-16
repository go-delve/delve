// +build linux darwin

package gdbserial

import (
	"syscall"
	"unsafe"
)

func backgroundSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pgid: 0, Foreground: false}
}

func moveToForeground(pid int) {
	syscall.Syscall(syscall.SYS_IOCTL, uintptr(0), uintptr(syscall.TIOCSPGRP), uintptr(unsafe.Pointer(&pid)))
}
