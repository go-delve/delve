package logflags

import (
	"bytes"
	"io"
	"reflect"
	"testing"

	"github.com/sirupsen/logrus"
)

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

	expectedLogger := &logrusLogger{}
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

	actual := makeLogger(true, Fields{"foo": "bar"})
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

	actual := makeLogger(false, Fields{"foo": "bar"})

	actualEntry, expectedType := actual.(*logrusLogger)
	if !expectedType {
		t.Fatalf("expected actual to be of type <%v>; but was <%v>", reflect.TypeOf((*logrus.Entry)(nil)), reflect.TypeOf(actualEntry))
	}
	if actualEntry.Entry.Logger.Level != logrus.ErrorLevel {
		t.Fatalf("expected actualEntry.Entry.Logger.Level to be <%v>; but was <%v>", logrus.ErrorLevel, actualEntry.Logger.Level)
	}
	if actualEntry.Entry.Logger.Out != logOut {
		t.Fatalf("expected actualEntry.Entry.Logger.Out to be <%v>; but was <%v>", logOut, actualEntry.Logger.Out)
	}
	if actualEntry.Entry.Logger.Formatter != textFormatterInstance {
		t.Fatalf("expected actualEntry.Entry.Logger.Formatter to be <%v>; but was <%v>", textFormatterInstance, actualEntry.Logger.Formatter)
	}
	if len(actualEntry.Entry.Data) != 1 || actualEntry.Entry.Data["foo"] != "bar" {
		t.Fatalf("expected actualEntry.Entry.Data to be {'foo':'bar'}; but was <%v>", actualEntry.Data)
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

	actual := makeLogger(true, Fields{"foo": "bar"})

	actualEntry, expectedType := actual.(*logrusLogger)
	if !expectedType {
		t.Fatalf("expected actual to be of type <%v>; but was <%v>", reflect.TypeOf((*logrus.Entry)(nil)), reflect.TypeOf(actualEntry))
	}
	if actualEntry.Entry.Logger.Level != logrus.DebugLevel {
		t.Fatalf("expected actualEntry.Entry.Logger.Level to be <%v>; but was <%v>", logrus.DebugLevel, actualEntry.Logger.Level)
	}
	if actualEntry.Entry.Logger.Out != logOut {
		t.Fatalf("expected actualEntry.Entry.Logger.Out to be <%v>; but was <%v>", logOut, actualEntry.Logger.Out)
	}
	if actualEntry.Entry.Logger.Formatter != textFormatterInstance {
		t.Fatalf("expected actualEntry.Entry.Logger.Formatter to be <%v>; but was <%v>", textFormatterInstance, actualEntry.Logger.Formatter)
	}
	if len(actualEntry.Entry.Data) != 1 || actualEntry.Entry.Data["foo"] != "bar" {
		t.Fatalf("expected actualEntry.Entry.Data to be {'foo':'bar'}; but was <%v>", actualEntry.Data)
	}
}

type bufferWriter struct {
	bytes.Buffer
}

func (bw bufferWriter) Close() error {
	return nil
}
