package terminal

import (
	"fmt"
	"testing"

	"github.com/derekparker/delve/service"
)

func TestCommandDefault(t *testing.T) {
	var (
		cmds = Commands{}
		cmd  = cmds.Find("non-existant-command")
	)

	err := cmd(nil)
	if err == nil {
		t.Fatal("cmd() did not default")
	}

	if err.Error() != "command not available" {
		t.Fatal("wrong command output")
	}
}

func TestCommandReplay(t *testing.T) {
	cmds := DebugCommands(nil)
	cmds.Register("foo", func(client service.Client, args ...string) error { return fmt.Errorf("registered command") }, "foo command")
	cmd := cmds.Find("foo")

	err := cmd(nil)
	if err.Error() != "registered command" {
		t.Fatal("wrong command output")
	}

	cmd = cmds.Find("")
	err = cmd(nil)
	if err.Error() != "registered command" {
		t.Fatal("wrong command output")
	}
}

func TestCommandReplayWithoutPreviousCommand(t *testing.T) {
	var (
		cmds = DebugCommands(nil)
		cmd  = cmds.Find("")
		err  = cmd(nil)
	)

	if err != nil {
		t.Error("Null command not returned", err)
	}
}
