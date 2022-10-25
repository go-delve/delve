// Package dap implements VSCode's Debug Adaptor Protocol (DAP).
// This allows delve to communicate with frontends using DAP
// without a separate adaptor. The frontend will run the debugger
// (which now doubles as an adaptor) in server mode listening on
// a port and communicating over TCP. This is work in progress,
// so for now Delve in dap mode only supports synchronous
// request-response communication, blocking while processing each request.
// For DAP details see https://microsoft.github.io/debug-adapter-protocol.
package dap

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/constant"
	"go/parser"
	"io"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-delve/delve/pkg/gobuild"
	"github.com/go-delve/delve/pkg/goversion"
	"github.com/go-delve/delve/pkg/locspec"
	"github.com/go-delve/delve/pkg/logflags"
	"github.com/go-delve/delve/pkg/proc"

	"github.com/go-delve/delve/service"
	"github.com/go-delve/delve/service/api"
	"github.com/go-delve/delve/service/debugger"
	"github.com/go-delve/delve/service/internal/sameuser"
	"github.com/google/go-dap"

	"github.com/sirupsen/logrus"
)

// Server implements a DAP server that can accept a single client for
// a single debug session (for now). It does not yet support restarting.
// That means that in addition to explicit shutdown requests,
// program termination and failed or closed client connection
// would also result in stopping this single-use server.
//
// The DAP server operates via the following goroutines:
//
// (1) Main goroutine where the server is created via NewServer(),
// started via Run() and stopped via Stop(). Once the server is
// started, this goroutine blocks until it receives a stop-server
// signal that can come from an OS interrupt (such as Ctrl-C) or
// config.DisconnectChan (passed to NewServer()) as a result of
// client connection failure or closure or a DAP disconnect request.
//
// (2) Run goroutine started from Run() that serves as both
// a listener and a client goroutine. It accepts a client connection,
// reads, decodes and dispatches each request from the client.
// For synchronous requests, it issues commands to the
// underlying debugger and sends back events and responses.
// These requests block while the debuggee is running, so,
// where applicable, the handlers need to check if debugging
// state is running, so there is a need for a halt request or
// a dummy/error response to avoid blocking.
//
// This is the only goroutine that sends a stop-server signal
// via config.DisconnecChan when encountering a client connection
// error or responding to a (synchronous) DAP disconnect request.
// Once stop is triggered, the goroutine exits.
//
// Unlike rpccommon, there is not another layer of per-client
// goroutines here because the dap server does not support
// multiple clients.
//
// (3) Per-request goroutine is started for each asynchronous request
// that resumes execution. We check if target is running already, so
// there should be no more than one pending asynchronous request at
// a time. This goroutine issues commands to the underlying debugger
// and sends back events and responses. It takes a setup-done channel
// as an argument and temporarily blocks the request loop until setup
// for asynchronous execution is complete and targe is running.
// Once done, it unblocks processing of parallel requests unblocks
// (e.g. disconnecting while the program is running).
//
// These per-request goroutines never send a stop-server signal.
// They block on running debugger commands that are interrupted
// when halt is issued while stopping. At that point these goroutines
// wrap-up and exit.
type Server struct {
	// config is all the information necessary to start the debugger and server.
	config *Config
	// listener is used to accept the client connection.
	// When working with a predetermined client, this is nil.
	listener net.Listener
	// session is the debug session that comes with an client connection.
	session   *Session
	sessionMu sync.Mutex
}

// Session is an abstraction for serving and shutting down
// a DAP debug session with a pre-connected client.
// TODO(polina): move this to a different file/package
type Session struct {
	config *Config

	id int

	// stackFrameHandles maps frames of each goroutine to unique ids across all goroutines.
	// Reset at every stop.
	stackFrameHandles *handlesMap
	// variableHandles maps compound variables to unique references within their stack frame.
	// Reset at every stop.
	// See also comment for convertVariable.
	variableHandles *variablesHandlesMap
	// args tracks special settings for handling debug session requests.
	args launchAttachArgs
	// exceptionErr tracks the runtime error that last occurred.
	exceptionErr error
	// clientCapabilities tracks special settings for handling debug session requests.
	clientCapabilities dapClientCapabilites

	// mu synchronizes access to objects set on start-up (from run goroutine)
	// and stopped on teardown (from main goroutine)
	mu sync.Mutex

	// conn is the accepted client connection.
	conn *connection
	// debugger is the underlying debugger service.
	debugger *debugger.Debugger
	// binaryToRemove is the temp compiled binary to be removed on disconnect (if any).
	binaryToRemove string
	// noDebugProcess is set for the noDebug launch process.
	noDebugProcess *process

	// sendingMu synchronizes writing to conn
	// to ensure that messages do not get interleaved
	sendingMu sync.Mutex

	// runningCmd tracks whether the server is running an asynchronous
	// command that resumes execution, which may not correspond to the actual
	// running state of the process (e.g. if a command is temporarily interrupted).
	runningCmd bool
	runningMu  sync.Mutex

	// haltRequested tracks whether a halt of the program has been requested, which may
	// not correspond to whether a Halt Request has been sent to the target.
	haltRequested bool
	haltMu        sync.Mutex

	// changeStateMu must be held for a request to protect itself from another goroutine
	// changing the state of the running process at the same time.
	changeStateMu sync.Mutex
}

// Config is all the information needed to start the debugger, handle
// DAP connection traffic and signal to the server when it is time to stop.
type Config struct {
	*service.Config

	// log is used for structured logging.
	log *logrus.Entry
	// StopTriggered is closed when the server is Stop()-ed.
	// Can be used to safeguard against duplicate shutdown sequences.
	StopTriggered chan struct{}
}

type connection struct {
	io.ReadWriteCloser
	closed chan struct{}
}

func (c *connection) Close() error {
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	return c.ReadWriteCloser.Close()
}

func (c *connection) isClosed() bool {
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}

type process struct {
	*exec.Cmd
	exited chan struct{}
}

// launchAttachArgs captures arguments from launch/attach request that
// impact handling of subsequent requests.
// The fields with cfgName tag can be updated through an evaluation request.
type launchAttachArgs struct {
	// stopOnEntry is set to automatically stop the debugee after start.
	stopOnEntry bool
	// StackTraceDepth is the maximum length of the returned list of stack frames.
	StackTraceDepth int `cfgName:"stackTraceDepth"`
	// ShowGlobalVariables indicates if global package variables should be loaded.
	ShowGlobalVariables bool `cfgName:"showGlobalVariables"`
	// ShowRegisters indicates if register values should be loaded.
	ShowRegisters bool `cfgName:"showRegisters"`
	// GoroutineFilters are the filters used when loading goroutines.
	GoroutineFilters string `cfgName:"goroutineFilters"`
	// HideSystemGoroutines indicates if system goroutines should be removed from threads
	// responses.
	HideSystemGoroutines bool `cfgName:"hideSystemGoroutines"`
	// substitutePathClientToServer indicates rules for converting file paths between client and debugger.
	substitutePathClientToServer [][2]string `cfgName:"substitutePath"`
	// substitutePathServerToClient indicates rules for converting file paths between debugger and client.
	substitutePathServerToClient [][2]string
}

// defaultArgs borrows the defaults for the arguments from the original vscode-go adapter.
// TODO(polinasok): clean up this and its reference (Server.args)
// in favor of default*Config variables defined in types.go.
var defaultArgs = launchAttachArgs{
	stopOnEntry:                  false,
	StackTraceDepth:              50,
	ShowGlobalVariables:          false,
	HideSystemGoroutines:         false,
	ShowRegisters:                false,
	GoroutineFilters:             "",
	substitutePathClientToServer: [][2]string{},
	substitutePathServerToClient: [][2]string{},
}

// dapClientCapabilites captures arguments from intitialize request that
// impact handling of subsequent requests.
type dapClientCapabilites struct {
	supportsVariableType         bool
	supportsVariablePaging       bool
	supportsRunInTerminalRequest bool
	supportsMemoryReferences     bool
	supportsProgressReporting    bool
}

// DefaultLoadConfig controls how variables are loaded from the target's memory.
// These limits are conservative to minimize performance overhead for bulk loading.
// With dlv-dap, users do not have a way to adjust these.
// Instead we are focusing in interactive loading with nested reloads, array/map
// paging and context-specific string limits.
var DefaultLoadConfig = proc.LoadConfig{
	FollowPointers:     true,
	MaxVariableRecurse: 1,
	// TODO(polina): consider 1024 limit instead:
	// - vscode+C appears to use 1024 as the load limit
	// - vscode viewlet hover truncates at 1023 characters
	MaxStringLen:    512,
	MaxArrayValues:  64,
	MaxStructFields: -1,
}

const (
	// When a user examines a single string, we can relax the loading limit.
	maxSingleStringLen = 4 << 10 // 4096
	// Results of a call are single-use and transient. We need to maximize
	// what is presented. A common use case of a call injection is to
	// stringify complex data conveniently.
	maxStringLenInCallRetVars = 1 << 10 // 1024
)

var (
	// Max number of goroutines that we will return.
	// This is a var for testing
	maxGoroutines = 1 << 10
)

// NewServer creates a new DAP Server. It takes an opened Listener
// via config and assumes its ownership. config.DisconnectChan has to be set;
// it will be closed by the server when the client fails to connect,
// disconnects or requests shutdown. Once config.DisconnectChan is closed,
// Server.Stop() must be called to shutdown this single-user server.
//
// NewServer can be used to create a special DAP Server that works
// only with a predetermined client. In that case, config.Listener is
// nil and its RunWithClient must be used instead of Run.
func NewServer(config *service.Config) *Server {
	logger := logflags.DAPLogger()
	if config.Listener != nil {
		logflags.WriteDAPListeningMessage(config.Listener.Addr())
	} else {
		logger.Debug("DAP server for a predetermined client")
	}
	logger.Debug("DAP server pid = ", os.Getpid())
	if config.AcceptMulti {
		logger.Warn("DAP server does not support accept-multiclient mode")
		config.AcceptMulti = false
	}
	return &Server{
		config: &Config{
			Config:        config,
			log:           logger,
			StopTriggered: make(chan struct{}),
		},
		listener: config.Listener,
	}
}

var sessionCount = 0

// NewSession creates a new client session that can handle DAP traffic.
// It takes an open connection and provides a Close() method to shut it
// down when the DAP session disconnects or a connection error occurs.
func NewSession(conn io.ReadWriteCloser, config *Config, debugger *debugger.Debugger) *Session {
	sessionCount++
	if config.log == nil {
		config.log = logflags.DAPLogger()
	}
	config.log.Debugf("DAP connection %d started", sessionCount)
	if config.StopTriggered == nil {
		config.log.Fatal("Session must be configured with StopTriggered")
	}
	return &Session{
		config:            config,
		id:                sessionCount,
		conn:              &connection{conn, make(chan struct{})},
		stackFrameHandles: newHandlesMap(),
		variableHandles:   newVariablesHandlesMap(),
		args:              defaultArgs,
		exceptionErr:      nil,
		debugger:          debugger,
	}
}

// If user-specified options are provided via Launch/AttachRequest,
// we override the defaults for optional args.
func (s *Session) setLaunchAttachArgs(args LaunchAttachCommonConfig) error {
	s.args.stopOnEntry = args.StopOnEntry
	if depth := args.StackTraceDepth; depth > 0 {
		s.args.StackTraceDepth = depth
	}
	s.args.ShowGlobalVariables = args.ShowGlobalVariables
	s.args.ShowRegisters = args.ShowRegisters
	s.args.HideSystemGoroutines = args.HideSystemGoroutines
	s.args.GoroutineFilters = args.GoroutineFilters
	if paths := args.SubstitutePath; len(paths) > 0 {
		clientToServer := make([][2]string, 0, len(paths))
		serverToClient := make([][2]string, 0, len(paths))
		for _, p := range paths {
			clientToServer = append(clientToServer, [2]string{p.From, p.To})
			serverToClient = append(serverToClient, [2]string{p.To, p.From})
		}
		s.args.substitutePathClientToServer = clientToServer
		s.args.substitutePathServerToClient = serverToClient
	}
	return nil
}

// Stop stops the DAP debugger service, closes the listener and the client
// connection. It shuts down the underlying debugger and kills the target
// process if it was launched by it or stops the noDebug process.
// This method mustn't be called more than once.
// StopTriggered notifies other goroutines that stop is in progreess.
func (s *Server) Stop() {
	s.config.log.Debug("DAP server stopping...")
	defer s.config.log.Debug("DAP server stopped")
	close(s.config.StopTriggered)

	if s.listener != nil {
		// If run goroutine is blocked on accept, this will unblock it.
		_ = s.listener.Close()
	}

	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	if s.session == nil {
		return
	}
	// If run goroutine is blocked on read, this will unblock it.
	s.session.Close()
}

// Close closes the underlying debugger/process and connection.
// May be called more than once.
func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.debugger != nil {
		killProcess := s.debugger.AttachPid() == 0
		s.stopDebugSession(killProcess)
	} else if s.noDebugProcess != nil {
		s.stopNoDebugProcess()
	}
	// The binary is no longer in use by the debugger. It is safe to remove it.
	if s.binaryToRemove != "" {
		gobuild.Remove(s.binaryToRemove)
		s.binaryToRemove = "" // avoid error printed on duplicate removal
	}
	// Close client connection last, so other shutdown stages
	// can send client notifications.
	// Unless Stop() was called after read loop in ServeDAPCodec()
	// returned, this will result in a closed connection error
	// on next read, breaking out the read loop andd
	// allowing the run goroutinee to exit.
	// This connection is closed here and in serveDAPCodec().
	// If this was a forced shutdown, external stop logic can close this first.
	// If this was a client loop exit (on error or disconnect), serveDAPCodec()
	// will be first.
	// Duplicate close calls return an error, but are not fatal.
	_ = s.conn.Close()
}

// triggerServerStop closes DisconnectChan if not nil, which
// signals that client sent a disconnect request or there was connection
// failure or closure. Since the server currently services only one
// client, this is used as a signal to stop the entire server.
// The function safeguards agaist closing the channel more
// than once and can be called multiple times. It is not thread-safe
// and is currently only called from the run goroutine.
func (c *Config) triggerServerStop() {
	// Avoid accidentally closing the channel twice and causing a panic, when
	// this function is called more than once because stop was triggered
	// by multiple conditions simultenously.
	if c.DisconnectChan != nil {
		close(c.DisconnectChan)
		c.DisconnectChan = nil
	}
	// There should be no logic here after the stop-server
	// signal that might cause everything to shutdown before this
	// logic gets executed.
}

// Run launches a new goroutine where it accepts a client connection
// and starts processing requests from it. Use Stop() to close connection.
// The server does not support multiple clients, serially or in parallel.
// The server should be restarted for every new debug session.
// The debugger won't be started until launch/attach request is received.
// TODO(polina): allow new client connections for new debug sessions,
// so the editor needs to launch dap server only once? Note that some requests
// may change the server's environment (e.g. see dlvCwd of launch configuration).
// So if we want to reuse this server for multiple independent debugging sessions
// we need to take that into consideration.
func (s *Server) Run() {
	if s.listener == nil {
		s.config.log.Fatal("Misconfigured server: no Listener is configured.")
		return
	}

	go func() {
		conn, err := s.listener.Accept() // listener is closed in Stop()
		if err != nil {
			select {
			case <-s.config.StopTriggered:
			default:
				s.config.log.Errorf("Error accepting client connection: %s\n", err)
				s.config.triggerServerStop()
			}
			return
		}
		if s.config.CheckLocalConnUser {
			if !sameuser.CanAccept(s.listener.Addr(), conn.LocalAddr(), conn.RemoteAddr()) {
				s.config.log.Error("Error accepting client connection: Only connections from the same user that started this instance of Delve are allowed to connect. See --only-same-user.")
				s.config.triggerServerStop()
				return
			}
		}
		s.runSession(conn)
	}()
}

func (s *Server) runSession(conn io.ReadWriteCloser) {
	s.sessionMu.Lock()
	s.session = NewSession(conn, s.config, nil) // closed in Stop()
	s.sessionMu.Unlock()
	s.session.ServeDAPCodec()
}

// RunWithClient is similar to Run but works only with an already established
// connection instead of waiting on the listener to accept a new client.
// RunWithClient takes ownership of conn. Debugger won't be started
// until a launch/attach request is received over the connection.
func (s *Server) RunWithClient(conn net.Conn) {
	if s.listener != nil {
		s.config.log.Fatal("RunWithClient must not be used when the Server is configured with a Listener")
		return
	}
	s.config.log.Debugf("Connected to the client at %s", conn.RemoteAddr())
	go s.runSession(conn)
}

func (s *Session) address() string {
	if s.config.Listener != nil {
		return s.config.Listener.Addr().String()
	}
	if netconn, ok := s.conn.ReadWriteCloser.(net.Conn); ok {
		return netconn.LocalAddr().String()
	}
	return ""
}

// ServeDAPCodec reads and decodes requests from the client
// until it encounters an error or EOF, when it sends
// a disconnect signal and returns.
func (s *Session) ServeDAPCodec() {
	// Close conn, but not the debugger in case we are in AcceptMuli mode.
	// If not, debugger will be shut down in Stop().
	defer s.conn.Close()
	reader := bufio.NewReader(s.conn)
	for {
		request, err := dap.ReadProtocolMessage(reader)
		// Handle dap.DecodeProtocolMessageFieldError errors gracefully by responding with an ErrorResponse.
		// For example:
		// -- "Request command 'foo' is not supported" means we
		// potentially got some new DAP request that we do not yet have
		// decoding support for, so we can respond with an ErrorResponse.
		//
		// Other errors, such as unmarshalling errors, will log the error and cause the server to trigger
		// a stop.
		if err != nil {
			s.config.log.Debug("DAP error: ", err)
			select {
			case <-s.config.StopTriggered:
			default:
				if !s.config.AcceptMulti {
					defer s.config.triggerServerStop()
				}
				if err != io.EOF { // EOF means client closed connection
					if decodeErr, ok := err.(*dap.DecodeProtocolMessageFieldError); ok {
						// Send an error response to the users if we were unable to process the message.
						s.sendInternalErrorResponse(decodeErr.Seq, err.Error())
						continue
					}
					s.config.log.Error("DAP error: ", err)
				}
			}
			return
		}
		s.handleRequest(request)

		if _, ok := request.(*dap.DisconnectRequest); ok {
			// disconnect already shut things down and triggered stopping
			return
		}
	}
}

