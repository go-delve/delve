package formatter

import (
	"fmt"
	"html"
	"io"
	"strings"

	"github.com/koron/beni/theme"
	"github.com/koron/beni/token"
)

var htmlInfo = Info{
	Name:      "HTML",
	Aliases:   []string{"html"},
	Filenames: []string{"*.html", "*.htm"},
}

type htmlFactory struct {
}

func (f *htmlFactory) Info() Info {
	return htmlInfo
}

func (f *htmlFactory) New(t theme.Theme, w io.Writer) (Formatter, error) {
	return &htmlFormatter{
		formatter: formatter{
			info:   htmlInfo,
			theme:  t,
			writer: w,
		},
		cssClasses: []string{"highlight"},
		// TODO: support these options.
		wrap:        true,
		inlineStyle: false,
		lineNumbers: false,
		startLine:   1,
	}, nil
}

type htmlFormatter struct {
	formatter
	cssClasses  []string
	wrap        bool
	inlineStyle bool
	lineNumbers bool
	startLine   int
}

func (f *htmlFormatter) Start() (err error) {
	// FIXME: support lineNumbers option.
	_, err = f.writer.Write([]byte("<pre><code class=\"" +
		strings.Join(f.cssClasses, " ") + "\">"))
	return
}

func (f *htmlFormatter) Emit(c token.Code, s string) (err error) {
	// FIXME: support lineNumbers option.
	_, err = fmt.Fprintf(f.writer, `<span class="%s">%s</span>`,
		c.ShortName(), html.EscapeString(s))
	return
}

func (f *htmlFormatter) End() (err error) {
	// FIXME: support lineNumbers option.
	_, err = f.writer.Write([]byte("</code></pre>"))
	return
}

// HTML formatter factory.
var HTML = &htmlFactory{}
