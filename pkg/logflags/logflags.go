package logflags

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

var debugger = false
var gdbWire = false
var lldbServerOutput = false
var debugLineErrors = false
var rpc = false
var fnCall = false
var minidump = false

func makeLogger(flag bool, fields logrus.Fields) *logrus.Entry {
	logger := logrus.New().WithFields(fields)
	logger.Logger.Formatter = &textFormatter{}
	logger.Logger.Level = logrus.DebugLevel
	if !flag {
		logger.Logger.Level = logrus.PanicLevel
	}
	return logger
}

// GdbWire returns true if the gdbserial package should log all the packets
// exchanged with the stub.
func GdbWire() bool {
	return gdbWire
}

// GdbWireLogger returns a configured logger for the gdbserial wire protocol.
func GdbWireLogger() *logrus.Entry {
	return makeLogger(gdbWire, logrus.Fields{"layer": "gdbconn"})
}

// Debugger returns true if the debugger package should log.
func Debugger() bool {
	return debugger
}

// DebuggerLogger returns a logger for the debugger package.
func DebuggerLogger() *logrus.Entry {
	return makeLogger(debugger, logrus.Fields{"layer": "debugger"})
}

// LLDBServerOutput returns true if the output of the LLDB server should be
// redirected to standard output instead of suppressed.
func LLDBServerOutput() bool {
	return lldbServerOutput
}

// DebugLineErrors returns true if pkg/dwarf/line should log its recoverable
// errors.
func DebugLineErrors() bool {
	return debugLineErrors
}

// RPC returns true if RPC messages should be logged.
func RPC() bool {
	return rpc
}

// RPCLogger returns a logger for RPC messages.
func RPCLogger() *logrus.Entry {
	return makeLogger(rpc, logrus.Fields{"layer": "rpc"})
}

// FnCall returns true if the function call protocol should be logged.
func FnCall() bool {
	return fnCall
}

func FnCallLogger() *logrus.Entry {
	return makeLogger(fnCall, logrus.Fields{"layer": "proc", "kind": "fncall"})
}

// Minidump returns true if the minidump loader should be logged.
func Minidump() bool {
	return minidump
}

func MinidumpLogger() *logrus.Entry {
	return makeLogger(minidump, logrus.Fields{"layer": "core", "kind": "minidump"})
}

var errLogstrWithoutLog = errors.New("--log-output specified without --log")

// Setup sets debugger flags based on the contents of logstr.
func Setup(logFlag bool, logstr string) error {
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	if !logFlag {
		log.SetOutput(ioutil.Discard)
		if logstr != "" {
			return errLogstrWithoutLog
		}
		return nil
	}
	if logstr == "" {
		logstr = "debugger"
	}
	v := strings.Split(logstr, ",")
	for _, logcmd := range v {
		switch logcmd {
		case "debugger":
			debugger = true
		case "gdbwire":
			gdbWire = true
		case "lldbout":
			lldbServerOutput = true
		case "debuglineerr":
			debugLineErrors = true
		case "rpc":
			rpc = true
		case "fncall":
			fnCall = true
		case "minidump":
			minidump = true
		}
	}
	return nil
}

// textFormatter is a simplified version of logrus.TextFormatter that
// doesn't make logs unreadable when they are output to a text file or to a
// terminal that doesn't support colors.
type textFormatter struct {
}

func (f *textFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	var b *bytes.Buffer
	if entry.Buffer != nil {
		b = entry.Buffer
	} else {
		b = &bytes.Buffer{}
	}

	keys := make([]string, 0, len(entry.Data))
	for k := range entry.Data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	b.WriteString(entry.Time.Format(time.RFC3339))
	b.WriteByte(' ')
	b.WriteString(entry.Level.String())
	b.WriteByte(' ')
	for i, key := range keys {
		b.WriteString(key)
		b.WriteByte('=')
		stringVal, ok := entry.Data[key].(string)
		if !ok {
			stringVal = fmt.Sprint(entry.Data[key])
		}
		if f.needsQuoting(stringVal) {
			fmt.Fprintf(b, "%q", stringVal)
		} else {
			b.WriteString(stringVal)
		}
		if i != len(keys)-1 {
			b.WriteByte(',')
		} else {
			b.WriteByte(' ')
		}
	}
	b.WriteString(entry.Message)
	b.WriteByte('\n')
	return b.Bytes(), nil
}

func (f *textFormatter) needsQuoting(text string) bool {
	for _, ch := range text {
		if !((ch >= 'a' && ch <= 'z') ||
			(ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') ||
			ch == '-' || ch == '.' || ch == '_' || ch == '/' || ch == '@' || ch == '^' || ch == '+') {
			return true
		}
	}
	return false
}
