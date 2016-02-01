package service

import "net"

// Config provides the configuration to start a Debugger and expose it with a
// service.
//
// Only one of ProcessArgs or AttachPid should be specified. If ProcessArgs is
// provided, a new process will be launched. Otherwise, the debugger will try
// to attach to an existing process with AttachPid.
type Config struct {
	// Listener is used to serve requests.
	Listener net.Listener
	// ProcessArgs are the arguments to launch a new process.
	ProcessArgs []string
	// AttachPid is the PID of an existing process to which the debugger should
	// attach.
	AttachPid int
	// AcceptMulti configures the server to accept multiple connection
	// Note that the server API is not reentrant and clients will have to coordinate
	AcceptMulti bool
}
