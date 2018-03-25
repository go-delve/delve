package logflags

import (
	"errors"
	"strings"
)

var debugger = false
var gdbWire = false
var lldbServerOutput = false
var suppressedErrors = false
var debugLineErrors = false

// GdbWire returns true if the gdbserial package should log all the packets
// exchanged with the stub.
func GdbWire() bool {
	return gdbWire
}

// Debugger returns true if the debugger package should log.
func Debugger() bool {
	return debugger
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

var errLogstrWithoutLog = errors.New("--log-output specified without --log")

// Setup sets debugger flags based on the contents of logstr.
func Setup(log bool, logstr string) error {
	if !log {
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
		}
	}
	return nil
}
