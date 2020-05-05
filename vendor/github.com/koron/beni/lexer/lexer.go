package lexer

import (
	"io"
	"io/ioutil"

	"github.com/koron/beni/token"
)

// Info for lexer.
type Info struct {
	Name           string
	Aliases        []string
	Filenames      []string
	AliasFilenames []string
	Mimetypes      []string
	Priority       int    // FIXME: not used yet
	Description    string
}

// Emitter receives parsed tokens.
type Emitter interface {
	Emit(token.Code, string) error
}

// Lexer interface.
type Lexer interface {
	Info() Info
	ParseString(s string, e Emitter) error
	GetDebug() bool
	SetDebug(v bool)
}

// Factory of lexer.
type Factory interface {
	Info() Info
	New() (Lexer, error)
}

// Parse a reader with lexer.
func Parse(l Lexer, r io.Reader, e Emitter) error {
	b, err := ioutil.ReadAll(r)
	if err != nil {
		return err
	}
	return l.ParseString(string(b), e)
}
