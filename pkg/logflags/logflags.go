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
	"sync"
	"time"
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

type Fields map[string]interface{}

func MakeLogger(flag bool, fields Fields) *Logger {
	logger := &Logger{fields: fields}
	logger.everything = flag
	return logger
}

// GdbWire returns true if the gdbserial package should log all the packets
// exchanged with the stub.
func GdbWire() bool {
	return gdbWire
}

// GdbWireLogger returns a configured logger for the gdbserial wire protocol.
func GdbWireLogger() *Logger {
	return MakeLogger(gdbWire, Fields{"layer": "gdbconn"})
}

// Debugger returns true if the debugger package should log.
func Debugger() bool {
	return debugger
}

// DebuggerLogger returns a logger for the debugger package.
func DebuggerLogger() *Logger {
	return MakeLogger(debugger, Fields{"layer": "debugger"})
}

// DebugLineErrorsLogger returns a logger for debug_line parsing
func DebugLineErrorsLogger() *Logger {
	return MakeLogger(debugLineErrors, Fields{"layer": "dwarf-line"})
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
func RPCLogger() *Logger {
	return MakeLogger(rpc, Fields{"layer": "rpc"})
}

// DAP returns true if dap package should log.
func DAP() bool {
	return dap
}

// DAPLogger returns a logger for dap package.
func DAPLogger() *Logger {
	return MakeLogger(dap, Fields{"layer": "dap"})
}

// FnCall returns true if the function call protocol should be logged.
func FnCall() bool {
	return fnCall
}

func FnCallLogger() *Logger {
	return MakeLogger(fnCall, Fields{"layer": "proc", "kind": "fncall"})
}

// Minidump returns true if the minidump loader should be logged.
func Minidump() bool {
	return minidump
}

func MinidumpLogger() *Logger {
	return MakeLogger(minidump, Fields{"layer": "core", "kind": "minidump"})
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
	msg := fmt.Sprintf("%s server listening at: %s", server, addr)
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
		// If adding another value, do make sure to
		// update "Help about logging flags" in commands.go.
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
		default:
			fmt.Fprintf(os.Stderr, "Warning: unknown log output value %q, run 'dlv help log' for usage.\n", logcmd)
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

var bufferPool *sync.Pool = func() *sync.Pool {
	return &sync.Pool{
		New: func() interface{} {
			return new(bytes.Buffer)
		},
	}
}()

type Logger struct {
	fields     Fields
	everything bool // there are only two log levels, everything or only errors
	mu         sync.Mutex
}

func (logger *Logger) Debugf(fmtstr string, args ...interface{}) {
	if logger.everything {
		logger.log("debug", true, fmtstr, args...)
	}
}

func (logger *Logger) Debug(args ...interface{}) {
	if logger.everything {
		logger.log("debug", false, "%v", args...) // the "%v" only exists to shut up 'go vet', it does nothing.
	}
}

func (logger *Logger) Warnf(fmtstr string, args ...interface{}) {
	if logger.everything {
		logger.log("warning", true, fmtstr, args...)
	}
}

func (logger *Logger) Warn(args ...interface{}) {
	if logger.everything {
		logger.log("warning", false, "%v", args...) // the "%v" only exists to shut up 'go vet', it does nothing.
	}
}

func (logger *Logger) Infof(fmtstr string, args ...interface{}) {
	if logger.everything {
		logger.log("info", true, fmtstr, args...)
	}
}

func (logger *Logger) Info(args ...interface{}) {
	if logger.everything {
		logger.log("info", false, "%v", args...) // the "%v" only exists to shut up 'go vet', it does nothing.
	}
}

func (logger *Logger) Errorf(fmtstr string, args ...interface{}) {
	logger.log("error", true, fmtstr, args...)
}

func (logger *Logger) Error(args ...interface{}) {
	logger.log("error", false, "%v", args...) // the "%v" only exists to shut up 'go vet', it does nothing.
}

func (logger *Logger) log(level string, isfmt bool, fmtstr string, args ...interface{}) {
	b := bufferPool.Get().(*bytes.Buffer)
	defer bufferPool.Put(b)
	b.Reset()

	keys := make([]string, 0, len(logger.fields))
	for k := range logger.fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	b.WriteString(time.Now().Format(time.RFC3339))
	b.WriteByte(' ')
	b.WriteString(level)
	b.WriteByte(' ')
	for i, key := range keys {
		b.WriteString(key)
		b.WriteByte('=')
		stringVal, ok := logger.fields[key].(string)
		if !ok {
			stringVal = fmt.Sprint(logger.fields[key])
		}
		if needsQuoting(stringVal) {
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
	if isfmt {
		fmt.Fprintf(b, fmtstr, args...)
	} else {
		fmt.Fprint(b, args...)
	}
	b.WriteByte('\n')
	logger.mu.Lock()
	if logOut != nil {
		_, _ = logOut.Write(b.Bytes())
	} else {
		_, _ = os.Stderr.Write(b.Bytes())
	}
	logger.mu.Unlock()
}

func needsQuoting(text string) bool {
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
