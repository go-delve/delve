package proc

import "os"

type OutputRedirect struct {
	Path string
	File *os.File
}
