package config

import (
	"bytes"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"unicode"
)

// SplitQuotedFields is like strings.Fields but ignores spaces inside areas surrounded
// by the specified quote character.
// To specify a single quote use backslash to escape it: \'
func SplitQuotedFields(in string, quote rune) []string {
	type stateEnum int
	const (
		inSpace stateEnum = iota
		inField
		inQuote
		inQuoteEscaped
	)
	state := inSpace
	r := []string{}
	var buf bytes.Buffer

	for _, ch := range in {
		switch state {
		case inSpace:
			if ch == quote {
				state = inQuote
			} else if !unicode.IsSpace(ch) {
				buf.WriteRune(ch)
				state = inField
			}

		case inField:
			if ch == quote {
				state = inQuote
			} else if unicode.IsSpace(ch) {
				r = append(r, buf.String())
				buf.Reset()
				state = inSpace
			} else {
				buf.WriteRune(ch)
			}

		case inQuote:
			if ch == quote {
				state = inField
			} else if ch == '\\' {
				state = inQuoteEscaped
			} else {
				buf.WriteRune(ch)
			}

		case inQuoteEscaped:
			buf.WriteRune(ch)
			state = inQuote
		}
	}

	if state == inField || buf.Len() != 0 {
		r = append(r, buf.String())
	}

	return r
}

func ConfigureSetSimple(rest string, cfgname string, field reflect.Value) error {
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
			if rest != "true" && rest != "false" {
				return reflect.ValueOf(nil), fmt.Errorf("argument to %q must be true or false", cfgname)
			}
			v := rest == "true"
			return reflect.ValueOf(&v), nil
		case reflect.String:
			unquoted, err := strconv.Unquote(rest)
			if err == nil {
				rest = unquoted
			}
			return reflect.ValueOf(&rest), nil
		case reflect.Interface:
			// We special case this particular configuration key because historically we accept both a numerical value and a string value for it.
			if cfgname == "source-list-line-color" {
				n, err := strconv.Atoi(rest)
				if err == nil {
					if n < 0 {
						return reflect.ValueOf(nil), fmt.Errorf("argument to %q must be a number greater than zero", cfgname)
					}
					return reflect.ValueOf(&n), nil
				}
				unquoted, err := strconv.Unquote(rest)
				if err == nil {
					rest = unquoted
				}
				return reflect.ValueOf(&rest), nil
			}
			fallthrough
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

func ConfigureList(w io.Writer, config interface{}, tag string) {
	it := IterateConfiguration(config, tag)
	for it.Next() {
		fieldName, field := it.Field()
		if fieldName == "" {
			continue
		}

		writeField(w, field, fieldName)
	}
}

func writeField(w io.Writer, field reflect.Value, fieldName string) {
	switch field.Kind() {
	case reflect.Interface:
		switch field := field.Interface().(type) {
		case string:
			fmt.Fprintf(w, "%s\t%q\n", fieldName, field)
		default:
			fmt.Fprintf(w, "%s\t%v\n", fieldName, field)
		}
	case reflect.Ptr:
		if !field.IsNil() {
			fmt.Fprintf(w, "%s\t%v\n", fieldName, field.Elem())
		} else {
			fmt.Fprintf(w, "%s\t<not defined>\n", fieldName)
		}
	case reflect.String:
		fmt.Fprintf(w, "%s\t%q\n", fieldName, field)
	default:
		fmt.Fprintf(w, "%s\t%v\n", fieldName, field)
	}
}

type configureIterator struct {
	cfgValue reflect.Value
	cfgType  reflect.Type
	i        int
	tag      string
}

func IterateConfiguration(conf interface{}, tag string) *configureIterator {
	cfgValue := reflect.ValueOf(conf).Elem()
	cfgType := cfgValue.Type()

	return &configureIterator{cfgValue, cfgType, -1, tag}
}

func (it *configureIterator) Next() bool {
	it.i++
	return it.i < it.cfgValue.NumField()
}

func (it *configureIterator) Field() (name string, field reflect.Value) {
	name = it.cfgType.Field(it.i).Tag.Get(it.tag)
	if comma := strings.Index(name, ","); comma >= 0 {
		name = name[:comma]
	}
	field = it.cfgValue.Field(it.i)
	return
}

func ConfigureListByName(conf interface{}, name, tag string) string {
	if name == "" {
		return ""
	}
	it := IterateConfiguration(conf, tag)
	for it.Next() {
		fieldName, field := it.Field()
		if fieldName == name {
			var buf bytes.Buffer
			writeField(&buf, field, fieldName)
			return buf.String()
		}
	}
	return ""
}

func ConfigureFindFieldByName(conf interface{}, name, tag string) reflect.Value {
	it := IterateConfiguration(conf, tag)
	for it.Next() {
		fieldName, field := it.Field()
		if fieldName == name {
			return field
		}
	}
	return reflect.ValueOf(nil)
}

func Split2PartsBySpace(s string) []string {
	v := strings.SplitN(s, " ", 2)
	for i := range v {
		v[i] = strings.TrimSpace(v[i])
	}
	return v
}
