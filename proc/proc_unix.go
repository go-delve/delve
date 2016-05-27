// +build linux darwin

package proc

import sys "golang.org/x/sys/unix"

func stop(p *Process) (err error) {
	return sys.Kill(p.Pid, sys.SIGTRAP)
}
