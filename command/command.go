// Package command implements functions for responding to user
// input and dispatching to appropriate backend commands.
package command

import (
	"debug/gosym"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/derekparker/dbg/proctl"
)

type cmdfunc func(proc *proctl.DebuggedProcess, args ...string) error

type Commands struct {
	cmds map[string]cmdfunc
}

// Returns a Commands struct with default commands defined.
func DebugCommands() *Commands {
	cmds := map[string]cmdfunc{
		"exit":     exitFunc,
		"continue": cont,
		"next":     next,
		"break":    breakpoint,
		"step":     step,
		"clear":    clear,
		"":         nullCommand,
	}

	return &Commands{cmds}
}

// Register custom commands. Expects cf to be a func of type cmdfunc,
// returning only an error.
func (c *Commands) Register(cmdstr string, cf cmdfunc) {
	c.cmds[cmdstr] = cf
}

// Find will look up the command function for the given command input.
// If it cannot find the command it will defualt to noCmdAvailable().
// If the command is an empty string it will replay the last command.
func (c *Commands) Find(cmdstr string) cmdfunc {
	cmd, ok := c.cmds[cmdstr]
	if !ok {
		return noCmdAvailable
	}

	// Allow <enter> to replay last command
	c.cmds[""] = cmd

	return cmd
}

func CommandFunc(fn func() error) cmdfunc {
	return func(p *proctl.DebuggedProcess, args ...string) error {
		return fn()
	}
}

func noCmdAvailable(p *proctl.DebuggedProcess, ars ...string) error {
	return fmt.Errorf("command not available")
}

func exitFunc(p *proctl.DebuggedProcess, ars ...string) error {
	os.Exit(0)
	return nil
}

func nullCommand(p *proctl.DebuggedProcess, ars ...string) error {
	return nil
}

func cont(p *proctl.DebuggedProcess, ars ...string) error {
	return p.Continue()
}

func step(p *proctl.DebuggedProcess, args ...string) error {
	err := p.Step()
	if err != nil {
		return err
	}

	regs, err := p.Registers()
	if err != nil {
		return err
	}

	f, l, _ := p.GoSymTable.PCToLine(regs.PC())
	fmt.Printf("Stopped at: %s:%d\n", f, l)

	return nil
}

func next(p *proctl.DebuggedProcess, args ...string) error {
	err := p.Next()
	if err != nil {
		return err
	}

	regs, err := p.Registers()
	if err != nil {
		return err
	}

	f, l, _ := p.GoSymTable.PCToLine(regs.PC())
	fmt.Printf("Stopped at: %s:%d\n", f, l)

	return nil
}

func clear(p *proctl.DebuggedProcess, args ...string) error {
	fname := args[0]
	fn := p.GoSymTable.LookupFunc(fname)
	if fn == nil {
		return fmt.Errorf("No function named %s", fname)
	}

	bp, err := p.Clear(fn.Entry)
	if err != nil {
		return err
	}

	fmt.Printf("Breakpoint cleared at %#v for %s %s:%d\n", bp.Addr, bp.FunctionName, bp.File, bp.Line)

	return nil
}

func breakpoint(p *proctl.DebuggedProcess, args ...string) error {
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

		pc, fn, err = p.GoSymTable.LineToPC(f, l)
		if err != nil {
			return err
		}
	} else {
		fn = p.GoSymTable.LookupFunc(fname)
		pc = fn.Entry
	}

	if fn == nil {
		return fmt.Errorf("No function named %s", fname)
	}

	bp, err := p.Break(uintptr(pc))
	if err != nil {
		return err
	}

	fmt.Printf("Breakpoint set at %#v for %s %s:%d\n", bp.Addr, bp.FunctionName, bp.File, bp.Line)

	return nil
}
