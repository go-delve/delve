package formatter

import "strings"

// All formatters.
var All = []Factory{
	HTML,
	Terminal256,
}

var table map[string]Factory

// Find returns matched Factory.
func Find(s string) Factory {
	// FIXME: consider aliases.
	if table == nil {
		table = make(map[string]Factory)
		for _, f := range All {
			table[strings.ToLower(f.Info().Name)] = f
		}
	}
	f, ok := table[strings.ToLower(s)]
	if !ok {
		return nil
	}
	return f
}
