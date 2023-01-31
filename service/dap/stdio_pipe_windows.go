//go:build windows
// +build windows

package dap

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
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

	res[0] = stdoutPath
	res[1] = stderrPath
	return res, nil
}
