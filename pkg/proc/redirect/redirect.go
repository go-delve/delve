package redirect

import (
	"errors"
	"os"
)

var ErrorNotImplemented = errors.New("not implemented")

// Redirect stpecify how the program is redirected.
// If the function is not supported, should return ErrorNotImplemented.
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

type onlyRedirectPath struct {
	redirects [3]string
}

func NewRedirectPath(redirects [3]string) *onlyRedirectPath {
	return &onlyRedirectPath{redirects: redirects}
}

// RedirectPath
func (o *onlyRedirectPath) RedirectPath() (redirects [3]string, err error) {
	return o.redirects, nil
}

// RedirectFile
func (o *onlyRedirectPath) RedirectReaderFile() (readerFile [3]*os.File, err error) {
	return readerFile, ErrorNotImplemented
}

// RedirectWriterFile
func (o *onlyRedirectPath) RedirectWriterFile() (writerFile [3]*os.File, err error) {
	return writerFile, ErrorNotImplemented
}

// ReStart
func (o *onlyRedirectPath) ReStart() error {
	return ErrorNotImplemented
}
