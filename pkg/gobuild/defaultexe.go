package gobuild

import (
	"os"
	"runtime"

	"github.com/go-delve/delve/pkg/logflags"
)

// DefaultDebugBinaryPath returns an unused file path in the current
// directory named 'name' followed by a random string
func DefaultDebugBinaryPath(name string) string {
	pattern := name
	if runtime.GOOS == "windows" {
		pattern += "*.exe"
	}
	f, err := os.CreateTemp(".", pattern)
	if err != nil {
		logflags.DebuggerLogger().Errorf("could not create temporary file for build output: %v", err)
		if runtime.GOOS == "windows" {
			return name + ".exe"
		}
		return name
	}
	r := f.Name()
	f.Close()
	return r
}
