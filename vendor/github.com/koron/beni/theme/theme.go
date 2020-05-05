package theme

import (
	"github.com/koron/beni/token"
)

// Color indicates hightlight color.
type Color struct {
	Red   uint8
	Green uint8
	Blue  uint8
}

func (c Color) IntValue() int {
	return (int(c.Red) << 16) + (int(c.Green) << 8) + int(c.Blue)
}

// ColorCode is index of color pallet.
type ColorCode int32

// Style indicates hightlight style for syntax elements.
type Style struct {
	Fg     ColorCode
	Bg     ColorCode
	Bold   bool
	Italic bool
}

// Theme interface.
type Theme interface {
	GetName() string
	GetStyle(tc token.Code) Style
	GetColor(cc ColorCode) Color
}

// Definition describes Theme.
type Definition struct {
	Name     string
	Palettes map[ColorCode]Color
	Styles   map[token.Code]Style
}

// GetName returns name of theme.
func (t *Definition) GetName() string {
	return t.Name
}

// GetStyle returns Style for token.Code.
func (t *Definition) GetStyle(tc token.Code) Style {
	// FIXME: style cascading
	s := t.findStyle(tc)
	for s == nil {
		tc = tc.Parent()
		if tc == 0 {
			return Style{}
		}
		s = t.findStyle(tc)
	}
	return *s
}

// GetColor returns Color for ColorCode.
func (t *Definition) GetColor(cc ColorCode) Color {
	color, ok := t.Palettes[cc]
	if !ok {
		return Color{}
	}
	return color
}

func (t *Definition) findStyle(tc token.Code) *Style {
	s, ok := t.Styles[tc]
	if !ok {
		return nil
	}
	return &s
}
