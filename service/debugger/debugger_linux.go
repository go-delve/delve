package debugger

import (
	"fmt"
	"io/ioutil"
	"os"
	"syscall"
)

func attachErrorMessage(pid int, err error) error {
	fallbackerr := fmt.Errorf("could not attach to pid %d: %s", pid, err)
	if serr, ok := err.(syscall.Errno); ok {
		switch serr {
		case syscall.EPERM:
			bs, err := ioutil.ReadFile("/proc/sys/kernel/yama/ptrace_scope")
			if err == nil && len(bs) >= 1 && bs[0] != '0' {
				return fmt.Errorf("Could not attach to pid %d: set /proc/sys/kernel/yama/ptrace_scope to 0", pid)
			}
			fi, err := os.Stat(fmt.Sprintf("/proc/%d", pid))
			if err != nil {
				return fallbackerr
			}
			if fi.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
				return fmt.Errorf("Could not attach to pid %d: current user does not own the process", pid)
			}
		}
	}
	return fallbackerr
}
