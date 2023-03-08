//go:build windows
// +build windows

package proc

import (
	"io"
	"os"
)

type stdioRedirector struct {
	writers [3]*os.File
	readers [3]*os.File
}

func NewRedirector() (redirect *stdioRedirector, err error) {
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return redirect, err
	}

	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		return redirect, err
	}

	return &stdioRedirector{
		writers: [3]*os.File{nil, stdoutWriter, stderrWriter},
		readers: [3]*os.File{nil, stdoutReader, stderrReader},
	}, err
}

// Writer return [3]{stdin,stdout,stderr}
func (s *stdioRedirector) Writer() [3]OutputRedirect {
	return NewRedirectByFile(s.writers)
}

// Reader return  [2]{stdout,stderr}
func (s *stdioRedirector) Reader() (reader [2]io.ReadCloser, err error) {
	reader[0] = s.readers[1]
	reader[1] = s.readers[2]
	return reader, nil
}
