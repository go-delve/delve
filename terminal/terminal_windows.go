package terminal

import (
	"io"
	"os"
	"strings"
	"syscall"

	"github.com/mattn/go-colorable"
)

// getColorableWriter will return a writer that is capable
// of interpreting ANSI escape codes for terminal colors.
func getColorableWriter() io.Writer {
	if strings.ToLower(os.Getenv("ConEmuANSI")) == "on" {
		// The ConEmu terminal is installed. Use it.
		return os.Stdout
	}

	const ENABLE_VIRTUAL_TERMINAL_PROCESSING = 0x0004

	h, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	if err != nil {
		return os.Stdout
	}
	var m uint32
	err = syscall.GetConsoleMode(h, &m)
	if err != nil {
		return os.Stdout
	}
	if m&ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0 {
		return os.Stdout
	}
	return colorable.NewColorableStdout()
}
