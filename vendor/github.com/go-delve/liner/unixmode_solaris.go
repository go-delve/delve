package liner

import (
	"golang.org/x/sys/unix"
)

func (mode *termios) ApplyMode() error {
	return unix.IoctlSetTermios(unix.Stdin, setTermios, (*unix.Termios)(mode))
}

// TerminalMode returns the current terminal input mode as an InputModeSetter.
//
// This function is provided for convenience, and should
// not be necessary for most users of liner.
func TerminalMode() (ModeApplier, error) {
	return getMode(unix.Stdin)
}

func getMode(handle int) (*termios, error) {
	tos, err := unix.IoctlGetTermios(handle, getTermios)
	return (*termios)(tos), err
}