// In case a handler panics, we catch the panic to avoid crashing both
// the server and the target. We send an error response back, but
// in case its a dup and ignored by the client, we also log the error.
func (s *Session) recoverPanic(request dap.Message) {
	if ierr := recover(); ierr != nil {
		s.config.log.Errorf("recovered panic: %s\n%s\n", ierr, debug.Stack())
		s.sendInternalErrorResponse(request.GetSeq(), fmt.Sprintf("%v", ierr))
	}
}

func (s *Session) handleRequest(request dap.Message) {
	defer s.recoverPanic(request)
	jsonmsg, _ := json.Marshal(request)
	s.config.log.Debug("[<- from client]", string(jsonmsg))

	if _, ok := request.(dap.RequestMessage); !ok {
		s.sendInternalErrorResponse(request.GetSeq(), fmt.Sprintf("Unable to process non-request %#v\n", request))
		return
	}

	if s.isNoDebug() {
		switch request := request.(type) {
		case *dap.DisconnectRequest:
			s.onDisconnectRequest(request)
		case *dap.RestartRequest:
			s.sendUnsupportedErrorResponse(request.Request)
		default:
			r := request.(dap.RequestMessage).GetRequest()
			s.sendErrorResponse(*r, NoDebugIsRunning, "noDebug mode", fmt.Sprintf("unable to process '%s' request", r.Command))
		}
		return
	}

	// These requests, can be handled regardless of whether the targret is running
	switch request := request.(type) {
	case *dap.InitializeRequest: // Required
		s.onInitializeRequest(request)
		return
	case *dap.LaunchRequest: // Required
		s.onLaunchRequest(request)
		return
	case *dap.AttachRequest: // Required
		s.onAttachRequest(request)
		return
	case *dap.DisconnectRequest: // Required
		s.onDisconnectRequest(request)
		return
	case *dap.PauseRequest: // Required
		s.onPauseRequest(request)
		return
	case *dap.TerminateRequest: // Optional (capability ‘supportsTerminateRequest‘)
		/*TODO*/ s.onTerminateRequest(request) // not yet implemented
		return
	case *dap.RestartRequest: // Optional (capability ‘supportsRestartRequest’)
		/*TODO*/ s.onRestartRequest(request) // not yet implemented
		return
	}

	// Most requests cannot be processed while the debuggee is running.
	// We have a couple of options for handling these without blocking
	// the request loop indefinitely when we are in running state.
	// --1-- Return a dummy response or an error right away.
	// --2-- Halt execution, process the request, maybe resume execution.
	// --3-- Handle such requests asynchronously and let them block until
	// the process stops or terminates (e.g. using a channel and a single
	// goroutine to preserve the order). This might not be appropriate
	// for requests such as continue or step because they would skip
	// the stop, resuming execution right away. Other requests
	// might not be relevant anymore when the stop is finally reached, and
	// state changed from the previous snapshot. The user might want to
	// resume execution before the backlog of buffered requests is cleared,
	// so we would have to either cancel them or delay processing until
	// the next stop. In addition, the editor itself might block waiting
	// for these requests to return. We are not aware of any requests
	// that would benefit from this approach at this time.
	if s.debugger != nil && s.debugger.IsRunning() || s.isRunningCmd() {
		switch request := request.(type) {
		case *dap.ThreadsRequest: // Required
			// On start-up, the client requests the baseline of currently existing threads
			// right away as there are a number of DAP requests that require a thread id
			// (pause, continue, stacktrace, etc). This can happen after the program
			// continues on entry, preventing the client from handling any pause requests
			// from the user. We remedy this by sending back a placeholder thread id
			// for the current goroutine.
			response := &dap.ThreadsResponse{
				Response: *newResponse(request.Request),
				Body:     dap.ThreadsResponseBody{Threads: []dap.Thread{{Id: -1, Name: "Current"}}},
			}
			s.send(response)
		case *dap.SetBreakpointsRequest: // Required
			s.changeStateMu.Lock()
			defer s.changeStateMu.Unlock()
			s.config.log.Debug("halting execution to set breakpoints")
			_, err := s.halt()
			if err != nil {
				s.sendErrorResponse(request.Request, UnableToSetBreakpoints, "Unable to set or clear breakpoints", err.Error())
				return
			}
			s.onSetBreakpointsRequest(request)
		case *dap.SetFunctionBreakpointsRequest: // Optional (capability ‘supportsFunctionBreakpoints’)
			s.changeStateMu.Lock()
			defer s.changeStateMu.Unlock()
			s.config.log.Debug("halting execution to set breakpoints")
			_, err := s.halt()
			if err != nil {
				s.sendErrorResponse(request.Request, UnableToSetBreakpoints, "Unable to set or clear breakpoints", err.Error())
				return
			}
			s.onSetFunctionBreakpointsRequest(request)
		default:
			r := request.(dap.RequestMessage).GetRequest()
			s.sendErrorResponse(*r, DebuggeeIsRunning, fmt.Sprintf("Unable to process `%s`", r.Command), "debuggee is running")
		}
		return
	}

	// Requests below can only be handled while target is stopped.
	// Some of them are blocking and will be handled synchronously
	// on this goroutine while non-blocking requests will be dispatched
	// to another goroutine. Please note that because of the running
	// check above, there should be no more than one pending asynchronous
	// request at a time.

	// Non-blocking request handlers will signal when they are ready
	// setting up for async execution, so more requests can be processed.
	resumeRequestLoop := make(chan struct{})

	switch request := request.(type) {
	//--- Asynchronous requests ---
	case *dap.ConfigurationDoneRequest: // Optional (capability ‘supportsConfigurationDoneRequest’)
		go func() {
			defer s.recoverPanic(request)
			s.onConfigurationDoneRequest(request, resumeRequestLoop)
		}()
		<-resumeRequestLoop
	case *dap.ContinueRequest: // Required
		go func() {
			defer s.recoverPanic(request)
			s.onContinueRequest(request, resumeRequestLoop)
		}()
		<-resumeRequestLoop
	case *dap.NextRequest: // Required
		go func() {
			defer s.recoverPanic(request)
			s.onNextRequest(request, resumeRequestLoop)
		}()
		<-resumeRequestLoop
	case *dap.StepInRequest: // Required
		go func() {
			defer s.recoverPanic(request)
			s.onStepInRequest(request, resumeRequestLoop)
		}()
		<-resumeRequestLoop
	case *dap.StepOutRequest: // Required
		go func() {
			defer s.recoverPanic(request)
			s.onStepOutRequest(request, resumeRequestLoop)
		}()
		<-resumeRequestLoop
	case *dap.StepBackRequest: // Optional (capability ‘supportsStepBack’)
		go func() {
			defer s.recoverPanic(request)
			s.onStepBackRequest(request, resumeRequestLoop)
		}()
		<-resumeRequestLoop
	case *dap.ReverseContinueRequest: // Optional (capability ‘supportsStepBack’)
		go func() {
			defer s.recoverPanic(request)
			s.onReverseContinueRequest(request, resumeRequestLoop)
		}()
		<-resumeRequestLoop
	//--- Synchronous requests ---
	case *dap.SetBreakpointsRequest: // Required
		s.onSetBreakpointsRequest(request)
	case *dap.SetFunctionBreakpointsRequest: // Optional (capability ‘supportsFunctionBreakpoints’)
		s.onSetFunctionBreakpointsRequest(request)
	case *dap.SetInstructionBreakpointsRequest: // Optional (capability 'supportsInstructionBreakpoints')
		s.onSetInstructionBreakpointsRequest(request)
	case *dap.SetExceptionBreakpointsRequest: // Optional (capability ‘exceptionBreakpointFilters’)
		s.onSetExceptionBreakpointsRequest(request)
	case *dap.ThreadsRequest: // Required
		s.onThreadsRequest(request)
	case *dap.StackTraceRequest: // Required
		s.onStackTraceRequest(request)
	case *dap.ScopesRequest: // Required
		s.onScopesRequest(request)
	case *dap.VariablesRequest: // Required
		s.onVariablesRequest(request)
	case *dap.EvaluateRequest: // Required
		s.onEvaluateRequest(request)
	case *dap.SetVariableRequest: // Optional (capability ‘supportsSetVariable’)
		s.onSetVariableRequest(request)
	case *dap.ExceptionInfoRequest: // Optional (capability ‘supportsExceptionInfoRequest’)
		s.onExceptionInfoRequest(request)
	case *dap.DisassembleRequest: // Optional (capability ‘supportsDisassembleRequest’)
		s.onDisassembleRequest(request)
	//--- Requests that we may want to support ---
	case *dap.SourceRequest: // Required
		/*TODO*/ s.sendUnsupportedErrorResponse(request.Request) // https://github.com/go-delve/delve/issues/2851
	case *dap.SetExpressionRequest: // Optional (capability ‘supportsSetExpression’)
		/*TODO*/ s.onSetExpressionRequest(request) // Not yet implemented
	case *dap.LoadedSourcesRequest: // Optional (capability ‘supportsLoadedSourcesRequest’)
		/*TODO*/ s.onLoadedSourcesRequest(request) // Not yet implemented
	case *dap.ReadMemoryRequest: // Optional (capability ‘supportsReadMemoryRequest‘)
		/*TODO*/ s.onReadMemoryRequest(request) // Not yet implemented
	case *dap.CancelRequest: // Optional (capability ‘supportsCancelRequest’)
		/*TODO*/ s.onCancelRequest(request) // Not yet implemented (does this make sense?)
	case *dap.ModulesRequest: // Optional (capability ‘supportsModulesRequest’)
		/*TODO*/ s.sendUnsupportedErrorResponse(request.Request) // Not yet implemented (does this make sense?)
	//--- Requests that we do not plan to support ---
	case *dap.RestartFrameRequest: // Optional (capability ’supportsRestartFrame’)
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.GotoRequest: // Optional (capability ‘supportsGotoTargetsRequest’)
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.TerminateThreadsRequest: // Optional (capability ‘supportsTerminateThreadsRequest’)
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.StepInTargetsRequest: // Optional (capability ‘supportsStepInTargetsRequest’)
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.GotoTargetsRequest: // Optional (capability ‘supportsGotoTargetsRequest’)
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.CompletionsRequest: // Optional (capability ‘supportsCompletionsRequest’)
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.DataBreakpointInfoRequest: // Optional (capability ‘supportsDataBreakpoints’)
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.SetDataBreakpointsRequest: // Optional (capability ‘supportsDataBreakpoints’)
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.BreakpointLocationsRequest: // Optional (capability ‘supportsBreakpointLocationsRequest’)
		s.sendUnsupportedErrorResponse(request.Request)
	default:
		// This is a DAP message that go-dap has a struct for, so
		// decoding succeeded, but this function does not know how
		// to handle.
		s.sendInternalErrorResponse(request.GetSeq(), fmt.Sprintf("Unable to process %#v\n", request))
	}
}

func (s *Session) send(message dap.Message) {
	jsonmsg, _ := json.Marshal(message)
	s.config.log.Debug("[-> to client]", string(jsonmsg))
	// TODO(polina): consider using a channel for all the sends and to have a dedicated
	// goroutine that reads from that channel and sends over the connection.
	// This will avoid blocking on slow network sends.
	s.sendingMu.Lock()
	defer s.sendingMu.Unlock()
	err := dap.WriteProtocolMessage(s.conn, message)
	if err != nil {
		s.config.log.Debug(err)
	}
}

func (s *Session) logToConsole(msg string) {
	s.send(&dap.OutputEvent{
		Event: *newEvent("output"),
		Body: dap.OutputEventBody{
			Output:   msg + "\n",
			Category: "console",
		}})
}

func (s *Session) onInitializeRequest(request *dap.InitializeRequest) {
	s.setClientCapabilities(request.Arguments)
	if request.Arguments.PathFormat != "path" {
		s.sendErrorResponse(request.Request, FailedToInitialize, "Failed to initialize",
			fmt.Sprintf("Unsupported 'pathFormat' value '%s'.", request.Arguments.PathFormat))
		return
	}
	if !request.Arguments.LinesStartAt1 {
		s.sendErrorResponse(request.Request, FailedToInitialize, "Failed to initialize",
			"Only 1-based line numbers are supported.")
		return
	}
	if !request.Arguments.ColumnsStartAt1 {
		s.sendErrorResponse(request.Request, FailedToInitialize, "Failed to initialize",
			"Only 1-based column numbers are supported.")
		return
	}

	// TODO(polina): Respond with an error if debug session started
	// with an initialize request is in progress?
	response := &dap.InitializeResponse{Response: *newResponse(request.Request)}
	response.Body.SupportsConfigurationDoneRequest = true
	response.Body.SupportsConditionalBreakpoints = true
	response.Body.SupportsDelayedStackTraceLoading = true
	response.Body.SupportsFunctionBreakpoints = true
	response.Body.SupportsInstructionBreakpoints = true
	response.Body.SupportsExceptionInfoRequest = true
	response.Body.SupportsSetVariable = true
	response.Body.SupportsEvaluateForHovers = true
	response.Body.SupportsClipboardContext = true
	response.Body.SupportsSteppingGranularity = true
	response.Body.SupportsLogPoints = true
	response.Body.SupportsDisassembleRequest = true
	// To be enabled by CapabilitiesEvent based on launch configuration
	response.Body.SupportsStepBack = false
	response.Body.SupportTerminateDebuggee = false
	// TODO(polina): support these requests in addition to vscode-go feature parity
	response.Body.SupportsTerminateRequest = false
	response.Body.SupportsRestartRequest = false
	response.Body.SupportsSetExpression = false
	response.Body.SupportsLoadedSourcesRequest = false
	response.Body.SupportsReadMemoryRequest = false
	response.Body.SupportsCancelRequest = false
	s.send(response)
}

func (s *Session) setClientCapabilities(args dap.InitializeRequestArguments) {
	s.clientCapabilities.supportsMemoryReferences = args.SupportsMemoryReferences
	s.clientCapabilities.supportsProgressReporting = args.SupportsProgressReporting
	s.clientCapabilities.supportsRunInTerminalRequest = args.SupportsRunInTerminalRequest
	s.clientCapabilities.supportsVariablePaging = args.SupportsVariablePaging
	s.clientCapabilities.supportsVariableType = args.SupportsVariableType
}

// Default output file pathname for the compiled binary in debug or test modes.
// This is relative to the current working directory of the server.
const defaultDebugBinary string = "./__debug_bin"

func cleanExeName(name string) string {
	if runtime.GOOS == "windows" && filepath.Ext(name) != ".exe" {
		return name + ".exe"
	}
	return name
}

func (s *Session) onLaunchRequest(request *dap.LaunchRequest) {
	var err error
	if s.debugger != nil {
		s.sendShowUserErrorResponse(request.Request, FailedToLaunch, "Failed to launch",
			fmt.Sprintf("debug session already in progress at %s - use remote attach mode to connect to a server with an active debug session", s.address()))
		return
	}

	var args = defaultLaunchConfig // narrow copy for initializing non-zero default values
	if err := unmarshalLaunchAttachArgs(request.Arguments, &args); err != nil {
		s.sendShowUserErrorResponse(request.Request,
			FailedToLaunch, "Failed to launch", fmt.Sprintf("invalid debug configuration - %v", err))
		return
	}
	s.config.log.Debug("parsed launch config: ", prettyPrint(args))

	if args.DlvCwd != "" {
		if err := os.Chdir(args.DlvCwd); err != nil {
			s.sendShowUserErrorResponse(request.Request,
				FailedToLaunch, "Failed to launch", fmt.Sprintf("failed to chdir to %q - %v", args.DlvCwd, err))
			return
		}
	}

	for k, v := range args.Env {
		if v != nil {
			if err := os.Setenv(k, *v); err != nil {
				s.sendShowUserErrorResponse(request.Request, FailedToLaunch, "Failed to launch", fmt.Sprintf("failed to setenv(%v) - %v", k, err))
				return
			}
		} else {
			if err := os.Unsetenv(k); err != nil {
				s.sendShowUserErrorResponse(request.Request, FailedToLaunch, "Failed to launch", fmt.Sprintf("failed to unsetenv(%v) - %v", k, err))
				return
			}
		}
	}

	if args.Mode == "" {
		args.Mode = "debug"
	}
	if !isValidLaunchMode(args.Mode) {
		s.sendShowUserErrorResponse(request.Request, FailedToLaunch, "Failed to launch",
			fmt.Sprintf("invalid debug configuration - unsupported 'mode' attribute %q", args.Mode))
		return
	}

	if args.Program == "" && args.Mode != "replay" { // Only fail on modes requiring a program
		s.sendShowUserErrorResponse(request.Request, FailedToLaunch, "Failed to launch",
			"The program attribute is missing in debug configuration.")
		return
	}

	if args.Backend == "" {
		args.Backend = "default"
	}

	if args.Mode == "replay" {
		// Validate trace directory
		if args.TraceDirPath == "" {
			s.sendShowUserErrorResponse(request.Request, FailedToLaunch, "Failed to launch",
				"The 'traceDirPath' attribute is missing in debug configuration.")
			return
		}

		// Assign the rr trace directory path to debugger configuration
		s.config.Debugger.CoreFile = args.TraceDirPath
		args.Backend = "rr"
	}
	if args.Mode == "core" {
		// Validate core dump path
		if args.CoreFilePath == "" {
			s.sendShowUserErrorResponse(request.Request, FailedToLaunch, "Failed to launch",
				"The 'coreFilePath' attribute is missing in debug configuration.")
			return
		}
		// Assign the non-empty core file path to debugger configuration. This will
		// trigger a native core file replay instead of an rr trace replay
		s.config.Debugger.CoreFile = args.CoreFilePath
		args.Backend = "core"
	}

	s.config.Debugger.Backend = args.Backend

	// Prepare the debug executable filename, building it if necessary
	debugbinary := args.Program
	if args.Mode == "debug" || args.Mode == "test" {
		if args.Output == "" {
			args.Output = cleanExeName(defaultDebugBinary)
		} else {
			args.Output = cleanExeName(args.Output)
		}
		args.Output, err = filepath.Abs(args.Output)
		if err != nil {
			s.sendShowUserErrorResponse(request.Request, FailedToLaunch, "Failed to launch", err.Error())
			return
		}
		debugbinary = args.Output

		var cmd string
		var out []byte
		var err error
		switch args.Mode {
		case "debug":
			cmd, out, err = gobuild.GoBuildCombinedOutput(args.Output, []string{args.Program}, args.BuildFlags)
		case "test":
			cmd, out, err = gobuild.GoTestBuildCombinedOutput(args.Output, []string{args.Program}, args.BuildFlags)
		}
		args.DlvCwd, _ = filepath.Abs(args.DlvCwd)
		s.config.log.Debugf("building from %q: [%s]", args.DlvCwd, cmd)
		if err != nil {
			s.send(&dap.OutputEvent{
				Event: *newEvent("output"),
				Body: dap.OutputEventBody{
					Output:   fmt.Sprintf("Build Error: %s\n%s (%s)\n", cmd, strings.TrimSpace(string(out)), err.Error()),
					Category: "stderr",
				}})
			// Users are used to checking the Debug Console for build errors.
			// No need to bother them with a visible pop-up.
			s.sendErrorResponse(request.Request, FailedToLaunch, "Failed to launch",
				"Build error: Check the debug console for details.")
			return
		}
		s.mu.Lock()
		s.binaryToRemove = args.Output
		s.mu.Unlock()
	}
	s.config.ProcessArgs = append([]string{debugbinary}, args.Args...)

	if err := s.setLaunchAttachArgs(args.LaunchAttachCommonConfig); err != nil {
		s.sendShowUserErrorResponse(request.Request, FailedToLaunch, "Failed to launch", err.Error())
		return
	}

	if args.Cwd == "" {
		if args.Mode == "test" {
			// In test mode, run the test binary from the package directory
			// like in `go test` and `dlv test` by default.
			args.Cwd = s.getPackageDir(args.Program)
		} else {
			args.Cwd = "."
		}
	}
	s.config.Debugger.WorkingDir = args.Cwd

	// Backend layers will interpret paths relative to server's working directory:
	// reflect that before logging.
	argsToLog := args
	argsToLog.Program, _ = filepath.Abs(args.Program)
	argsToLog.Cwd, _ = filepath.Abs(args.Cwd)
	s.config.log.Debugf("launching binary '%s' with config: %s", debugbinary, prettyPrint(argsToLog))

	if args.NoDebug {
		s.mu.Lock()
		cmd, err := s.newNoDebugProcess(debugbinary, args.Args, s.config.Debugger.WorkingDir)
		s.mu.Unlock()
		if err != nil {
			s.sendShowUserErrorResponse(request.Request, FailedToLaunch, "Failed to launch", err.Error())
			return
		}
		// Skip 'initialized' event, which will prevent the client from sending
		// debug-related requests.
		s.send(&dap.LaunchResponse{Response: *newResponse(request.Request)})

		// Start the program on a different goroutine, so we can listen for disconnect request.
		go func() {
			if err := cmd.Wait(); err != nil {
				s.config.log.Debugf("program exited with error: %v", err)
			}
			close(s.noDebugProcess.exited)
			s.logToConsole(proc.ErrProcessExited{Pid: cmd.ProcessState.Pid(), Status: cmd.ProcessState.ExitCode()}.Error())
			s.send(&dap.TerminatedEvent{Event: *newEvent("terminated")})
		}()
		return
	}

	func() {
		s.mu.Lock()
		defer s.mu.Unlock() // Make sure to unlock in case of panic that will become internal error
		s.debugger, err = debugger.New(&s.config.Debugger, s.config.ProcessArgs)
	}()
	if err != nil {
		s.sendShowUserErrorResponse(request.Request, FailedToLaunch, "Failed to launch", err.Error())
		return
	}
	// Enable StepBack controls on supported backends
	if s.config.Debugger.Backend == "rr" {
		s.send(&dap.CapabilitiesEvent{Event: *newEvent("capabilities"), Body: dap.CapabilitiesEventBody{Capabilities: dap.Capabilities{SupportsStepBack: true}}})
	}

	// Notify the client that the debugger is ready to start accepting
	// configuration requests for setting breakpoints, etc. The client
	// will end the configuration sequence with 'configurationDone'.
	s.send(&dap.InitializedEvent{Event: *newEvent("initialized")})
	s.send(&dap.LaunchResponse{Response: *newResponse(request.Request)})
}

