package token

// Code for token.
type Code int32

// Name returns name of the token.
func (c Code) Name() string {
	s, _ := ToName(c)
	return s
}

// String returns name of the token.
func (c Code) String() string {
	return c.Name()
}

// ShortName returns short name of the token.
func (c Code) ShortName() string {
	s, _ := ToShortName(c)
	return s
}

// Parent returns code of parent token for the token.
func (c Code) Parent() Code {
	p, _ := ToParent(c)
	return p
}

// Token data.
type Token struct {
	// Code of the token.
	Code Code
	// Name of the token.
	Name string
	// Short name of the token.
	ShortName string
	// Parent token code of the token.
	Parent Code
}

const (
	Text Code = iota + 1
	TextWhitespace

	Error

	Other

	Keyword
	KeywordConstant
	KeywordDeclaration
	KeywordNamespace
	KeywordPseudo
	KeywordReserved
	KeywordType
	KeywordVariable

	Name
	NameAttribute
	NameBuiltin
	NameBuiltinPseudo
	NameClass
	NameConstant
	NameDecorator
	NameEntity
	NameException
	NameFunction
	NameProperty
	NameLabel
	NameNamespace
	NameOther
	NameTag
	NameVariable
	NameVariableClass
	NameVariableGlobal
	NameVariableInstance

	Literal
	LiteralDate
	LiteralString
	LiteralStringBacktick
	LiteralStringChar
	LiteralStringDoc
	LiteralStringDouble
	LiteralStringEscape
	LiteralStringHeredoc
	LiteralStringInterpol
	LiteralStringOther
	LiteralStringRegex
	LiteralStringSingle
	LiteralStringSymbol
	LiteralNumber
	LiteralNumberFloat
	LiteralNumberHex
	LiteralNumberInteger
	LiteralNumberIntegerLong
	LiteralNumberOct
	LiteralNumberBin
	LiteralNumberOther

	Operator
	OperatorWord

	Punctuation
	PunctuationIndicator

	Comment
	CommentDoc
	CommentMultiline
	CommentPreproc
	CommentSingle
	CommentSpecial

	Generic
	GenericDeleted
	GenericEmph
	GenericError
	GenericHeading
	GenericInserted
	GenericOutput
	GenericPrompt
	GenericStrong
	GenericSubheading
	GenericTraceback
	GenericLineno
)

