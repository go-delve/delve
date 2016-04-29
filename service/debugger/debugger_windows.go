package debugger

import (
	"fmt"
)

func attachErrorMessage(pid int, err error) error {
	return fmt.Errorf("could not attach to pid %d: %s", pid, err)
}

func stopProcess(pid int) error {
	// We cannot gracefully stop a process on Windows,
	// so just ignore this request and let `Detach` kill
	// the process.
	return nil
}
