// +build !windows

package terminal

import (
	"io"
	"os"
)

// getColorableWriter returns two values. First is Writer supported colors.
// If return nil, colors will be disabled.
func getColorableWriter() io.Writer {
	return os.Stdout
}
