package logflags

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

var any = false
var debugger = false
var gdbWire = false
var lldbServerOutput = false
var debugLineErrors = false
var rpc = false
var dap = false
var fnCall = false
var minidump = false
var stack = false

var logOut io.WriteCloser

func makeLogger(flag bool, attrs ...interface{}) Logger {
	if lf := loggerFactory; lf != nil {
		fields := make(Fields)
		for i := 0; i < len(attrs); i += 2 {
			fields[attrs[i].(string)] = attrs[i+1]
		}
		return lf(flag, fields, logOut)
	}

	var out io.WriteCloser = os.Stderr
	if logOut != nil {
		out = logOut
	}
	level := slog.LevelError
	if flag {
		level = slog.LevelDebug
	}
	logger := slog.New(newTextHandler(out, &slog.HandlerOptions{Level: level})).With(attrs...)
	return slogLogger{logger}
}

// Any returns true if any logging is enabled.
func Any() bool {
	return any
}

// GdbWire returns true if the gdbserial package should log all the packets
// exchanged with the stub.
func GdbWire() bool {
	return gdbWire
}

// GdbWireLogger returns a configured logger for the gdbserial wire protocol.
func GdbWireLogger() Logger {
	return makeLogger(gdbWire, "layer", "gdbconn")
}

// Debugger returns true if the debugger package should log.
func Debugger() bool {
	return debugger
}

// DebuggerLogger returns a logger for the debugger package.
func DebuggerLogger() Logger {
	return makeLogger(debugger, "layer", "debugger")
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

// DebugLineLogger returns a logger for the dwarf/line package.
func DebugLineLogger() Logger {
	return makeLogger(debugLineErrors, "layer", "dwarf-line")
}

// RPC returns true if RPC messages should be logged.
func RPC() bool {
	return rpc
}

// RPCLogger returns a logger for RPC messages.
func RPCLogger() Logger {
	return rpcLogger(rpc)
}

// rpcLogger returns a logger for RPC messages set to a specific minimal log level.
func rpcLogger(flag bool) Logger {
	return makeLogger(flag, "layer", "rpc")
}

// DAP returns true if dap package should log.
func DAP() bool {
	return dap
}

// DAPLogger returns a logger for dap package.
func DAPLogger() Logger {
	return makeLogger(dap, "layer", "dap")
}

// FnCall returns true if the function call protocol should be logged.
func FnCall() bool {
	return fnCall
}

func FnCallLogger() Logger {
	return makeLogger(fnCall, "layer", "proc", "kind", "fncall")
}

// Minidump returns true if the minidump loader should be logged.
func Minidump() bool {
	return minidump
}

func MinidumpLogger() Logger {
	return makeLogger(minidump, "layer", "core", "kind", "minidump")
}

// Stack returns true if the stacktracer should be logged.
func Stack() bool {
	return stack
}

func StackLogger() Logger {
	return makeLogger(stack, "layer", "core", "kind", "stack")
}

// WriteDAPListeningMessage writes the "DAP server listening" message in dap mode.
func WriteDAPListeningMessage(addr net.Addr) {
	writeListeningMessage("DAP", addr)
}

// WriteAPIListeningMessage writes the "API server listening" message in headless mode.
func WriteAPIListeningMessage(addr net.Addr) {
	writeListeningMessage("API", addr)
}

func writeListeningMessage(server string, addr net.Addr) {
	msg := fmt.Sprintf("%s server listening at: %s", server, addr)
	if logOut != nil {
		fmt.Fprintln(logOut, msg)
	} else {
		fmt.Println(msg)
	}
	tcpAddr, _ := addr.(*net.TCPAddr)
	if tcpAddr == nil || tcpAddr.IP.IsLoopback() {
		return
	}
	logger := rpcLogger(true)
	logger.Warnf("Listening for remote connections (connections are not authenticated nor encrypted)")
}

func WriteError(msg string) {
	if logOut != nil {
		fmt.Fprintln(logOut, msg)
	} else {
		fmt.Fprintln(os.Stderr, msg)
	}
}

func WriteCgoFlagsWarning() {
	makeLogger(true, "layer", "dlv").Warn("CGO_CFLAGS already set, Cgo code could be optimized.")
}

var errLogstrWithoutLog = errors.New("--log-output specified without --log")

// Setup sets debugger flags based on the contents of logstr.
// If logDest is not empty logs will be redirected to the file descriptor or
// file path specified by logDest.
func Setup(logFlag bool, logstr, logDest string) error {
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
		log.SetOutput(io.Discard)
		if logstr != "" {
			return errLogstrWithoutLog
		}
		return nil
	}
	if logstr == "" {
		logstr = "debugger"
	}
	any = true
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
		case "stack":
			stack = true
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

type textHandler struct {
	out               io.WriteCloser
	opts              slog.HandlerOptions
	attrs             []slog.Attr
	preformattedAttrs string // preformatted version of attrs for optimization purposes
}

func newTextHandler(out io.WriteCloser, opts *slog.HandlerOptions) *textHandler {
	return &textHandler{
		out:  out,
		opts: *opts,
	}
}

func (h *textHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.opts.Level.Level()
}

func (h *textHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h2 := *h
	h2.attrs = append(h2.attrs, attrs...)
	m := map[string]slog.Value{}
	keys := []string{}
	for i := range attrs {
		m[attrs[i].Key] = attrs[i].Value
		keys = append(keys, attrs[i].Key)
	}
	sort.Strings(keys)
	b := new(bytes.Buffer)
	for _, key := range keys {
		appendAttr(b, key, m[key])
	}
	b.Truncate(b.Len() - 1)
	h2.preformattedAttrs = b.String()
	return &h2
}

func appendAttr(b *bytes.Buffer, key string, val slog.Value) {
	b.WriteString(key)
	b.WriteByte('=')
	stringVal := val.String()
	if needsQuoting(stringVal) {
		fmt.Fprintf(b, "%q", stringVal)
	} else {
		b.WriteString(stringVal)
	}
	b.WriteByte(',')
}

func (h *textHandler) WithGroup(group string) slog.Handler {
	// group not handled
	return h
}

func (h *textHandler) Handle(_ context.Context, entry slog.Record) error {
	b := &bytes.Buffer{}

	b.WriteString(entry.Time.Format(time.RFC3339))
	b.WriteByte(' ')
	b.WriteString(strings.ToLower(entry.Level.String()))
	b.WriteByte(' ')
	b.WriteString(h.preformattedAttrs)

	if entry.NumAttrs() > 0 {
		if len(h.preformattedAttrs) > 0 {
			b.WriteByte(' ')
		}
		entry.Attrs(func(attr slog.Attr) bool {
			appendAttr(b, attr.Key, attr.Value)
			return true
		})
		b.Truncate(b.Len() - 1)
	}

	if len(h.preformattedAttrs) > 0 || entry.NumAttrs() > 0 {
		b.WriteByte(' ')
	}

	b.WriteString(entry.Message)
	b.WriteByte('\n')
	_, err := h.out.Write(b.Bytes())
	return err
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
