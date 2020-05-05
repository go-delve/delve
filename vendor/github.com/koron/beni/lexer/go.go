package lexer

import (
	t "github.com/koron/beni/token"
)

// Go lexer info.
var goInfo = Info{
	Name:        "Go",
	Aliases:     []string{"go", "golang"},
	Filenames:   []string{`.*\.go`},
	Mimetypes:   []string{"text/x-go", "application/x-go", "text/x-gosrc"},
	Description: "The Go programming language (http://golang.org)",
}

var (
	goKeywords = []string{
		"break", "default", "func", "interface", "select",
		"case", "defer", "go", "map", "struct",
		"chan", "else", "goto", "package", "switch",
		"const", "fallthrough", "if", "range", "type",
		"continue", "for", "import", "return", "var",
	}

	goOperators = []string{
		"+=", "++", "+", "&^=", "&^", "&=", "&&", "&", "==", "=",
		"!=", "!", "-=", "--", "-", "|=", "||", "|", "<=", "<-",
		"<<=", "<<", "<", "*=", "*", "^=", "^", ">>=", ">>", ">=",
		">", "/=", "/", ":=", "%", "%=", "...", ".", ":",
	}

	goSeparators = []string{
		"(", ")", "[", "]", "{", "}", ",", ";",
	}

	goTypes = []string{
		"bool", "byte", "complex64", "complex128", "error",
		"float32", "float64", "int8", "int16", "int32",
		"int64", "int", "rune", "string", "uint8",
		"uint16", "uint32", "uint64", "uintptr", "uint",
	}

	goConstants = []string{
		"true", "false", "iota", "nil",
	}

	goFunctions = []string{
		"append", "cap", "close", "complex", "copy",
		"delete", "imag", "len", "make", "new",
		"panic", "print", "println", "real", "recover",
	}
)

var goStates = map[RegexpLexerState][]RegexpLexerRule{
	Root: []RegexpLexerRule{
		// Comments
		RegexpLexerRule{
			Name:    "line comment",
			Pattern: "^//[^\\n]*",
			Action:  RegexpEmit(t.Comment),
		},
		RegexpLexerRule{
			Name:    "general comment",
			Pattern: "^(?s:/\\*.*?\\*/)",
			Action:  RegexpEmit(t.Comment),
		},

		// Keywords
		RegexpLexerRule{
			Name:    "keyword",
			Pattern: regexpKeywordsPattern(goKeywords...),
			Action:  RegexpEmit(t.Keyword),
		},
		RegexpLexerRule{
			Name:    "predeclared type",
			Pattern: regexpKeywordsPattern(goTypes...),
			Action:  RegexpEmit(t.KeywordType),
		},
		RegexpLexerRule{
			Name:    "predeclared function",
			Pattern: regexpKeywordsPattern(goFunctions...),
			Action:  RegexpEmit(t.NameBuiltin),
		},
		RegexpLexerRule{
			Name:    "predeclared constant",
			Pattern: regexpKeywordsPattern(goConstants...),
			Action:  RegexpEmit(t.NameConstant),
		},

		// Literals (except strings)
		RegexpLexerRule{
			Name: "imaginary lit",
			Pattern: "^(?:" + regexpJoin(
				"\\d+i",
				`\d+(?:[eE][+-]?\d+)i`,
				`.\d+(?:[eE][+-]?\d+)?i`,
				`\d+\.\d+(?:[eE][+-]?\d+)?i`,
			) + ")",
			Action: RegexpEmit(t.LiteralNumber),
		},

		// Float literals
		RegexpLexerRule{
			Name: "float lit",
			Pattern: "^(?:" + regexpJoin(
				`\d+(?:[eE][+-]?\d+)`,
				`.\d+(?:[eE][+-]?\d+)?`,
				`\d+\.\d+(?:[eE][+-]?\d+)?`,
			) + ")",
			Action: RegexpEmit(t.LiteralNumber),
		},

		// Integer literals
		RegexpLexerRule{
			Name:    "octal lit",
			Pattern: "^0[0-7]+",
			Action:  RegexpEmit(t.LiteralNumberHex),
		},
		RegexpLexerRule{
			Name:    "hex lit",
			Pattern: "^0[xX][[:xdigit:]]+",
			Action:  RegexpEmit(t.LiteralNumberHex),
		},
		RegexpLexerRule{
			Name:    "decimal lit",
			Pattern: "^(?:0|[1-9]\\d*)",
			Action:  RegexpEmit(t.LiteralNumberInteger),
		},

		// Character literal
		RegexpLexerRule{
			Name: "char lit",
			Pattern: "^'(?:" + regexpJoin(
				"\\\\[abfnrtv'\"]",
				"\\\\u[[:xdigit:]]{4}",
				"\\\\U[[:xdigit:]]{8}",
				"\\\\x[[:xdigit:]]{2}",
				"\\\\[0-7]{3}",
				"[^\\\\]",
			) + ")'",
			Action: RegexpEmit(t.LiteralStringChar),
		},

		// Operators and separators
		RegexpLexerRule{
			Name:    "operator",
			Pattern: regexpSymbolicsPattern(goOperators...),
			Action:  RegexpEmit(t.Operator),
		},
		RegexpLexerRule{
			Name:    "separator",
			Pattern: regexpSymbolicsPattern(goSeparators...),
			Action:  RegexpEmit(t.Punctuation),
		},

		// Identifiers
		RegexpLexerRule{
			Name:    "identifier",
			Pattern: "^[\\pL][\\pL\\d_]*",
			Action:  RegexpEmit(t.Name),
		},

		// Strings
		RegexpLexerRule{
			Name:    "raw string lit",
			Pattern: "^(?s:`[^`]*`)",
			Action:  RegexpEmit(t.LiteralString),
		},
		RegexpLexerRule{
			Name:    "interpreted string lit",
			Pattern: `^"(?:\\\\|\\"|[^"])*"`,
			Action:  RegexpEmit(t.LiteralString),
		},

		// Others
		RegexpLexerRule{
			Pattern: "^\\s+",
			Action:  RegexpEmit(t.Other),
		},
	},
}

type goFactory struct {
}

func (f *goFactory) Info() Info {
	return goInfo
}

func (f *goFactory) New() (Lexer, error) {
	return NewRegexpLexer(&RegexpLexerDef{
		Info:   goInfo,
		States: goStates,
	})
}

// Go lexer factory.
var Go = &goFactory{}
