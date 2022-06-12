package debugger

import (
	"debug/pe"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/go-delve/delve/service/api"
)

func attachErrorMessage(pid int, err error) error {
	return fmt.Errorf("could not attach to pid %d: %s", pid, err)
}

func verifyBinaryFormat(exePath string) error {
	f, err := os.Open(exePath)
	if err != nil {
		return err
	}
	defer f.Close()

	// Make sure the binary exists and is an executable file
	if filepath.Base(exePath) == exePath {
		if _, err := exec.LookPath(exePath); err != nil {
			return err
		}
	}

	exe, err := pe.NewFile(f)
	if err != nil {
		return api.ErrNotExecutable
	}
	exe.Close()
	return nil
}
