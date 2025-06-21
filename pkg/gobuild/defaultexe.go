package gobuild

import (
	"fmt"
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
		// check for existing file that matches the pattern
		globPattern := name + "*.exe"
		pattern += ".exe"
		files, err := filepath.Glob(globPattern)

		if err != nil {
			logflags.DebuggerLogger().Errorf("could not create temporary file for build output: %v", err)
		}

		// if there are no files, create a new one using the pattern directly
		if len(files) == 0 {

			// create a new file with the pattern
			f, err := os.Create(pattern)
			if err != nil {
				logflags.DebuggerLogger().Errorf("could not create temporary file for build output: %v", err)
				fmt.Println(err)
				return ""
			}
			r := f.Name()
			f.Close()

			fmt.Println(r)
			return r
		}
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
