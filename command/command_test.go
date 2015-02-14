package command

import (
	"testing"

	"github.com/derekparker/delve/proctl"
)

func TestCommandDefault(t *testing.T) {
	var (
		cmds = Commands{}
		cmd  = cmds.Find("non-existant-command")
	)

	output := cmd(nil)
	if output.Err == nil {
		t.Fatal("cmd() did not default")
	}

	if output.Err.Error() != "command not available" {
		t.Fatal("wrong command output")
	}
}

func TestCommandReplay(t *testing.T) {
	cmds := DebugCommands()
	cmds.Register("foo", func(p *proctl.DebuggedProcess, args ...string) *cmdOutput {
		return &cmdOutput{Out: "registered command"}
	}, "foo command")
	cmd := cmds.Find("foo")

	output := cmd(nil)
	if output.Out != "registered command" || output.Err != nil {
		t.Fatal("wrong command output")
	}

	cmd = cmds.Find("")
	output = cmd(nil)
	if output.Out != "registered command" || output.Err != nil {
		t.Fatal("wrong command output")
	}
}

func TestCommandReplayWithoutPreviousCommand(t *testing.T) {
	var (
		cmds   = DebugCommands()
		cmd    = cmds.Find("")
		output = cmd(nil)
	)

	if output.Err != nil {
		t.Error("Null command not returned", output.Err)
	}
}
