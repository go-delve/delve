//go:build !windows

package terminal

import (
	"io"
	"os"
)

// getColorableWriter simply returns stdout on
// *nix machines.
func getColorableWriter() io.Writer {
	return os.Stdout
}
