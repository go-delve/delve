package main

import (
	"bufio"
	"debug/gosym"
	"fmt"
	"os"
	"path/filepath"
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

		cmdstr, args := parseCommand(cmdstr)

		cmd := cmds.Find(cmdstr)
		err = cmd(args...)
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
	cmds.Register("continue", command.CommandFunc(proc.Continue))
	cmds.Register("step", func(args ...string) error {
		err := proc.Step()
		if err != nil {
			return err
		}

		regs, err := proc.Registers()
		if err != nil {
			return err
		}

		f, l, _ := proc.GoSymTable.PCToLine(regs.PC())
		fmt.Printf("Stopped at: %s:%d\n", f, l)

		return nil
	})

	cmds.Register("clear", func(args ...string) error {
		fname := args[0]
		fn := proc.GoSymTable.LookupFunc(fname)
		if fn == nil {
			return fmt.Errorf("No function named %s", fname)
		}

		bp, err := proc.Clear(fn.Entry)
		if err != nil {
			return err
		}

		fmt.Printf("Breakpoint cleared at %#v for %s %s:%d\n", bp.Addr, bp.FunctionName, bp.File, bp.Line)

		return nil
	})

	cmds.Register("break", func(args ...string) error {
		var (
			fn    *gosym.Func
			pc    uint64
			fname = args[0]
		)

		if strings.ContainsRune(fname, ':') {
			fl := strings.Split(fname, ":")

			f, err := filepath.Abs(fl[0])
			if err != nil {
				return err
			}

			l, err := strconv.Atoi(fl[1])
			if err != nil {
				return err
			}

			pc, fn, err = proc.GoSymTable.LineToPC(f, l)
			if err != nil {
				return err
			}
		} else {
			fn = proc.GoSymTable.LookupFunc(fname)
			pc = fn.Entry
		}

		if fn == nil {
			return fmt.Errorf("No function named %s", fname)
		}

		bp, err := proc.Break(uintptr(pc))
		if err != nil {
			return err
		}

		fmt.Printf("Breakpoint set at %#v for %s %s:%d\n", bp.Addr, bp.FunctionName, bp.File, bp.Line)

		return nil
	})
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