func (s *Session) getPackageDir(pkg string) string {
	cmd := exec.Command("go", "list", "-f", "{{.Dir}}", pkg)
	out, err := cmd.Output()
	if err != nil {
		s.config.log.Debugf("failed to determin package directory for %v: %v\n%s", pkg, err, out)
		return "."
	}
	return string(bytes.TrimSpace(out))
}

// newNoDebugProcess is called from onLaunchRequest (run goroutine) and
// requires holding mu lock. It prepares process exec.Cmd to be started.
func (s *Session) newNoDebugProcess(program string, targetArgs []string, wd string) (*exec.Cmd, error) {
	if s.noDebugProcess != nil {
		return nil, fmt.Errorf("another launch request is in progress")
	}
	cmd := exec.Command(program, targetArgs...)
	cmd.Stdout, cmd.Stderr, cmd.Stdin, cmd.Dir = os.Stdout, os.Stderr, os.Stdin, wd
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	s.noDebugProcess = &process{Cmd: cmd, exited: make(chan struct{})}
	return cmd, nil
}

// stopNoDebugProcess is called from Stop (main goroutine) and
// onDisconnectRequest (run goroutine) and requires holding mu lock.
func (s *Session) stopNoDebugProcess() {
	if s.noDebugProcess == nil {
		// We already handled termination or there was never a process
		return
	}
	select {
	case <-s.noDebugProcess.exited:
		s.noDebugProcess = nil
		return
	default:
	}

	// TODO(hyangah): gracefully terminate the process and its children processes.
	s.logToConsole(fmt.Sprintf("Terminating process %d", s.noDebugProcess.Process.Pid))
	s.noDebugProcess.Process.Kill() // Don't check error. Process killing and self-termination may race.

	// Wait for kill to complete or time out
	select {
	case <-time.After(5 * time.Second):
		s.config.log.Debug("noDebug process kill timed out")
	case <-s.noDebugProcess.exited:
		s.config.log.Debug("noDebug process killed")
		s.noDebugProcess = nil
	}
}

// onDisconnectRequest handles the DisconnectRequest. Per the DAP spec,
// it disconnects the debuggee and signals that the debug adaptor
// (in our case this TCP server) can be terminated.
func (s *Session) onDisconnectRequest(request *dap.DisconnectRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.debugger != nil && s.config.AcceptMulti && !request.Arguments.TerminateDebuggee {
		// This is a multi-use server/debugger, so a disconnect request that doesn't
		// terminate the debuggee should clean up only the client connection and pointer to debugger,
		// but not the entire server.
		status := "halted"
		if s.isRunningCmd() {
			status = "running"
		} else if s, err := s.debugger.State(false); processExited(s, err) {
			status = "exited"
		}
		s.logToConsole(fmt.Sprintf("Closing client session, but leaving multi-client DAP server at %s with debuggee %s", s.config.Listener.Addr().String(), status))
		s.send(&dap.DisconnectResponse{Response: *newResponse(request.Request)})
		s.send(&dap.TerminatedEvent{Event: *newEvent("terminated")})
		s.conn.Close()
		s.debugger = nil
		// The target is left in whatever state it is already in - halted or running.
		// The users therefore have the flexibility to choose the appropriate state
		// for their case before disconnecting. This is also desirable in case of
		// the client connection fails unexpectedly and the user needs to reconnect.
		// TODO(polina): should we always issue a continue here if it is not running
		// like is done in vscode-go legacy adapter?
		// Ideally we want to use bool suspendDebuggee flag, but it is not yet
		// available in vscode: https://github.com/microsoft/vscode/issues/134412
		return
	}

	defer s.config.triggerServerStop()
	var err error
	if s.debugger != nil {
		// We always kill launched programs.
		// In case of attach, we leave the program
		// running by default, which can be
		// overridden by an explicit request to terminate.
		killProcess := s.debugger.AttachPid() == 0 || request.Arguments.TerminateDebuggee
		err = s.stopDebugSession(killProcess)
	} else if s.noDebugProcess != nil {
		s.stopNoDebugProcess()
	}
	if err != nil {
		s.sendErrorResponse(request.Request, DisconnectError, "Error while disconnecting", err.Error())
	} else {
		s.send(&dap.DisconnectResponse{Response: *newResponse(request.Request)})
	}
	// The debugging session has ended, so we send a terminated event.
	s.send(&dap.TerminatedEvent{Event: *newEvent("terminated")})
}

// stopDebugSession is called from Stop (main goroutine) and
// onDisconnectRequest (run goroutine) and requires holding mu lock.
// Returns any detach error other than proc.ErrProcessExited.
func (s *Session) stopDebugSession(killProcess bool) error {
	s.changeStateMu.Lock()
	defer func() {
		// Avoid running stop sequence twice.
		// It's not fatal, but will result in duplicate logging.
		s.debugger = nil
		s.changeStateMu.Unlock()
	}()
	if s.debugger == nil {
		return nil
	}
	var err error
	var exited error
	// Halting will stop any debugger command that's pending on another
	// per-request goroutine. Tell auto-resumer not to resume, so the
	// goroutine can wrap-up and exit.
	s.setHaltRequested(true)
	state, err := s.halt()
	if err == proc.ErrProcessDetached {
		s.config.log.Debug("halt returned error: ", err)
		return nil
	}
	if err != nil {
		switch err.(type) {
		case proc.ErrProcessExited:
			exited = err
		default:
			s.config.log.Error("halt returned error: ", err)
			if err.Error() == "no such process" {
				exited = err
			}
		}
	} else if state.Exited {
		exited = proc.ErrProcessExited{Pid: s.debugger.ProcessPid(), Status: state.ExitStatus}
		s.config.log.Debug("halt returned state: ", exited)
	}
	if exited != nil {
		// TODO(suzmue): log exited error when the process exits, which may have been before
		// halt was called.
		s.logToConsole(exited.Error())
		s.logToConsole("Detaching")
	} else if killProcess {
		s.logToConsole("Detaching and terminating target process")
	} else {
		s.logToConsole("Detaching without terminating target process")
	}
	err = s.debugger.Detach(killProcess)
	if err != nil {
		switch err.(type) {
		case proc.ErrProcessExited:
			s.config.log.Debug(err)
			s.logToConsole(exited.Error())
			err = nil
		default:
			s.config.log.Error("detach returned error: ", err)
		}
	}
	return err
}

// halt sends a halt request if the debuggee is running.
// changeStateMu should be held when calling (*Server).halt.
func (s *Session) halt() (*api.DebuggerState, error) {
	s.config.log.Debug("halting")
	// Only send a halt request if the debuggee is running.
	if s.debugger.IsRunning() {
		return s.debugger.Command(&api.DebuggerCommand{Name: api.Halt}, nil)
	}
	s.config.log.Debug("process not running")
	return s.debugger.State(false)
}

func (s *Session) isNoDebug() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.noDebugProcess != nil
}

func (s *Session) onSetBreakpointsRequest(request *dap.SetBreakpointsRequest) {
	if request.Arguments.Source.Path == "" {
		s.sendErrorResponse(request.Request, UnableToSetBreakpoints, "Unable to set or clear breakpoints", "empty file path")
		return
	}

	clientPath := request.Arguments.Source.Path
	serverPath := s.toServerPath(clientPath)

	// Get all existing breakpoints that match for this source.
	sourceRequestPrefix := fmt.Sprintf("sourceBp Path=%q ", request.Arguments.Source.Path)

	breakpoints := s.setBreakpoints(sourceRequestPrefix, len(request.Arguments.Breakpoints), func(i int) *bpMetadata {
		want := request.Arguments.Breakpoints[i]
		return &bpMetadata{
			name:         fmt.Sprintf("%s Line=%d Column=%d", sourceRequestPrefix, want.Line, want.Column),
			condition:    want.Condition,
			hitCondition: want.HitCondition,
			logMessage:   want.LogMessage,
		}
	}, func(i int) (*bpLocation, error) {
		want := request.Arguments.Breakpoints[i]
		return &bpLocation{
			file: serverPath,
			line: want.Line,
		}, nil
	})

	response := &dap.SetBreakpointsResponse{Response: *newResponse(request.Request)}
	response.Body.Breakpoints = breakpoints

	s.send(response)
}

type bpMetadata struct {
	name         string
	condition    string
	hitCondition string
	logMessage   string
}

type bpLocation struct {
	file  string
	line  int
	addr  uint64
	addrs []uint64
}

// setBreakpoints is a helper function for setting source, function and instruction
// breakpoints. It takes the prefix of the name for all breakpoints that should be
// included, the total number of breakpoints, and functions for computing the metadata
// and the location. The location is computed separately because this may be more
// expensive to compute and may not always be necessary.
func (s *Session) setBreakpoints(prefix string, totalBps int, metadataFunc func(i int) *bpMetadata, locFunc func(i int) (*bpLocation, error)) []dap.Breakpoint {
	// If a breakpoint:
	// -- exists and not in request => ClearBreakpoint
	// -- exists and in request => AmendBreakpoint
	// -- doesn't exist and in request => SetBreakpoint

	// Get all existing breakpoints matching the prefix.
	existingBps := s.getMatchingBreakpoints(prefix)

	// createdBps is a set of breakpoint names that have been added
	// during this request. This is used to catch duplicate set
	// breakpoints requests and to track which breakpoints need to
	// be deleted.
	createdBps := make(map[string]struct{}, len(existingBps))

	breakpoints := make([]dap.Breakpoint, totalBps)
	// Amend existing breakpoints.
	for i := 0; i < totalBps; i++ {
		want := metadataFunc(i)
		got, ok := existingBps[want.name]
		if got == nil || !ok {
			// Skip if the breakpoint does not already exist.
			continue
		}

		var err error
		if _, ok := createdBps[want.name]; ok {
			err = fmt.Errorf("breakpoint already exists")
		} else {
			got.Disabled = false
			got.Cond = want.condition
			got.HitCond = want.hitCondition
			err = setLogMessage(got, want.logMessage)
			if err == nil {
				err = s.debugger.AmendBreakpoint(got)
			}
		}
		createdBps[want.name] = struct{}{}
		s.updateBreakpointsResponse(breakpoints, i, err, got)
	}

	// Clear breakpoints.
	// Any breakpoint that existed before this request but was not amended must be deleted.
	s.clearBreakpoints(existingBps, createdBps)

	// Add new breakpoints.
	for i := 0; i < totalBps; i++ {
		want := metadataFunc(i)
		if _, ok := existingBps[want.name]; ok {
			continue
		}

		var got *api.Breakpoint
		wantLoc, err := locFunc(i)
		if err == nil {
			if _, ok := createdBps[want.name]; ok {
				err = fmt.Errorf("breakpoint already exists")
			} else {
				bp := &api.Breakpoint{
					Name:    want.name,
					File:    wantLoc.file,
					Line:    wantLoc.line,
					Addr:    wantLoc.addr,
					Addrs:   wantLoc.addrs,
					Cond:    want.condition,
					HitCond: want.hitCondition,
				}
				err = setLogMessage(bp, want.logMessage)
				if err == nil {
					// Create new breakpoints.
					got, err = s.debugger.CreateBreakpoint(bp, "", nil, false)
				}
			}
		}
		createdBps[want.name] = struct{}{}
		s.updateBreakpointsResponse(breakpoints, i, err, got)
	}
	return breakpoints
}

func setLogMessage(bp *api.Breakpoint, msg string) error {
	tracepoint, userdata, err := parseLogPoint(msg)
	if err != nil {
		return err
	}
	bp.Tracepoint = tracepoint
	if userdata != nil {
		bp.UserData = *userdata
	}
	return nil
}

func (s *Session) updateBreakpointsResponse(breakpoints []dap.Breakpoint, i int, err error, got *api.Breakpoint) {
	breakpoints[i].Verified = (err == nil)
	if err != nil {
		breakpoints[i].Message = err.Error()
	} else {
		path := s.toClientPath(got.File)
		breakpoints[i].Id = got.ID
		breakpoints[i].Line = got.Line
		breakpoints[i].Source = dap.Source{Name: filepath.Base(path), Path: path}
	}
}

// functionBpPrefix is the prefix of bp.Name for every breakpoint bp set
// in this request.
const functionBpPrefix = "functionBreakpoint"

func (s *Session) onSetFunctionBreakpointsRequest(request *dap.SetFunctionBreakpointsRequest) {
	breakpoints := s.setBreakpoints(functionBpPrefix, len(request.Arguments.Breakpoints), func(i int) *bpMetadata {
		want := request.Arguments.Breakpoints[i]
		return &bpMetadata{
			name:         fmt.Sprintf("%s Name=%s", functionBpPrefix, want.Name),
			condition:    want.Condition,
			hitCondition: want.HitCondition,
			logMessage:   "",
		}
	}, func(i int) (*bpLocation, error) {
		want := request.Arguments.Breakpoints[i]
		// Set the function breakpoint breakpoint
		spec, err := locspec.Parse(want.Name)
		if err != nil {
			return nil, err
		}
		if loc, ok := spec.(*locspec.NormalLocationSpec); !ok || loc.FuncBase == nil {
			// Other locations do not make sense in the context of function breakpoints.
			// Regex locations are likely to resolve to multiple places and offset locations
			// are only meaningful at the time the breakpoint was created.
			return nil, fmt.Errorf("breakpoint name %q could not be parsed as a function. name must be in the format 'funcName', 'funcName:line' or 'fileName:line'", want.Name)
		}

		if want.Name[0] == '.' {
			return nil, fmt.Errorf("breakpoint names that are relative paths are not supported")
		}
		// Find the location of the function name. CreateBreakpoint requires the name to include the base
		// (e.g. main.functionName is supported but not functionName).
		// We first find the location of the function, and then set breakpoints for that location.
		var locs []api.Location
		locs, err = s.debugger.FindLocationSpec(-1, 0, 0, want.Name, spec, true, s.args.substitutePathClientToServer)
		if err != nil {
			return nil, err
		}
		if len(locs) == 0 {
			return nil, err
		}
		if len(locs) > 0 {
			s.config.log.Debugf("multiple locations found for %s", want.Name)
		}

		// Set breakpoint using the PCs that were found.
		loc := locs[0]
		return &bpLocation{addr: loc.PC, addrs: loc.PCs}, nil
	})

	response := &dap.SetFunctionBreakpointsResponse{Response: *newResponse(request.Request)}
	response.Body.Breakpoints = breakpoints

	s.send(response)
}

const instructionBpPrefix = "instructionBreakpoint"

