// +build !windows

package terminal

import (
	"os"
	"strings"
)

// supportsEscapeCodes returns true if console handles escape codes.
func supportsEscapeCodes() bool {
	return strings.ToLower(os.Getenv("TERM")) != "dumb"
}
