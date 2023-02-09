//go:build !windows
// +build !windows

package dap

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/go-delve/delve/pkg/proc/redirect"
)

type redirector struct {
	stdoutPath string
	stderrPath string
}

func NewRedirector() (red *redirector, err error) {
	r := make([]byte, 4)
	if _, err := rand.Read(r); err != nil {
		return nil, err
	}

	var (
		prefix     = filepath.Join(os.TempDir(), hex.EncodeToString(r))
		stdoutPath = prefix + "stdout"
		stderrPath = prefix + "stderr"
	)

	if err := syscall.Mkfifo(stdoutPath, 0o600); err != nil {
		return nil, err
	}

	if err := syscall.Mkfifo(stderrPath, 0o600); err != nil {
		_ = os.Remove(stdoutPath)
		return nil, err
	}

	return &redirector{
		stdoutPath: stdoutPath,
		stderrPath: stderrPath,
	}, nil
}

// RedirectFile
func (r *redirector) RedirectReaderFile() (readerFile [3]*os.File, err error) {
	return readerFile, redirect.ErrorNotImplemented
}

// RedirectWriterFile
func (r *redirector) RedirectWriterFile() (writerFile [3]*os.File, err error) {
	return writerFile, redirect.ErrorNotImplemented
}

// RedirectPath
func (r *redirector) RedirectPath() (redirects [3]string, err error) {
	// TODO support stdin
	return [3]string{"", r.stdoutPath, r.stderrPath}, nil
}

// ReStart
func (r *redirector) ReStart() error {
	panic("not implemented") // TODO: Implement
}

func (r *redirector) ReadRedirect(stdType string, f func(reader io.Reader)) error {
	path := r.stderrPath
	if stdType == "stdout" {
		path = r.stdoutPath
	}

	defer os.Remove(path)

	stdoutFile, err := os.OpenFile(path, os.O_RDONLY, os.ModeNamedPipe)
	if err != nil {
		return err
	}

	f(stdoutFile)
	return nil
}
