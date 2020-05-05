package beni

import (
	"fmt"
	"io"

	"github.com/koron/beni/formatter"
	"github.com/koron/beni/lexer"
	"github.com/koron/beni/theme"
)

// Highlight convert source code to syntax highlighted.
func Highlight(r io.Reader, w io.Writer, lexerOrFileName, themeName, formatName string) error {
	// Prepare modules: lexer factory, theme, formatter factory.
	lf := lexer.Find(lexerOrFileName)
	if lf == nil {
		return fmt.Errorf("lexer not found: %s", lexerOrFileName)
	}
	t := theme.Find(themeName)
	if t == nil {
		return fmt.Errorf("theme not found: %s", themeName)
	}
	ff := formatter.Find(formatName)
	if ff == nil {
		return fmt.Errorf("formatter not found: %s", formatName)
	}

	// Prepare lexer and formatter.
	l, err := lf.New()
	if err != nil {
		return err
	}
	f, err := ff.New(t, w)
	if err != nil {
		return err
	}

	// Parse and format text.
	if err = f.Start(); err != nil {
		return err
	}
	if err = lexer.Parse(l, r, f); err != nil {
		return err
	}
	if err = f.End(); err != nil {
		return err
	}

	return nil
}
