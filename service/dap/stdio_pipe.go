//go:build !windows
// +build !windows

package dap

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

func generateStdioTempPipes() (res [2]string, err error) {
	r := make([]byte, 4)
	if _, err := rand.Read(r); err != nil {
		return res, err
	}

	var (
		prefix     = filepath.Join(os.TempDir(), hex.EncodeToString(r))
		stdoutPath = prefix + "stdout"
		stderrPath = prefix + "stderr"
	)

	if err := syscall.Mkfifo(stdoutPath, 0o600); err != nil {
		return res, err
	}

	if err := syscall.Mkfifo(stderrPath, 0o600); err != nil {
		_ = os.Remove(stdoutPath)
		return res, err
	}

	res[0] = stdoutPath
	res[1] = stderrPath
	return res, nil
}

func ReadRedirect(path string, f func(reader io.Reader)) error {
	fmt.Println("qq")
	stdioFile, err := os.OpenFile(path, os.O_RDONLY, os.ModeNamedPipe)
	if err != nil {
		return err
	}

	f(stdioFile)
	_ = os.Remove(path)
	return nil
}
