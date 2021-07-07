package macutil

import (
	"errors"
	"syscall"
)

// CheckRosetta returns an error if the calling process is being translated
// by Apple Rosetta.
func CheckRosetta() error {
	pt, err := syscall.Sysctl("sysctl.proc_translated")
	if err != nil {
		return nil
	}
	if len(pt) > 0 && pt[0] == 1 {
		return errors.New("can not run under Rosetta, check that the installed build of Go is right for your CPU architecture")
	}
	return nil
}
