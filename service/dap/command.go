package dap

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/go-delve/delve/pkg/config"
	"github.com/google/go-dap"
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

	config -list <parameter>
	
	Show value of a configuration parameter.
	
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
	if name == "-list" {
		if len(argv) > 1 {
			return config.ConfigureListByName(&s.args, argv[1], "cfgName"), nil
		}
		return listConfig(&s.args), nil
	}
	updated, res, err := configureSet(&s.args, expr)
	if err != nil {
		return "", err
	}

	if updated {
		// Send invalidated events for areas that are affected by configuration changes.
		switch name {
		case "showGlobalVariables", "showRegisters":
			// Variable data has become invalidated.
			s.send(&dap.InvalidatedEvent{
				Event: *newEvent("invalidated"),
				Body: dap.InvalidatedEventBody{
					Areas: []dap.InvalidatedAreas{"variables"},
				},
			})
		case "goroutineFilters", "hideSystemGoroutines":
			// Thread related data has become invalidated.
			s.send(&dap.InvalidatedEvent{
				Event: *newEvent("invalidated"),
				Body: dap.InvalidatedEventBody{
					Areas: []dap.InvalidatedAreas{"threads"},
				},
			})
		}
		res += "\nUpdated"
	}
	return res, nil
}
