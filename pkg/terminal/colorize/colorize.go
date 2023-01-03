package colorize

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/ioutil"
	"path/filepath"
	"reflect"
	"sort"
)

// Style describes the style of a chunk of text.
type Style uint8

const (
	NormalStyle Style = iota
	KeywordStyle
	StringStyle
	NumberStyle
	CommentStyle
	LineNoStyle
	ArrowStyle
	TabStyle
)

// Print prints to out a syntax highlighted version of the text read from
// reader, between lines startLine and endLine.
func Print(out io.Writer, path string, reader io.Reader, startLine, endLine, arrowLine int, colorEscapes map[Style]string, altTabStr string) error {
	buf, err := ioutil.ReadAll(reader)
	if err != nil {
		return err
	}

	w := &lineWriter{
		w:            out,
		lineRange:    [2]int{startLine, endLine},
		arrowLine:    arrowLine,
		colorEscapes: colorEscapes,
	}
	if len(altTabStr) > 0 {
		w.tabBytes = []byte(altTabStr)
	} else {
		w.tabBytes = []byte("\t")
	}

	if filepath.Ext(path) != ".go" {
		w.Write(NormalStyle, buf, true)
		return nil
	}

	var fset token.FileSet
	f, err := parser.ParseFile(&fset, path, buf, parser.ParseComments)
	if err != nil {
		w.Write(NormalStyle, buf, true)
		return nil
	}

	var base int

	fset.Iterate(func(file *token.File) bool {
		base = file.Base()
		return false
	})

	toks := []colorTok{}

	emit := func(tok token.Token, start, end token.Pos) {
		if _, ok := tokenToStyle[tok]; !ok {
			return
		}
		start -= token.Pos(base)
		if end == token.NoPos {
			// end == token.NoPos it's a keyword and we have to find where it ends by looking at the file
			for end = start; end < token.Pos(len(buf)); end++ {
				if buf[end] < 'a' || buf[end] > 'z' {
					break
				}
			}
		} else {
			end -= token.Pos(base)
		}
		if start < 0 || start >= end || end > token.Pos(len(buf)) {
			// invalid token?
			return
		}
		toks = append(toks, colorTok{tok, int(start), int(end)})
	}

	for _, cgrp := range f.Comments {
		for _, cmnt := range cgrp.List {
			emit(token.COMMENT, cmnt.Pos(), cmnt.End())
		}
	}

	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			return true
		}

		switch n := n.(type) {
		case *ast.File:
			emit(token.PACKAGE, f.Package, token.NoPos)
			return true
		case *ast.BasicLit:
			emit(n.Kind, n.Pos(), n.End())
			return true
		case *ast.Ident:
			//TODO(aarzilli): builtin functions? basic types?
			return true
		case *ast.IfStmt:
			emit(token.IF, n.If, token.NoPos)
			if n.Else != nil {
				for elsepos := int(n.Body.End()) - base; elsepos < len(buf)-4; elsepos++ {
					if string(buf[elsepos:][:4]) == "else" {
						emit(token.ELSE, token.Pos(elsepos+base), token.Pos(elsepos+base+4))
						break
					}
				}
			}
			return true
		}

		nval := reflect.ValueOf(n)
		if nval.Kind() != reflect.Ptr {
			return true
		}
		nval = nval.Elem()
		if nval.Kind() != reflect.Struct {
			return true
		}

		tokposval := nval.FieldByName("TokPos")
		tokval := nval.FieldByName("Tok")
		if tokposval != (reflect.Value{}) && tokval != (reflect.Value{}) {
			emit(tokval.Interface().(token.Token), tokposval.Interface().(token.Pos), token.NoPos)
		}

		for _, kwname := range []string{"Case", "Begin", "Defer", "For", "Func", "Go", "Interface", "Map", "Return", "Select", "Struct", "Switch"} {
			kwposval := nval.FieldByName(kwname)
			if kwposval != (reflect.Value{}) {
				kwpos, ok := kwposval.Interface().(token.Pos)
				if ok && kwpos != token.NoPos {
					emit(token.ILLEGAL, kwpos, token.NoPos)
				}
			}
		}

		return true
	})

	sort.Slice(toks, func(i, j int) bool { return toks[i].start < toks[j].start })

	flush := func(start, end int, style Style) {
		if start < end {
			w.Write(style, buf[start:end], end == len(buf))
		}
	}

	cur := 0
	for _, tok := range toks {
		flush(cur, tok.start, NormalStyle)
		flush(tok.start, tok.end, tokenToStyle[tok.tok])
		cur = tok.end
	}
	if cur != len(buf) {
		flush(cur, len(buf), NormalStyle)
	}

	return nil
}

