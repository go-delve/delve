// Package command implements functions for responding to user
// input and dispatching to appropriate backend commands.
package command

import (
	"fmt"
	"os"
)

type cmdfunc func(args ...string) error

type Commands struct {
	cmds map[string]cmdfunc
}

// Returns a Commands struct with default commands defined.
func DebugCommands() *Commands {
	cmds := map[string]cmdfunc{
		"exit": exitFunc,
		"":     nullCommand,
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
	return func(args ...string) error {
		return fn()
	}
}

func noCmdAvailable(args ...string) error {
	return fmt.Errorf("command not available")
}

func exitFunc(args ...string) error {
	os.Exit(0)
	return nil
}

func nullCommand(args ...string) error {
	return nil
}
