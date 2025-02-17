// Package tcxpgrp implements POSIX.1 (IEEE Std 1003.1) tcgetpgrp and
// tcsetpgrp functions for Go programming language.
//
// There is also a function for determining if the calling process is a
// foreground process.
//
// This package is Linux/UNIX specific.
package tcxpgrp

import "golang.org/x/sys/unix"

// TcGetpgrp gets the process group ID of the foreground process
// group associated with the terminal referred to by fd.
//
// See POSIX.1 documentation for more details:
// http://pubs.opengroup.org/onlinepubs/009695399/functions/tcgetpgrp.html
func TcGetpgrp(fd int) (pgrp int, err error) {
	return unix.IoctlGetInt(fd, unix.TIOCGPGRP)
}

// TcSetpgrp sets the foreground process group ID associated with the
// terminal referred to by fd to pgrp.
//
// See POSIX.1 documentation for more details:
// https://pubs.opengroup.org/onlinepubs/9699919799/functions/tcsetpgrp.html
func TcSetpgrp(fd int, pgrp int) (err error) {
	return unix.IoctlSetPointerInt(fd, unix.TIOCSPGRP, pgrp)
}

// IsForeground returns true if the calling process is a foreground process.
//
// Note that the foreground/background status of a process can change
// at any moment if the user utilizes the shell job control commands (fg/bg).
//
// Example use for command line tools: suppress extra output if a
// process is running in background, provide verbose output when
// running on foreground.
func IsForeground() bool {
	fd, err := unix.Open("/dev/tty", 0666, unix.O_RDONLY)
	if err != nil {
		return false
	}
	defer unix.Close(fd)

	pgrp1, err := TcGetpgrp(fd)
	if err != nil {
		return false
	}
	pgrp2 := unix.Getpgrp()
	return pgrp1 == pgrp2
}
