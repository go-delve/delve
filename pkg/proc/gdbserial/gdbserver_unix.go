// +build linux darwin

package gdbserial

import "syscall"

func backgroundSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pgid: 0, Foreground: false}
}
