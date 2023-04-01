//go:build windows
// +build windows

package proc

import (
	"io"
	"os"
)

func NamedPipe() (reader [2]io.ReadCloser, output [3]OutputRedirect, err error) {
	reader[0], output[1].File, err = os.Pipe()
	if err != nil {
		return reader, output, err
	}

	reader[1], output[2].File, err = os.Pipe()
	if err != nil {
		return reader, output, err
	}

	return reader, output, nil
}
