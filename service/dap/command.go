package dap

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/go-delve/delve/pkg/config"
)

func (s *Session) delveCmd(goid, frame int, cmdstr string) (string, error) {
	vals := strings.SplitN(strings.TrimSpace(cmdstr), " ", 2)
	cmdname := vals[0]
	var args string
	if len(vals) > 1 {
		args = strings.TrimSpace(vals[1])
	}
	for _, cmd := range debugCommands(s) {
		for _, alias := range cmd.aliases {
			if alias == cmdname {
				return cmd.cmdFn(goid, frame, args)
			}
		}
	}
	return "", errNoCmd
}

type cmdfunc func(goid, frame int, args string) (string, error)

type command struct {
	aliases []string
	helpMsg string
	cmdFn   cmdfunc
}

const (
	msgHelp = `Prints the help message.
	
help [command]

Type "help" followed by the name of a command for more information about it.`

	msgConfig = `Changes configuration parameters.
	
	config -list
	
	Show all configuration parameters.
	
	config <parameter> <value>
	
	Changes the value of a configuration parameter.
	
	config substitutePath <from> <to>
	config substitutePath <from>
	
	Adds or removes a path substitution rule.`
)

// debugCommands returns a list of commands with default commands defined.
func debugCommands(s *Session) []command {
	return []command{
		{aliases: []string{"help", "h"}, cmdFn: s.helpMessage, helpMsg: msgHelp},
		{aliases: []string{"config"}, cmdFn: s.evaluateConfig, helpMsg: msgConfig},
	}
}

var errNoCmd = errors.New("command not available")

func (s *Session) helpMessage(_, _ int, args string) (string, error) {
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

	for _, cmd := range debugCommands(s) {
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

func (s *Session) evaluateConfig(_, _ int, expr string) (string, error) {
	argv := config.Split2PartsBySpace(expr)
	name := argv[0]
	switch name {
	case "-list":
		return listConfig(&s.args), nil
	default:
		res, err := configureSet(&s.args, expr)
		if err != nil {
			return "", err
		}
		return res, nil
	}
}
