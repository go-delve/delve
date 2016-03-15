package terminal

import (
	"os"
	"strings"
	"syscall"
)

// supportsEscapeCodes returns true if console handles escape codes.
func supportsEscapeCodes() bool {
	if strings.ToLower(os.Getenv("TERM")) == "dumb" {
		return false
	}
	if strings.ToLower(os.Getenv("ConEmuANSI")) == "on" {
		// The ConEmu terminal is installed. Use it.
		return true
	}

	const ENABLE_VIRTUAL_TERMINAL_PROCESSING = 0x0004

	h, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	if err != nil {
		return false
	}
	var m uint32
	err = syscall.GetConsoleMode(h, &m)
	if err != nil {
		return false
	}
	return m&ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0
}
