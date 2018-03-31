package logflags

import "strings"

var debugger = false
var gdbWire = false
var lldbServerOutput = false

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

// Setup sets debugger flags based on the contents of logstr.
func Setup(logstr string) {
	if logstr == "true" || logstr == "" {
		debugger = true
		return
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
		}
	}
}
