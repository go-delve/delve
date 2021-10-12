package dap

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

type cmdPrefix int

type cmdfunc func(args string) (string, error)

type command struct {
	aliases []string
	helpMsg string
	cmdFn   cmdfunc
}

// debugCommands returns a Commands struct with default commands defined.
func debugCommands(s *Server) []command {
	return []command{
		{aliases: []string{"help", "h"}, cmdFn: s.helpMessage, helpMsg: `Prints the help message.

	help [command]

Type "help" followed by the name of a command for more information about it.`},
		{aliases: []string{"config"}, cmdFn: s.evaluateConfig, helpMsg: `Changes configuration parameters.

config -list

Show all configuration parameters.

config <parameter> <value>

Changes the value of a configuration parameter.

config substitutePath <from> <to>
config substitutePath <from>

Adds or removes a path substitution rule.`},
	}
}

var errNoCmd = errors.New("command not available")

func (s *Server) helpMessage(args string) (string, error) {
	var buf bytes.Buffer
	if args != "" {
		for _, cmd := range debugCommands(s) {
			for _, alias := range cmd.aliases {
				if alias == args {
					return cmd.helpMsg, nil
				}
			}
		}
		return "", errNoCmd
	}

	fmt.Fprintln(&buf, "The following commands are available:")

	for _, cmd := range s.debugCommands(s) {
		h := cmd.helpMsg
		if idx := strings.Index(h, "\n"); idx >= 0 {
			h = h[:idx]
		}
		if len(cmd.aliases) > 1 {
			fmt.Fprintf(&buf, "    %s (alias: %s) \t %s\n", cmd.aliases[0], strings.Join(cmd.aliases[1:], " | "), h)
		} else {
			fmt.Fprintf(&buf, "    %s \t %s\n", cmd.aliases[0], h)
		}
	}

	fmt.Fprintln(&buf)
	fmt.Fprintln(&buf, "Type help followed by a command for full documentation.")
	return buf.String(), nil
}
