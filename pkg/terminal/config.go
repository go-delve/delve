package terminal

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
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
		}
		return nil
	}
}

type configureIterator struct {
	cfgValue reflect.Value
	cfgType  reflect.Type
	i        int
}

func iterateConfiguration(conf *config.Config) *configureIterator {
	cfgValue := reflect.ValueOf(conf).Elem()
	cfgType := cfgValue.Type()

	return &configureIterator{cfgValue, cfgType, -1}
}

func (it *configureIterator) Next() bool {
	it.i++
	return it.i < it.cfgValue.NumField()
}

func (it *configureIterator) Field() (name string, field reflect.Value) {
	name = it.cfgType.Field(it.i).Tag.Get("yaml")
	if comma := strings.Index(name, ","); comma >= 0 {
		name = name[:comma]
	}
	field = it.cfgValue.Field(it.i)
	return
}

func configureFindFieldByName(conf *config.Config, name string) reflect.Value {
	it := iterateConfiguration(conf)
	for it.Next() {
		fieldName, field := it.Field()
		if fieldName == name {
			return field
		}
	}
	return reflect.ValueOf(nil)
}

func configureList(t *Term) error {
	w := new(tabwriter.Writer)
	w.Init(os.Stdout, 0, 8, 1, ' ', 0)

	it := iterateConfiguration(t.conf)
	for it.Next() {
		fieldName, field := it.Field()
		if fieldName == "" {
			continue
		}

		if field.Kind() == reflect.Ptr {
			if !field.IsNil() {
				fmt.Fprintf(w, "%s\t%v\n", fieldName, field.Elem())
			} else {
				fmt.Fprintf(w, "%s\t<not defined>\n", fieldName)
			}
		} else {
			fmt.Fprintf(w, "%s\t%v\n", fieldName, field)
		}
	}
	return w.Flush()
}

func configureSet(t *Term, args string) error {
	v := split2PartsBySpace(args)

	cfgname := v[0]
	var rest string
	if len(v) == 2 {
		rest = v[1]
	}

	if cfgname == "alias" {
		return configureSetAlias(t, rest)
	}

	field := configureFindFieldByName(t.conf, cfgname)
	if !field.CanAddr() {
		return fmt.Errorf("%q is not a configuration parameter", cfgname)
	}

	if field.Kind() == reflect.Slice && field.Type().Elem().Name() == "SubstitutePathRule" {
		return configureSetSubstitutePath(t, rest)
	}

	simpleArg := func(typ reflect.Type) (reflect.Value, error) {
		switch typ.Kind() {
		case reflect.Int:
			n, err := strconv.Atoi(rest)
			if err != nil {
				return reflect.ValueOf(nil), fmt.Errorf("argument to %q must be a number", cfgname)
			}
			if n < 0 {
				return reflect.ValueOf(nil), fmt.Errorf("argument to %q must be a number greater than zero", cfgname)
			}
			return reflect.ValueOf(&n), nil
		case reflect.Bool:
			v := rest == "true"
			return reflect.ValueOf(&v), nil
		default:
			return reflect.ValueOf(nil), fmt.Errorf("unsupported type for configuration key %q", cfgname)
		}
	}

	if field.Kind() == reflect.Ptr {
		val, err := simpleArg(field.Type().Elem())
		if err != nil {
			return err
		}
		field.Set(val)
	} else {
		val, err := simpleArg(field.Type())
		if err != nil {
			return err
		}
		field.Set(val.Elem())
	}
	return nil
}

func configureSetSubstitutePath(t *Term, rest string) error {
	argv := config.SplitQuotedFields(rest, '"')
	switch len(argv) {
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
		t.conf.SubstitutePath = append(t.conf.SubstitutePath, config.SubstitutePathRule{argv[0], argv[1]})
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
