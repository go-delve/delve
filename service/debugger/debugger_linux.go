package debugger

import (
	"fmt"
	"io/ioutil"
	"os"
	"syscall"

	sys "golang.org/x/sys/unix"
)

//lint:file-ignore ST1005 errors here can be capitalized

func attachErrorMessage(pid int, err error) error {
	fallbackerr := fmt.Errorf("could not attach to pid %d: %s", pid, err)
	if serr, ok := err.(syscall.Errno); ok {
		switch serr {
		case syscall.EPERM:
			bs, err := ioutil.ReadFile("/proc/sys/kernel/yama/ptrace_scope")
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

func stopProcess(pid int) error {
	return sys.Kill(pid, sys.SIGSTOP)
}
