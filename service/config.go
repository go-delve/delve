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
	// WorkingDir is working directory of the new process. This field is used
	// only when launching a new process.
	WorkingDir string

	// AttachPid is the PID of an existing process to which the debugger should
	// attach.
	AttachPid int
	// AcceptMulti configures the server to accept multiple connection.
	// Note that the server API is not reentrant and clients will have to coordinate.
	AcceptMulti bool
	// APIVersion selects which version of the API to serve (default: 1).
	APIVersion int

	// CoreFile specifies the path to the core dump to open.
	CoreFile string

	// DebugInfoDirectories is the list of directories to look for
	// when resolving external debug info files.
	DebugInfoDirectories []string

	// Selects server backend.
	Backend string

	// Foreground lets target process access stdin.
	Foreground bool

	// DisconnectChan will be closed by the server when the client disconnects
	DisconnectChan chan<- struct{}
}
