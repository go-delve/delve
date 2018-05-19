package gdbserial

import "syscall"

func backgroundSysProcAttr() *syscall.SysProcAttr {
	return nil
}

func moveToForeground(pid int) {
	panic("lldb backend not supported on windows")
}
