//go:build !windows && go1.12
// +build !windows,go1.12

package pty

import "os"

func ioctl(f *os.File, cmd, ptr uintptr) error {
	sc, e := f.SyscallConn()
	if e != nil {
		return ioctl_inner(f.Fd(), cmd, ptr) // fall back to blocking io (old behavior)
	}

	ch := make(chan error, 1)
	defer close(ch)

	e = sc.Control(func(fd uintptr) { ch <- ioctl_inner(fd, cmd, ptr) })
	if e != nil {
		return e
	}
	e = <-ch
	return e
}
