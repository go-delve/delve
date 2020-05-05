package theme

import "strings"

// All themes.
var All = []Theme{
	Base16,
}

var table map[string]Theme

// Find returns matched Theme.
func Find(s string) Theme {
	// FIXME: consider aliases.
	if table == nil {
		table = make(map[string]Theme)
		for _, t := range All {
			table[strings.ToLower(t.GetName())] = t
		}
	}
	t, ok := table[strings.ToLower(s)]
	if !ok {
		return nil
	}
	return t
}
