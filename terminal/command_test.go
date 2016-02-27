package terminal

import (
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/derekparker/delve/proc/test"
	"github.com/derekparker/delve/service"
	"github.com/derekparker/delve/service/api"
	"github.com/derekparker/delve/service/rpc"
)

type FakeTerminal struct {
	*Term
	t testing.TB
}

func (ft *FakeTerminal) Exec(cmdstr string) (outstr string, err error) {
	cmdstr, args := parseCommand(cmdstr)
	cmd := ft.cmds.Find(cmdstr)

	outfh, err := ioutil.TempFile("", "cmdtestout")
	if err != nil {
		ft.t.Fatalf("could not create temporary file: %v", err)
	}

	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outfh, outfh
	defer func() {
		os.Stdout, os.Stderr = stdout, stderr
		outfh.Close()
		outbs, err1 := ioutil.ReadFile(outfh.Name())
		if err1 != nil {
			ft.t.Fatalf("could not read temporary output file: %v", err)
		}
		outstr = string(outbs)
		os.Remove(outfh.Name())
	}()
	err = cmd(ft.Term, args)
	return
}

func (ft *FakeTerminal) MustExec(cmdstr string) string {
	outstr, err := ft.Exec(cmdstr)
	if err != nil {
		ft.t.Fatalf("Error executing <%s>: %v", cmdstr, err)
	}
	return outstr
}

func withTestTerminal(name string, t testing.TB, fn func(*FakeTerminal)) {
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("couldn't start listener: %s\n", err)
	}
	defer listener.Close()
	server := rpc.NewServer(&service.Config{
		Listener:    listener,
		ProcessArgs: []string{test.BuildFixture(name).Path},
	}, false)
	if err := server.Run(); err != nil {
		t.Fatal(err)
	}
	client := rpc.NewClient(listener.Addr().String())
	defer func() {
		client.Detach(true)
	}()
	ft := &FakeTerminal{
		t:    t,
		Term: New(client, nil),
	}
	fn(ft)
}

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

func TestIssue411(t *testing.T) {
	withTestTerminal("math", t, func(term *FakeTerminal) {
		term.MustExec("break math.go:8")
		term.MustExec("trace math.go:9")
		term.MustExec("continue")
		out := term.MustExec("next")
		if !strings.HasPrefix(out, "> main.main()") {
			t.Fatalf("Wrong output for next: <%s>", out)
		}
	})
}
