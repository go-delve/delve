//go:build windows
// +build windows

package dap

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"github.com/go-delve/delve/pkg/util"
)

func generateStdioTempPipes() (res [2]string, err error) {
	r := make([]byte, 4)
	if _, err := rand.Read(r); err != nil {
		return res, err
	}

	var (
		prefix     = hex.EncodeToString(r)
		stdoutPath = prefix + "stdout"
		stderrPath = prefix + "stderr"
	)

	store := util.GetRedirectStrore()
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return res, err
	}

	store.Store(stdoutPath, &util.Pipe{Reader: stdoutReader, Writer: stdoutWriter})

	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		return res, err
	}

	store.Store(stderrPath, &util.Pipe{Reader: stderrReader, Writer: stderrWriter})

	res[0] = stdoutPath
	res[1] = stderrPath
	return res, nil
}

func ReadRedirect(path string, f func(reader io.Reader)) error {
	pipe, exist := util.GetRedirectStrore().Load(path)
	if !exist {
		return fmt.Errorf("redirect key(%s) not found", path)
	}

	f(pipe.Reader)
	return nil
}
