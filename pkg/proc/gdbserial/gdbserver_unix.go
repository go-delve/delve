//go:build linux || darwin || freebsd

package gdbserial

import (
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"
)

func sysProcAttr(foreground bool) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pgid: 0, Foreground: foreground}
}

func foregroundSignalsIgnore() {
	signal.Ignore(syscall.SIGTTOU, syscall.SIGTTIN)
}

func tcsetpgrp(fd uintptr, pid int) error {
	pgid, _ := syscall.Getpgid(pid)
	if pid == pgid {
		return unix.IoctlSetPointerInt(int(fd), unix.TIOCSPGRP, pid)
	}
	return nil
}
