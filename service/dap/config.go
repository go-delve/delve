package dap

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/go-delve/delve/pkg/config"
)

func listConfig(args *launchAttachArgs) string {
	var buf bytes.Buffer
	config.ConfigureList(&buf, args, "cfgName")
	return buf.String()
}

func configureSet(sargs *launchAttachArgs, args string) (bool, string, error) {
	v := config.Split2PartsBySpace(args)

	cfgname := v[0]
	var rest string
	if len(v) == 2 {
		rest = v[1]
	}

	field := config.ConfigureFindFieldByName(sargs, cfgname, "cfgName")
	if !field.CanAddr() {
		return false, "", fmt.Errorf("%q is not a configuration parameter", cfgname)
	}

	if cfgname == "substitutePath" {
		err := configureSetSubstitutePath(sargs, rest)
		if err != nil {
			return false, "", err
		}
		// Print the updated client to server and server to client maps.
		return true, config.ConfigureListByName(sargs, cfgname, "cfgName"), nil
	}

	if cfgname == "showPprofLabels" {
		err := configureSetShowPprofLabels(sargs, rest)
		if err != nil {
			return false, "", err
		}
		// Print the updated labels
		return true, config.ConfigureListByName(sargs, cfgname, "cfgName"), nil
	}

	err := config.ConfigureSetSimple(rest, cfgname, field)
	if err != nil {
		return false, "", err
	}
	return true, config.ConfigureListByName(sargs, cfgname, "cfgName"), nil
}

func configureSetSubstitutePath(args *launchAttachArgs, rest string) error {
	if strings.TrimSpace(rest) == "-clear" {
		args.substitutePathClientToServer = args.substitutePathClientToServer[:0]
		args.substitutePathServerToClient = args.substitutePathServerToClient[:0]
		return nil
	}
	argv := config.SplitQuotedFields(rest, '"')
	if len(argv) == 2 && argv[0] == "-clear" {
		argv = argv[1:]
	}
	switch len(argv) {
	case 0:
		// do nothing, let caller show the current list of substitute path rules
		return nil
	case 1: // delete substitute-path rule
		for i := range args.substitutePathClientToServer {
			if args.substitutePathClientToServer[i][0] == argv[0] {
				copy(args.substitutePathClientToServer[i:], args.substitutePathClientToServer[i+1:])
				args.substitutePathClientToServer = args.substitutePathClientToServer[:len(args.substitutePathClientToServer)-1]
				copy(args.substitutePathServerToClient[i:], args.substitutePathServerToClient[i+1:])
				args.substitutePathServerToClient = args.substitutePathServerToClient[:len(args.substitutePathServerToClient)-1]
				return nil
			}
		}
		return fmt.Errorf("could not find rule for %q", argv[0])
	case 2: // add substitute-path rule
		for i := range args.substitutePathClientToServer {
			if args.substitutePathClientToServer[i][0] == argv[0] {
				args.substitutePathClientToServer[i][1] = argv[1]
				args.substitutePathServerToClient[i][0] = argv[1]
				return nil
			}
		}
		args.substitutePathClientToServer = append(args.substitutePathClientToServer, [2]string{argv[0], argv[1]})
		args.substitutePathServerToClient = append(args.substitutePathServerToClient, [2]string{argv[1], argv[0]})

	default:
		return fmt.Errorf("too many arguments to \"config substitutePath\"")
	}
	return nil
}

func configureSetShowPprofLabels(args *launchAttachArgs, rest string) error {
	if strings.TrimSpace(rest) == "-clear" {
		args.ShowPprofLabels = args.ShowPprofLabels[:0]
		return nil
	}
	delete := false
	argv := config.SplitQuotedFields(rest, '"')
	if len(argv) == 2 && argv[0] == "-clear" {
		argv = argv[1:]
		delete = true
	}
	switch len(argv) {
	case 0:
		// do nothing, let caller show the current list of labels
		return nil
	case 1:
		if delete {
			for i := range args.ShowPprofLabels {
				if args.ShowPprofLabels[i] == argv[0] {
					copy(args.ShowPprofLabels[i:], args.ShowPprofLabels[i+1:])
					args.ShowPprofLabels = args.ShowPprofLabels[:len(args.ShowPprofLabels)-1]
					return nil
				}
			}
			return fmt.Errorf("could not find label %q", argv[0])
		} else {
			args.ShowPprofLabels = append(args.ShowPprofLabels, argv[0])
		}
	default:
		return fmt.Errorf("too many arguments to \"config showPprofLabels\"")
	}
	return nil
}
