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
		pattern += ".exe"
		// Try to create the file atomically with O_EXCL to avoid race conditions
		f, err := os.OpenFile(pattern, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			// Successfully created the file
			r := f.Name()
			f.Close()
			return r
		}
		// If file already exists or other error, fall back to CreateTemp
		// (we don't need to specifically check os.IsExist because CreateTemp will handle any case)
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