// Definition of all tokens
var Tokens = []Token{
	Token{Text, "Text", "", 0},
	Token{TextWhitespace, "Whitespace", "w", Text},

	Token{Error, "Error", "err", 0},
	Token{Other, "Other", "x", 0},

	Token{Keyword, "Keyword", "k", 0},
	Token{KeywordConstant, "Constant", "kc", Keyword},
	Token{KeywordDeclaration, "Declaration", "kd", Keyword},
	Token{KeywordNamespace, "Namespace", "kn", Keyword},
	Token{KeywordPseudo, "Pseudo", "kp", Keyword},
	Token{KeywordReserved, "Reserved", "kr", Keyword},
	Token{KeywordType, "Type", "kt", Keyword},
	Token{KeywordVariable, "Variable", "kv", Keyword},

	Token{Name, "Name", "n", 0},
	Token{NameAttribute, "Attribute", "na", Name},
	Token{NameBuiltin, "Builtin", "nb", Name},
	Token{NameBuiltinPseudo, "Pseudo", "bp", NameBuiltin},
	Token{NameClass, "Class", "nc", Name},
	Token{NameConstant, "Constant", "no", Name},
	Token{NameDecorator, "Decorator", "nd", Name},
	Token{NameEntity, "Entity", "ni", Name},
	Token{NameException, "Exception", "ne", Name},
	Token{NameFunction, "Function", "nf", Name},
	Token{NameProperty, "Property", "py", Name},
	Token{NameLabel, "Label", "nl", Name},
	Token{NameNamespace, "Namespace", "nn", Name},
	Token{NameOther, "Other", "nx", Name},
	Token{NameTag, "Tag", "nt", Name},
	Token{NameVariable, "Variable", "nv", Name},
	Token{NameVariableClass, "Class", "vc", NameVariable},
	Token{NameVariableGlobal, "Global", "vg", NameVariable},
	Token{NameVariableInstance, "Instance", "vi", NameVariable},

	Token{Literal, "Literal", "l", 0},
	Token{LiteralDate, "Date", "ld", Literal},
	Token{LiteralString, "String", "s", Literal},
	Token{LiteralStringBacktick, "Backtick", "sb", LiteralString},
	Token{LiteralStringChar, "Char", "sc", LiteralString},
	Token{LiteralStringDoc, "Doc", "sd", LiteralString},
	Token{LiteralStringDouble, "Double", "s2", LiteralString},
	Token{LiteralStringEscape, "Escape", "se", LiteralString},
	Token{LiteralStringHeredoc, "Heredoc", "sh", LiteralString},
	Token{LiteralStringInterpol, "Interpol", "si", LiteralString},
	Token{LiteralStringOther, "Other", "sx", LiteralString},
	Token{LiteralStringRegex, "Regex", "sr", LiteralString},
	Token{LiteralStringSingle, "Single", "s1", LiteralString},
	Token{LiteralStringSymbol, "Symbol", "ss", LiteralString},
	Token{LiteralNumber, "Number", "m", Literal},
	Token{LiteralNumberFloat, "Float", "mf", LiteralNumber},
	Token{LiteralNumberHex, "Hex", "mh", LiteralNumber},
	Token{LiteralNumberInteger, "Integer", "mi", LiteralNumber},
	Token{LiteralNumberIntegerLong, "Long", "il", LiteralNumberInteger},
	Token{LiteralNumberOct, "Oct", "mo", LiteralNumber},
	Token{LiteralNumberBin, "Bin", "mb", LiteralNumber},
	Token{LiteralNumberOther, "Other", "mx", LiteralNumber},

	Token{Operator, "Operator", "o", 0},
	Token{OperatorWord, "Word", "ow", Operator},

	Token{Punctuation, "Punctuation", "p", 0},
	Token{PunctuationIndicator, "PunctuationIndicator", "pi", Punctuation},

	Token{Comment, "Comment", "c", 0},
	Token{CommentDoc, "Doc", "cd", Comment},
	Token{CommentMultiline, "Multiline", "cm", Comment},
	Token{CommentPreproc, "Preproc", "cp", Comment},
	Token{CommentSingle, "Single", "c1", Comment},
	Token{CommentSpecial, "Special", "cs", Comment},

	Token{Generic, "Generic", "g", 0},
	Token{GenericDeleted, "Deleted", "gd", Generic},
	Token{GenericEmph, "Emph", "ge", Generic},
	Token{GenericError, "Error", "gr", Generic},
	Token{GenericHeading, "Heading", "gh", Generic},
	Token{GenericInserted, "Inserted", "gi", Generic},
	Token{GenericOutput, "Output", "go", Generic},
	Token{GenericPrompt, "Prompt", "gp", Generic},
	Token{GenericStrong, "Strong", "gs", Generic},
	Token{GenericSubheading, "Subheading", "gu", Generic},
	Token{GenericTraceback, "Traceback", "gt", Generic},
	Token{GenericLineno, "Lineno", "gl", Generic},
}

// Map of code to token.
var Table = make(map[Code]Token)

func init() {
	for _, t := range Tokens {
		Table[t.Code] = t
	}
}

// ToName returns name of the token.
func ToName(c Code) (string, bool) {
	t, ok := Table[c]
	if !ok {
		return "", false
	}
	return t.Name, true
}

// ToShortName returns short name of the token.
func ToShortName(c Code) (string, bool) {
	td, ok := Table[c]
	if !ok {
		return "", false
	}
	return td.ShortName, true
}

// ToParent returns parent token of the token.
func ToParent(c Code) (Code, bool) {
	td, ok := Table[c]
	if !ok {
		return 0, false
	}
	return td.Parent, true
}
