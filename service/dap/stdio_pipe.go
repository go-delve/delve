//go:build !linux
// +build !linux

package dap

import (
	"errors"
)

func generateStdioTempPipes() (res [2]string, err error) {
	err = errors.New("Unimplemented")
	return res, err
}
