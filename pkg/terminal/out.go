package terminal

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/go-delve/delve/pkg/terminal/colorize"
	"github.com/mattn/go-isatty"
)

// transcriptWriter writes to a pagingWriter and also, optionally, to a
// buffered file.
type transcriptWriter struct {
	fileOnly     bool
	pw           *pagingWriter
	file         *bufio.Writer
	fh           io.Closer
	colorEscapes map[colorize.Style]string
	altTabString string
}

func (w *transcriptWriter) Write(p []byte) (nn int, err error) {
	if !w.fileOnly {
		nn, err = w.pw.Write(p)
	}
	if err == nil {
		if w.file != nil {
			return w.file.Write(p)
		}
	}
	return
}

// ColorizePrint prints to out a syntax highlighted version of the text read from
// reader, between lines startLine and endLine.
func (w *transcriptWriter) ColorizePrint(path string, reader io.ReadSeeker, startLine, endLine, arrowLine int) error {
	var err error
	if !w.fileOnly {
		err = colorize.Print(w.pw.w, path, reader, startLine, endLine, arrowLine, w.colorEscapes, w.altTabString)
	}
	if err == nil {
		if w.file != nil {
			reader.Seek(0, io.SeekStart)
			return colorize.Print(w.file, path, reader, startLine, endLine, arrowLine, nil, w.altTabString)
		}
	}
	return err
}

// Echo outputs str only to the optional transcript file.
func (w *transcriptWriter) Echo(str string) {
	if w.file != nil {
		w.file.WriteString(str)
	}
}

// Flush flushes the optional transcript file.
func (w *transcriptWriter) Flush() {
	if w.file != nil {
		w.file.Flush()
	}
}

// CloseTranscript closes the optional transcript file.
func (w *transcriptWriter) CloseTranscript() error {
	if w.file == nil {
		return nil
	}
	w.file.Flush()
	w.fileOnly = false
	err := w.fh.Close()
	w.file = nil
	w.fh = nil
	return err
}

// TranscribeTo starts transcribing the output to the specified file. If
// fileOnly is true the output will only go to the file, output to the
// io.Writer will be suppressed.
func (w *transcriptWriter) TranscribeTo(fh io.WriteCloser, fileOnly bool) {
	if w.file == nil {
		w.CloseTranscript()
	}
	w.fh = fh
	w.file = bufio.NewWriter(fh)
	w.fileOnly = fileOnly
}

// pagingWriter writes to w. If PageMaybe is called, after a large amount of
// text has been written to w it will pipe the output to a pager instead.
type pagingWriter struct {
	mode     pagingWriterMode
	w        io.Writer
	buf      []byte
	cmd      *exec.Cmd
	cmdStdin io.WriteCloser
	pager    string
	lastnl   bool
	cancel   func()

	lines, columns int
}

type pagingWriterMode uint8

const (
	pagingWriterNormal pagingWriterMode = iota
	pagingWriterMaybe
	pagingWriterPaging
)

func (w *pagingWriter) Write(p []byte) (nn int, err error) {
	switch w.mode {
	default:
		fallthrough
	case pagingWriterNormal:
		return w.w.Write(p)
	case pagingWriterMaybe:
		w.buf = append(w.buf, p...)
		if w.largeOutput() {
			w.cmd = exec.Command(w.pager)
			w.cmd.Stdout = os.Stdout
			w.cmd.Stderr = os.Stderr

			var err1, err2 error
			w.cmdStdin, err1 = w.cmd.StdinPipe()
			err2 = w.cmd.Start()
			if err1 != nil || err2 != nil {
				w.cmd = nil
				w.mode = pagingWriterNormal
				return w.w.Write(p)
			}
			if !w.lastnl {
				w.w.Write([]byte("\n"))
			}
			w.w.Write([]byte("Sending output to pager...\n"))
			w.cmdStdin.Write(w.buf)
			w.buf = nil
			w.mode = pagingWriterPaging
			return len(p), nil
		} else {
			if len(p) > 0 {
				w.lastnl = p[len(p)-1] == '\n'
			}
			return w.w.Write(p)
		}
	case pagingWriterPaging:
		n, err := w.cmdStdin.Write(p)
		if err != nil && w.cancel != nil {
			w.cancel()
			w.cancel = nil
		}
		return n, err
	}
}

// Reset returns the pagingWriter to its normal mode.
func (w *pagingWriter) Reset() {
	if w.mode == pagingWriterNormal {
		return
	}
	w.mode = pagingWriterNormal
	w.buf = nil
	if w.cmd != nil {
		w.cmdStdin.Close()
		w.cmd.Wait()
		w.cmd = nil
		w.cmdStdin = nil
	}
}

// PageMaybe configures pagingWriter to cache the output, after a large
// amount of text has been written to w it will automatically switch to
// piping output to a pager.
// The cancel function is called the first time a write to the pager errors.
func (w *pagingWriter) PageMaybe(cancel func()) {
	if w.mode != pagingWriterNormal {
		return
	}
	dlvpager := os.Getenv("DELVE_PAGER")
	if dlvpager == "" {
		if stdout, _ := w.w.(*os.File); stdout != nil {
			if !isatty.IsTerminal(stdout.Fd()) {
				return
			}
		}
		if strings.ToLower(os.Getenv("TERM")) == "dumb" {
			return
		}
	}
	w.mode = pagingWriterMaybe
	w.pager = dlvpager
	if w.pager == "" {
		w.pager = os.Getenv("PAGER")
		if w.pager == "" {
			w.pager = "more"
		}
	}
	w.lastnl = true
	w.cancel = cancel
	w.getWindowSize()
}

func (w *pagingWriter) largeOutput() bool {
	lines := 0
	lineStart := 0
	for i := range w.buf {
		if i-lineStart > w.columns || w.buf[i] == '\n' {
			lineStart = i
			lines++
			if lines > w.lines {
				return true
			}
		}
	}
	return false
}
