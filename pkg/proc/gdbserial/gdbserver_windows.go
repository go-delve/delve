package gdbserial

import "syscall"

func sysProcAttr(foreground bool) *syscall.SysProcAttr {
	return nil
}

func foregroundSignalsIgnore() {
}
