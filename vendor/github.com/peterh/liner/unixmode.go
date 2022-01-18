//go:build linux || darwin || freebsd || openbsd || netbsd
// +build linux darwin freebsd openbsd netbsd

package liner

import (
	"syscall"
	"unsafe"
)

func (mode *termios) ApplyMode() error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(syscall.Stdin), setTermios, uintptr(unsafe.Pointer(mode)))

	if errno != 0 {
		return errno
	}
	return nil
}

// TerminalMode returns the current terminal input mode as an InputModeSetter.
//
// This function is provided for convenience, and should
// not be necessary for most users of liner.
func TerminalMode() (ModeApplier, error) {
	return getMode(syscall.Stdin)
}

func getMode(handle int) (*termios, error) {
	var mode termios
	var err error
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(handle), getTermios, uintptr(unsafe.Pointer(&mode)))
	if errno != 0 {
		err = errno
	}

	return &mode, err
}
