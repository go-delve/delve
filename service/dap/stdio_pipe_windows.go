//go:build windows
// +build windows

package dap

import (
	"crypto/rand"
	"fmt"
	"io"
	"os"

	"github.com/go-delve/delve/pkg/proc/redirect"
	"github.com/go-delve/delve/pkg/util"
)

type redirector struct {
	stdoutWriterFile *os.File
	stderrWriterFile *os.File
	stdoutReaderFile *os.File
	stderrReaderFile *os.File
}

func NewRedirector() (red *redirector, err error) {
	r := make([]byte, 4)
	if _, err := rand.Read(r); err != nil {
		return nil, err
	}

	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return nil, err
	}

	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		return nil, err
	}

	return &redirector{
		stdoutWriterFile: stdoutWriter,
		stderrWriterFile: stderrWriter,
		stdoutReaderFile: stdoutReader,
		stderrReaderFile: stderrReader,
	}, nil

}

// RedirectPath
func (r *redirector) RedirectPath() (redirects [3]string, err error) {
	return redirects, redirect.ErrorNotImplemented
}

// RedirectFile
func (r *redirector) RedirectReaderFile() (readerFile [3]*os.File, err error) {
	return [3]*os.File{nil, r.stdoutReaderFile, r.stderrReaderFile}, nil
}

// RedirectWriterFile
func (r *redirector) RedirectWriterFile() (writerFile [3]*os.File, err error) {
	return [3]*os.File{nil, r.stdoutWriterFile, r.stderrWriterFile}, nil
}

// ReStart
func (r *redirector) ReStart() error {
	panic("not implemented") // TODO: Implement
}

func ReadRedirect(path string, f func(reader io.Reader)) error {
	pipe, exist := util.GetRedirectStrore().Load(path)
	if !exist {
		return fmt.Errorf("redirect key(%s) not found", path)
	}

	f(pipe.Reader)
	return nil
}

func (r *redirector) ReadRedirect(stdType string, f func(reader io.Reader)) error {
	if stdType == "stdout" {
		f(r.stdoutReaderFile)
	} else {
		f(r.stderrReaderFile)
	}
	return nil
}
