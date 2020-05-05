package lexer

// RegexpLexerState is state of RegexpLexer.
type RegexpLexerState int32

const (
	// Root is initial state.
	Root RegexpLexerState = iota

	// JavaClass is indicate class for Java.
	JavaClass
	// JavaImport is indicate import for Java.
	JavaImport
)

func (s RegexpLexerState) String() string {
	switch s {
	case Root:
		return "Root"

	case JavaClass:
		return "JavaClass"
	case JavaImport:
		return "JavaImport"

	default:
		return "UNKNOWN"
	}
}
