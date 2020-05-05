package lexer

import (
	"log"
	"regexp"
	"strings"
)

// All factories.
var All = []Factory{
	Go,
	Java,
}

var table map[string]Factory

func matchFilename(t, p string) bool {
	matched, err := regexp.MatchString(p, t)
	if err != nil {
		log.Printf("lexer has illegal pattern: %s", p)
		return false
	}
	return matched
}

func matchByFilenames(s string, n Info) bool {
	for _, p := range n.Filenames {
		if matchFilename(s, p) {
			return true
		}
	}
	for _, p := range n.AliasFilenames {
		if matchFilename(s, p) {
			return true
		}
	}
	return false
}

// FindByFilename returns a factory which matched with filename.
func FindByFilename(s string) Factory {
	for _, f := range All {
		if matchByFilenames(s, f.Info()) {
			return f
		}
	}
	return nil
}

// Find returns matched Theme.
func Find(lexerOrFileName string) Factory {
	if table == nil {
		table = make(map[string]Factory)
		for _, f := range All {
			table[strings.ToLower(f.Info().Name)] = f
		}
	}
	f, ok := table[strings.ToLower(lexerOrFileName)]
	if !ok {
		return FindByFilename(lexerOrFileName)
	}
	return f
}