var tokenToStyle = map[token.Token]Style{
	token.ILLEGAL:     KeywordStyle,
	token.COMMENT:     CommentStyle,
	token.INT:         NumberStyle,
	token.FLOAT:       NumberStyle,
	token.IMAG:        NumberStyle,
	token.CHAR:        StringStyle,
	token.STRING:      StringStyle,
	token.BREAK:       KeywordStyle,
	token.CASE:        KeywordStyle,
	token.CHAN:        KeywordStyle,
	token.CONST:       KeywordStyle,
	token.CONTINUE:    KeywordStyle,
	token.DEFAULT:     KeywordStyle,
	token.DEFER:       KeywordStyle,
	token.ELSE:        KeywordStyle,
	token.FALLTHROUGH: KeywordStyle,
	token.FOR:         KeywordStyle,
	token.FUNC:        KeywordStyle,
	token.GO:          KeywordStyle,
	token.GOTO:        KeywordStyle,
	token.IF:          KeywordStyle,
	token.IMPORT:      KeywordStyle,
	token.INTERFACE:   KeywordStyle,
	token.MAP:         KeywordStyle,
	token.PACKAGE:     KeywordStyle,
	token.RANGE:       KeywordStyle,
	token.RETURN:      KeywordStyle,
	token.SELECT:      KeywordStyle,
	token.STRUCT:      KeywordStyle,
	token.SWITCH:      KeywordStyle,
	token.TYPE:        KeywordStyle,
	token.VAR:         KeywordStyle,
}

type colorTok struct {
	tok        token.Token // the token type or ILLEGAL for keywords
	start, end int         // start and end positions of the token
}

type lineWriter struct {
	w         io.Writer
	lineRange [2]int
	arrowLine int

	curStyle Style
	started  bool
	lineno   int

	colorEscapes map[Style]string

	tabBytes []byte
}

func (w *lineWriter) style(style Style) {
	if w.colorEscapes == nil {
		return
	}
	esc := w.colorEscapes[style]
	if esc == "" {
		esc = w.colorEscapes[NormalStyle]
	}
	fmt.Fprintf(w.w, "%s", esc)
}

func (w *lineWriter) inrange() bool {
	lno := w.lineno
	if !w.started {
		lno = w.lineno + 1
	}
	return lno >= w.lineRange[0] && lno < w.lineRange[1]
}

func (w *lineWriter) nl() {
	w.lineno++
	if !w.inrange() || !w.started {
		return
	}
	w.style(ArrowStyle)
	if w.lineno == w.arrowLine {
		fmt.Fprintf(w.w, "=>")
	} else {
		fmt.Fprintf(w.w, "  ")
	}
	w.style(LineNoStyle)
	fmt.Fprintf(w.w, "%4d:\t", w.lineno)
	w.style(w.curStyle)
}

func (w *lineWriter) writeInternal(style Style, data []byte) {
	if !w.inrange() {
		return
	}

	if !w.started {
		w.started = true
		w.curStyle = style
		w.nl()
	} else if w.curStyle != style {
		w.curStyle = style
		w.style(w.curStyle)
	}

	w.w.Write(data)
}

func (w *lineWriter) Write(style Style, data []byte, last bool) {
	cur := 0
	for i := range data {
		switch data[i] {
		case '\n':
			if last && i == len(data)-1 {
				w.writeInternal(style, data[cur:i])
				if w.curStyle != NormalStyle {
					w.style(NormalStyle)
				}
				if w.inrange() {
					w.w.Write([]byte{'\n'})
				}
				last = false
			} else {
				w.writeInternal(style, data[cur:i+1])
				w.nl()
			}
			cur = i + 1
		case '\t':
			w.writeInternal(style, data[cur:i])
			w.writeInternal(TabStyle, w.tabBytes)
			cur = i + 1
		}
	}
	if cur < len(data) {
		w.writeInternal(style, data[cur:])
	}
	if last {
		if w.curStyle != NormalStyle {
			w.style(NormalStyle)
		}
		if w.inrange() {
			w.w.Write([]byte{'\n'})
		}
	}
}
