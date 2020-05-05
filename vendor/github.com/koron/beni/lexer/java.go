package lexer

import (
	"fmt"

	t "github.com/koron/beni/token"
)

var javaInfo = Info{
	Name:        "Java",
	Aliases:     []string{"java"},
	Filenames:   []string{`.*\.java`},
	Mimetypes:   []string{"text/x-java"},
	Description: "The Java programming language (java.com)",
}

var (
	javaKeywords = []string{
		"assert", "break", "case", "catch", "continue", "default", "do",
		"else", "finally", "for", "if", "goto", "instanceof", "new", "return",
		"switch", "this", "throw", "try", "while",
	}

	javaDeclarations = []string{
		"abstract", "const", "enum", "extends", "final", "implements",
		"native", "private", "protected", "public", "static", "strictfp",
		"super", "synchronized", "throws", "transient", "volatile",
	}

	javaTypes = []string{
		"boolean", "byte", "char", "double", "float", "int", "long", "short",
		"void",
	}

	javaSpaces = "(?s:\\s+)"

	javaID = "[a-zA-Z_][a-zA-Z0-9_]*"
)

var javaStates = map[RegexpLexerState][]RegexpLexerRule{
	Root: []RegexpLexerRule{
		RegexpLexerRule{
			Pattern: "^" +
				"(\\s*(?:[A-Za-z_][0-9A-Za-z_.\\[\\]]*\\s+)+?)" +
				"(" + javaID + ")" +
				"(\\s*)(\\()",
			Action: func(c RegexpLexerContext, groups []string) error {
				if err := c.ParseString(groups[1]); err != nil {
					return err
				}
				if err := c.Emit(t.NameFunction, groups[2]); err != nil {
					return err
				}
				if err := c.Emit(t.Text, groups[3]); err != nil {
					return err
				}
				return c.Emit(t.Punctuation, groups[4])
			},
		},
		RegexpLexerRule{Pattern: `^\s+`, Action: RegexpEmit(t.Text)},
		RegexpLexerRule{
			Pattern: "^//.*?$",
			Action:  RegexpEmit(t.CommentSingle),
		},
		RegexpLexerRule{
			Pattern: `^(?s:/\*.*?\*/)`,
			Action:  RegexpEmit(t.CommentMultiline),
		},
		RegexpLexerRule{
			Pattern: "^@" + javaID,
			Action:  RegexpEmit(t.NameDecorator),
		},
		RegexpLexerRule{
			Pattern: regexpKeywordsPattern(javaKeywords...),
			Action:  RegexpEmit(t.Keyword),
		},
		RegexpLexerRule{
			Pattern: regexpKeywordsPattern(javaDeclarations...),
			Action:  RegexpEmit(t.KeywordDeclaration),
		},
		RegexpLexerRule{
			Pattern: regexpKeywordsPattern(javaTypes...),
			Action:  RegexpEmit(t.KeywordType),
		},
		RegexpLexerRule{
			Pattern: "^package\\b",
			Action:  RegexpEmit(t.KeywordNamespace),
		},
		RegexpLexerRule{
			Pattern: "^(?:true|false|null)\\b",
			Action:  RegexpEmit(t.KeywordConstant),
		},
		RegexpLexerRule{
			Pattern: "^(?:class|interface)\\b",
			Action:  RegexpEmitPush(t.KeywordDeclaration, JavaClass),
		},
		RegexpLexerRule{
			Pattern: "^import\b",
			Action:  RegexpEmitPush(t.KeywordNamespace, JavaImport),
		},
		RegexpLexerRule{
			Pattern: "^\"(\\\\|\\\"|[^\"])*\"",
			Action:  RegexpEmit(t.LiteralString),
		},
		RegexpLexerRule{
			Pattern: "^'(?:\\.|[^\\]|\\\\u[0-9a-fA-F]{4})'",
			Action:  RegexpEmit(t.LiteralStringChar),
		},
		RegexpLexerRule{
			Pattern: "^(\\.)(" + javaID + ")",
			Action: func(c RegexpLexerContext, groups []string) error {
				if len(groups) != 3 {
					return fmt.Errorf("expected 3 groups, acutual %d",
						len(groups))
				}
				if err := c.Emit(t.Operator, groups[1]); err != nil {
					return err
				}
				return c.Emit(t.NameAttribute, groups[2])
			},
		},
		RegexpLexerRule{
			Pattern: "^" + javaID + ":",
			Action:  RegexpEmit(t.NameLabel),
		},
		RegexpLexerRule{
			Pattern: "^\\$?" + javaID,
			Action:  RegexpEmit(t.Name),
		},
		RegexpLexerRule{
			Pattern: "^[~^*~%&\\[\\](){}<>\\|+=:;,./?-]",
			Action:  RegexpEmit(t.Operator),
		},
		RegexpLexerRule{
			Pattern: "^[0-9][0-9]*\\.[0-9]+([eE][0-9]+)?[fd]?",
			Action:  RegexpEmit(t.LiteralNumber),
		},
		RegexpLexerRule{
			Pattern: "^0x[0-9a-fA-F]+",
			Action:  RegexpEmit(t.LiteralNumberHex),
		},
		RegexpLexerRule{
			Pattern: "^[0-9]+L?",
			Action:  RegexpEmit(t.LiteralNumberInteger),
		},
	},

	JavaClass: []RegexpLexerRule{
		RegexpLexerRule{
			Pattern: "^" + javaSpaces,
			Action:  RegexpEmit(t.Text),
		},
		RegexpLexerRule{
			Pattern: "^" + javaID,
			Action:  RegexpEmitPop(t.NameClass),
		},
	},

	JavaImport: []RegexpLexerRule{
		RegexpLexerRule{
			Pattern: "^" + javaSpaces,
			Action:  RegexpEmit(t.Text),
		},
		RegexpLexerRule{
			Pattern: "^(?i:[a-zA-Z0-9_.]+\\*?)",
			Action:  RegexpEmitPop(t.NameNamespace),
		},
	},
}

type javaFactory struct {
}

func (f *javaFactory) Info() Info {
	return javaInfo
}

func (f *javaFactory) New() (Lexer, error) {
	return NewRegexpLexer(&RegexpLexerDef{
		Info:   javaInfo,
		States: javaStates,
	})
}

// Java lexer factory.
var Java = &javaFactory{}
