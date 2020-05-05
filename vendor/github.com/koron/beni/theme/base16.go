package theme

import (
	"github.com/koron/beni/token"
)

const (
	base16c00 ColorCode = iota + 1
	base16c01
	base16c02
	base16c03
	base16c04
	base16c05
	base16c06
	base16c07
	base16c08
	base16c09
	base16c0A
	base16c0B
	base16c0C
	base16c0D
	base16c0E
	base16c0F
)

// Base16 is 16 colors basic theme.
var Base16 = &Definition{
	Name: "base16",

	Palettes: map[ColorCode]Color{
		base16c00: Color{0x15, 0x15, 0x15},
		base16c01: Color{0x20, 0x20, 0x20},
		base16c02: Color{0x30, 0x30, 0x30},
		base16c03: Color{0x50, 0x50, 0x50},
		base16c04: Color{0xb0, 0xb0, 0xb0},
		base16c05: Color{0xd0, 0xd0, 0xd0},
		base16c06: Color{0xe0, 0xe0, 0xe0},
		base16c07: Color{0xf5, 0xf5, 0xf5},
		base16c08: Color{0xac, 0x41, 0x42},
		base16c09: Color{0xd2, 0x84, 0x45},
		base16c0A: Color{0xf4, 0xbf, 0x75},
		base16c0B: Color{0x90, 0xa9, 0x59},
		base16c0C: Color{0x75, 0xb5, 0xaa},
		base16c0D: Color{0x6a, 0x9f, 0xb5},
		base16c0E: Color{0xaa, 0x75, 0x9f},
		base16c0F: Color{0x8f, 0x55, 0x36},
	},

	Styles: map[token.Code]Style{
		token.Text:  Style{Fg: base16c05, Bg: base16c00},
		token.Error: Style{Fg: base16c00, Bg: base16c08},

		token.Comment:        Style{Fg: base16c03},
		token.CommentPreproc: Style{Fg: base16c0A},

		token.NameTag:     Style{Fg: base16c0A},
		token.Operator:    Style{Fg: base16c05},
		token.Punctuation: Style{Fg: base16c05},

		token.GenericInserted: Style{Fg: base16c0B},
		token.GenericDeleted:  Style{Fg: base16c08},
		token.GenericHeading: Style{
			Fg:   base16c0D,
			Bg:   base16c00,
			Bold: true,
		},

		token.Keyword:            Style{Fg: base16c0E},
		token.KeywordConstant:    Style{Fg: base16c09},
		token.KeywordType:        Style{Fg: base16c09},
		token.KeywordDeclaration: Style{Fg: base16c09},

		token.LiteralString:         Style{Fg: base16c0B},
		token.LiteralStringRegex:    Style{Fg: base16c0C},
		token.LiteralStringInterpol: Style{Fg: base16c0F},
		token.LiteralStringEscape:   Style{Fg: base16c0F},

		token.NameNamespace: Style{Fg: base16c0A},
		token.NameClass:     Style{Fg: base16c0A},
		token.NameConstant:  Style{Fg: base16c0A},
		token.NameAttribute: Style{Fg: base16c0D},

		token.LiteralNumber:       Style{Fg: base16c0B},
		token.LiteralStringSymbol: Style{Fg: base16c0B},
	},
}
