//go:build !windows
// +build !windows

package proc

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
)

type StdioRedirector struct {
	Paths     [3]string
	needClear int32
}

func NewRedirector() (redirect *StdioRedirector, err error) {
	r := make([]byte, 4)
	if _, err := rand.Read(r); err != nil {
		return redirect, err
	}

	var (
		prefix     = filepath.Join(os.TempDir(), hex.EncodeToString(r))
		stdoutPath = prefix + "stdout"
		stderrPath = prefix + "stderr"
	)

	if err := syscall.Mkfifo(stdoutPath, 0o600); err != nil {
		return redirect, err
	}

	if err := syscall.Mkfifo(stderrPath, 0o600); err != nil {
		_ = os.Remove(stdoutPath)
		return redirect, err
	}

	return &StdioRedirector{Paths: [3]string{"", stdoutPath, stderrPath}}, nil
}

// Writer return [3]{stdin,stdout,stderr}
func (s *StdioRedirector) Writer() [3]OutputRedirect {
	return NewRedirectByPath(s.Paths)
}

type warpClose struct {
	*os.File
	path string
}

func (s *warpClose) Close() error {
	defer os.Remove(s.path)
	return s.File.Close()
}

func newWarpClose(file *os.File, path string) io.ReadCloser {
	return &warpClose{File: file, path: path}
}

// Reader return []{stdout,stderr}.
// Delete the file when the Close interface is called.
// OpenFile(path,os.O_RDONLY,os.ModeNamedPipe) will be blocked.
// The Reader should be called asynchronously.
func (s *StdioRedirector) Reader() (reader [2]io.ReadCloser, err error) {
	atomic.StoreInt32(&s.needClear, 1)
	defer atomic.StoreInt32(&s.needClear, 0)

	stdoutFile, err := os.OpenFile(s.Paths[1], os.O_RDONLY, os.ModeNamedPipe)
	if err != nil {
		return reader, err
	}

	stderrFile, err := os.OpenFile(s.Paths[2], os.O_RDONLY, os.ModeNamedPipe)
	if err != nil {
		return reader, err
	}

	reader[0] = newWarpClose(stdoutFile, s.Paths[1])
	reader[1] = newWarpClose(stderrFile, s.Paths[2])

	return reader, nil
}

// Clear Clean up resources created by redirects.
// When the s.Reader() method is blocked by os.OpenFile(only-read),
// it opens the file in write mode and call file.Close().
func (s *StdioRedirector) Clear() error {
	if atomic.LoadInt32(&s.needClear) == 1 {
		stdoutFile, err := os.OpenFile(s.Paths[1], os.O_WRONLY, os.ModeNamedPipe)
		if err != nil {
			return err
		}

		stderrFile, err := os.OpenFile(s.Paths[2], os.O_WRONLY, os.ModeNamedPipe)
		if err != nil {
			return err
		}

		stderrFile.Close()
		stdoutFile.Close()
	}

	return nil
}