func (s *Session) onSetInstructionBreakpointsRequest(request *dap.SetInstructionBreakpointsRequest) {
	breakpoints := s.setBreakpoints(instructionBpPrefix, len(request.Arguments.Breakpoints), func(i int) *bpMetadata {
		want := request.Arguments.Breakpoints[i]
		return &bpMetadata{
			name:         fmt.Sprintf("%s PC=%s", instructionBpPrefix, want.InstructionReference),
			condition:    want.Condition,
			hitCondition: want.HitCondition,
			logMessage:   "",
		}
	}, func(i int) (*bpLocation, error) {
		want := request.Arguments.Breakpoints[i]
		addr, err := strconv.ParseInt(want.InstructionReference, 0, 64)
		if err != nil {
			return nil, err
		}
		return &bpLocation{addr: uint64(addr)}, nil
	})

	response := &dap.SetInstructionBreakpointsResponse{Response: *newResponse(request.Request)}
	response.Body.Breakpoints = breakpoints
	s.send(response)
}

func (s *Session) clearBreakpoints(existingBps map[string]*api.Breakpoint, amendedBps map[string]struct{}) error {
	for req, bp := range existingBps {
		if _, ok := amendedBps[req]; ok {
			continue
		}
		_, err := s.debugger.ClearBreakpoint(bp)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) getMatchingBreakpoints(prefix string) map[string]*api.Breakpoint {
	existing := s.debugger.Breakpoints(false)
	matchingBps := make(map[string]*api.Breakpoint, len(existing))
	for _, bp := range existing {
		// Skip special breakpoints such as for panic.
		if bp.ID < 0 {
			continue
		}
		// Skip breakpoints that do not meet the condition.
		if !strings.HasPrefix(bp.Name, prefix) {
			continue
		}
		matchingBps[bp.Name] = bp
	}
	return matchingBps
}

func (s *Session) onSetExceptionBreakpointsRequest(request *dap.SetExceptionBreakpointsRequest) {
	// Unlike what DAP documentation claims, this request is always sent
	// even though we specified no filters at initialization. Handle as no-op.
	s.send(&dap.SetExceptionBreakpointsResponse{Response: *newResponse(request.Request)})
}

func closeIfOpen(ch chan struct{}) {
	if ch != nil {
		select {
		case <-ch:
			// already closed
		default:
			close(ch)
		}
	}
}

// onConfigurationDoneRequest handles 'configurationDone' request.
// This is an optional request enabled by capability ‘supportsConfigurationDoneRequest’.
// It gets triggered after all the debug requests that follow initialized event,
// so the s.debugger is guaranteed to be set. Expects the target to be halted.
func (s *Session) onConfigurationDoneRequest(request *dap.ConfigurationDoneRequest, allowNextStateChange chan struct{}) {
	defer closeIfOpen(allowNextStateChange)
	if s.args.stopOnEntry {
		e := &dap.StoppedEvent{
			Event: *newEvent("stopped"),
			Body:  dap.StoppedEventBody{Reason: "entry", ThreadId: 1, AllThreadsStopped: true},
		}
		s.send(e)
	}
	s.debugger.TargetGroup().KeepSteppingBreakpoints = proc.HaltKeepsSteppingBreakpoints | proc.TracepointKeepsSteppingBreakpoints

	s.logToConsole("Type 'dlv help' for list of commands.")
	s.send(&dap.ConfigurationDoneResponse{Response: *newResponse(request.Request)})

	if !s.args.stopOnEntry {
		s.runUntilStopAndNotify(api.Continue, allowNextStateChange)
	}
}

// onContinueRequest handles 'continue' request.
// This is a mandatory request to support.
func (s *Session) onContinueRequest(request *dap.ContinueRequest, allowNextStateChange chan struct{}) {
	s.send(&dap.ContinueResponse{
		Response: *newResponse(request.Request),
		Body:     dap.ContinueResponseBody{AllThreadsContinued: true}})
	s.runUntilStopAndNotify(api.Continue, allowNextStateChange)
}

func fnName(loc *proc.Location) string {
	if loc.Fn == nil {
		return "???"
	}
	return loc.Fn.Name
}

func fnPackageName(loc *proc.Location) string {
	if loc.Fn == nil {
		// attribute unknown functions to the runtime
		return "runtime"
	}
	return loc.Fn.PackageName()
}

// onThreadsRequest handles 'threads' request.
// This is a mandatory request to support.
// It is sent in response to configurationDone response and stopped events.
// Depending on the debug session stage, goroutines information
// might not be available. However, the DAP spec states that
// "even if a debug adapter does not support multiple threads,
// it must implement the threads request and return a single
// (dummy) thread". Therefore, this handler never returns
// an error response. If the dummy thread is returned in its place,
// the next waterfall request for its stackTrace will return the error.
func (s *Session) onThreadsRequest(request *dap.ThreadsRequest) {
	var err error
	var gs []*proc.G
	var next int
	if s.debugger != nil {
		gs, next, err = s.debugger.Goroutines(0, maxGoroutines)
		if err == nil {
			// Parse the goroutine arguments.
			filters, _, _, _, _, _, _, parseErr := api.ParseGoroutineArgs(s.args.GoroutineFilters)
			if parseErr != nil {
				s.logToConsole(parseErr.Error())
			}
			if s.args.HideSystemGoroutines {
				filters = append(filters, api.ListGoroutinesFilter{
					Kind:    api.GoroutineUser,
					Negated: false,
				})
			}
			gs = s.debugger.FilterGoroutines(gs, filters)
		}
	}

	var threads []dap.Thread
	if err != nil {
		switch err.(type) {
		case proc.ErrProcessExited:
			// If the program exits very quickly, the initial threads request will complete after it has exited.
			// A TerminatedEvent has already been sent. Ignore the err returned in this case.
			s.config.log.Debug(err)
		default:
			s.send(&dap.OutputEvent{
				Event: *newEvent("output"),
				Body: dap.OutputEventBody{
					Output:   fmt.Sprintf("Unable to retrieve goroutines: %s\n", err.Error()),
					Category: "stderr",
				}})
		}
		threads = []dap.Thread{{Id: 1, Name: "Dummy"}}
	} else if len(gs) == 0 {
		threads = []dap.Thread{{Id: 1, Name: "Dummy"}}
	} else {
		state, err := s.debugger.State( /*nowait*/ true)
		if err != nil {
			s.config.log.Debug("Unable to get debugger state: ", err)
		}

		if next >= 0 {
			s.logToConsole(fmt.Sprintf("Too many goroutines, only loaded %d", len(gs)))

			// Make sure the selected goroutine is included in the list of threads
			// to return.
			if state != nil && state.SelectedGoroutine != nil {
				var selectedFound bool
				for _, g := range gs {
					if g.ID == state.SelectedGoroutine.ID {
						selectedFound = true
						break
					}
				}
				if !selectedFound {
					g, err := s.debugger.FindGoroutine(state.SelectedGoroutine.ID)
					if err != nil {
						s.config.log.Debug("Error getting selected goroutine: ", err)
					} else {
						// TODO(suzmue): Consider putting the selected goroutine at the top.
						// To be consistent we may want to do this for all threads requests.
						gs = append(gs, g)
					}
				}
			}
		}

		threads = make([]dap.Thread, len(gs))
		s.debugger.LockTarget()
		defer s.debugger.UnlockTarget()

		for i, g := range gs {
			selected := ""
			if state != nil && state.SelectedGoroutine != nil && g.ID == state.SelectedGoroutine.ID {
				selected = "* "
			}
			thread := ""
			if g.Thread != nil && g.Thread.ThreadID() != 0 {
				thread = fmt.Sprintf(" (Thread %d)", g.Thread.ThreadID())
			}
			// File name and line number are communicated via `stackTrace`
			// so no need to include them here.
			loc := g.UserCurrent()
			threads[i].Name = fmt.Sprintf("%s[Go %d] %s%s", selected, g.ID, fnName(&loc), thread)
			threads[i].Id = int(g.ID)
		}
	}

	response := &dap.ThreadsResponse{
		Response: *newResponse(request.Request),
		Body:     dap.ThreadsResponseBody{Threads: threads},
	}
	s.send(response)
}

// onAttachRequest handles 'attach' request.
// This is a mandatory request to support.
// Attach debug sessions support the following modes:
//
//   - [DEFAULT] "local" -- attaches debugger to a local running process.
//     Required args: processID
//   - "remote" -- attaches client to a debugger already attached to a process.
//     Required args: none (host/port are used externally to connect)
func (s *Session) onAttachRequest(request *dap.AttachRequest) {
	var args AttachConfig = defaultAttachConfig // narrow copy for initializing non-zero default values
	if err := unmarshalLaunchAttachArgs(request.Arguments, &args); err != nil {
		s.sendShowUserErrorResponse(request.Request, FailedToAttach, "Failed to attach", fmt.Sprintf("invalid debug configuration - %v", err))
		return
	}
	s.config.log.Debug("parsed launch config: ", prettyPrint(args))

	switch args.Mode {
	case "":
		args.Mode = "local"
		fallthrough
	case "local":
		if s.debugger != nil {
			s.sendShowUserErrorResponse(
				request.Request, FailedToAttach,
				"Failed to attach",
				fmt.Sprintf("debug session already in progress at %s - use remote mode to connect to a server with an active debug session", s.address()))
			return
		}
		if args.ProcessID == 0 {
			s.sendShowUserErrorResponse(request.Request, FailedToAttach, "Failed to attach",
				"The 'processId' attribute is missing in debug configuration")
			return
		}
		s.config.Debugger.AttachPid = args.ProcessID
		if args.Backend == "" {
			args.Backend = "default"
		}
		s.config.Debugger.Backend = args.Backend
		s.config.log.Debugf("attaching to pid %d", args.ProcessID)
		var err error
		func() {
			s.mu.Lock()
			defer s.mu.Unlock() // Make sure to unlock in case of panic that will become internal error
			s.debugger, err = debugger.New(&s.config.Debugger, nil)
		}()
		if err != nil {
			s.sendShowUserErrorResponse(request.Request, FailedToAttach, "Failed to attach", err.Error())
			return
		}
		// Give the user an option to terminate debuggee when client disconnects (default is to leave it)
		s.send(&dap.CapabilitiesEvent{Event: *newEvent("capabilities"), Body: dap.CapabilitiesEventBody{Capabilities: dap.Capabilities{SupportTerminateDebuggee: true}}})
	case "remote":
		if s.debugger == nil {
			s.sendShowUserErrorResponse(request.Request, FailedToAttach, "Failed to attach", "no debugger found")
			return
		}
		s.config.log.Debug("debugger already started")
		// Halt for configuration sequence. onConfigurationDone will restart
		// execution if user requested !stopOnEntry.
		s.changeStateMu.Lock()
		defer s.changeStateMu.Unlock()
		if _, err := s.halt(); err != nil {
			s.sendShowUserErrorResponse(request.Request, FailedToAttach, "Failed to attach", err.Error())
			return
		}
		// Enable StepBack controls on supported backends
		if s.config.Debugger.Backend == "rr" {
			s.send(&dap.CapabilitiesEvent{Event: *newEvent("capabilities"), Body: dap.CapabilitiesEventBody{Capabilities: dap.Capabilities{SupportsStepBack: true}}})
		}
		// Customize termination options for debugger and debuggee
		if s.config.AcceptMulti {
			// User can stop debugger with process or leave it running
			s.send(&dap.CapabilitiesEvent{Event: *newEvent("capabilities"), Body: dap.CapabilitiesEventBody{Capabilities: dap.Capabilities{SupportTerminateDebuggee: true}}})
			// TODO(polina): support SupportSuspendDebuggee when available
		} else if s.config.Debugger.AttachPid > 0 {
			// User can stop debugger with process or leave the processs running
			s.send(&dap.CapabilitiesEvent{Event: *newEvent("capabilities"), Body: dap.CapabilitiesEventBody{Capabilities: dap.Capabilities{SupportTerminateDebuggee: true}}})
		} // else program was launched and the only option will be to stop both
	default:
		s.sendShowUserErrorResponse(request.Request, FailedToAttach, "Failed to attach",
			fmt.Sprintf("invalid debug configuration - unsupported 'mode' attribute %q", args.Mode))
		return
	}

	if err := s.setLaunchAttachArgs(args.LaunchAttachCommonConfig); err != nil {
		s.sendShowUserErrorResponse(request.Request, FailedToAttach, "Failed to attach", err.Error())
		return
	}

	// Notify the client that the debugger is ready to start accepting
	// configuration requests for setting breakpoints, etc. The client
	// will end the configuration sequence with 'configurationDone'.
	s.send(&dap.InitializedEvent{Event: *newEvent("initialized")})
	s.send(&dap.AttachResponse{Response: *newResponse(request.Request)})
}

// onNextRequest handles 'next' request.
// This is a mandatory request to support.
func (s *Session) onNextRequest(request *dap.NextRequest, allowNextStateChange chan struct{}) {
	s.sendStepResponse(request.Arguments.ThreadId, &dap.NextResponse{Response: *newResponse(request.Request)})
	s.stepUntilStopAndNotify(api.Next, request.Arguments.ThreadId, request.Arguments.Granularity, allowNextStateChange)
}

// onStepInRequest handles 'stepIn' request
// This is a mandatory request to support.
func (s *Session) onStepInRequest(request *dap.StepInRequest, allowNextStateChange chan struct{}) {
	s.sendStepResponse(request.Arguments.ThreadId, &dap.StepInResponse{Response: *newResponse(request.Request)})
	s.stepUntilStopAndNotify(api.Step, request.Arguments.ThreadId, request.Arguments.Granularity, allowNextStateChange)
}

// onStepOutRequest handles 'stepOut' request
// This is a mandatory request to support.
func (s *Session) onStepOutRequest(request *dap.StepOutRequest, allowNextStateChange chan struct{}) {
	s.sendStepResponse(request.Arguments.ThreadId, &dap.StepOutResponse{Response: *newResponse(request.Request)})
	s.stepUntilStopAndNotify(api.StepOut, request.Arguments.ThreadId, request.Arguments.Granularity, allowNextStateChange)
}

func (s *Session) sendStepResponse(threadId int, message dap.Message) {
	// All of the threads will be continued by this request, so we need to send
	// a continued event so the UI can properly reflect the current state.
	s.send(&dap.ContinuedEvent{
		Event: *newEvent("continued"),
		Body: dap.ContinuedEventBody{
			ThreadId:            threadId,
			AllThreadsContinued: true,
		},
	})
	s.send(message)
}

func stoppedGoroutineID(state *api.DebuggerState) (id int64) {
	if state.SelectedGoroutine != nil {
		id = state.SelectedGoroutine.ID
	} else if state.CurrentThread != nil {
		id = state.CurrentThread.GoroutineID
	}
	return id
}

// stoppedOnBreakpointGoroutineID gets the goroutine id of the first goroutine
// that is stopped on a real breakpoint, starting with the selected goroutine.
func (s *Session) stoppedOnBreakpointGoroutineID(state *api.DebuggerState) (int64, *api.Breakpoint) {
	// Use the first goroutine that is stopped on a breakpoint.
	gs := s.stoppedGs(state)
	if len(gs) == 0 {
		return 0, nil
	}
	goid := gs[0]
	if goid == 0 {
		return goid, state.CurrentThread.Breakpoint
	}
	g, _ := s.debugger.FindGoroutine(goid)
	if g == nil || g.Thread == nil {
		return goid, nil
	}
	bp := g.Thread.Breakpoint()
	if bp == nil || bp.Breakpoint == nil || bp.Breakpoint.Logical == nil {
		return goid, nil
	}
	abp := api.ConvertLogicalBreakpoint(bp.Breakpoint.Logical)
	api.ConvertPhysicalBreakpoints(abp, []int{0}, []*proc.Breakpoint{bp.Breakpoint})
	return goid, abp
}

// stepUntilStopAndNotify is a wrapper around runUntilStopAndNotify that
// first switches selected goroutine. allowNextStateChange is
// a channel that will be closed to signal that an
// asynchornous command has completed setup or was interrupted
// due to an error, so the server is ready to receive new requests.
func (s *Session) stepUntilStopAndNotify(command string, threadId int, granularity dap.SteppingGranularity, allowNextStateChange chan struct{}) {
	defer closeIfOpen(allowNextStateChange)
	_, err := s.debugger.Command(&api.DebuggerCommand{Name: api.SwitchGoroutine, GoroutineID: int64(threadId)}, nil)
	if err != nil {
		s.config.log.Errorf("Error switching goroutines while stepping: %v", err)
		// If we encounter an error, we will have to send a stopped event
		// since we already sent the step response.
		stopped := &dap.StoppedEvent{Event: *newEvent("stopped")}
		stopped.Body.AllThreadsStopped = true
		if state, err := s.debugger.State(false); err != nil {
			s.config.log.Errorf("Error retrieving state: %e", err)
		} else {
			stopped.Body.ThreadId = int(stoppedGoroutineID(state))
		}
		stopped.Body.Reason = "error"
		stopped.Body.Text = err.Error()
		s.send(stopped)
		return
	}

	if granularity == "instruction" {
		switch command {
		case api.ReverseNext:
			command = api.ReverseStepInstruction
		default:
			// TODO(suzmue): consider differentiating between next, step in, and step out.
			// For example, next could step over call requests.
			command = api.StepInstruction
		}
	}
	s.runUntilStopAndNotify(command, allowNextStateChange)
}

// onPauseRequest handles 'pause' request.
// This is a mandatory request to support.
func (s *Session) onPauseRequest(request *dap.PauseRequest) {
	s.changeStateMu.Lock()
	defer s.changeStateMu.Unlock()
	s.setHaltRequested(true)
	_, err := s.halt()
	if err != nil {
		s.sendErrorResponse(request.Request, UnableToHalt, "Unable to halt execution", err.Error())
		return
	}
	s.send(&dap.PauseResponse{Response: *newResponse(request.Request)})
	// No need to send any event here.
	// If we received this request while stopped, there already was an event for the stop.
	// If we received this while running, then doCommand will unblock and trigger the right
	// event, using debugger.StopReason because manual stop reason always wins even if we
	// simultaneously receive a manual stop request and hit a breakpoint.
}

// stackFrame represents the index of a frame within
// the context of a stack of a specific goroutine.
type stackFrame struct {
	goroutineID int
	frameIndex  int
}

// onStackTraceRequest handles ‘stackTrace’ requests.
// This is a mandatory request to support.
// As per DAP spec, this request only gets triggered as a follow-up
// to a successful threads request as part of the "request waterfall".
func (s *Session) onStackTraceRequest(request *dap.StackTraceRequest) {
	if s.debugger == nil {
		s.sendErrorResponse(request.Request, UnableToProduceStackTrace, "Unable to produce stack trace", "debugger is nil")
		return
	}

	goroutineID := request.Arguments.ThreadId
	start := request.Arguments.StartFrame
	if start < 0 {
		start = 0
	}
	levels := s.args.StackTraceDepth
	if request.Arguments.Levels > 0 {
		levels = request.Arguments.Levels
	}

	// Since the backend doesn't support paging, we load all frames up to
	// the requested depth and then slice them here per
	// `supportsDelayedStackTraceLoading` capability.
	frames, err := s.debugger.Stacktrace(int64(goroutineID), start+levels-1, 0)
	if err != nil {
		s.sendErrorResponse(request.Request, UnableToProduceStackTrace, "Unable to produce stack trace", err.Error())
		return
	}

	// Determine if the goroutine is a system goroutine.
	isSystemGoroutine := true
	if g, _ := s.debugger.FindGoroutine(int64(goroutineID)); g != nil {
		isSystemGoroutine = g.System(s.debugger.Target())
	}

	stackFrames := []dap.StackFrame{} // initialize to empty, since nil is not an accepted response.
	for i := 0; i < levels && i+start < len(frames); i++ {
		frame := frames[start+i]
		loc := &frame.Call
		uniqueStackFrameID := s.stackFrameHandles.create(stackFrame{goroutineID, start + i})
		stackFrame := dap.StackFrame{Id: uniqueStackFrameID, Line: loc.Line, Name: fnName(loc), InstructionPointerReference: fmt.Sprintf("%#x", loc.PC)}
		if loc.File != "<autogenerated>" {
			clientPath := s.toClientPath(loc.File)
			stackFrame.Source = dap.Source{Name: filepath.Base(clientPath), Path: clientPath}
		}
		stackFrame.Column = 0

		packageName := fnPackageName(loc)
		if !isSystemGoroutine && packageName == "runtime" {
			stackFrame.PresentationHint = "subtle"
		}
		stackFrames = append(stackFrames, stackFrame)
	}

	totalFrames := len(frames)
	if len(frames) >= start+levels && !frames[len(frames)-1].Bottom {
		// We don't know the exact number of available stack frames, so
		// add an arbitrary number so the client knows to request additional
		// frames.
		totalFrames += s.args.StackTraceDepth
	}
	response := &dap.StackTraceResponse{
		Response: *newResponse(request.Request),
		Body:     dap.StackTraceResponseBody{StackFrames: stackFrames, TotalFrames: totalFrames},
	}
	s.send(response)
}

// onScopesRequest handles 'scopes' requests.
// This is a mandatory request to support.
// It is automatically sent as part of the threads > stacktrace > scopes > variables
// "waterfall" to highlight the topmost frame at stops, after an evaluate request
// for the selected scope or when a user selects different scopes in the UI.
func (s *Session) onScopesRequest(request *dap.ScopesRequest) {
	sf, ok := s.stackFrameHandles.get(request.Arguments.FrameId)
	if !ok {
		s.sendErrorResponse(request.Request, UnableToListLocals, "Unable to list locals", fmt.Sprintf("unknown frame id %d", request.Arguments.FrameId))
		return
	}

	goid := sf.(stackFrame).goroutineID
	frame := sf.(stackFrame).frameIndex

	// Check if the function is optimized.
	fn, err := s.debugger.Function(int64(goid), frame, 0, DefaultLoadConfig)
	if fn == nil || err != nil {
		var details string
		if err != nil {
			details = err.Error()
		}
		s.sendErrorResponse(request.Request, UnableToListArgs, "Unable to find enclosing function", details)
		return
	}
	suffix := ""
	if fn.Optimized() {
		suffix = " (warning: optimized function)"
	}
	// Retrieve arguments
	args, err := s.debugger.FunctionArguments(int64(goid), frame, 0, DefaultLoadConfig)
	if err != nil {
		s.sendErrorResponse(request.Request, UnableToListArgs, "Unable to list args", err.Error())
		return
	}

	// Retrieve local variables
	locals, err := s.debugger.LocalVariables(int64(goid), frame, 0, DefaultLoadConfig)
	if err != nil {
		s.sendErrorResponse(request.Request, UnableToListLocals, "Unable to list locals", err.Error())
		return
	}
	locScope := &fullyQualifiedVariable{&proc.Variable{Name: fmt.Sprintf("Locals%s", suffix), Children: slicePtrVarToSliceVar(append(args, locals...))}, "", true, 0}
	scopeLocals := dap.Scope{Name: locScope.Name, VariablesReference: s.variableHandles.create(locScope)}
	scopes := []dap.Scope{scopeLocals}

	if s.args.ShowGlobalVariables {
		// Limit what global variables we will return to the current package only.
		// TODO(polina): This is how vscode-go currently does it to make
		// the amount of the returned data manageable. In fact, this is
		// considered so expensive even with the package filter, that
		// the default for showGlobalVariables was recently flipped to
		// not showing. If we delay loading of the globals until the corresponding
		// scope is expanded, generating an explicit variable request,
		// should we consider making all globals accessible with a scope per package?
		// Or users can just rely on watch variables.
		currPkg, err := s.debugger.CurrentPackage()
		if err != nil {
			s.sendErrorResponse(request.Request, UnableToListGlobals, "Unable to list globals", err.Error())
			return
		}
		currPkgFilter := fmt.Sprintf("^%s\\.", currPkg)
		globals, err := s.debugger.PackageVariables(currPkgFilter, DefaultLoadConfig)
		if err != nil {
			s.sendErrorResponse(request.Request, UnableToListGlobals, "Unable to list globals", err.Error())
			return
		}
		// Remove package prefix from the fully-qualified variable names.
		// We will include the package info once in the name of the scope instead.
		for i, g := range globals {
			globals[i].Name = strings.TrimPrefix(g.Name, currPkg+".")
		}

		globScope := &fullyQualifiedVariable{&proc.Variable{
			Name:     fmt.Sprintf("Globals (package %s)", currPkg),
			Children: slicePtrVarToSliceVar(globals),
		}, currPkg, true, 0}
		scopeGlobals := dap.Scope{Name: globScope.Name, VariablesReference: s.variableHandles.create(globScope)}
		scopes = append(scopes, scopeGlobals)
	}

	if s.args.ShowRegisters {
		// Retrieve registers
		regs, err := s.debugger.ScopeRegisters(int64(goid), frame, 0, false)
		if err != nil {
			s.sendErrorResponse(request.Request, UnableToListRegisters, "Unable to list registers", err.Error())
			return
		}
		outRegs := api.ConvertRegisters(regs, s.debugger.DwarfRegisterToString, false)
		regsVar := make([]proc.Variable, len(outRegs))
		for i, r := range outRegs {
			regsVar[i] = proc.Variable{
				Name:  r.Name,
				Value: constant.MakeString(r.Value),
				Kind:  reflect.Kind(proc.VariableConstant),
			}
		}
		regsScope := &fullyQualifiedVariable{&proc.Variable{Name: "Registers", Children: regsVar}, "", true, 0}
		scopeRegisters := dap.Scope{Name: regsScope.Name, VariablesReference: s.variableHandles.create(regsScope)}
		scopes = append(scopes, scopeRegisters)
	}
	response := &dap.ScopesResponse{
		Response: *newResponse(request.Request),
		Body:     dap.ScopesResponseBody{Scopes: scopes},
	}
	s.send(response)
}

func slicePtrVarToSliceVar(vars []*proc.Variable) []proc.Variable {
	r := make([]proc.Variable, len(vars))
	for i := range vars {
		r[i] = *vars[i]
	}
	return r
}

// onVariablesRequest handles 'variables' requests.
// This is a mandatory request to support.
func (s *Session) onVariablesRequest(request *dap.VariablesRequest) {
	ref := request.Arguments.VariablesReference
	v, ok := s.variableHandles.get(ref)
	if !ok {
		s.sendErrorResponse(request.Request, UnableToLookupVariable, "Unable to lookup variable", fmt.Sprintf("unknown reference %d", ref))
		return
	}

	// If there is a filter applied, we will need to create a new variable that includes
	// the values actually needed to load. This cannot be done when loading the parent
	// node, since it is unknown at that point which children will need to be loaded.
	if request.Arguments.Filter == "indexed" {
		var err error
		v, err = s.maybeLoadResliced(v, request.Arguments.Start, request.Arguments.Count)
		if err != nil {
			s.sendErrorResponse(request.Request, UnableToLookupVariable, "Unable to lookup variable", err.Error())
			return
		}
	}

	children := []dap.Variable{} // must return empty array, not null, if no children
	if request.Arguments.Filter == "named" || request.Arguments.Filter == "" {
		named, err := s.metadataToDAPVariables(v)
		if err != nil {
			s.sendErrorResponse(request.Request, UnableToLookupVariable, "Unable to lookup variable", err.Error())
			return
		}
		children = append(children, named...)
	}
	if request.Arguments.Filter == "indexed" || request.Arguments.Filter == "" {
		indexed, err := s.childrenToDAPVariables(v)
		if err != nil {
			s.sendErrorResponse(request.Request, UnableToLookupVariable, "Unable to lookup variable", err.Error())
			return
		}
		children = append(children, indexed...)
	}
	response := &dap.VariablesResponse{
		Response: *newResponse(request.Request),
		Body:     dap.VariablesResponseBody{Variables: children},
	}
	s.send(response)
}

func (s *Session) maybeLoadResliced(v *fullyQualifiedVariable, start, count int) (*fullyQualifiedVariable, error) {
	if start == 0 {
		want := count
		if v.Kind == reflect.Map {
			// For maps, we need to have 2*count children since each key-value pair is two variables.
			want *= 2
		}
		if want == len(v.Children) {
			// If we have already loaded the correct children,
			// just return the variable.
			return v, nil
		}
	}
	indexedLoadConfig := DefaultLoadConfig
	indexedLoadConfig.MaxArrayValues = count
	newV, err := s.debugger.LoadResliced(v.Variable, start, indexedLoadConfig)
	if err != nil {
		return nil, err
	}
	return &fullyQualifiedVariable{newV, v.fullyQualifiedNameOrExpr, false, start}, nil
}

// getIndexedVariableCount returns the number of indexed variables
// for a DAP variable. For maps this may be less than the actual
// number of children returned, since a key-value pair may be split
// into two separate children.
func getIndexedVariableCount(v *proc.Variable) int {
	indexedVars := 0
	switch v.Kind {
	case reflect.Array, reflect.Slice, reflect.Map:
		indexedVars = int(v.Len)
	}
	return indexedVars
}

// childrenToDAPVariables returns the DAP presentation of the referenced variable's children.
func (s *Session) childrenToDAPVariables(v *fullyQualifiedVariable) ([]dap.Variable, error) {
	// TODO(polina): consider convertVariableToString instead of convertVariable
	// and avoid unnecessary creation of variable handles when this is called to
	// compute evaluate names when this is called from onSetVariableRequest.
	children := []dap.Variable{} // must return empty array, not null, if no children

	switch v.Kind {
	case reflect.Map:
		for i := 0; i < len(v.Children); i += 2 {
			// A map will have twice as many children as there are key-value elements.
			kvIndex := i / 2
			// Process children in pairs: even indices are map keys, odd indices are values.
			keyv, valv := &v.Children[i], &v.Children[i+1]
			keyexpr := fmt.Sprintf("(*(*%q)(%#x))", keyv.TypeString(), keyv.Addr)
			valexpr := fmt.Sprintf("%s[%s]", v.fullyQualifiedNameOrExpr, keyexpr)
			switch keyv.Kind {
			// For value expression, use the key value, not the corresponding expression if the key is a scalar.
			case reflect.Bool, reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128,
				reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
				reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
				valexpr = fmt.Sprintf("%s[%s]", v.fullyQualifiedNameOrExpr, api.VariableValueAsString(keyv))
			case reflect.String:
				if key := constant.StringVal(keyv.Value); keyv.Len == int64(len(key)) { // fully loaded
					valexpr = fmt.Sprintf("%s[%q]", v.fullyQualifiedNameOrExpr, key)
				}
			}
			key, keyref := s.convertVariable(keyv, keyexpr)
			val, valref := s.convertVariable(valv, valexpr)
			keyType := s.getTypeIfSupported(keyv)
			valType := s.getTypeIfSupported(valv)
			// If key or value or both are scalars, we can use
			// a single variable to represent key:value format.
			// Otherwise, we must return separate variables for both.
			if keyref > 0 && valref > 0 { // Both are not scalars
				keyvar := dap.Variable{
					Name:               fmt.Sprintf("[key %d]", v.startIndex+kvIndex),
					EvaluateName:       keyexpr,
					Type:               keyType,
					Value:              key,
					VariablesReference: keyref,
					IndexedVariables:   getIndexedVariableCount(keyv),
					NamedVariables:     getNamedVariableCount(keyv),
				}
				valvar := dap.Variable{
					Name:               fmt.Sprintf("[val %d]", v.startIndex+kvIndex),
					EvaluateName:       valexpr,
					Type:               valType,
					Value:              val,
					VariablesReference: valref,
					IndexedVariables:   getIndexedVariableCount(valv),
					NamedVariables:     getNamedVariableCount(valv),
				}
				children = append(children, keyvar, valvar)
			} else { // At least one is a scalar
				keyValType := valType
				if len(keyType) > 0 && len(valType) > 0 {
					keyValType = fmt.Sprintf("%s: %s", keyType, valType)
				}
				kvvar := dap.Variable{
					Name:         key,
					EvaluateName: valexpr,
					Type:         keyValType,
					Value:        val,
				}
				if keyref != 0 { // key is a type to be expanded
					if len(key) > maxMapKeyValueLen {
						// Truncate and make unique
						kvvar.Name = fmt.Sprintf("%s... @ %#x", key[0:maxMapKeyValueLen], keyv.Addr)
					}
					kvvar.VariablesReference = keyref
					kvvar.IndexedVariables = getIndexedVariableCount(keyv)
					kvvar.NamedVariables = getNamedVariableCount(keyv)
				} else if valref != 0 { // val is a type to be expanded
					kvvar.VariablesReference = valref
					kvvar.IndexedVariables = getIndexedVariableCount(valv)
					kvvar.NamedVariables = getNamedVariableCount(valv)
				}
				children = append(children, kvvar)
			}
		}
	case reflect.Slice, reflect.Array:
		children = make([]dap.Variable, len(v.Children))
		for i := range v.Children {
			idx := v.startIndex + i
			cfqname := fmt.Sprintf("%s[%d]", v.fullyQualifiedNameOrExpr, idx)
			cvalue, cvarref := s.convertVariable(&v.Children[i], cfqname)
			children[i] = dap.Variable{
				Name:               fmt.Sprintf("[%d]", idx),
				EvaluateName:       cfqname,
				Type:               s.getTypeIfSupported(&v.Children[i]),
				Value:              cvalue,
				VariablesReference: cvarref,
				IndexedVariables:   getIndexedVariableCount(&v.Children[i]),
				NamedVariables:     getNamedVariableCount(&v.Children[i]),
			}
		}
	default:
		children = make([]dap.Variable, len(v.Children))
		for i := range v.Children {
			c := &v.Children[i]
			cfqname := fmt.Sprintf("%s.%s", v.fullyQualifiedNameOrExpr, c.Name)

			if strings.HasPrefix(c.Name, "~") || strings.HasPrefix(c.Name, ".") {
				cfqname = ""
			} else if v.isScope && v.fullyQualifiedNameOrExpr == "" {
				cfqname = c.Name
			} else if v.fullyQualifiedNameOrExpr == "" {
				cfqname = ""
			} else if v.Kind == reflect.Interface {
				cfqname = fmt.Sprintf("%s.(%s)", v.fullyQualifiedNameOrExpr, c.Name) // c is data
			} else if v.Kind == reflect.Ptr {
				cfqname = fmt.Sprintf("(*%v)", v.fullyQualifiedNameOrExpr) // c is the nameless pointer value
			} else if v.Kind == reflect.Complex64 || v.Kind == reflect.Complex128 {
				cfqname = "" // complex children are not struct fields and can't be accessed directly
			}
			cvalue, cvarref := s.convertVariable(c, cfqname)

			// Annotate any shadowed variables to "(name)" in order
			// to distinguish from non-shadowed variables.
			// TODO(suzmue): should we support a special evaluateName syntax that
			// can access shadowed variables?
			name := c.Name
			if c.Flags&proc.VariableShadowed == proc.VariableShadowed {
				name = fmt.Sprintf("(%s)", name)
			}

			if v.isScope && v.Name == "Registers" {
				// Align all of the register names.
				name = fmt.Sprintf("%6s", strings.ToLower(c.Name))
				// Set the correct evaluate name for the register.
				cfqname = fmt.Sprintf("_%s", strings.ToUpper(c.Name))
				// Unquote the value
				if ucvalue, err := strconv.Unquote(cvalue); err == nil {
					cvalue = ucvalue
				}
			}

			children[i] = dap.Variable{
				Name:               name,
				EvaluateName:       cfqname,
				Type:               s.getTypeIfSupported(c),
				Value:              cvalue,
				VariablesReference: cvarref,
				IndexedVariables:   getIndexedVariableCount(c),
				NamedVariables:     getNamedVariableCount(c),
			}
		}
	}
	return children, nil
}

func getNamedVariableCount(v *proc.Variable) int {
	namedVars := 0
	if v.Kind == reflect.Map && v.Len > 0 {
		// len
		namedVars += 1
	}
	if isListOfBytesOrRunes(v) {
		// string value of array/slice of bytes and runes.
		namedVars += 1
	}

	return namedVars
}

// metadataToDAPVariables returns the DAP presentation of the referenced variable's metadata.
// These are included as named variables
func (s *Session) metadataToDAPVariables(v *fullyQualifiedVariable) ([]dap.Variable, error) {
	children := []dap.Variable{} // must return empty array, not null, if no children

	if v.Kind == reflect.Map && v.Len > 0 {
		children = append(children, dap.Variable{
			Name:         "len()",
			Value:        fmt.Sprintf("%d", v.Len),
			Type:         "int",
			EvaluateName: fmt.Sprintf("len(%s)", v.fullyQualifiedNameOrExpr),
		})
	}

	if isListOfBytesOrRunes(v.Variable) {
		// Return the string value of []byte or []rune.
		typeName := api.PrettyTypeName(v.DwarfType)
		loadExpr := fmt.Sprintf("string(*(*%q)(%#x))", typeName, v.Addr)

		s.config.log.Debugf("loading %s (type %s) with %s", v.fullyQualifiedNameOrExpr, typeName, loadExpr)
		// We know that this is an array/slice of Uint8 or Int32, so we will load up to MaxStringLen.
		config := DefaultLoadConfig
		config.MaxArrayValues = config.MaxStringLen
		vLoaded, err := s.debugger.EvalVariableInScope(-1, 0, 0, loadExpr, config)
		if err == nil {
			val := s.convertVariableToString(vLoaded)
			// TODO(suzmue): Add evaluate name. Using string(name) will not get the same result because the
			// MaxArrayValues is not auto adjusted in evaluate requests like MaxStringLen is adjusted.
			children = append(children, dap.Variable{
				Name:  "string()",
				Value: val,
				Type:  "string",
			})
		} else {
			s.config.log.Debugf("failed to load %q: %v", v.fullyQualifiedNameOrExpr, err)
		}
	}
	return children, nil
}

func isListOfBytesOrRunes(v *proc.Variable) bool {
	if len(v.Children) > 0 && (v.Kind == reflect.Array || v.Kind == reflect.Slice) {
		childKind := v.Children[0].RealType.Common().ReflectKind
		return childKind == reflect.Uint8 || childKind == reflect.Int32
	}
	return false
}

func (s *Session) getTypeIfSupported(v *proc.Variable) string {
	if !s.clientCapabilities.supportsVariableType {
		return ""
	}
	return v.TypeString()
}

// convertVariable converts proc.Variable to dap.Variable value and reference
// while keeping track of the full qualified name or load expression.
// Variable reference is used to keep track of the children associated with each
// variable. It is shared with the host via scopes or evaluate response and is an index
// into the s.variableHandles map, used to look up variables and their children on
// subsequent variables requests. A positive reference signals the host that another
// variables request can be issued to get the elements of the compound variable. As a
// custom, a zero reference, reminiscent of a zero pointer, is used to indicate that
// a scalar variable cannot be "dereferenced" to get its elements (as there are none).
func (s *Session) convertVariable(v *proc.Variable, qualifiedNameOrExpr string) (value string, variablesReference int) {
	return s.convertVariableWithOpts(v, qualifiedNameOrExpr, 0)
}

func (s *Session) convertVariableToString(v *proc.Variable) string {
	val, _ := s.convertVariableWithOpts(v, "", skipRef)
	return val
}

const (
	// Limit the length of a string representation of a compound or reference type variable.
	maxVarValueLen = 1 << 8 // 256
	// Limit the length of an inlined map key.
	maxMapKeyValueLen = 64
)

// Flags for convertVariableWithOpts option.
type convertVariableFlags uint8

const (
	skipRef convertVariableFlags = 1 << iota
	showFullValue
)

// convertVariableWithOpts allows to skip reference generation in case all we need is
// a string representation of the variable. When the variable is a compound or reference
// type variable and its full string representation can be larger than defaultMaxValueLen,
// this returns a truncated value unless showFull option flag is set.
func (s *Session) convertVariableWithOpts(v *proc.Variable, qualifiedNameOrExpr string, opts convertVariableFlags) (value string, variablesReference int) {
	canHaveRef := false
	maybeCreateVariableHandle := func(v *proc.Variable) int {
		canHaveRef = true
		if opts&skipRef != 0 {
			return 0
		}
		return s.variableHandles.create(&fullyQualifiedVariable{v, qualifiedNameOrExpr, false /*not a scope*/, 0})
	}
	value = api.ConvertVar(v).SinglelineString()
	if v.Unreadable != nil {
		return value, 0
	}

	// Some of the types might be fully or partially not loaded based on LoadConfig.
	// Those that are fully missing (e.g. due to hitting MaxVariableRecurse), can be reloaded in place.
	var reloadVariable = func(v *proc.Variable, qualifiedNameOrExpr string) (value string) {
		// We might be loading variables from the frame that's not topmost, so use
		// frame-independent address-based expression, not fully-qualified name as per
		// https://github.com/go-delve/delve/blob/master/Documentation/api/ClientHowto.md#looking-into-variables.
		// TODO(polina): Get *proc.Variable object from debugger instead. Export a function to set v.loaded to false
		// and call v.loadValue gain with a different load config. It's more efficient, and it's guaranteed to keep
		// working with generics.
		value = api.ConvertVar(v).SinglelineString()
		typeName := api.PrettyTypeName(v.DwarfType)
		loadExpr := fmt.Sprintf("*(*%q)(%#x)", typeName, v.Addr)
		s.config.log.Debugf("loading %s (type %s) with %s", qualifiedNameOrExpr, typeName, loadExpr)
		// Make sure we can load the pointers directly, not by updating just the child
		// This is not really necessary now because users have no way of setting FollowPointers to false.
		config := DefaultLoadConfig
		config.FollowPointers = true
		vLoaded, err := s.debugger.EvalVariableInScope(-1, 0, 0, loadExpr, config)
		if err != nil {
			value += fmt.Sprintf(" - FAILED TO LOAD: %s", err)
		} else {
			v.Children = vLoaded.Children
			value = api.ConvertVar(v).SinglelineString()
		}
		return value
	}

	switch v.Kind {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		n, _ := strconv.ParseUint(api.ConvertVar(v).Value, 10, 64)
		value = fmt.Sprintf("%s = %#x", value, n)
	case reflect.UnsafePointer:
		// Skip child reference
	case reflect.Ptr:
		if v.DwarfType != nil && len(v.Children) > 0 && v.Children[0].Addr != 0 && v.Children[0].Kind != reflect.Invalid {
			if v.Children[0].OnlyAddr { // Not loaded
				if v.Addr == 0 {
					// This is equvalent to the following with the cli:
					//    (dlv) p &a7
					//    (**main.FooBar)(0xc0000a3918)
					//
					// TODO(polina): what is more appropriate?
					// Option 1: leave it unloaded because it is a special case
					// Option 2: load it, but then we have to load the child, not the parent, unlike all others
					// TODO(polina): see if reloadVariable can be reused here
					cTypeName := api.PrettyTypeName(v.Children[0].DwarfType)
					cLoadExpr := fmt.Sprintf("*(*%q)(%#x)", cTypeName, v.Children[0].Addr)
					s.config.log.Debugf("loading *(%s) (type %s) with %s", qualifiedNameOrExpr, cTypeName, cLoadExpr)
					cLoaded, err := s.debugger.EvalVariableInScope(-1, 0, 0, cLoadExpr, DefaultLoadConfig)
					if err != nil {
						value += fmt.Sprintf(" - FAILED TO LOAD: %s", err)
					} else {
						cLoaded.Name = v.Children[0].Name // otherwise, this will be the pointer expression
						v.Children = []proc.Variable{*cLoaded}
						value = api.ConvertVar(v).SinglelineString()
					}
				} else {
					value = reloadVariable(v, qualifiedNameOrExpr)
				}
			}
			if !v.Children[0].OnlyAddr {
				variablesReference = maybeCreateVariableHandle(v)
			}
		}
	case reflect.Slice, reflect.Array:
		if v.Len > int64(len(v.Children)) { // Not fully loaded
			if v.Base != 0 && len(v.Children) == 0 { // Fully missing
				value = reloadVariable(v, qualifiedNameOrExpr)
			} else if !s.clientCapabilities.supportsVariablePaging {
				value = fmt.Sprintf("(loaded %d/%d) ", len(v.Children), v.Len) + value
			}
		}
		if v.Base != 0 && len(v.Children) > 0 {
			variablesReference = maybeCreateVariableHandle(v)
		}
	case reflect.Map:
		if v.Len > int64(len(v.Children)/2) { // Not fully loaded
			if len(v.Children) == 0 { // Fully missing
				value = reloadVariable(v, qualifiedNameOrExpr)
			} else if !s.clientCapabilities.supportsVariablePaging {
				value = fmt.Sprintf("(loaded %d/%d) ", len(v.Children)/2, v.Len) + value
			}
		}
		if v.Base != 0 && len(v.Children) > 0 {
			variablesReference = maybeCreateVariableHandle(v)
		}
	case reflect.String:
		// TODO(polina): implement auto-loading here.
	case reflect.Interface:
		if v.Addr != 0 && len(v.Children) > 0 && v.Children[0].Kind != reflect.Invalid && v.Children[0].Addr != 0 {
			if v.Children[0].OnlyAddr { // Not loaded
				value = reloadVariable(v, qualifiedNameOrExpr)
			}
			if !v.Children[0].OnlyAddr {
				variablesReference = maybeCreateVariableHandle(v)
			}
		}
	case reflect.Struct:
		if v.Len > int64(len(v.Children)) { // Not fully loaded
			if len(v.Children) == 0 { // Fully missing
				value = reloadVariable(v, qualifiedNameOrExpr)
			} else { // Partially missing (TODO)
				value = fmt.Sprintf("(loaded %d/%d) ", len(v.Children), v.Len) + value
			}
		}
		if len(v.Children) > 0 {
			variablesReference = maybeCreateVariableHandle(v)
		}
	case reflect.Complex64, reflect.Complex128:
		v.Children = make([]proc.Variable, 2)
		v.Children[0].Name = "real"
		v.Children[0].Value = constant.Real(v.Value)
		v.Children[1].Name = "imaginary"
		v.Children[1].Value = constant.Imag(v.Value)
		if v.Kind == reflect.Complex64 {
			v.Children[0].Kind = reflect.Float32
			v.Children[1].Kind = reflect.Float32
		} else {
			v.Children[0].Kind = reflect.Float64
			v.Children[1].Kind = reflect.Float64
		}
		fallthrough
	default: // Complex, Scalar, Chan, Func
		if len(v.Children) > 0 {
			variablesReference = maybeCreateVariableHandle(v)
		}
	}

	// By default, only values of variables that have children can be truncated.
	// If showFullValue is set, then all value strings are not truncated.
	canTruncateValue := showFullValue&opts == 0
	if len(value) > maxVarValueLen && canTruncateValue && canHaveRef {
		value = value[:maxVarValueLen] + "..."
	}
	return value, variablesReference
}

// onEvaluateRequest handles 'evalute' requests.
// This is a mandatory request to support.
// Support the following expressions:
//
//   - {expression} - evaluates the expression and returns the result as a variable
//   - call {function} - injects a function call and returns the result as a variable
//   - config {expression} - updates configuration parameters
//
// TODO(polina): users have complained about having to click to expand multi-level
// variables, so consider also adding the following:
//
//   - print {expression} - return the result as a string like from dlv cli
func (s *Session) onEvaluateRequest(request *dap.EvaluateRequest) {
	showErrorToUser := request.Arguments.Context != "watch" && request.Arguments.Context != "repl" && request.Arguments.Context != "hover"
	if s.debugger == nil {
		s.sendErrorResponseWithOpts(request.Request, UnableToEvaluateExpression, "Unable to evaluate expression", "debugger is nil", showErrorToUser)
		return
	}

	// Default to the topmost stack frame of the current goroutine in case
	// no frame is specified (e.g. when stopped on entry or no call stack frame is expanded)
	goid, frame := -1, 0
	if sf, ok := s.stackFrameHandles.get(request.Arguments.FrameId); ok {
		goid = sf.(stackFrame).goroutineID
		frame = sf.(stackFrame).frameIndex
	}

	response := &dap.EvaluateResponse{Response: *newResponse(request.Request)}
	expr := request.Arguments.Expression

	if isConfig, err := regexp.MatchString(`^\s*dlv\s+\S+`, expr); err == nil && isConfig { // dlv {command}
		expr := strings.Replace(expr, "dlv ", "", 1)
		result, err := s.delveCmd(goid, frame, expr)
		if err != nil {
			s.sendErrorResponseWithOpts(request.Request, UnableToRunDlvCommand, "Unable to run dlv command", err.Error(), showErrorToUser)
			return
		}
		response.Body = dap.EvaluateResponseBody{
			Result: result,
		}
	} else if isCall, err := regexp.MatchString(`^\s*call\s+\S+`, expr); err == nil && isCall { // call {expression}
		expr := strings.Replace(expr, "call ", "", 1)
		_, retVars, err := s.doCall(goid, frame, expr)
		if err != nil {
			s.sendErrorResponseWithOpts(request.Request, UnableToEvaluateExpression, "Unable to evaluate expression", err.Error(), showErrorToUser)
			return
		}
		// The call completed and we can reply with its return values (if any)
		if len(retVars) > 0 {
			// Package one or more return values in a single scope-like nameless variable
			// that preserves their names.
			retVarsAsVar := &proc.Variable{Children: slicePtrVarToSliceVar(retVars)}
			// As a shortcut also express the return values as a single string.
			retVarsAsStr := ""
			for _, v := range retVars {
				retVarsAsStr += s.convertVariableToString(v) + ", "
			}
			response.Body = dap.EvaluateResponseBody{
				Result:             strings.TrimRight(retVarsAsStr, ", "),
				VariablesReference: s.variableHandles.create(&fullyQualifiedVariable{retVarsAsVar, "", false /*not a scope*/, 0}),
			}
		}
	} else { // {expression}
		exprVar, err := s.debugger.EvalVariableInScope(int64(goid), frame, 0, expr, DefaultLoadConfig)
		if err != nil {
			s.sendErrorResponseWithOpts(request.Request, UnableToEvaluateExpression, "Unable to evaluate expression", err.Error(), showErrorToUser)
			return
		}

		ctxt := request.Arguments.Context
		switch ctxt {
		case "repl", "variables", "hover", "clipboard":
			if exprVar.Kind == reflect.String {
				if strVal := constant.StringVal(exprVar.Value); exprVar.Len > int64(len(strVal)) {
					// Reload the string value with a bigger limit.
					loadCfg := DefaultLoadConfig
					loadCfg.MaxStringLen = maxSingleStringLen
					if v, err := s.debugger.EvalVariableInScope(int64(goid), frame, 0, request.Arguments.Expression, loadCfg); err != nil {
						s.config.log.Debugf("Failed to load more for %v: %v", request.Arguments.Expression, err)
					} else {
						exprVar = v
					}
				}
			}
		}
		var opts convertVariableFlags
		// Send the full value when the context is "clipboard" or "variables" since
		// these contexts are used to copy the value.
		if ctxt == "clipboard" || ctxt == "variables" {
			opts |= showFullValue
		}
		exprVal, exprRef := s.convertVariableWithOpts(exprVar, fmt.Sprintf("(%s)", request.Arguments.Expression), opts)
		response.Body = dap.EvaluateResponseBody{Result: exprVal, Type: s.getTypeIfSupported(exprVar), VariablesReference: exprRef, IndexedVariables: getIndexedVariableCount(exprVar), NamedVariables: getNamedVariableCount(exprVar)}
	}
	s.send(response)
}

func (s *Session) doCall(goid, frame int, expr string) (*api.DebuggerState, []*proc.Variable, error) {
	// This call might be evaluated in the context of the frame that is not topmost
	// if the editor is set to view the variables for one of the parent frames.
	// If the call expression refers to any of these variables, unlike regular
	// expressions, it will evaluate them in the context of the topmost frame,
	// and the user will get an unexpected result or an unexpected symbol error.
	// We prevent this but disallowing any frames other than topmost.
	if frame > 0 {
		return nil, nil, fmt.Errorf("call is only supported with topmost stack frame")
	}
	stateBeforeCall, err := s.debugger.State( /*nowait*/ true)
	if err != nil {
		return nil, nil, err
	}
	// The return values of injected function calls are volatile.
	// Load as much useful data as possible.
	// TODO: investigate whether we need to increase other limits. For example,
	// the return value is a pointer to a temporary object, which can become
	// invalid by other injected function calls. Do we care about such use cases?
	loadCfg := DefaultLoadConfig
	loadCfg.MaxStringLen = maxStringLenInCallRetVars

	// TODO(polina): since call will resume execution of all goroutines,
	// we should do this asynchronously and send a continued event to the
	// editor, followed by a stop event when the call completes.
	state, err := s.debugger.Command(&api.DebuggerCommand{
		Name:                 api.Call,
		ReturnInfoLoadConfig: api.LoadConfigFromProc(&loadCfg),
		Expr:                 expr,
		UnsafeCall:           false,
		GoroutineID:          int64(goid),
	}, nil)
	if processExited(state, err) {
		e := &dap.TerminatedEvent{Event: *newEvent("terminated")}
		s.send(e)
		return nil, nil, errors.New("terminated")
	}
	if err != nil {
		return nil, nil, err
	}

	// After the call is done, the goroutine where we injected the call should
	// return to the original stopped line with return values. However,
	// it is not guaranteed to be selected due to the possibility of the
	// of simultaenous breakpoints. Therefore, we check all threads.
	var retVars []*proc.Variable
	found := false
	for _, t := range state.Threads {
		if t.GoroutineID == stateBeforeCall.SelectedGoroutine.ID &&
			t.Line == stateBeforeCall.SelectedGoroutine.CurrentLoc.Line && t.CallReturn {
			found = true
			// The call completed. Get the return values.
			retVars, err = s.debugger.FindThreadReturnValues(t.ID, loadCfg)
			if err != nil {
				return nil, nil, err
			}
			break
		}
	}
	// Normal function calls expect return values, but call commands
	// used for variable assignments do not return a value when they succeed.
	// In go '=' is not an operator. Check if go/parser complains.
	// If the above Call command passed but the expression is not a valid
	// go expression, we just handled a variable assignment request.
	isAssignment := false
	if _, err := parser.ParseExpr(expr); err != nil {
		isAssignment = true
	}

	// note: as described in https://github.com/golang/go/issues/25578, function call injection
	// causes to resume the entire Go process. Due to this limitation, there is no guarantee
	// that the process is in the same state even after the injected call returns normally
	// without any surprises such as breakpoints or panic. To handle this correctly we need
	// to reset all the handles (both variables and stack frames).
	//
	// We considered sending a stopped event after each call unconditionally, but a stopped
	// event can be expensive and can interact badly with the client-side optimization
	// to refresh information. For example, VS Code reissues scopes/evaluate (for watch) after
	// completing a setVariable or evaluate request for repl context. Thus, for now, we
	// do not trigger a stopped event and hope editors to refetch the updated state as soon
	// as the user resumes debugging.

	if !found || !isAssignment && retVars == nil {
		// The call got interrupted by a stop (e.g. breakpoint in injected
		// function call or in another goroutine).
		s.resetHandlesForStoppedEvent()
		s.sendStoppedEvent(state)

		// TODO(polina): once this is asynchronous, we could wait to reply until the user
		// continues, call ends, original stop point is hit and return values are available
		// instead of returning an error 'call stopped' here.
		return nil, nil, errors.New("call stopped")
	}
	return state, retVars, nil
}

func (s *Session) sendStoppedEvent(state *api.DebuggerState) {
	stopped := &dap.StoppedEvent{Event: *newEvent("stopped")}
	stopped.Body.AllThreadsStopped = true
	stopped.Body.ThreadId = int(stoppedGoroutineID(state))
	stopped.Body.Reason = s.debugger.StopReason().String()
	s.send(stopped)
}

// onTerminateRequest sends a not-yet-implemented error response.
// Capability 'supportsTerminateRequest' is not set in 'initialize' response.
func (s *Session) onTerminateRequest(request *dap.TerminateRequest) {
	s.sendNotYetImplementedErrorResponse(request.Request)
}

// onRestartRequest sends a not-yet-implemented error response
// Capability 'supportsRestartRequest' is not set in 'initialize' response.
func (s *Session) onRestartRequest(request *dap.RestartRequest) {
	s.sendNotYetImplementedErrorResponse(request.Request)
}

// onStepBackRequest handles 'stepBack' request.
// This is an optional request enabled by capability ‘supportsStepBackRequest’.
func (s *Session) onStepBackRequest(request *dap.StepBackRequest, allowNextStateChange chan struct{}) {
	s.sendStepResponse(request.Arguments.ThreadId, &dap.StepBackResponse{Response: *newResponse(request.Request)})
	s.stepUntilStopAndNotify(api.ReverseNext, request.Arguments.ThreadId, request.Arguments.Granularity, allowNextStateChange)
}

// onReverseContinueRequest performs a rewind command call up to the previous
// breakpoint or the start of the process
// This is an optional request enabled by capability ‘supportsStepBackRequest’.
func (s *Session) onReverseContinueRequest(request *dap.ReverseContinueRequest, allowNextStateChange chan struct{}) {
	s.send(&dap.ReverseContinueResponse{
		Response: *newResponse(request.Request),
	})
	s.runUntilStopAndNotify(api.Rewind, allowNextStateChange)
}

// computeEvaluateName finds the named child, and computes its evaluate name.
func (s *Session) computeEvaluateName(v *fullyQualifiedVariable, cname string) (string, error) {
	children, err := s.childrenToDAPVariables(v)
	if err != nil {
		return "", err
	}
	for _, c := range children {
		if c.Name == cname {
			if c.EvaluateName != "" {
				return c.EvaluateName, nil
			}
			return "", errors.New("cannot set the variable without evaluate name")
		}
	}
	return "", errors.New("failed to find the named variable")
}

// onSetVariableRequest handles 'setVariable' requests.
func (s *Session) onSetVariableRequest(request *dap.SetVariableRequest) {
	arg := request.Arguments

	v, ok := s.variableHandles.get(arg.VariablesReference)
	if !ok {
		s.sendErrorResponse(request.Request, UnableToSetVariable, "Unable to lookup variable", fmt.Sprintf("unknown reference %d", arg.VariablesReference))
		return
	}
	// We need to translate the arg.Name to its evaluateName if the name
	// refers to a field or element of a variable.
	// https://github.com/microsoft/vscode/issues/120774
	evaluateName, err := s.computeEvaluateName(v, arg.Name)
	if err != nil {
		s.sendErrorResponse(request.Request, UnableToSetVariable, "Unable to set variable", err.Error())
		return
	}

	// By running EvalVariableInScope, we get the type info of the variable
	// that can be accessed with the evaluateName, and ensure the variable we are
	// trying to update is valid and accessible from the top most frame & the
	// current goroutine.
	goid, frame := -1, 0
	evaluated, err := s.debugger.EvalVariableInScope(int64(goid), frame, 0, evaluateName, DefaultLoadConfig)
	if err != nil {
		s.sendErrorResponse(request.Request, UnableToSetVariable, "Unable to lookup variable", err.Error())
		return
	}

	useFnCall := false
	switch evaluated.Kind {
	case reflect.String:
		useFnCall = true
	default:
		// TODO(hyangah): it's possible to set a non-string variable using (`call i = fn()`)
		// and we don't support it through the Set Variable request yet.
		// If we want to support it for non-string types, we need to parse arg.Value.
	}

	if useFnCall {
		// TODO(hyangah): function call injection currentlly allows to assign return values of
		// a function call to variables. So, curious users would find set variable
		// on string would accept expression like `fn()`.
		if state, retVals, err := s.doCall(goid, frame, fmt.Sprintf("%v=%v", evaluateName, arg.Value)); err != nil {
			s.sendErrorResponse(request.Request, UnableToSetVariable, "Unable to set variable", err.Error())
			return
		} else if retVals != nil {
			// The assignment expression isn't supposed to return values, but we got them.
			// That indicates something went wrong (e.g. panic).
			// TODO: isn't it simpler to do this in s.doCall?
			s.resetHandlesForStoppedEvent()
			s.sendStoppedEvent(state)

			var r []string
			for _, v := range retVals {
				r = append(r, s.convertVariableToString(v))
			}
			msg := "interrupted"
			if len(r) > 0 {
				msg = "interrupted:" + strings.Join(r, ", ")
			}

			s.sendErrorResponse(request.Request, UnableToSetVariable, "Unable to set variable", msg)
			return
		}
	} else {
		if err := s.debugger.SetVariableInScope(int64(goid), frame, 0, evaluateName, arg.Value); err != nil {
			s.sendErrorResponse(request.Request, UnableToSetVariable, "Unable to set variable", err.Error())
			return
		}
	}
	// * Note on inconsistent state after set variable:
	//
	// The variable handles may be in inconsistent state - for example,
	// let's assume there are two aliased variables pointing to the same
	// memory and both are already loaded and cached in the variable handle.
	// VSCode tries to locally update the UI when the set variable
	// request succeeds, and may issue additional scopes or evaluate requests
	// to update the variable/watch sections if necessary.
	//
	// More complicated situation is when the set variable involves call
	// injection - after the injected call is completed, the debugee can
	// be in a completely different state (see the note in doCall) due to
	// how the call injection is implemented. Ideally, we need to also refresh
	// the stack frames but that is complicated. For now we don't try to actively
	// invalidate this state hoping that the editors will refetch the state
	// as soon as the user resumes debugging.

	response := &dap.SetVariableResponse{Response: *newResponse(request.Request)}
	response.Body.Value = arg.Value
	// TODO(hyangah): instead of arg.Value, reload the variable and return
	// the presentation of the new value.
	s.send(response)
}

// onSetExpression sends a not-yet-implemented error response.
// Capability 'supportsSetExpression' is not set 'initialize' response.
func (s *Session) onSetExpressionRequest(request *dap.SetExpressionRequest) {
	s.sendNotYetImplementedErrorResponse(request.Request)
}

// onLoadedSourcesRequest sends a not-yet-implemented error response.
// Capability 'supportsLoadedSourcesRequest' is not set 'initialize' response.
func (s *Session) onLoadedSourcesRequest(request *dap.LoadedSourcesRequest) {
	s.sendNotYetImplementedErrorResponse(request.Request)
}

// onReadMemoryRequest sends a not-yet-implemented error response.
// Capability 'supportsReadMemoryRequest' is not set 'initialize' response.
func (s *Session) onReadMemoryRequest(request *dap.ReadMemoryRequest) {
	s.sendNotYetImplementedErrorResponse(request.Request)
}

var invalidInstruction = dap.DisassembledInstruction{
	Instruction: "invalid instruction",
}

// onDisassembleRequest handles 'disassemble' requests.
// Capability 'supportsDisassembleRequest' is set in 'initialize' response.
func (s *Session) onDisassembleRequest(request *dap.DisassembleRequest) {
	// TODO(suzmue): microsoft/vscode#129655 is discussing the difference between
	// memory reference and instructionPointerReference, which are currently
	// being used interchangeably by vscode.
	addr, err := strconv.ParseUint(request.Arguments.MemoryReference, 0, 64)
	if err != nil {
		s.sendErrorResponse(request.Request, UnableToDisassemble, "Unable to disassemble", err.Error())
		return
	}

	// If the requested memory address is an invalid location, return all invalid instructions.
	// TODO(suzmue): consider adding fake addresses that would allow us to receive out of bounds
	// requests that include valid instructions designated by InstructionOffset or InstructionCount.
	if addr == 0 || addr == uint64(math.MaxUint64) {
		instructions := make([]dap.DisassembledInstruction, request.Arguments.InstructionCount)
		for i := range instructions {
			instructions[i] = invalidInstruction
			instructions[i].Address = request.Arguments.MemoryReference
		}
		response := &dap.DisassembleResponse{
			Response: *newResponse(request.Request),
			Body: dap.DisassembleResponseBody{
				Instructions: instructions,
			},
		}
		s.send(response)
		return
	}

	start := uint64(addr)
	maxInstructionLength := s.debugger.Target().BinInfo().Arch.MaxInstructionLength()
	byteOffset := request.Arguments.InstructionOffset * maxInstructionLength
	// Adjust the offset to include instructions before the requested address.
	if byteOffset < 0 {
		start = uint64(int(addr) + byteOffset)
	}
	// Adjust the number of instructions to include enough instructions after
	// the requested address.
	count := request.Arguments.InstructionCount
	if byteOffset > 0 {
		count += byteOffset
	}
	end := uint64(int(addr) + count*maxInstructionLength)

	// Make sure the PCs are lined up with instructions.
	start, end = alignPCs(s.debugger.Target().BinInfo(), start, end)

	// Disassemble the instructions
	procInstructions, err := s.debugger.Disassemble(-1, start, end)
	if err != nil {
		s.sendErrorResponse(request.Request, UnableToDisassemble, "Unable to disassemble", err.Error())
		return
	}

	// Find the section of instructions that were requested.
	procInstructions, offset, err := findInstructions(procInstructions, addr, request.Arguments.InstructionOffset, request.Arguments.InstructionCount)
	if err != nil {
		s.sendErrorResponse(request.Request, UnableToDisassemble, "Unable to disassemble", err.Error())
		return
	}

	// Turn the given range of instructions into dap instructions.
	instructions := make([]dap.DisassembledInstruction, request.Arguments.InstructionCount)
	lastFile, lastLine := "", -1
	for i := range instructions {
		// i is not in a valid range, use an address that is just before or after
		// the range. This ensures that it can still be parsed as an int.
		if i < offset {
			// i is not in a valid range.
			instructions[i] = invalidInstruction
			instructions[i].Address = "0x0"
			continue
		}
		if (i - offset) >= len(procInstructions) {
			// i is not in a valid range.
			instructions[i] = invalidInstruction
			instructions[i].Address = fmt.Sprintf("%#x", uint64(math.MaxUint64))
			continue

		}
		instruction := api.ConvertAsmInstruction(procInstructions[i-offset], s.debugger.AsmInstructionText(&procInstructions[i-offset], proc.GoFlavour))
		instructions[i] = dap.DisassembledInstruction{
			Address:          fmt.Sprintf("%#x", instruction.Loc.PC),
			InstructionBytes: fmt.Sprintf("%x", instruction.Bytes),
			Instruction:      instruction.Text,
		}
		// Only set the location on the first instruction for a given line.
		if instruction.Loc.File != lastFile || instruction.Loc.Line != lastLine {
			instructions[i].Location = dap.Source{Path: instruction.Loc.File}
			instructions[i].Line = instruction.Loc.Line
			lastFile, lastLine = instruction.Loc.File, instruction.Loc.Line
		}
	}

	response := &dap.DisassembleResponse{
		Response: *newResponse(request.Request),
		Body: dap.DisassembleResponseBody{
			Instructions: instructions,
		},
	}
	s.send(response)
}

func findInstructions(procInstructions []proc.AsmInstruction, addr uint64, instructionOffset, count int) ([]proc.AsmInstruction, int, error) {
	ref := sort.Search(len(procInstructions), func(i int) bool {
		return procInstructions[i].Loc.PC >= addr
	})
	if ref == len(procInstructions) || procInstructions[ref].Loc.PC != uint64(addr) {
		return nil, -1, fmt.Errorf("could not find memory reference")
	}
	// offset is the number of instructions that should appear before the first instruction
	// returned by findInstructions.
	offset := 0
	if ref+instructionOffset < 0 {
		offset = -(ref + instructionOffset)
	}
	// Figure out the index to slice at.
	startIdx := ref + instructionOffset
	endIdx := ref + instructionOffset + count
	if endIdx <= 0 || startIdx >= len(procInstructions) {
		return []proc.AsmInstruction{}, 0, nil
	}
	// Adjust start and end to be inbounds.
	if startIdx < 0 {
		offset = -startIdx
		startIdx = 0
	}
	if endIdx > len(procInstructions) {
		endIdx = len(procInstructions)
	}
	return procInstructions[startIdx:endIdx], offset, nil
}

func getValidRange(bi *proc.BinaryInfo) (uint64, uint64) {
	return bi.Functions[0].Entry, bi.Functions[len(bi.Functions)-1].End
}

func alignPCs(bi *proc.BinaryInfo, start, end uint64) (uint64, uint64) {
	// We want to find the function locations position that would enclose
	// the range from start to end.
	//
	// Example:
	//
	// 0x0000	instruction (func1)
	// 0x0004	instruction (func1)
	// 0x0008	instruction (func1)
	// 0x000c	nop
	// 0x000e	nop
	// 0x0000	nop
	// 0x0002	nop
	// 0x0004	instruction (func2)
	// 0x0008	instruction (func2)
	// 0x000c	instruction (func2)
	//
	// start values:
	// < 0x0000			at func1.Entry	=	0x0000
	// 0x0000-0x000b	at func1.Entry	=	0x0000
	// 0x000c-0x0003	at func1.End	=	0x000c
	// 0x0004-0x000f	at func2.Entry	=	0x0004
	// > 0x000f			at func2.End	=	0x0010
	//
	// end values:
	// < 0x0000			at func1.Entry	=	0x0000
	// 0x0000-0x000b	at func1.End	=	0x0000
	// 0x000c-0x0003	at func2.Entry	=	0x000c
	// 0x0004-0x000f	at func2.End	=	0x0004
	// > 0x000f			at func2.End	=	0x0004
	// Handle start values:
	fn := bi.PCToFunc(start)
	if fn != nil {
		// start is in a funcition.
		start = fn.Entry
	} else if b, pc := checkOutOfAddressSpace(start, bi); b {
		start = pc
	} else {
		// Otherwise it must come after some function.
		i := sort.Search(len(bi.Functions), func(i int) bool {
			fn := bi.Functions[len(bi.Functions)-(i+1)]
			return start >= fn.End
		})
		start = bi.Functions[len(bi.Functions)-(i+1)].Entry
	}

	// Handle end values:
	if fn := bi.PCToFunc(end); fn != nil {
		// end is in a funcition.
		end = fn.End
	} else if b, pc := checkOutOfAddressSpace(end, bi); b {
		end = pc
	} else {
		// Otherwise it must come before some function.
		i := sort.Search(len(bi.Functions), func(i int) bool {
			fn := bi.Functions[i]
			return end < fn.Entry
		})
		end = bi.Functions[i].Entry
		const limit = 10 * 1024
		if end-start > limit {
			end = start + limit
		}
	}

	return start, end
}

func checkOutOfAddressSpace(pc uint64, bi *proc.BinaryInfo) (bool, uint64) {
	entry, end := getValidRange(bi)
	if pc < entry {
		return true, entry
	}
	if pc >= end {
		return true, end
	}
	return false, pc
}

// onCancelRequest sends a not-yet-implemented error response.
// Capability 'supportsCancelRequest' is not set 'initialize' response.
func (s *Session) onCancelRequest(request *dap.CancelRequest) {
	s.sendNotYetImplementedErrorResponse(request.Request)
}

// onExceptionInfoRequest handles 'exceptionInfo' requests.
// Capability 'supportsExceptionInfoRequest' is set in 'initialize' response.
func (s *Session) onExceptionInfoRequest(request *dap.ExceptionInfoRequest) {
	goroutineID := int64(request.Arguments.ThreadId)
	var body dap.ExceptionInfoResponseBody
	// Get the goroutine and the current state.
	g, err := s.debugger.FindGoroutine(goroutineID)
	if err != nil {
		s.sendErrorResponse(request.Request, UnableToGetExceptionInfo, "Unable to get exception info", err.Error())
		return
	}
	if g == nil {
		s.sendErrorResponse(request.Request, UnableToGetExceptionInfo, "Unable to get exception info", fmt.Sprintf("could not find goroutine %d", goroutineID))
		return
	}
	var bpState *proc.BreakpointState
	if g.Thread != nil {
		bpState = g.Thread.Breakpoint()
	}
	// Check if this goroutine ID is stopped at a breakpoint.
	includeStackTrace := true
	if bpState != nil && bpState.Breakpoint != nil && bpState.Breakpoint.Logical != nil && (bpState.Breakpoint.Logical.Name == proc.FatalThrow || bpState.Breakpoint.Logical.Name == proc.UnrecoveredPanic) {
		switch bpState.Breakpoint.Logical.Name {
		case proc.FatalThrow:
			body.ExceptionId = "fatal error"
			body.Description, err = s.throwReason(goroutineID)
			if err != nil {
				body.Description = fmt.Sprintf("Error getting throw reason: %s", err.Error())
				// This is not currently working for Go 1.16.
				ver := goversion.ParseProducer(s.debugger.TargetGoVersion())
				if ver.Major == 1 && ver.Minor == 16 {
					body.Description = "Throw reason unavailable, see https://github.com/golang/go/issues/46425"
				}
			}
		case proc.UnrecoveredPanic:
			body.ExceptionId = "panic"
			// Attempt to get the value of the panic message.
			body.Description, err = s.panicReason(goroutineID)
			if err != nil {
				body.Description = fmt.Sprintf("Error getting panic message: %s", err.Error())
			}
		}
	} else {
		// If this thread is not stopped on a breakpoint, then a runtime error must have occurred.
		// If we do not have any error saved, or if this thread is not current thread,
		// return an error.
		if s.exceptionErr == nil {
			s.sendErrorResponse(request.Request, UnableToGetExceptionInfo, "Unable to get exception info", "no runtime error found")
			return
		}

		state, err := s.debugger.State( /*nowait*/ true)
		if err != nil {
			s.sendErrorResponse(request.Request, UnableToGetExceptionInfo, "Unable to get exception info", err.Error())
			return
		}
		if s.exceptionErr.Error() != "next while nexting" && (state == nil || state.CurrentThread == nil || g.Thread == nil || state.CurrentThread.ID != g.Thread.ThreadID()) {
			s.sendErrorResponse(request.Request, UnableToGetExceptionInfo, "Unable to get exception info", fmt.Sprintf("no exception found for goroutine %d", goroutineID))
			return
		}
		body.ExceptionId = "runtime error"
		body.Description = s.exceptionErr.Error()
		if body.Description == "bad access" {
			body.Description = BetterBadAccessError
		}
		if body.Description == "next while nexting" {
			body.ExceptionId = "invalid command"
			body.Description = BetterNextWhileNextingError
			includeStackTrace = false
		}
	}

	if includeStackTrace {
		frames, err := s.stacktrace(goroutineID, g)
		if err != nil {
			body.Details.StackTrace = fmt.Sprintf("Error getting stack trace: %s", err.Error())
		} else {
			body.Details.StackTrace = frames
		}
	}
	response := &dap.ExceptionInfoResponse{
		Response: *newResponse(request.Request),
		Body:     body,
	}
	s.send(response)
}

func (s *Session) stacktrace(goroutineID int64, g *proc.G) (string, error) {
	frames, err := s.debugger.Stacktrace(goroutineID, s.args.StackTraceDepth, 0)
	if err != nil {
		return "", err
	}
	apiFrames, err := s.debugger.ConvertStacktrace(frames, nil)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	fmt.Fprintln(&buf, "Stack:")
	userLoc := g.UserCurrent()
	userFuncPkg := fnPackageName(&userLoc)
	api.PrintStack(s.toClientPath, &buf, apiFrames, "\t", false, func(s api.Stackframe) bool {
		// Include all stack frames if the stack trace is for a system goroutine,
		// otherwise, skip runtime stack frames.
		if userFuncPkg == "runtime" {
			return true
		}
		return s.Location.Function != nil && !strings.HasPrefix(s.Location.Function.Name(), "runtime.")
	})
	return buf.String(), nil
}

func (s *Session) throwReason(goroutineID int64) (string, error) {
	return s.getExprString("s", goroutineID, 0)
}

func (s *Session) panicReason(goroutineID int64) (string, error) {
	return s.getExprString("(*msgs).arg.(data)", goroutineID, 0)
}

func (s *Session) getExprString(expr string, goroutineID int64, frame int) (string, error) {
	exprVar, err := s.debugger.EvalVariableInScope(goroutineID, frame, 0, expr, DefaultLoadConfig)
	if err != nil {
		return "", err
	}
	if exprVar.Value == nil {
		return "", exprVar.Unreadable
	}
	return exprVar.Value.String(), nil
}

// sendErrorResponseWithOpts offers configuration options.
//
//	showUser - if true, the error will be shown to the user (e.g. via a visible pop-up)
func (s *Session) sendErrorResponseWithOpts(request dap.Request, id int, summary, details string, showUser bool) {
	er := &dap.ErrorResponse{}
	er.Type = "response"
	er.Command = request.Command
	er.RequestSeq = request.Seq
	er.Success = false
	er.Message = summary
	er.Body.Error.Id = id
	er.Body.Error.Format = fmt.Sprintf("%s: %s", summary, details)
	er.Body.Error.ShowUser = showUser
	s.config.log.Debug(er.Body.Error.Format)
	s.send(er)
}

// sendErrorResponse sends an error response with showUser disabled (default).
func (s *Session) sendErrorResponse(request dap.Request, id int, summary, details string) {
	s.sendErrorResponseWithOpts(request, id, summary, details, false /*showUser*/)
}

// sendShowUserErrorResponse sends an error response with showUser enabled.
func (s *Session) sendShowUserErrorResponse(request dap.Request, id int, summary, details string) {
	s.sendErrorResponseWithOpts(request, id, summary, details, true /*showUser*/)
}

// sendInternalErrorResponse sends an "internal error" response back to the client.
// We only take a seq here because we don't want to make assumptions about the
// kind of message received by the server that this error is a reply to.
func (s *Session) sendInternalErrorResponse(seq int, details string) {
	er := &dap.ErrorResponse{}
	er.Type = "response"
	er.RequestSeq = seq
	er.Success = false
	er.Message = "Internal Error"
	er.Body.Error.Id = InternalError
	er.Body.Error.Format = fmt.Sprintf("%s: %s", er.Message, details)
	s.config.log.Debug(er.Body.Error.Format)
	s.send(er)
}

func (s *Session) sendUnsupportedErrorResponse(request dap.Request) {
	s.sendErrorResponse(request, UnsupportedCommand, "Unsupported command",
		fmt.Sprintf("cannot process %q request", request.Command))
}

func (s *Session) sendNotYetImplementedErrorResponse(request dap.Request) {
	s.sendErrorResponse(request, NotYetImplemented, "Not yet implemented",
		fmt.Sprintf("cannot process %q request", request.Command))
}

func newResponse(request dap.Request) *dap.Response {
	return &dap.Response{
		ProtocolMessage: dap.ProtocolMessage{
			Seq:  0,
			Type: "response",
		},
		Command:    request.Command,
		RequestSeq: request.Seq,
		Success:    true,
	}
}

func newEvent(event string) *dap.Event {
	return &dap.Event{
		ProtocolMessage: dap.ProtocolMessage{
			Seq:  0,
			Type: "event",
		},
		Event: event,
	}
}

const BetterBadAccessError = `invalid memory address or nil pointer dereference [signal SIGSEGV: segmentation violation]
Unable to propagate EXC_BAD_ACCESS signal to target process and panic (see https://github.com/go-delve/delve/issues/852)`
const BetterNextWhileNextingError = `Unable to step while the previous step is interrupted by a breakpoint.
Use 'Continue' to resume the original step command.`

func (s *Session) resetHandlesForStoppedEvent() {
	s.stackFrameHandles.reset()
	s.variableHandles.reset()
	s.exceptionErr = nil
}

func processExited(state *api.DebuggerState, err error) bool {
	_, isexited := err.(proc.ErrProcessExited)
	return isexited || err == nil && state.Exited
}

func (s *Session) setRunningCmd(running bool) {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()
	s.runningCmd = running
}

func (s *Session) isRunningCmd() bool {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()
	return s.runningCmd
}

func (s *Session) setHaltRequested(requested bool) {
	s.haltMu.Lock()
	defer s.haltMu.Unlock()
	s.haltRequested = requested
}

func (s *Session) checkHaltRequested() bool {
	s.haltMu.Lock()
	defer s.haltMu.Unlock()
	return s.haltRequested
}

// resumeOnce is a helper function to resume the execution
// of the target when the program is halted.
func (s *Session) resumeOnce(command string, allowNextStateChange chan struct{}) (bool, *api.DebuggerState, error) {
	// No other goroutines should be able to try to resume
	// or halt execution while this goroutine is resuming
	// execution, so we do not miss those events.
	asyncSetupDone := make(chan struct{}, 1)
	defer closeIfOpen(asyncSetupDone)
	s.changeStateMu.Lock()
	go func() {
		defer s.changeStateMu.Unlock()
		defer closeIfOpen(allowNextStateChange)
		<-asyncSetupDone
	}()

	// There may have been a manual halt while the program was
	// stopped. If this happened, do not resume execution of
	// the program.
	if s.checkHaltRequested() {
		state, err := s.debugger.State(false)
		return false, state, err
	}
	state, err := s.debugger.Command(&api.DebuggerCommand{Name: command}, asyncSetupDone)
	return true, state, err
}

// runUntilStopAndNotify runs a debugger command until it stops on
// termination, error, breakpoint, etc, when an appropriate
// event needs to be sent to the client. allowNextStateChange is
// a channel that will be closed to signal that an
// asynchornous command has completed setup or was interrupted
// due to an error, so the server is ready to receive new requests.
func (s *Session) runUntilStopAndNotify(command string, allowNextStateChange chan struct{}) {
	state, err := s.runUntilStop(command, allowNextStateChange)

	if s.conn.isClosed() {
		s.config.log.Debugf("connection %d closed - stopping %q command", s.id, command)
		return
	}

	if processExited(state, err) {
		s.send(&dap.TerminatedEvent{Event: *newEvent("terminated")})
		return
	}

	stopReason := s.debugger.StopReason()
	file, line := "?", -1
	if state != nil && state.CurrentThread != nil {
		file, line = state.CurrentThread.File, state.CurrentThread.Line
	}
	s.config.log.Debugf("%q command stopped - reason %q, location %s:%d", command, stopReason, file, line)

	s.resetHandlesForStoppedEvent()
	stopped := &dap.StoppedEvent{Event: *newEvent("stopped")}
	stopped.Body.AllThreadsStopped = true

	if err == nil {
		if stopReason == proc.StopManual {
			if err := s.debugger.CancelNext(); err != nil {
				s.config.log.Error(err)
			} else {
				state.NextInProgress = false
			}
		}
		stopped.Body.ThreadId = int(stoppedGoroutineID(state))

		switch stopReason {
		case proc.StopNextFinished:
			stopped.Body.Reason = "step"
		case proc.StopManual: // triggered by halt
			stopped.Body.Reason = "pause"
		case proc.StopUnknown: // can happen while terminating
			stopped.Body.Reason = "unknown"
		case proc.StopWatchpoint:
			stopped.Body.Reason = "data breakpoint"
		default:
			stopped.Body.Reason = "breakpoint"
			goid, bp := s.stoppedOnBreakpointGoroutineID(state)
			stopped.Body.ThreadId = int(goid)
			if bp != nil {
				switch bp.Name {
				case proc.FatalThrow:
					stopped.Body.Reason = "exception"
					stopped.Body.Description = "fatal error"
					stopped.Body.Text, _ = s.throwReason(int64(stopped.Body.ThreadId))
				case proc.UnrecoveredPanic:
					stopped.Body.Reason = "exception"
					stopped.Body.Description = "panic"
					stopped.Body.Text, _ = s.panicReason(int64(stopped.Body.ThreadId))
				}
				if strings.HasPrefix(bp.Name, functionBpPrefix) {
					stopped.Body.Reason = "function breakpoint"
				}
				if strings.HasPrefix(bp.Name, instructionBpPrefix) {
					stopped.Body.Reason = "instruction breakpoint"
				}
				stopped.Body.HitBreakpointIds = []int{bp.ID}
			}
		}

		// Override the stop reason if there was a manual stop request.
		// TODO(suzmue): move this logic into the runUntilStop command
		// so that the stop reason is determined by that function which
		// has all the context.
		if stopped.Body.Reason != "exception" && s.checkHaltRequested() {
			s.config.log.Debugf("manual halt requested, stop reason %q converted to \"pause\"", stopped.Body.Reason)
			stopped.Body.Reason = "pause"
			stopped.Body.HitBreakpointIds = []int{}
		}

	} else {
		s.exceptionErr = err
		s.config.log.Error("runtime error: ", err)
		stopped.Body.Reason = "exception"
		stopped.Body.Description = "runtime error"
		stopped.Body.Text = err.Error()
		// Special case in the spirit of https://github.com/microsoft/vscode-go/issues/1903
		if stopped.Body.Text == "bad access" {
			stopped.Body.Text = BetterBadAccessError
		}
		if stopped.Body.Text == "next while nexting" {
			stopped.Body.Description = "invalid command"
			stopped.Body.Text = BetterNextWhileNextingError
			s.logToConsole(fmt.Sprintf("%s: %s", stopped.Body.Description, stopped.Body.Text))
		}

		state, err := s.debugger.State( /*nowait*/ true)
		if err == nil {
			stopped.Body.ThreadId = int(stoppedGoroutineID(state))
		}
	}

	// NOTE: If we happen to be responding to another request with an is-running
	// error while this one completes, it is possible that the error response
	// will arrive after this stopped event.
	s.send(stopped)

	// Send an output event with more information if next is in progress.
	if state != nil && state.NextInProgress {
		s.logToConsole("Step interrupted by a breakpoint. Use 'Continue' to resume the original step command.")
	}
}

func (s *Session) runUntilStop(command string, allowNextStateChange chan struct{}) (*api.DebuggerState, error) {
	// Clear any manual stop requests that came in before we started running.
	s.setHaltRequested(false)

	s.setRunningCmd(true)
	defer s.setRunningCmd(false)

	var state *api.DebuggerState
	var err error
	for s.isRunningCmd() {
		state, err = resumeOnceAndCheckStop(s, command, allowNextStateChange)
		command = api.DirectionCongruentContinue
	}
	return state, err
}

// Make this a var so it can be stubbed in testing.
var resumeOnceAndCheckStop = func(s *Session, command string, allowNextStateChange chan struct{}) (*api.DebuggerState, error) {
	return s.resumeOnceAndCheckStop(command, allowNextStateChange)
}

func (s *Session) resumeOnceAndCheckStop(command string, allowNextStateChange chan struct{}) (*api.DebuggerState, error) {
	resumed, state, err := s.resumeOnce(command, allowNextStateChange)
	// We should not try to process the log points if the program was not
	// resumed or there was an error.
	if !resumed || processExited(state, err) || state == nil || err != nil || s.conn.isClosed() {
		s.setRunningCmd(false)
		return state, err
	}

	s.handleLogPoints(state)
	gsOnBp := s.stoppedGs(state)

	switch s.debugger.StopReason() {
	case proc.StopBreakpoint, proc.StopManual:
		// Make sure a real manual stop was requested or a real breakpoint was hit.
		if len(gsOnBp) > 0 || s.checkHaltRequested() {
			s.setRunningCmd(false)
		}
	default:
		s.setRunningCmd(false)
	}

	// Stepping a single instruction will never require continuing again.
	if command == api.StepInstruction || command == api.ReverseStepInstruction {
		s.setRunningCmd(false)
	}

	return state, err
}

func (s *Session) handleLogPoints(state *api.DebuggerState) {
	for _, th := range state.Threads {
		if bp := th.Breakpoint; bp != nil {
			s.logBreakpointMessage(bp, th.GoroutineID)
		}
	}
}

func (s *Session) stoppedGs(state *api.DebuggerState) (gs []int64) {
	// Check the current thread first. There may be no selected goroutine.
	if state.CurrentThread.Breakpoint != nil && !state.CurrentThread.Breakpoint.Tracepoint {
		gs = append(gs, state.CurrentThread.GoroutineID)
	}
	if s.debugger.StopReason() == proc.StopHardcodedBreakpoint {
		gs = append(gs, stoppedGoroutineID(state))
	}
	for _, th := range state.Threads {
		// Some threads may be stopped on a hardcoded breakpoint.
		// TODO(suzmue): This is a workaround for detecting hard coded breakpoints,
		// though this check is likely not sufficient. It would be better to resolve
		// this in the debugger layer instead.
		if th.Function.Name() == "runtime.breakpoint" {
			gs = append(gs, th.GoroutineID)
			continue
		}
		// We already added the current thread if it had a breakpoint.
		if th.ID == state.CurrentThread.ID {
			continue
		}
		if bp := th.Breakpoint; bp != nil {
			if !th.Breakpoint.Tracepoint {
				gs = append(gs, th.GoroutineID)
			}
		}
	}
	return gs
}

func (s *Session) logBreakpointMessage(bp *api.Breakpoint, goid int64) bool {
	if !bp.Tracepoint {
		return false
	}
	if lMsg, ok := bp.UserData.(logMessage); ok {
		msg := lMsg.evaluate(s, goid)
		s.send(&dap.OutputEvent{
			Event: *newEvent("output"),
			Body: dap.OutputEventBody{
				Category: "stdout",
				Output:   fmt.Sprintf("> [Go %d]: %s\n", goid, msg),
				Source: dap.Source{
					Path: s.toClientPath(bp.File),
				},
				Line: bp.Line,
			},
		})
	}
	return true
}

func (msg *logMessage) evaluate(s *Session, goid int64) string {
	evaluated := make([]interface{}, len(msg.args))
	for i := range msg.args {
		exprVar, err := s.debugger.EvalVariableInScope(goid, 0, 0, msg.args[i], DefaultLoadConfig)
		if err != nil {
			evaluated[i] = fmt.Sprintf("{eval err: %e}", err)
			continue
		}
		evaluated[i], _ = s.convertVariableWithOpts(exprVar, "", skipRef|showFullValue)
	}
	return fmt.Sprintf(msg.format, evaluated...)
}

func (s *Session) toClientPath(path string) string {
	if len(s.args.substitutePathServerToClient) == 0 {
		return path
	}
	clientPath := locspec.SubstitutePath(path, s.args.substitutePathServerToClient)
	if clientPath != path {
		s.config.log.Debugf("server path=%s converted to client path=%s\n", path, clientPath)
	}
	return clientPath
}

func (s *Session) toServerPath(path string) string {
	if len(s.args.substitutePathClientToServer) == 0 {
		return path
	}
	serverPath := locspec.SubstitutePath(path, s.args.substitutePathClientToServer)
	if serverPath != path {
		s.config.log.Debugf("client path=%s converted to server path=%s\n", path, serverPath)
	}
	return serverPath
}

type logMessage struct {
	format string
	args   []string
}

// parseLogPoint parses a log message according to the DAP spec:
//
//	"Expressions within {} are interpolated."
func parseLogPoint(msg string) (bool, *logMessage, error) {
	// Note: All braces *must* come in pairs, even those within an
	// expression to be interpolated.
	// TODO(suzmue): support individual braces in string values in
	// eval expressions.
	var args []string

	var isArg bool
	var formatSlice, argSlice []rune
	braceCount := 0
	for _, r := range msg {
		if isArg {
			switch r {
			case '}':
				if braceCount--; braceCount == 0 {
					argStr := strings.TrimSpace(string(argSlice))
					if len(argStr) == 0 {
						return false, nil, fmt.Errorf("empty evaluation string")
					}
					args = append(args, argStr)
					formatSlice = append(formatSlice, '%', 's')
					isArg = false
					continue
				}
			case '{':
				braceCount += 1
			}
			argSlice = append(argSlice, r)
			continue
		}

		switch r {
		case '}':
			return false, nil, fmt.Errorf("invalid log point format, unexpected '}'")
		case '{':
			if braceCount++; braceCount == 1 {
				isArg, argSlice = true, []rune{}
				continue
			}
		}
		formatSlice = append(formatSlice, r)
	}
	if isArg {
		return false, nil, fmt.Errorf("invalid log point format")
	}
	if len(formatSlice) == 0 {
		return false, nil, nil
	}
	return true, &logMessage{
		format: string(formatSlice),
		args:   args,
	}, nil
}
