package main

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/derekparker/dbg/command"
	"github.com/derekparker/dbg/proctl"
)

type term struct {
	stdin *bufio.Reader
}

func main() {
	// We must ensure here that we are running on the same thread during
	// the execution of dbg. This is due to the fact that ptrace(2) expects
	// all commands after PTRACE_ATTACH to come from the same thread.
	runtime.LockOSThread()

	var (
		t    = newTerm()
		cmds = command.DebugCommands()
	)

	if len(os.Args) == 1 {
		die("You must provide a pid\n")
	}

	pid, err := strconv.Atoi(os.Args[1])
	if err != nil {
		die(err)
	}

	dbgproc, err := proctl.NewDebugProcess(pid)
	if err != nil {
		die("Could not start debugging process:", err)
	}

	for {
		cmdstr, err := t.promptForInput()
		if err != nil {
			die("Prompt for input failed.\n")
		}

		cmdstr, args := parseCommand(cmdstr)

		cmd := cmds.Find(cmdstr)
		err = cmd(dbgproc, args...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Command failed: %s\n", err)
		}
	}
}

func die(args ...interface{}) {
	fmt.Fprint(os.Stderr, args)
	os.Exit(1)
}

func newTerm() *term {
	return &term{
		stdin: bufio.NewReader(os.Stdin),
	}
}

func parseCommand(cmdstr string) (string, []string) {
	vals := strings.Split(cmdstr, " ")
	return vals[0], vals[1:]
}

func (t *term) promptForInput() (string, error) {
	fmt.Print("dbg> ")

	line, err := t.stdin.ReadString('\n')
	if err != nil {
		return "", err
	}

	return strings.TrimSuffix(line, "\n"), nil
}
