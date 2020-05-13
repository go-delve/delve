package service

import (
	"net"

	"github.com/go-delve/delve/service/debugger"
)

// Config provides the configuration to start a Debugger and expose it with a
// service.
//
// Only one of ProcessArgs or AttachPid should be specified. If ProcessArgs is
// provided, a new process will be launched. Otherwise, the debugger will try
// to attach to an existing process with AttachPid.
type Config struct {
	// Debugger configuration object, used to configure the underlying
	// debugger used by the server.
	Debugger debugger.Config

	// Listener is used to serve requests.
	Listener net.Listener

	// ProcessArgs are the arguments to launch a new process.
	ProcessArgs []string

	// AcceptMulti configures the server to accept multiple connection.
	// Note that the server API is not reentrant and clients will have to coordinate.
	AcceptMulti bool

	// APIVersion selects which version of the API to serve (default: 1).
	APIVersion int

	// CheckLocalConnUser is true if the debugger should check that local
	// connections come from the same user that started the headless server
	CheckLocalConnUser bool

	// DisconnectChan will be closed by the server when the client disconnects
	DisconnectChan chan<- struct{}
}
