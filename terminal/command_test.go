package terminal

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/derekparker/delve/proc/test"
	"github.com/derekparker/delve/service/api"
)

func TestCommandDefault(t *testing.T) {
	var (
		cmds = Commands{}
		cmd  = cmds.Find("non-existant-command")
	)

	err := cmd(nil, "")
	if err == nil {
		t.Fatal("cmd() did not default")
	}

	if err.Error() != "command not available" {
		t.Fatal("wrong command output")
	}
}

func TestCommandReplay(t *testing.T) {
	cmds := DebugCommands(nil)
	cmds.Register("foo", func(t *Term, args string) error { return fmt.Errorf("registered command") }, "foo command")
	cmd := cmds.Find("foo")

	err := cmd(nil, "")
	if err.Error() != "registered command" {
		t.Fatal("wrong command output")
	}

	cmd = cmds.Find("")
	err = cmd(nil, "")
	if err.Error() != "registered command" {
		t.Fatal("wrong command output")
	}
}

func TestCommandReplayWithoutPreviousCommand(t *testing.T) {
	var (
		cmds = DebugCommands(nil)
		cmd  = cmds.Find("")
		err  = cmd(nil, "")
	)

	if err != nil {
		t.Error("Null command not returned", err)
	}
}

func TestCommandThread(t *testing.T) {
	var (
		cmds = DebugCommands(nil)
		cmd  = cmds.Find("thread")
	)

	err := cmd(nil, "")
	if err == nil {
		t.Fatal("thread terminal command did not default")
	}

	if err.Error() != "you must specify a thread" {
		t.Fatal("wrong command output: ", err.Error())
	}
}

func TestExecuteFile(t *testing.T) {
	breakCount := 0
	traceCount := 0
	c := &Commands{
		client: nil,
		cmds: []command{
			{aliases: []string{"trace"}, cmdFn: func(t *Term, args string) error {
				traceCount++
				return nil
			}},
			{aliases: []string{"break"}, cmdFn: func(t *Term, args string) error {
				breakCount++
				return nil
			}},
		},
	}

	fixturesDir := test.FindFixturesDir()

	err := c.executeFile(nil, filepath.Join(fixturesDir, "bpfile"))

	if err != nil {
		t.Fatalf("executeFile: %v", err)
	}

	if breakCount != 1 || traceCount != 1 {
		t.Fatalf("Wrong counts break: %d trace: %d\n", breakCount, traceCount)
	}
}

func TestIssue354(t *testing.T) {
	printStack([]api.Stackframe{}, "")
	printStack([]api.Stackframe{{api.Location{PC: 0, File: "irrelevant.go", Line: 10, Function: nil}, nil, nil}}, "")
}
