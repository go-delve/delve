// +build linux darwin openbsd freebsd netbsd

package liner

import (
	"os"
	"syscall"
)

func handleCtrlZ() {
	pid := os.Getpid()
	pgrp, err := syscall.Getpgid(pid)
	if err == nil {
		syscall.Kill(-pgrp, syscall.SIGTSTP)
	}
}
