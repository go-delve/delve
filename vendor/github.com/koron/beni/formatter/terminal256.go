package formatter

import (
	"io"
	"strconv"
	"strings"

	"github.com/koron/beni/theme"
	"github.com/koron/beni/token"
)

var terminal256info = Info{
	Name:    "Terminal256",
	Aliases: []string{"terminal256", "console256", "256"},
}

type terminal256color struct {
	r, g, b uint8
}

var terminal256pallets = func() []terminal256color {
	p := make([]terminal256color, 0, 256)

	// colors 0..15: 16 basic colors
	p = append(p, terminal256color{0x00, 0x00, 0x00}) // #0
	p = append(p, terminal256color{0xcd, 0x00, 0x00}) // #1
	p = append(p, terminal256color{0x00, 0xcd, 0x00}) // #2
	p = append(p, terminal256color{0xcd, 0xcd, 0x00}) // #3
	p = append(p, terminal256color{0x00, 0x00, 0xee}) // #4
	p = append(p, terminal256color{0xcd, 0x00, 0xcd}) // #5
	p = append(p, terminal256color{0x00, 0xcd, 0xcd}) // #6
	p = append(p, terminal256color{0xe5, 0xe5, 0xe5}) // #7
	p = append(p, terminal256color{0x7f, 0x7f, 0x7f}) // #8
	p = append(p, terminal256color{0xff, 0x00, 0x00}) // #9
	p = append(p, terminal256color{0x00, 0xff, 0x00}) // #10
	p = append(p, terminal256color{0xff, 0xff, 0x00}) // #11
	p = append(p, terminal256color{0x5c, 0x5c, 0xff}) // #12
	p = append(p, terminal256color{0xff, 0x00, 0xff}) // #13
	p = append(p, terminal256color{0x00, 0xff, 0xff}) // #14
	p = append(p, terminal256color{0xff, 0xff, 0xff}) // #15

	// colors 16..232: the 6x6x6 color cube
	v := []uint8{0x00, 0x5f, 0x87, 0xaf, 0xd7, 0xff}
	for i := 0; i < 217; i++ {
		r := v[(i/36)%6]
		g := v[(i/6)%6]
		b := v[i%6]
		p = append(p, terminal256color{r, g, b})
	}

	// colors 233..253: grayscale
	for i := uint8(18); i <= 228; i += 10 {
		p = append(p, terminal256color{i, i, i})
	}

	return p
}()

func terminal256closest(c theme.Color) int {
	min := 257 * 257 * 3
	closest := 0
	for i, tc := range terminal256pallets {
		r := int(c.Red) - int(tc.r)
		g := int(c.Green) - int(tc.g)
		b := int(c.Blue) - int(tc.b)
		d := r*r + g*g + b*b
		if d < min {
			min = d
			closest = i
		}
	}
	return closest
}

type terminal256Factory struct {
}

func (*terminal256Factory) Info() Info {
	return terminal256info
}

func (*terminal256Factory) New(t theme.Theme, w io.Writer) (Formatter, error) {
	return &terminal256{
		formatter: formatter{
			info:   terminal256info,
			theme:  t,
			writer: w,
		},
		colorCache:   make(map[int]int),
		currentStyle: theme.Style{},
	}, nil
}

type terminal256 struct {
	formatter
	colorCache   map[int]int
	currentStyle theme.Style
}

func (f *terminal256) Start() error {
	// nothing todo.
	return nil
}

func (f *terminal256) Emit(c token.Code, s string) error {
	var err error
	f.currentStyle = f.lookup(c)
	ss := f.styleString()
	if len(ss) == 0 {
		_, err = f.writer.Write([]byte(s))
	} else {
		f.writer.Write([]byte(ss))
		r := strings.NewReplacer("\n", "\n"+ss)
		_, err = r.WriteString(f.writer, s)
		f.writer.Write([]byte(f.resetString()))
	}
	return err
}

func (f *terminal256) End() error {
	// nothing todo.
	return nil
}

func (f *terminal256) colorIndex(c theme.Color) int {
	cv := c.IntValue()
	idx, ok := f.colorCache[cv]
	if !ok {
		idx = terminal256closest(c)
		f.colorCache[cv] = idx
	}
	return idx
}

func (f *terminal256) colorIndexString(cc theme.ColorCode) string {
	n := f.colorIndex(f.theme.GetColor(cc))
	return strconv.Itoa(n)
}

func (f *terminal256) escapeString(d ...string) string {
	if len(d) == 0 {
		return ""
	}
	return "\033[" + strings.Join(d, ";") + "m"
}

func (f *terminal256) resetString() string {
	attrs := make([]string, 0, 3)
	if f.currentStyle.Fg != 0 {
		attrs = append(attrs, "39")
	}
	if f.currentStyle.Bg != 0 {
		attrs = append(attrs, "49")
	}
	if f.currentStyle.Bold || f.currentStyle.Italic {
		attrs = append(attrs, "00")
	}
	return f.escapeString(attrs...)
}

func (f *terminal256) styleString() string {
	attrs := make([]string, 0, 8)
	if fg := f.currentStyle.Fg; fg != 0 {
		attrs = append(attrs, "38", "5", f.colorIndexString(fg))
	}
	if bg := f.currentStyle.Bg; bg != 0 {
		attrs = append(attrs, "48", "5", f.colorIndexString(bg))
	}
	if f.currentStyle.Bold {
		attrs = append(attrs, "01")
	}
	if f.currentStyle.Italic {
		attrs = append(attrs, "04")
	}
	return f.escapeString(attrs...)
}

// Terminal256 formatter factory.
var Terminal256 = &terminal256Factory{}
