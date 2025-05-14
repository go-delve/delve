package gobuild

import (
	"os"
	"path/filepath"
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

	// check for existing file that matches the pattern
	files, err := filepath.Glob(pattern)

	// if there are no files, create a new one using the pattern directly
	if len(files) == 0 {

		pattern = name
		if runtime.GOOS == "windows" {
			pattern += ".exe"
		}
		return pattern
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
