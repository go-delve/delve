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

	"github.com/go-delve/delve/pkg/proc"
)

func NewRedirector() (redirect proc.Redirect, err error) {
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

	return proc.NewRedirectByPath([3]string{"", stdoutPath, stderrPath}), nil
}

func ReadRedirect(stdType string, redirect proc.Redirect, f func(reader io.Reader)) error {
	path := redirect.Paths[1]
	if stdType == "stderr" {
		path = redirect.Paths[2]
	}

	defer os.Remove(path)

	stdoutFile, err := os.OpenFile(path, os.O_RDONLY, os.ModeNamedPipe)
	if err != nil {
		return err
	}

	f(stdoutFile)
	return nil
}
