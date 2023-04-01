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

type openOnRead struct {
	p      string
	rd     io.ReadCloser
	status int32 // 0: initialization 1: file is opened 2: file is closed
}

func (oor *openOnRead) Read(p []byte) (n int, err error) {
	if oor.rd != nil {
		return oor.rd.Read(p)
	}

	// try from "into" to "open"
	if !atomic.CompareAndSwapInt32(&oor.status, 0, 1) {
		return 0, io.EOF
	}

	fh, err := os.OpenFile(oor.p, os.O_RDONLY, os.ModeNamedPipe)
	if err != nil {
		return 0, err
	}

	oor.rd = fh
	return oor.rd.Read(p)
}

func (oor *openOnRead) Close() error {
	defer os.Remove(oor.p)

	// try from "init" to "close".
	if !atomic.CompareAndSwapInt32(&oor.status, 0, 2) {
		// try from "open" to "close"
		if atomic.CompareAndSwapInt32(&oor.status, 1, 2) {
			// Make the "oor.Read()" function stop blocking
			_, err := os.OpenFile(oor.p, os.O_WRONLY, os.ModeNamedPipe)
			if err != nil {
				return err
			}
		}
	}

	return oor.rd.Close()
}

func NamedPipe() (reader [2]io.ReadCloser, output [3]OutputRedirect, err error) {
	r := make([]byte, 4)
	if _, err = rand.Read(r); err != nil {
		return reader, output, err
	}

	var (
		prefix     = filepath.Join(os.TempDir(), hex.EncodeToString(r))
		stdoutPath = prefix + "stdout"
		stderrPath = prefix + "stderr"
	)

	if err = syscall.Mkfifo(stdoutPath, 0o600); err != nil {
		return reader, output, err
	}

	if err := syscall.Mkfifo(stderrPath, 0o600); err != nil {
		_ = os.Remove(stdoutPath)
		return reader, output, err
	}

	reader[0] = &openOnRead{p: stdoutPath}
	reader[1] = &openOnRead{p: stderrPath}

	return reader, NewRedirectByPath([3]string{"", stdoutPath, stderrPath}), nil
}
