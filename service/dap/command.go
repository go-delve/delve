package dap

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/go-delve/delve/pkg/config"
	"github.com/go-delve/delve/pkg/terminal"
	"github.com/go-delve/delve/service/api"
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

	msgExamineMemory = `Examine raw memory at the given address.

Examine memory:

	examinemem [-fmt <format>] [-count|-len <count>] [-size <size>] <address>
	examinemem [-fmt <format>] [-count|-len <count>] [-size <size>] -x <expression>

Format represents the data format and the value is one of this list (default hex): bin(binary), oct(octal), dec(decimal), hex(hexadecimal) and raw.
Length is the number of bytes (default 1) and must be less than or equal to 1000.
Address is the memory location of the target to examine. Please note '-len' is deprecated by '-count and -size'.
Expression can be an integer expression or pointer value of the memory location to examine.

For example:

    x -fmt hex -count 20 -size 1 0xc00008af38
    x -fmt hex -count 20 -size 1 -x 0xc00008af38 + 8
    x -fmt hex -count 20 -size 1 -x &myVar
    x -fmt hex -count 20 -size 1 -x myPtrVar`
)

// debugCommands returns a list of commands with default commands defined.
func debugCommands(s *Session) []command {
	return []command{
		{aliases: []string{"help", "h"}, cmdFn: s.helpMessage, helpMsg: msgHelp},
		{aliases: []string{"config"}, cmdFn: s.evaluateConfig, helpMsg: msgConfig},
		{aliases: []string{"sources", "s"}, cmdFn: s.sources, helpMsg: msgSources},
		{aliases: []string{"target"}, cmdFn: s.targetCmd, helpMsg: msgTarget},
		{aliases: []string{"examinemem", "x"}, cmdFn: s.examineMemory, helpMsg: msgExamineMemory},
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

func (s *Session) examineMemory(goid, frame int, argstr string) (string, error) {

	var (
		args    terminal.ExamineMemoryArgs
		address uint64
		err     error
	)

	if err = terminal.ParseExamineMemoryArg(&args, argstr); err != nil {
		return fmt.Errorf("bad arguments: %w", err).Error(), nil
	}

	if args.IsExpr {
		pvar, err := s.debugger.EvalVariableInScope(int64(goid), frame, 0, args.Operand, DefaultLoadConfig)
		if err != nil {
			return "", err
		}
		val := api.ConvertVar(pvar)

		// "-x &myVar" or "-x myPtrVar"
		if val.Kind == reflect.Ptr {
			if len(val.Children) < 1 {
				return fmt.Errorf("bug? invalid pointer: %#v", val).Error(), nil
			}
			address = val.Children[0].Addr
			// "-x 0xc000079f20 + 8" or -x 824634220320 + 8
		} else if val.Kind == reflect.Int && val.Value != "" {
			address, err = strconv.ParseUint(val.Value, 0, 64)
			if err != nil {
				return fmt.Errorf("bad expression result: %q: %s", val.Value, err).Error(), nil
			}
		} else {
			return fmt.Errorf("unsupported expression type: %s", val.Kind).Error(), nil
		}
	} else {
		address, err = strconv.ParseUint(args.Operand, 0, 64)
		if err != nil {
			return fmt.Errorf("convert address into uintptr type failed, %s", err).Error(), nil
		}
	}

	memory, err := s.debugger.ExamineMemory(address, int(args.Count*args.Size))
	if err != nil {
		return fmt.Errorf("examine memory error: %w", err).Error(), nil
	}

	return api.PrettyExamineMemory(uintptr(address), memory, true, args.Format, int(args.Size)), nil
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
	argv := config.Split2PartsBySpace(argstr)
	switch argv[0] {
	case "list":
		tgrp, unlock := s.debugger.LockTargetGroup()
		defer unlock()
		curpid := tgrp.Selected.Pid()
		var tgtListStr strings.Builder
		for _, tgt := range tgrp.Targets() {
			if _, err := tgt.Valid(); err == nil {
				selected := ""
				if tgt.Pid() == curpid {
					selected = "*"
				}
				tgtListStr.WriteString(fmt.Sprintf("%s\t%d\t%s\n", selected, tgt.Pid(), tgt.CmdLine))
			}
		}
		return tgtListStr.String(), nil
	case "follow-exec":
		if len(argv) == 1 {
			if s.debugger.FollowExecEnabled() {
				return "Follow exec mode is enabled", nil
			} else {
				return "Follow exec mode is disabled", nil
			}
		}
		argv = config.Split2PartsBySpace(argv[1])
		switch argv[0] {
		case "-on":
			var regex string
			if len(argv) == 2 {
				regex = argv[1]
			}
			if err := s.debugger.FollowExec(true, regex); err != nil {
				return "", err
			}
			if regex != "" {
				return fmt.Sprintf("Follow exec mode enabled with regex %q", regex), nil
			}
			return "Follow exec mode enabled", nil
		case "-off":
			if len(argv) > 1 {
				return "", errors.New("too many arguments")
			}
			if err := s.debugger.FollowExec(false, ""); err != nil {
				return "", err
			}
			return "Follow exec mode disabled", nil
		default:
			return "", fmt.Errorf("unknown argument %q to 'target follow-exec'", argv[0])
		}
	case "switch": // TODO: This may cause inconsistency between debugger and frontend.
		tgrp, unlock := s.debugger.LockTargetGroup()
		defer unlock()
		pid, err := strconv.Atoi(argv[1])
		if err != nil {
			return "", err
		}
		found := false
		for _, tgt := range tgrp.Targets() {
			if _, err = tgt.Valid(); err == nil && tgt.Pid() == pid {
				found = true
				tgrp.Selected = tgt
				tgt.SwitchThread(pid)
			}
		}
		if !found {
			return "", fmt.Errorf("could not find target %d", pid)
		}
		return fmt.Sprintf("Switched to process %d", pid), err
	default:
		return "", fmt.Errorf("unknown target command")
	}
}
