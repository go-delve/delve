package debugger

import (
	"fmt"
	sys "golang.org/x/sys/unix"
)

func attachErrorMessage(pid int, err error) error {
	return fmt.Errorf("could not attach to pid %d: %s", pid, err)
}
