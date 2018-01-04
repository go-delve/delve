// Package argv parse command line string into arguments array using the bash syntax.
package argv

import "strings"

// ParseEnv parsing environment variables as key/value pair.
//
// Item will be ignored if one of the key and value is empty.
func ParseEnv(env []string) map[string]string {
	var m map[string]string
	for _, e := range env {
		secs := strings.SplitN(e, "=", 2)
		if len(secs) == 2 {
			key := strings.TrimSpace(secs[0])
			val := strings.TrimSpace(secs[1])
			if key == "" || val == "" {
				continue
			}
			if m == nil {
				m = make(map[string]string)
			}
			m[key] = val
		}
	}
	return m
}

// Argv split cmdline string as array of argument array by the '|' character.
//
// The parsing rules is same as bash. The environment variable will be replaced
// and string surround by '`' will be passed to reverse quote parser.
func Argv(cmdline []rune, env map[string]string, reverseQuoteParser ReverseQuoteParser) ([][]string, error) {
	return NewParser(NewScanner(cmdline, env), reverseQuoteParser).Parse()
}
