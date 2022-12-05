package gdbserial

import "syscall"

func sysProcAttr(foreground bool) *syscall.SysProcAttr {
	return nil
}

func foregroundSignalsIgnore() {
}

func tcsetpgrp(fd uintptr, pid int) error {
	return nil
}
