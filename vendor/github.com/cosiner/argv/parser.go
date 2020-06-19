package argv

import (
	"errors"
	"fmt"
	"os"
)

type (
	Expander func(string) (string, error)

	// Parser take tokens from Scanner, and do syntax checking, and generate the splitted arguments array.
	Parser struct {
		s                 *Scanner
		tokbuf            []Token
		backQuoteExpander Expander
		stringExpander    Expander

		sections    [][]string
		currSection []string

		currStrValid bool
		currStr      []rune
	}
)

// NewParser create a cmdline string parser.
func NewParser(s *Scanner, backQuoteExpander, stringExpander Expander) *Parser {
	if backQuoteExpander == nil {
		backQuoteExpander = func(r string) (string, error) {
			return r, fmt.Errorf("back quote doesn't allowed")
		}
	}
	if stringExpander == nil {
		stringExpander = func(runes string) (string, error) {
			s := os.ExpandEnv(string(runes))
			return s, nil
		}
	}

	return &Parser{
		s:                 s,
		backQuoteExpander: backQuoteExpander,
		stringExpander:    stringExpander,
	}
}

func (p *Parser) nextToken() (Token, error) {
	if l := len(p.tokbuf); l > 0 {
		tok := p.tokbuf[l-1]
		p.tokbuf = p.tokbuf[:l-1]
		return tok, nil
	}

	return p.s.Next()
}

var (
	// ErrInvalidSyntax was reported if there is a syntax error in command line string.
	ErrInvalidSyntax = errors.New("invalid syntax")
)

func (p *Parser) unreadToken(tok Token) {
	p.tokbuf = append(p.tokbuf, tok)
}

// Parse split command line string into arguments array.
//
// EBNF:
//   Cmdline = Section [ Pipe Cmdline ]
//   Section = [Space] SpacedSection { SpacedSection }
//   SpacedSection = MultipleUnit [Space]
//   MultipleUnit = Unit {Unit}
//   Unit = String | BackQuote | SingleQuote | DoubleQuote
func (p *Parser) Parse() ([][]string, error) {
	err := p.cmdline()
	if err != nil {
		return nil, err
	}
	return p.sections, nil
}

func (p *Parser) cmdline() error {
	err := p.section()
	if err != nil {
		return err
	}
	p.endSection()

	tok, err := p.nextToken()
	if err != nil {
		return err
	}
	if tok.Type == TokEOF {
		return nil
	}
	if !p.accept(tok.Type, TokPipe) {
		return ErrInvalidSyntax
	}
	return p.cmdline()
}

func (p *Parser) section() error {
	leftSpace, err := p.optional(TokSpace)
	if err != nil {
		return err
	}

	var hasUnit bool
	for {
		unit, ok, err := p.spacedSection()
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		hasUnit = true

		err = p.appendUnit(leftSpace, unit)
		if err != nil {
			return err
		}
		leftSpace = unit.rightSpace
	}
	if !hasUnit {
		return ErrInvalidSyntax
	}
	return nil
}

type unit struct {
	rightSpace bool
	toks       []Token
}

func (p *Parser) spacedSection() (u unit, ok bool, err error) {
	u.toks, ok, err = p.multipleUnit()
	if err != nil {
		return
	}
	if !ok {
		return u, false, nil
	}
	u.rightSpace, err = p.optional(TokSpace)
	return
}

func (p *Parser) multipleUnit() ([]Token, bool, error) {
	var (
		toks []Token
	)
	for {
		tok, ok, err := p.unit()
		if err != nil {
			return nil, false, err
		}
		if !ok {
			break
		}
		toks = append(toks, tok)
	}
	return toks, len(toks) > 0, nil
}

func (p *Parser) unit() (Token, bool, error) {
	tok, err := p.nextToken()
	if err != nil {
		return tok, false, err
	}
	if p.accept(tok.Type, TokString, TokStringSingleQuote, TokStringDoubleQuote, TokBackQuote) {
		return tok, true, nil
	}
	p.unreadToken(tok)
	return tok, false, nil
}

func (p *Parser) optional(typ TokenType) (bool, error) {
	tok, err := p.nextToken()
	if err != nil {
		return false, err
	}
	var ok bool
	if ok = p.accept(tok.Type, typ); !ok {
		p.unreadToken(tok)
	}
	return ok, nil
}

func (p *Parser) accept(t TokenType, types ...TokenType) bool {
	for _, typ := range types {
		if t == typ {
			return true
		}
	}
	return false
}

func (p *Parser) appendUnit(leftSpace bool, u unit) error {
	if leftSpace {
		p.currStr = p.currStr[:0]
	}
	for _, tok := range u.toks {
		switch tok.Type {
		case TokBackQuote:
			expanded, err := p.backQuoteExpander(string(tok.Value))
			if err != nil {
				return fmt.Errorf("expand string back quoted failed: %s, %w", string(tok.Value), err)
			}
			p.currStr = append(p.currStr, []rune(expanded)...)
		case TokString:
			expanded, err := p.stringExpander(string(tok.Value))
			if err != nil {
				return fmt.Errorf("expand string failed: %s, %w", string(tok.Value), err)
			}
			p.currStr = append(p.currStr, []rune(expanded)...)
		case TokStringSingleQuote:
			p.currStr = append(p.currStr, tok.Value...)
		case TokStringDoubleQuote:
			expanded, err := p.stringExpander(string(tok.Value))
			if err != nil {
				return fmt.Errorf("expand string double quoted failed: %s, %w", string(tok.Value), err)
			}
			p.currStr = append(p.currStr, []rune(expanded)...)
		}
	}
	p.currStrValid = true
	if u.rightSpace {
		p.currSection = append(p.currSection, string(p.currStr))
		p.currStr = p.currStr[:0]
		p.currStrValid = false
	}
	return nil
}

func (p *Parser) endSection() {
	if p.currStrValid {
		p.currSection = append(p.currSection, string(p.currStr))
	}
	p.currStr = p.currStr[:0]
	p.currStrValid = false
	if len(p.currSection) > 0 {
		p.sections = append(p.sections, p.currSection)
		p.currSection = nil
	}
}
