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

func (c *Commands) Find(cmdstr string) cmdfunc {
	cmd, ok := c.cmds[cmdstr]
	if !ok {
		return noCmdAvailable
	}

	return cmd
}

func noCmdAvailable() error {
	return fmt.Errorf("command not available")
}

func exitFunc() error {
	os.Exit(0)
	return nil
}
