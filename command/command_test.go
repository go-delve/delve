package command

import (
	"fmt"
	"testing"
)

func TestCommandDefault(t *testing.T) {
	var (
		cmds = Commands{make(map[string]cmdfunc)}
		cmd  = cmds.Find("non-existant-command")
	)

	err := cmd()
	if err == nil {
		t.Fatal("cmd() did not default")
	}

	if err.Error() != "command not available" {
		t.Fatal("wrong command output")
	}
}

func TestCommandRegister(t *testing.T) {
	cmds := Commands{make(map[string]cmdfunc)}

	cmds.Register("foo", func() error { return fmt.Errorf("registered command") })

	cmd := cmds.Find("foo")

	err := cmd()
	if err == nil {
		t.Fatal("cmd was not found")
	}

	if err.Error() != "registered command" {
		t.Fatal("wrong command output")
	}
}
