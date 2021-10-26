package dap

import (
	"bytes"
	"fmt"

	"github.com/go-delve/delve/pkg/config"
)

func listConfig(args *launchAttachArgs) string {
	var buf bytes.Buffer
	config.ConfigureList(&buf, args, "cfgName")
	return buf.String()
}

func configureSet(sargs *launchAttachArgs, args string) (string, error) {
	v := config.Split2PartsBySpace(args)

	cfgname := v[0]
	var rest string
	if len(v) == 2 {
		rest = v[1]
	}

	field := config.ConfigureFindFieldByName(sargs, cfgname, "cfgName")
	if !field.CanAddr() {
		return "", fmt.Errorf("%q is not a configuration parameter", cfgname)
	}

	// If there were no arguments provided, just list the value.
	if len(v) == 1 {
		return config.ConfigureListByName(sargs, cfgname, "cfgName"), nil
	}

	if cfgname == "substitutePath" {
		err := configureSetSubstitutePath(sargs, rest)
		if err != nil {
			return "", err
		}
		// Print the updated client to server and server to client maps.
		return fmt.Sprintf("%s\nUpdated", config.ConfigureListByName(sargs, cfgname, "cfgName")), nil
	}

	err := config.ConfigureSetSimple(rest, cfgname, field)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s\nUpdated", config.ConfigureListByName(sargs, cfgname, "cfgName")), nil
}

func configureSetSubstitutePath(args *launchAttachArgs, rest string) error {
	argv := config.SplitQuotedFields(rest, '"')
	switch len(argv) {
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
		return fmt.Errorf("too many arguments to \"config substitute-path\"")
	}
	return nil
}
