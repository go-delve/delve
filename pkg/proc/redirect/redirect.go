package redirect

import (
	"errors"
	"os"
)

var ErrorNotImplemented = errors.New("not implemented")

// Redirect
type Redirect interface {
	// RedirectPath
	RedirectPath() (redirects [3]string, err error)
	// RedirectFile
	RedirectReaderFile() (readerFile [3]*os.File, err error)
	// RedirectWriterFile
	RedirectWriterFile() (writerFile [3]*os.File, err error)
	// ReStart
	ReStart() error
}
