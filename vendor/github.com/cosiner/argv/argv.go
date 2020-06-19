// Package argv parse command line string into arguments array using the bash syntax.
package argv

// Argv split cmdline string as array of argument array by the '|' character.
//
// The parsing rules is same as bash. The environment variable will be replaced
// and string surround by '`' will be passed to reverse quote parser.
func Argv(cmdline string, backquoteExpander, stringExpander Expander) ([][]string, error) {
	return NewParser(NewScanner(cmdline), backquoteExpander, stringExpander).Parse()
}
