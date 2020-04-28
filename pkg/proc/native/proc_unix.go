// +build !windows

package native

import (
	"fmt"
	"os"
	"os/exec"

	isatty "github.com/mattn/go-isatty"
)

func attachProcessToTTY(process *exec.Cmd, tty string) (*os.File, error) {
	f, err := os.OpenFile(tty, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	if !isatty.IsTerminal(f.Fd()) {
		f.Close()
		return nil, fmt.Errorf("%s is not a terminal", f.Name())
	}
	process.Stdin = f
	process.Stdout = f
	process.Stderr = f
	process.SysProcAttr.Setpgid = false
	process.SysProcAttr.Setsid = true
	process.SysProcAttr.Setctty = true

	return f, nil
}
