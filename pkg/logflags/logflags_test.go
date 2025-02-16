package logflags

import (
	"bytes"
	"io"
	"log/slog"
	"testing"
)

type dummyLogger struct {
	Logger
}

func TestMakeLogger_usingLoggerFactory(t *testing.T) {
	if loggerFactory != nil {
		t.Fatalf("expected loggerFactory to be nil; but was <%v>", loggerFactory)
	}
	defer func() {
		loggerFactory = nil
	}()
	if logOut != nil {
		t.Fatalf("expected logOut to be nil; but was <%v>", logOut)
	}
	logOut = &bufferWriter{}
	defer func() {
		logOut = nil
	}()

	expectedLogger := &dummyLogger{}
	SetLoggerFactory(func(flag bool, fields Fields, out io.Writer) Logger {
		if flag != true {
			t.Fatalf("expected flag to be <%v>; but was <%v>", true, flag)
		}
		if len(fields) != 1 || fields["foo"] != "bar" {
			t.Fatalf("expected fields to be {'foo':'bar'}; but was <%v>", fields)
		}
		if out != logOut {
			t.Fatalf("expected out to be <%v>; but was <%v>", logOut, out)
		}
		return expectedLogger
	})

	actual := makeLogger(true, "foo", "bar")
	if actual != expectedLogger {
		t.Fatalf("expected actual to <%v>; but was <%v>", expectedLogger, actual)
	}
}

func TestMakeLogger_usingDefaultBehavior(t *testing.T) {
	if loggerFactory != nil {
		t.Fatalf("expected loggerFactory to be nil; but was <%v>", loggerFactory)
	}
	if logOut != nil {
		t.Fatalf("expected logOut to be nil; but was <%v>", logOut)
	}
	logOut = &bufferWriter{}
	defer func() {
		logOut = nil
	}()

	actual := makeLogger(false, "foo", "bar")

	actualEntry, expectedType := actual.(slogLogger)
	if !expectedType {
		t.Fatalf("expected actual to be of type slogLogger; but was %T", actual)
	}
	h, ok := actualEntry.s.Handler().(*textHandler)
	if !ok {
		t.Fatalf("expected handler to be *textHandler; but was %T", actualEntry.s.Handler())
	}
	if lvl := h.opts.Level.Level(); lvl != slog.LevelError {
		t.Fatalf("expected level to be <%v>; but was <%v>", slog.LevelError, lvl)
	}
	if h.out != logOut {
		t.Fatalf("expected output to be <%v>; but was <%v>", logOut, h.out)
	}
	if len(h.attrs) != 1 || h.attrs[0].Key != "foo" || h.attrs[0].Value.String() != "bar" {
		t.Fatalf("expected attributes to be {'foo':'bar'}; but was <%v>", h.attrs)
	}
}

func TestMakeLogger_usingDefaultBehaviorAndFlagged(t *testing.T) {
	if loggerFactory != nil {
		t.Fatalf("expected loggerFactory to be nil; but was <%v>", loggerFactory)
	}
	if logOut != nil {
		t.Fatalf("expected logOut to be nil; but was <%v>", logOut)
	}
	logOut = &bufferWriter{}
	defer func() {
		logOut = nil
	}()

	actual := makeLogger(true, "foo", "bar")

	actualEntry, expectedType := actual.(slogLogger)
	if !expectedType {
		t.Fatalf("expected actual to be of type slogLogger; but was %T", actual)
	}
	h, ok := actualEntry.s.Handler().(*textHandler)
	if !ok {
		t.Fatalf("expected actualEntry.Entry.Logger.Formatter to be *textHandler; but was %T", actualEntry.s.Handler())
	}
	if lvl := h.opts.Level.Level(); lvl != slog.LevelDebug {
		t.Fatalf("expected actualEntry.Entry.Logger.Level to be <%v>; but was <%v>", slog.LevelError, lvl)
	}
	if h.out != logOut {
		t.Fatalf("expected actualEntry.Entry.Logger.Out to be <%v>; but was <%v>", logOut, h.out)
	}
	if len(h.attrs) != 1 || h.attrs[0].Key != "foo" || h.attrs[0].Value.String() != "bar" {
		t.Fatalf("expected actualEntry.Entry.Data to be {'foo':'bar'}; but was <%v>", h.attrs)
	}
}

type bufferWriter struct {
	bytes.Buffer
}

func (bw bufferWriter) Close() error {
	return nil
}
