package argv

import (
	"bytes"
	"errors"
	"io"
	"os/exec"
	"strings"
)

// Run execute cmdline string and return the output
func Run(cmdline []rune, env map[string]string) ([]rune, error) {
	args, err := Argv(cmdline, env, Run)
	if err != nil {
		return nil, err
	}
	cmds, err := Cmds(args)
	if err != nil {
		return nil, err
	}

	output := bytes.NewBuffer(make([]byte, 0, 1024))
	err = Pipe(nil, output, cmds...)
	str := output.String()
	str = strings.TrimSpace(str)
	return []rune(str), err
}

// Cmds generate exec.Cmd for each command.
func Cmds(args [][]string) ([]*exec.Cmd, error) {
	var cmds []*exec.Cmd
	for _, argv := range args {
		if len(argv) == 0 {
			return nil, errors.New("invalid cmd")
		}

		cmds = append(cmds, exec.Command(argv[0], argv[1:]...))
	}
	return cmds, nil
}

// Pipe pipe previous command's stdout to next command's stdin, if in or
// out is nil, it will be ignored.
func Pipe(in io.Reader, out io.Writer, cmds ...*exec.Cmd) error {
	l := len(cmds)
	if l == 0 {
		return nil
	}

	var err error
	for i := 1; i < l; i++ {
		cmds[i].Stdin, err = cmds[i-1].StdoutPipe()
		if err != nil {
			break
		}
	}
	if err != nil {
		return err
	}
	if in != nil {
		cmds[0].Stdin = in
	}
	if out != nil {
		cmds[l-1].Stdout = out
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
