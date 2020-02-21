package logflags

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

var debugger = false
var gdbWire = false
var lldbServerOutput = false
var debugLineErrors = false
var rpc = false
var dap = false
var fnCall = false
var minidump = false

var logOut io.WriteCloser

func makeLogger(flag bool, fields logrus.Fields) *logrus.Entry {
	logger := logrus.New().WithFields(fields)
	logger.Logger.Formatter = &textFormatter{}
	if logOut != nil {
		logger.Logger.Out = logOut
	}
	logger.Logger.Level = logrus.DebugLevel
	if !flag {
		logger.Logger.Level = logrus.ErrorLevel
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

// DAP returns true if dap package should log.
func DAP() bool {
	return dap
}

// DAPLogger returns a logger for dap package.
func DAPLogger() *logrus.Entry {
	return makeLogger(dap, logrus.Fields{"layer": "dap"})
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

// WriteDAPListeningMessage writes the "DAP server listening" message in dap mode.
func WriteDAPListeningMessage(addr string) {
	writeListeningMessage("DAP", addr)
}

// WriteAPIListeningMessage writes the "API server listening" message in headless mode.
func WriteAPIListeningMessage(addr string) {
	writeListeningMessage("API", addr)
}

func writeListeningMessage(server string, addr string) {
        msg := fmt.Sprintf("%s server listening at: %s (pid = %d)", server, addr, os.Getpid())
	if logOut != nil {
		fmt.Fprintln(logOut, msg)
	} else {
		fmt.Println(msg)
	}
}

var errLogstrWithoutLog = errors.New("--log-output specified without --log")

// Setup sets debugger flags based on the contents of logstr.
// If logDest is not empty logs will be redirected to the file descriptor or
// file path specified by logDest.
func Setup(logFlag bool, logstr string, logDest string) error {
	if logDest != "" {
		n, err := strconv.Atoi(logDest)
		if err == nil {
			logOut = os.NewFile(uintptr(n), "delve-logs")
		} else {
			fh, err := os.Create(logDest)
			if err != nil {
				return fmt.Errorf("could not create log file: %v", err)
			}
			logOut = fh
		}
	}
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
		case "dap":
			dap = true
		case "fncall":
			fnCall = true
		case "minidump":
			minidump = true
			// If adding another value, do make sure to
			// update "Help about logging flags" in commands.go.
		}
	}
	return nil
}

// Close closes the logger output.
func Close() {
	if logOut != nil {
		logOut.Close()
	}
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
