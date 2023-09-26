//go:build !windows

package proc

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

type openOnRead struct {
	path string
	rd   io.ReadCloser
}

func (oor *openOnRead) Read(p []byte) (n int, err error) {
	if oor.rd != nil {
		return oor.rd.Read(p)
	}

	fh, err := os.OpenFile(oor.path, os.O_RDONLY, os.ModeNamedPipe)
	if err != nil {
		return 0, err
	}

	oor.rd = fh
	return oor.rd.Read(p)
}

func (oor *openOnRead) Close() error {
	defer os.Remove(oor.path)

	fh, _ := os.OpenFile(oor.path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
	if fh != nil {
		fh.Close()
	}

	return oor.rd.Close()
}

func Redirector() (reader io.ReadCloser, output OutputRedirect, err error) {
	r := make([]byte, 4)
	if _, err = rand.Read(r); err != nil {
		return reader, output, err
	}

	var path = filepath.Join(os.TempDir(), hex.EncodeToString(r))

	if err = syscall.Mkfifo(path, 0o600); err != nil {
		_ = os.Remove(path)
		return reader, output, err
	}

	return &openOnRead{path: path}, OutputRedirect{Path: path}, nil
}
