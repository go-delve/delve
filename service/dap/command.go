package dap

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/go-delve/delve/pkg/config"
	"github.com/go-delve/delve/pkg/proc"
	"github.com/google/go-dap"
)

func (s *Session) delveCmd(goid, frame int, cmdstr string) (string, error) {
	vals := strings.Fields(cmdstr)
	cmdname := vals[0]
	var args string
	if len(vals) > 1 {
		args = strings.Join(vals[1:], " ")
	}
	for _, cmd := range debugCommands(s) {
		if slices.Contains(cmd.aliases, cmdname) {
			return cmd.cmdFn(goid, frame, args)
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
	
dlv help [command]

Type "help" followed by the name of a command for more information about it.`

	msgConfig = `Changes configuration parameters.
	
	dlv config -list
	
		Show all configuration parameters.

	dlv config -list <parameter>
	
		Show value of a configuration parameter.
	
	dlv config <parameter> <value>
	
		Changes the value of a configuration parameter.
	
	dlv config substitutePath <from> <to>
	dlv config substitutePath <from>
	dlv config substitutePath -clear
	
		Adds or removes a path substitution rule. If -clear is used all substitutePath rules are removed.
		See also Documentation/cli/substitutepath.md.

	dlv config showPprofLabels <label>
	dlv config showPprofLabels -clear <label>
	dlv config showPprofLabels -clear

		Adds or removes a label key to show in the callstack view. If -clear is used without an argument,
		all labels are removed.`
	msgSources = `Print list of source files.

	dlv sources [<regex>]

If regex is specified only the source files matching it will be returned.`

	msgTarget = `Manages child process debugging.

        target follow-exec [-on [regex]] [-off]

Enables or disables follow exec mode. When follow exec mode Delve will automatically attach to new child processes executed by the target process. An optional regular expression can be passed to 'target follow-exec', only child processes with a command line matching the regular expression will be followed.

        target list

List currently attached processes.

        target switch [pid]

Switches to the specified process.`
)

type FollowExecMode int

const (
	FollowQuery   FollowExecMode = iota // 无参数，查询当前状态
	FollowOnAll                         // -on
	FollowOnRegex                       // -on "regex"
	FollowOff                           // -off
)

type TargetCommand struct {
	Subcommand  string
	SwitchPID   int // valid if Subcommand == "switch"
	FollowMode  FollowExecMode
	FollowRegex string // only valid if FollowMode == FollowOnRegex
}

func parseTargetArgs(argstr string) (*TargetCommand, error) {
	args := strings.Fields(argstr)
	if len(args) < 1 {
		return nil, errors.New("missing subcommand")
	}

	cmd := &TargetCommand{Subcommand: args[0]}

	switch args[0] {
	case "switch":
		if len(args) < 2 {
			return nil, errors.New("missing PID for switch")
		}
		pid, err := strconv.Atoi(args[1])
		if err != nil {
			return nil, fmt.Errorf("invalid PID: %v", err)
		}
		cmd.SwitchPID = pid
		return cmd, nil

	case "follow-exec":
		var onSet, offSet bool
		var onRegex string

		for i := 1; i < len(args); i++ {
			arg := args[i]
			switch arg {
			case "-on":
				onSet = true
				if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
					onRegex = args[i+1]
					i++
				}
			case "-off":
				offSet = true
			default:
				return nil, fmt.Errorf("unknown flag: %s", arg)
			}
		}

		switch {
		case onSet && offSet:
			return nil, errors.New("cannot use both -on and -off")
		case offSet:
			cmd.FollowMode = FollowOff
		case onSet && onRegex != "":
			cmd.FollowMode = FollowOnRegex
			cmd.FollowRegex = onRegex
		case onSet:
			cmd.FollowMode = FollowOnAll
		default:
			cmd.FollowMode = FollowQuery
		}
		return cmd, nil

	case "list":
		if len(args) > 1 {
			return nil, errors.New("list takes no arguments")
		}
		return cmd, nil

	default:
		return nil, fmt.Errorf("unknown subcommand: %s", args[0])
	}
}

// debugCommands returns a list of commands with default commands defined.
func debugCommands(s *Session) []command {
	return []command{
		{aliases: []string{"help", "h"}, cmdFn: s.helpMessage, helpMsg: msgHelp},
		{aliases: []string{"config"}, cmdFn: s.evaluateConfig, helpMsg: msgConfig},
		{aliases: []string{"sources", "s"}, cmdFn: s.sources, helpMsg: msgSources},
		{aliases: []string{"target"}, cmdFn: s.targetCmd, helpMsg: msgTarget},
	}
}

var errNoCmd = errors.New("command not available")

func (s *Session) helpMessage(_, _ int, args string) (string, error) {
	var buf bytes.Buffer
	if args != "" {
		for _, cmd := range debugCommands(s) {
			if slices.Contains(cmd.aliases, args) {
				return cmd.helpMsg, nil
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
			fmt.Fprintf(&buf, "    dlv %s (alias: %s) \t %s\n", cmd.aliases[0], strings.Join(cmd.aliases[1:], " | "), h)
		} else {
			fmt.Fprintf(&buf, "    dlv %s \t %s\n", cmd.aliases[0], h)
		}
	}

	fmt.Fprintln(&buf)
	fmt.Fprintln(&buf, "Type 'dlv help' followed by a command for full documentation.")
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
		case "goroutineFilters", "hideSystemGoroutines", "showPprofLabels":
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

func (s *Session) sources(_, _ int, filter string) (string, error) {
	sources, err := s.debugger.Sources(filter)
	if err != nil {
		return "", err
	}
	sort.Strings(sources)
	return strings.Join(sources, "\n"), nil
}

func (s *Session) targetCmd(_, _ int, argstr string) (string, error) {
	cmd, err := parseTargetArgs(argstr)
	if err != nil {
		return "", err
	}
	switch cmd.Subcommand {
	case "follow-exec":
		switch cmd.FollowMode {
		case FollowQuery:
			if s.debugger.FollowExecEnabled() {
				return "Follow exec mode is enabled", nil
			} else {
				return "Follow exec mode is disabled", nil
			}
		case FollowOnAll, FollowOnRegex:
			if err := s.debugger.FollowExec(true, cmd.FollowRegex); err != nil {
				return "", fmt.Errorf("failed to enable follow exec: %v", err)
			} else {
				return "", nil
			}
		case FollowOff:
			if err := s.debugger.FollowExec(false, ""); err != nil {
				return "", fmt.Errorf("failed to disable follow exec: %v", err)
			} else {
				return "", nil
			}
		}
	case "list":
		tgrp, unlock := s.debugger.LockTargetGroup()
		defer unlock()
		curpid := tgrp.Selected.Pid()
		tgtListStr := ""
		for _, tgt := range tgrp.Targets() {
			if _, err := tgt.Valid(); err == nil {
				selected := ""
				if tgt.Pid() == curpid {
					selected = "*"
				}
				tgtListStr += fmt.Sprintf("%s\t%d\t%s\n", selected, tgt.Pid(), tgt.CmdLine)
			}
		}
		return tgtListStr, nil
	case "switch": // TODO: This may cause inconsistency between debugger and frontend.
		tgrp, unlock := s.debugger.LockTargetGroup()
		t := proc.ValidTargets{Group: tgrp}
		defer unlock()
		found := false
		for t.Next() {
			if _, ok := t.FindThread(cmd.SwitchPID); ok {
				found = true
				tgrp.Selected = t.Target
			}
		}
		err = tgrp.Selected.SwitchThread(cmd.SwitchPID)
		if !found {
			return "", fmt.Errorf("could not find target %d", cmd.SwitchPID)
		}
		return fmt.Sprintf("Switched to process %d", cmd.SwitchPID), err
	default:
		return "", fmt.Errorf("unknown target command")
	}
	return "", nil
}
