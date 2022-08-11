//go:build linux
// +build linux

package dap

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"syscall"
)

func generateStdioTempPipes() (res [2]string, err error) {
	r := make([]byte, 4)
	if _, err := rand.Read(r); err != nil {
		return res, err
	}
	prefix := filepath.Join(os.TempDir(), hex.EncodeToString(r))
	stdoutPath := prefix + "stdout"
	stderrPath := prefix + "stderr"
	if err := syscall.Mkfifo(stdoutPath, 0o600); err != nil {
		return res, err
	}
	if err := syscall.Mkfifo(stderrPath, 0o600); err != nil {
		os.Remove(stdoutPath)
		return res, err
	}

	res[0] = stdoutPath
	res[1] = stderrPath
	return res, nil
}
