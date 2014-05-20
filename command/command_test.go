package command

import "testing"

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
