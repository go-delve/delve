package debugger

import (
	"fmt"
	"os"
	"syscall"
)

func init() {
	attachErrorMessage = attachErrorMessageLinux
}

//lint:file-ignore ST1005 errors here can be capitalized

func attachErrorMessageLinux(pid int, err error) error {
	fallbackerr := fmt.Errorf("could not attach to pid %d: %s", pid, err)
	if serr, ok := err.(syscall.Errno); ok {
		switch serr {
		case syscall.EPERM:
			bs, err := os.ReadFile("/proc/sys/kernel/yama/ptrace_scope")
			if err == nil && len(bs) >= 1 && bs[0] != '0' {
				// Yama documentation: https://www.kernel.org/doc/Documentation/security/Yama.txt
				return fmt.Errorf("Could not attach to pid %d: this could be caused by a kernel security setting, try writing \"0\" to /proc/sys/kernel/yama/ptrace_scope", pid)
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
