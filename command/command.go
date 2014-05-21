package command

import (
	"fmt"
	"os"
)

type cmdfunc func() error

type Commands struct {
	cmds map[string]cmdfunc
}

func DebugCommands() *Commands {
	cmds := map[string]cmdfunc{
		"exit": exitFunc,
	}

	return &Commands{cmds}
}

func (c *Commands) Register(cmdstr string, cf cmdfunc) {
	c.cmds[cmdstr] = cf
}

// Find will look up the command function for the given command input.
// If it cannot find the command it will defualt to noCmdAvailable().
// If the command is an empty string it will replay the last command.
func (c *Commands) Find(cmdstr string) cmdfunc {
	cmd, ok := c.cmds[cmdstr]
	if !ok {
		if cmdstr == "" {
			return nullCommand
		}

		return noCmdAvailable
	}

	// Allow <enter> to replay last command
	c.cmds[""] = cmd

	return cmd
}

func noCmdAvailable() error {
	return fmt.Errorf("command not available")
}

func exitFunc() error {
	os.Exit(0)
	return nil
}

func nullCommand() error {
	return nil
}
