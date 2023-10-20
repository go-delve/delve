package api

import (
	"strings"
	"unicode"
)

func ShortenType(typ string) string {
	out, ok := shortenTypeEx(typ)
	if !ok {
		return typ
	}
	return out
}

func shortenTypeEx(typ string) (string, bool) {
	switch {
	case strings.HasPrefix(typ, "["):
		for i := range typ {
			if typ[i] == ']' {
				sub, ok := shortenTypeEx(typ[i+1:])
				return typ[:i+1] + sub, ok
			}
		}
		return "", false
	case strings.HasPrefix(typ, "*"):
		sub, ok := shortenTypeEx(typ[1:])
		return "*" + sub, ok
	case strings.HasPrefix(typ, "map["):
		depth := 1
		for i := 4; i < len(typ); i++ {
			switch typ[i] {
			case '[':
				depth++
			case ']':
				depth--
				if depth == 0 {
					key, keyok := shortenTypeEx(typ[4:i])
					val, valok := shortenTypeEx(typ[i+1:])
					return "map[" + key + "]" + val, keyok && valok
				}
			}
		}
		return "", false
	case typ == "interface {}" || typ == "interface{}":
		return typ, true
	case typ == "struct {}" || typ == "struct{}":
		return typ, true
	default:
		if containsAnonymousType(typ) {
			return "", false
		}

		if lbrk := strings.Index(typ, "["); lbrk >= 0 {
			if typ[len(typ)-1] != ']' {
				return "", false
			}
			typ0, ok := shortenTypeEx(typ[:lbrk])
			if !ok {
				return "", false
			}
			args := strings.Split(typ[lbrk+1:len(typ)-1], ",")
			for i := range args {
				var ok bool
				args[i], ok = shortenTypeEx(strings.TrimSpace(args[i]))
				if !ok {
					return "", false
				}
			}
			return typ0 + "[" + strings.Join(args, ", ") + "]", true
		}

		slashnum := 0
		slash := -1
		for i, ch := range typ {
			if !unicode.IsLetter(ch) && !unicode.IsDigit(ch) && ch != '_' && ch != '.' && ch != '/' && ch != '@' && ch != '%' && ch != '-' {
				return "", false
			}
			if ch == '/' {
				slash = i
				slashnum++
			}
		}
		if slashnum <= 1 || slash < 0 {
			return typ, true
		}
		return typ[slash+1:], true
	}
}

func containsAnonymousType(typ string) bool {
	for _, thing := range []string{"interface {", "interface{", "struct {", "struct{", "func (", "func("} {
		idx := strings.Index(typ, thing)
		if idx >= 0 && idx+len(thing) < len(typ) {
			ch := typ[idx+len(thing)]
			if ch != '}' && ch != ')' {
				return true
			}
		}
	}
	return false
}
