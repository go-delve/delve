//go:build windows
// +build windows

package dap

import (
	"crypto/rand"
	"io"
	"os"

	"github.com/go-delve/delve/pkg/proc"
)

func NewRedirector() (redirect proc.Redirect, err error) {
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return redirect, err
	}

	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		return redirect, err
	}

	return proc.NewRedirectByFile([3]*os.File{nil, stdoutReader, stderrReader}, [3]*os.File{nil, stdoutWriter, stderrWriter}), nil
}

func ReadRedirect(stdType string, redirect proc.Redirect, f func(reader io.Reader)) error {
	if stdType == "stdout" {
		f(redirect.ReaderFiles[1])
	} else {
		f(redirect.ReaderFiles[2])
	}
	return nil
}
