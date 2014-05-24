package main

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/Dparker1990/dbg/command"
	"github.com/Dparker1990/dbg/proctl"
)

type term struct {
	stdin *bufio.Reader
}

func main() {
	// We must ensure here that we are running on the same thread during
	// the execution of dbg. This is due to the fact that ptrace(2) expects
	// all commands after PTRACE_ATTACH to come from the same thread.
	runtime.LockOSThread()

	t := newTerm()

	if len(os.Args) == 1 {
		printStderrAndDie("You must provide a pid\n")
	}

	pid, err := strconv.Atoi(os.Args[1])
	if err != nil {
		printStderrAndDie(err)
	}

	dbgproc, err := proctl.NewDebugProcess(pid)
	if err != nil {
		printStderrAndDie("Could not start debugging process:", err)
	}

	cmds := command.DebugCommands()
	registerProcessCommands(cmds, dbgproc)

	for {
		cmdstr, err := t.promptForInput()
		if err != nil {
			printStderrAndDie("Prompt for input failed.\n")
		}

		cmd := cmds.Find(cmdstr)
		err = cmd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Command failed: %s\n", err)
		}
	}
}

func printStderrAndDie(args ...interface{}) {
	fmt.Fprint(os.Stderr, args)
	os.Exit(1)
}

func registerProcessCommands(cmds *command.Commands, proc *proctl.DebuggedProcess) {
	cmds.Register("step", proc.Step)
	cmds.Register("continue", proc.Continue)
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
