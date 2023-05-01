package proc

import "os"

// OutputRedirect Specifies where the target program output will be redirected to.
// Only one of "Path" and "File" should be set.
type OutputRedirect struct {
	// Path File path.
	Path string
	// File Redirect file.
	File *os.File
}
