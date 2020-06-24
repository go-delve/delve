package argv

import (
	"errors"
	"io"
	"os"
	"os/exec"
)

func Cmds(args ...[]string) ([]*exec.Cmd, error) {
	var cmds []*exec.Cmd
	for _, argv := range args {
		if len(argv) == 0 {
			return nil, errors.New("invalid cmd")
		}

		cmds = append(cmds, exec.Command(argv[0], argv[1:]...))
	}
	return cmds, nil
}

func Pipe(stdin io.Reader, stdout, stderr io.Writer, cmds ...*exec.Cmd) error {
	if stdin == nil {
		stdin = os.Stdin
	}
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}
	l := len(cmds)
	if l == 0 {
		return nil
	}
	var err error
	for i := 0; i < l; i++ {
		if i == 0 {
			cmds[i].Stdin = stdin
		} else {
			cmds[i].Stdin, err = cmds[i-1].StdoutPipe()
			if stderr != nil {
				break
			}
		}
		cmds[i].Stderr = stderr
		if i == l-1 {
			cmds[i].Stdout = stdout
		}
	}
	if err != nil {
		return err
	}
	for i := range cmds {
		err = cmds[i].Start()
		if err != nil {
			return err
		}
	}
	for i := range cmds {
		err = cmds[i].Wait()
		if err != nil {
			return err
		}
	}
	return nil
}
