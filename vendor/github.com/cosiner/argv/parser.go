package argv

import "errors"

type (
	// ReverseQuoteParser parse strings quoted by '`' and return it's result. Commonly,
	// it should run it os command.
	ReverseQuoteParser func([]rune, map[string]string) ([]rune, error)

	// Parser take tokens from Scanner, and do syntax checking, and generate the splitted arguments array.
	Parser struct {
		s      *Scanner
		tokbuf []Token
		r      ReverseQuoteParser

		sections    [][]string
		currSection []string

		currStrValid bool
		currStr      []rune
	}
)

// NewParser create a cmdline string parser.
func NewParser(s *Scanner, r ReverseQuoteParser) *Parser {
	if r == nil {
		r = func(r []rune, env map[string]string) ([]rune, error) {
			return r, nil
		}
	}

	return &Parser{
		s: s,
		r: r,
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
//   Unit = String | ReverseQuote
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

	var isFirst = true
	for {
		unit, err := p.spacedSection()
		if isFirst {
			isFirst = false
		} else {
			if err == ErrInvalidSyntax {
				break
			}
		}
		if err != nil {
			return err
		}

		p.appendUnit(leftSpace, unit)
		leftSpace = unit.rightSpace
	}
	return nil
}

type unit struct {
	rightSpace bool
	toks       []Token
}

func (p *Parser) spacedSection() (u unit, err error) {
	u.toks, err = p.multipleUnit()
	if err != nil {
		return
	}
	u.rightSpace, err = p.optional(TokSpace)
	return
}

func (p *Parser) multipleUnit() ([]Token, error) {
	var (
		toks    []Token
		isFirst = true
	)
	for {
		tok, err := p.unit()
		if isFirst {
			isFirst = false
		} else {
			if err == ErrInvalidSyntax {
				break
			}
		}
		if err != nil {
			return nil, err
		}
		toks = append(toks, tok)
	}
	return toks, nil
}

func (p *Parser) unit() (Token, error) {
	tok, err := p.nextToken()
	if err != nil {
		return tok, err
	}
	if p.accept(tok.Type, TokString, TokReversequote) {
		return tok, nil
	}
	p.unreadToken(tok)
	return tok, ErrInvalidSyntax
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
		if tok.Type == TokReversequote {
			val, err := p.r(tok.Value, p.s.envs())
			if err != nil {
				return err
			}
			p.currStr = append(p.currStr, val...)
		} else {
			p.currStr = append(p.currStr, tok.Value...)
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
