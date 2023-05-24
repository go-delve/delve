package terminal

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"text/tabwriter"

	"github.com/go-delve/delve/pkg/config"
)

func configureCmd(t *Term, ctx callContext, args string) error {
	switch args {
	case "-list":
		return configureList(t)
	case "-save":
		return config.SaveConfig(t.conf)
	case "":
		return fmt.Errorf("wrong number of arguments to \"config\"")
	default:
		err := configureSet(t, args)
		if err != nil {
			return err
		}
		if t.client != nil { // only happens in tests
			lcfg := t.loadConfig()
			t.client.SetReturnValuesLoadConfig(&lcfg)
			t.updateConfig()
		}
		return nil
	}
}

func configureList(t *Term) error {
	w := new(tabwriter.Writer)
	w.Init(t.stdout, 0, 8, 1, ' ', 0)
	config.ConfigureList(w, t.conf, "yaml")
	return w.Flush()
}

func configureSet(t *Term, args string) error {
	v := config.Split2PartsBySpace(args)

	cfgname := v[0]
	var rest string
	if len(v) == 2 {
		rest = v[1]
	}

	switch cfgname {
	case "alias":
		return configureSetAlias(t, rest)
	case "debug-info-directories":
		return configureSetDebugInfoDirectories(t, rest)
	}

	field := config.ConfigureFindFieldByName(t.conf, cfgname, "yaml")
	if !field.CanAddr() {
		return fmt.Errorf("%q is not a configuration parameter", cfgname)
	}

	if field.Kind() == reflect.Slice && field.Type().Elem().Name() == "SubstitutePathRule" {
		return configureSetSubstitutePath(t, rest)
	}

	return config.ConfigureSetSimple(rest, cfgname, field)
}

func configureSetSubstitutePath(t *Term, rest string) error {
	if strings.TrimSpace(rest) == "-clear" {
		t.conf.SubstitutePath = t.conf.SubstitutePath[:0]
		return nil
	}
	argv := config.SplitQuotedFields(rest, '"')
	if len(argv) == 2 && argv[0] == "-clear" {
		argv = argv[1:]
	}
	switch len(argv) {
	case 0:
		w := new(tabwriter.Writer)
		w.Init(t.stdout, 0, 8, 1, ' ', 0)
		for i := range t.conf.SubstitutePath {
			fmt.Fprintf(w, "%q\t→\t%q\n", t.conf.SubstitutePath[i].From, t.conf.SubstitutePath[i].To)
		}
		w.Flush()
	case 1: // delete substitute-path rule
		for i := range t.conf.SubstitutePath {
			if t.conf.SubstitutePath[i].From == argv[0] {
				copy(t.conf.SubstitutePath[i:], t.conf.SubstitutePath[i+1:])
				t.conf.SubstitutePath = t.conf.SubstitutePath[:len(t.conf.SubstitutePath)-1]
				return nil
			}
		}
		return fmt.Errorf("could not find rule for %q", argv[0])
	case 2: // add substitute-path rule
		for i := range t.conf.SubstitutePath {
			if t.conf.SubstitutePath[i].From == argv[0] {
				t.conf.SubstitutePath[i].To = argv[1]
				return nil
			}
		}
		t.conf.SubstitutePath = append(t.conf.SubstitutePath, config.SubstitutePathRule{From: argv[0], To: argv[1]})
	default:
		return fmt.Errorf("too many arguments to \"config substitute-path\"")
	}
	return nil
}

func configureSetAlias(t *Term, rest string) error {
	argv := config.SplitQuotedFields(rest, '"')
	switch len(argv) {
	case 1: // delete alias rule
		for k := range t.conf.Aliases {
			v := t.conf.Aliases[k]
			for i := range v {
				if v[i] == argv[0] {
					copy(v[i:], v[i+1:])
					t.conf.Aliases[k] = v[:len(v)-1]
				}
			}
		}
	case 2: // add alias rule
		alias, cmd := argv[1], argv[0]
		if t.conf.Aliases == nil {
			t.conf.Aliases = make(map[string][]string)
		}
		t.conf.Aliases[cmd] = append(t.conf.Aliases[cmd], alias)
	}
	t.cmds.Merge(t.conf.Aliases)
	return nil
}

func configureSetDebugInfoDirectories(t *Term, rest string) error {
	v := config.Split2PartsBySpace(rest)

	if t.client != nil {
		did, err := t.client.GetDebugInfoDirectories()
		if err == nil {
			t.conf.DebugInfoDirectories = did
		}
	}

	switch v[0] {
	case "-clear":
		t.conf.DebugInfoDirectories = t.conf.DebugInfoDirectories[:0]
	case "-add":
		if len(v) < 2 {
			return errors.New("not enough arguments to \"config debug-info-directories\"")
		}
		t.conf.DebugInfoDirectories = append(t.conf.DebugInfoDirectories, v[1])
	case "-rm":
		if len(v) < 2 {
			return errors.New("not enough arguments to \"config debug-info-directories\"")
		}
		found := false
		for i := range t.conf.DebugInfoDirectories {
			if t.conf.DebugInfoDirectories[i] == v[1] {
				found = true
				t.conf.DebugInfoDirectories = append(t.conf.DebugInfoDirectories[:i], t.conf.DebugInfoDirectories[i+1:]...)
				break
			}
		}
		if !found {
			return fmt.Errorf("could not find %q in debug-info-directories", v[1])
		}
	default:
		return errors.New("wrong argument to \"config debug-info-directories\"")
	}

	if t.client != nil {
		t.client.SetDebugInfoDirectories(t.conf.DebugInfoDirectories)
	}
	return nil
}
