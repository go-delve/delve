//go:build linux || darwin || freebsd
// +build linux darwin freebsd

package gdbserial

import (
	"os/signal"
	"syscall"
)

func sysProcAttr(foreground bool) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pgid: 0, Foreground: foreground}
}

func foregroundSignalsIgnore() {
	signal.Ignore(syscall.SIGTTOU, syscall.SIGTTIN)
}
