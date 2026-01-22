package debugger

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
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
			// check if ptrace of non-child processes is disabled
			bs, err := os.ReadFile("/proc/sys/kernel/yama/ptrace_scope")
			if err == nil && len(bs) >= 1 && bs[0] != '0' {
				// Yama documentation: https://www.kernel.org/doc/Documentation/security/Yama.txt
				return fmt.Errorf("Could not attach to pid %d: this could be caused by a kernel security setting, try writing \"0\" to /proc/sys/kernel/yama/ptrace_scope", pid)
			}

			// check if the pid belongs to a different process
			fi, err := os.Stat(fmt.Sprintf("/proc/%d", pid))
			if err != nil {
				return fallbackerr
			}
			if fi.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
				return fmt.Errorf("Could not attach to pid %d: current user does not own the process", pid)
			}

			// check if the process is already being traced
			statusfh, err := os.Open(fmt.Sprintf("/proc/%d/status", pid))
			if err != nil {
				return fallbackerr
			}
			defer statusfh.Close()
			scan := bufio.NewScanner(statusfh)
			const tracerPidPrefix = "TracerPid:"
			for scan.Scan() {
				line := scan.Text()
				if strings.HasPrefix(line, tracerPidPrefix) {
					tpid, _ := strconv.Atoi(strings.TrimSpace(line[len(tracerPidPrefix):]))
					if tpid != 0 {
						return fmt.Errorf("could not attach to pid %d: already being debugged by pid %d", pid, tpid)
					}
				}
			}
		}
	}
	return fallbackerr
}
