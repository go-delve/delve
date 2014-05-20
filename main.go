package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/Dparker1990/dbg/command"
)

type term struct {
	stdin *bufio.Reader
}

func main() {
	var (
		t    = newTerm()
		cmds = command.DebugCommands()
	)

	for {
		cmdstr, err := t.promptForInput()
		if err != nil {
			fmt.Fprint(os.Stderr, "Prompt for input failed.")
			os.Exit(1)
		}

		cmd := cmds.Find(cmdstr)
		err = cmd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Command failed: %s\n", err)
		}
	}
}

func newTerm() *term {
	return &term{
		stdin: bufio.NewReader(os.Stdin),
	}
}

func (t *term) promptForInput() (string, error) {
	fmt.Print("dbg> ")

	line, err := t.stdin.ReadString('\n')
	if err != nil {
		return "", err
	}

	return strings.TrimSuffix(line, "\n"), nil
}
