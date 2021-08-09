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
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"

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
// TODO(polina): add another layer of per-client goroutines to support multiple clients
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
	config *service.Config
	// listener is used to accept the client connection.
	listener net.Listener
	// stopTriggered is closed when the server is Stop()-ed.
	stopTriggered chan struct{}
	// reader is used to read requests from the connection.
	reader *bufio.Reader
	// log is used for structured logging.
	log *logrus.Entry
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
	conn net.Conn
	// debugger is the underlying debugger service.
	debugger *debugger.Debugger
	// binaryToRemove is the temp compiled binary to be removed on disconnect (if any).
	binaryToRemove string
	// noDebugProcess is set for the noDebug launch process.
	noDebugProcess *exec.Cmd

	// sendingMu synchronizes writing to net.Conn
	// to ensure that messages do not get interleaved
	sendingMu sync.Mutex

	// running tracks the running state according to the server,
	// which may not correspond to the actual running state of
	// the process.
	running   bool
	runningMu sync.Mutex

	// resumeMu must be held for a request to resume execution of the
	// process.
	// We use this to guarantee that no other request can unexpectedly
	// change the state to running, when we expect the process to halted.
	// Do not attempt to lock if already holding sendingMu or runningMu.
	resumeMu sync.Mutex
}

// launchAttachArgs captures arguments from launch/attach request that
// impact handling of subsequent requests.
type launchAttachArgs struct {
	// stopOnEntry is set to automatically stop the debugee after start.
	stopOnEntry bool
	// stackTraceDepth is the maximum length of the returned list of stack frames.
	stackTraceDepth int
	// showGlobalVariables indicates if global package variables should be loaded.
	showGlobalVariables bool
	// substitutePathClientToServer indicates rules for converting file paths between client and debugger.
	// These must be directory paths.
	substitutePathClientToServer [][2]string
	// substitutePathServerToClient indicates rules for converting file paths between debugger and client.
	// These must be directory paths.
	substitutePathServerToClient [][2]string
}

// defaultArgs borrows the defaults for the arguments from the original vscode-go adapter.
var defaultArgs = launchAttachArgs{
	stopOnEntry:                  false,
	stackTraceDepth:              50,
	showGlobalVariables:          false,
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
// These limits are conservative to minimize performace overhead for bulk loading.
// With dlv-dap, users do not have a way to adjust these.
// Instead we are focusing in interacive loading with nested reloads, array/map
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
	// Max number of goroutines that we will return.
	maxGoroutines = 1 << 10
)

// NewServer creates a new DAP Server. It takes an opened Listener
// via config and assumes its ownership. config.DisconnectChan has to be set;
// it will be closed by the server when the client fails to connect,
// disconnects or requests shutdown. Once config.DisconnectChan is closed,
// Server.Stop() must be called to shutdown this single-user server.
func NewServer(config *service.Config) *Server {
	logger := logflags.DAPLogger()
	logflags.WriteDAPListeningMessage(config.Listener.Addr().String())
	logger.Debug("DAP server pid = ", os.Getpid())
	return &Server{
		config:            config,
		listener:          config.Listener,
		stopTriggered:     make(chan struct{}),
		log:               logger,
		stackFrameHandles: newHandlesMap(),
		variableHandles:   newVariablesHandlesMap(),
		args:              defaultArgs,
		exceptionErr:      nil,
	}
}

// If user-specified options are provided via Launch/AttachRequest,
// we override the defaults for optional args.
func (s *Server) setLaunchAttachArgs(request dap.LaunchAttachRequest) error {
	stop, ok := request.GetArguments()["stopOnEntry"].(bool)
	if ok {
		s.args.stopOnEntry = stop
	}
	depth, ok := request.GetArguments()["stackTraceDepth"].(float64)
	if ok && depth > 0 {
		s.args.stackTraceDepth = int(depth)
	}
	globals, ok := request.GetArguments()["showGlobalVariables"].(bool)
	if ok {
		s.args.showGlobalVariables = globals
	}
	paths, ok := request.GetArguments()["substitutePath"]
	if ok {
		typeMismatchError := fmt.Errorf("'substitutePath' attribute '%v' in debug configuration is not a []{'from': string, 'to': string}", paths)
		pathsParsed, ok := paths.([]interface{})
		if !ok {
			return typeMismatchError
		}
		clientToServer := make([][2]string, 0, len(pathsParsed))
		serverToClient := make([][2]string, 0, len(pathsParsed))
		for _, arg := range pathsParsed {
			pathMapping, ok := arg.(map[string]interface{})
			if !ok {
				return typeMismatchError
			}
			from, ok := pathMapping["from"].(string)
			if !ok {
				return typeMismatchError
			}
			to, ok := pathMapping["to"].(string)
			if !ok {
				return typeMismatchError
			}
			clientToServer = append(clientToServer, [2]string{from, to})
			serverToClient = append(serverToClient, [2]string{to, from})
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
func (s *Server) Stop() {
	s.log.Debug("DAP server stopping...")
	close(s.stopTriggered)
	_ = s.listener.Close()

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.debugger != nil {
		killProcess := s.config.Debugger.AttachPid == 0
		s.stopDebugSession(killProcess)
	} else {
		s.stopNoDebugProcess()
	}
	// The binary is no longer in use by the debugger. It is safe to remove it.
	if s.binaryToRemove != "" {
		gobuild.Remove(s.binaryToRemove)
	}
	// Close client connection last, so other shutdown stages
	// can send client notifications
	if s.conn != nil {
		// Unless Stop() was called after serveDAPCodec()
		// returned, this will result in closed connection error
		// on next read, breaking out of the read loop and
		// allowing the run goroutine to exit.
		_ = s.conn.Close()
	}
	s.log.Debug("DAP server stopped")
}

// triggerServerStop closes config.DisconnectChan if not nil, which
// signals that client sent a disconnect request or there was connection
// failure or closure. Since the server currently services only one
// client, this is used as a signal to stop the entire server.
// The function safeguards agaist closing the channel more
// than once and can be called multiple times. It is not thread-safe
// and is currently only called from the run goroutine.
func (s *Server) triggerServerStop() {
	// Avoid accidentally closing the channel twice and causing a panic, when
	// this function is called more than once. For example, we could have the
	// following sequence of events:
	// -- run goroutine: calls onDisconnectRequest()
	// -- run goroutine: calls triggerServerStop()
	// -- main goroutine: calls Stop()
	// -- main goroutine: Stop() closes client connection (or client closed it)
	// -- run goroutine: serveDAPCodec() gets "closed network connection"
	// -- run goroutine: serveDAPCodec() returns and calls triggerServerStop()
	if s.config.DisconnectChan != nil {
		close(s.config.DisconnectChan)
		s.config.DisconnectChan = nil
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
// so the editor needs to launch delve only once?
func (s *Server) Run() {
	go func() {
		conn, err := s.listener.Accept() // listener is closed in Stop()
		if err != nil {
			select {
			case <-s.stopTriggered:
			default:
				s.log.Errorf("Error accepting client connection: %s\n", err)
				s.triggerServerStop()
			}
			return
		}
		if s.config.CheckLocalConnUser {
			if !sameuser.CanAccept(s.listener.Addr(), conn.RemoteAddr()) {
				s.log.Error("Error accepting client connection: Only connections from the same user that started this instance of Delve are allowed to connect. See --only-same-user.")
				s.triggerServerStop()
				return
			}
		}
		s.mu.Lock()
		s.conn = conn // closed in Stop()
		s.mu.Unlock()
		s.serveDAPCodec()
	}()
}

// serveDAPCodec reads and decodes requests from the client
// until it encounters an error or EOF, when it sends
// a disconnect signal and returns.
func (s *Server) serveDAPCodec() {
	s.reader = bufio.NewReader(s.conn)
	for {
		request, err := dap.ReadProtocolMessage(s.reader)
		// Handle dap.DecodeProtocolMessageFieldError errors gracefully by responding with an ErrorResponse.
		// For example:
		// -- "Request command 'foo' is not supported" means we
		// potentially got some new DAP request that we do not yet have
		// decoding support for, so we can respond with an ErrorResponse.
		//
		// Other errors, such as unmarshalling errors, will log the error and cause the server to trigger
		// a stop.
		if err != nil {
			select {
			case <-s.stopTriggered:
			default:
				if err != io.EOF {
					if decodeErr, ok := err.(*dap.DecodeProtocolMessageFieldError); ok {
						// Send an error response to the users if we were unable to process the message.
						s.sendInternalErrorResponse(decodeErr.Seq, err.Error())
						continue
					}
					s.log.Error("DAP error: ", err)
				}
				s.triggerServerStop()
			}
			return
		}
		s.handleRequest(request)
	}
}

// In case a handler panics, we catch the panic to avoid crashing both
// the server and the target. We send an error response back, but
// in case its a dup and ignored by the client, we also log the error.
func (s *Server) recoverPanic(request dap.Message) {
	if ierr := recover(); ierr != nil {
		s.log.Errorf("recovered panic: %s\n%s\n", ierr, debug.Stack())
		s.sendInternalErrorResponse(request.GetSeq(), fmt.Sprintf("%v", ierr))
	}
}

func (s *Server) handleRequest(request dap.Message) {
	defer s.recoverPanic(request)
	// Lock resumeMu so that another goroutine cannot unexpectedly start
	// the debuggee.
	jsonmsg, _ := json.Marshal(request)
	s.log.Debug("[<- from client]", string(jsonmsg))

	if _, ok := request.(dap.RequestMessage); !ok {
		s.sendInternalErrorResponse(request.GetSeq(), fmt.Sprintf("Unable to process non-request %#v\n", request))
		return
	}

	// These requests, can be handled regardless of whether the targret is running
	switch request := request.(type) {
	case *dap.DisconnectRequest:
		// Required
		s.onDisconnectRequest(request)
		return
	case *dap.PauseRequest:
		// Required
		s.onPauseRequest(request)
		return
	case *dap.TerminateRequest:
		// Optional (capability ‘supportsTerminateRequest‘)
		// TODO: implement this request in V1
		s.onTerminateRequest(request)
		return
	case *dap.RestartRequest:
		// Optional (capability ‘supportsRestartRequest’)
		// TODO: implement this request in V1
		s.onRestartRequest(request)
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
	if s.debugger != nil && s.isRunning() {
		s.resumeMu.Lock()
		defer s.resumeMu.Unlock()
		switch request := request.(type) {
		case *dap.ThreadsRequest:
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
		case *dap.SetBreakpointsRequest:
			s.log.Debug("halting execution to set breakpoints")
			_, err := s.debugger.Command(&api.DebuggerCommand{Name: api.Halt}, nil)
			if err != nil {
				s.sendErrorResponse(request.Request, UnableToSetBreakpoints, "Unable to set or clear breakpoints", err.Error())
				return
			}
			s.logToConsole("Execution halted to set breakpoints - please resume execution manually")
			s.onSetBreakpointsRequest(request)
			// TODO(polina): consider resuming execution here automatically after suppressing
			// a stop event when an operation in doRunCommand returns. In case that operation
			// was already stopping for a different reason, we would need to examine the state
			// that is returned to determine if this halt was the cause of the stop or not.
			// We should stop with an event and not resume if one of the following is true:
			// - StopReason is anything but manual
			// - Any thread has a breakpoint or CallReturn set
			// - NextInProgress is false and the last command sent by the user was: next,
			//   step, stepOut, reverseNext, reverseStep or reverseStepOut
			// Otherwise, we can skip the stop event and resume the temporarily
			// interrupted process execution with api.DirectionCongruentContinue.
			// For this to apply in cases other than api.Continue, we would also need to
			// introduce a new version of halt that skips ClearInternalBreakpoints
			// in proc.(*Target).Continue, leaving NextInProgress as true.
		case *dap.SetFunctionBreakpointsRequest:
			s.log.Debug("halting execution to set breakpoints")
			_, err := s.debugger.Command(&api.DebuggerCommand{Name: api.Halt}, nil)
			if err != nil {
				s.sendErrorResponse(request.Request, UnableToSetBreakpoints, "Unable to set or clear breakpoints", err.Error())
				return
			}
			s.logToConsole("Execution halted to set breakpoints - please resume execution manually")
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
	case *dap.ConfigurationDoneRequest:
		// Optional (capability ‘supportsConfigurationDoneRequest’)
		go func() {
			defer s.recoverPanic(request)
			s.onConfigurationDoneRequest(request, resumeRequestLoop)
		}()
		<-resumeRequestLoop
	case *dap.ContinueRequest:
		// Required
		go func() {
			defer s.recoverPanic(request)
			s.onContinueRequest(request, resumeRequestLoop)
		}()
		<-resumeRequestLoop
	case *dap.NextRequest:
		// Required
		go func() {
			defer s.recoverPanic(request)
			s.onNextRequest(request, resumeRequestLoop)
		}()
		<-resumeRequestLoop
	case *dap.StepInRequest:
		// Required
		go func() {
			defer s.recoverPanic(request)
			s.onStepInRequest(request, resumeRequestLoop)
		}()
		<-resumeRequestLoop
	case *dap.StepOutRequest:
		// Required
		go func() {
			defer s.recoverPanic(request)
			s.onStepOutRequest(request, resumeRequestLoop)
		}()
		<-resumeRequestLoop
	case *dap.StepBackRequest:
		// Optional (capability ‘supportsStepBack’)
		go func() {
			defer s.recoverPanic(request)
			s.onStepBackRequest(request, resumeRequestLoop)
		}()
		<-resumeRequestLoop
	case *dap.ReverseContinueRequest:
		// Optional (capability ‘supportsStepBack’)
		go func() {
			defer s.recoverPanic(request)
			s.onReverseContinueRequest(request, resumeRequestLoop)
		}()
		<-resumeRequestLoop
	//--- Synchronous requests ---
	case *dap.InitializeRequest:
		// Required
		s.onInitializeRequest(request)
	case *dap.LaunchRequest:
		// Required
		s.onLaunchRequest(request)
	case *dap.AttachRequest:
		// Required
		s.onAttachRequest(request)
	case *dap.SetBreakpointsRequest:
		// Required
		s.onSetBreakpointsRequest(request)
	case *dap.SetFunctionBreakpointsRequest:
		// Optional (capability ‘supportsFunctionBreakpoints’)
		s.onSetFunctionBreakpointsRequest(request)
	case *dap.SetExceptionBreakpointsRequest:
		// Optional (capability ‘exceptionBreakpointFilters’)
		s.onSetExceptionBreakpointsRequest(request)
	case *dap.ThreadsRequest:
		// Required
		s.onThreadsRequest(request)
	case *dap.StackTraceRequest:
		// Required
		s.onStackTraceRequest(request)
	case *dap.ScopesRequest:
		// Required
		s.onScopesRequest(request)
	case *dap.VariablesRequest:
		// Required
		s.onVariablesRequest(request)
	case *dap.EvaluateRequest:
		// Required
		s.onEvaluateRequest(request)
	case *dap.SetVariableRequest:
		// Optional (capability ‘supportsSetVariable’)
		// Supported by vscode-go
		// TODO: implement this request in V0
		s.onSetVariableRequest(request)
	case *dap.SetExpressionRequest:
		// Optional (capability ‘supportsSetExpression’)
		// TODO: implement this request in V1
		s.onSetExpressionRequest(request)
	case *dap.LoadedSourcesRequest:
		// Optional (capability ‘supportsLoadedSourcesRequest’)
		// TODO: implement this request in V1
		s.onLoadedSourcesRequest(request)
	case *dap.ReadMemoryRequest:
		// Optional (capability ‘supportsReadMemoryRequest‘)
		// TODO: implement this request in V1
		s.onReadMemoryRequest(request)
	case *dap.DisassembleRequest:
		// Optional (capability ‘supportsDisassembleRequest’)
		// TODO: implement this request in V1
		s.onDisassembleRequest(request)
	case *dap.CancelRequest:
		// Optional (capability ‘supportsCancelRequest’)
		// TODO: does this request make sense for delve?
		s.onCancelRequest(request)
	case *dap.ExceptionInfoRequest:
		// Optional (capability ‘supportsExceptionInfoRequest’)
		s.onExceptionInfoRequest(request)
	//--- Requests that we do not plan to support ---
	case *dap.RestartFrameRequest:
		// Optional (capability ’supportsRestartFrame’)
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.GotoRequest:
		// Optional (capability ‘supportsGotoTargetsRequest’)
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.SourceRequest:
		// Required
		// This does not make sense in the context of Go as
		// the source cannot be a string eval'ed at runtime.
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.TerminateThreadsRequest:
		// Optional (capability ‘supportsTerminateThreadsRequest’)
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.StepInTargetsRequest:
		// Optional (capability ‘supportsStepInTargetsRequest’)
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.GotoTargetsRequest:
		// Optional (capability ‘supportsGotoTargetsRequest’)
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.CompletionsRequest:
		// Optional (capability ‘supportsCompletionsRequest’)
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.DataBreakpointInfoRequest:
		// Optional (capability ‘supportsDataBreakpoints’)
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.SetDataBreakpointsRequest:
		// Optional (capability ‘supportsDataBreakpoints’)
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.BreakpointLocationsRequest:
		// Optional (capability ‘supportsBreakpointLocationsRequest’)
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.ModulesRequest:
		// Optional (capability ‘supportsModulesRequest’)
		// TODO: does this request make sense for delve?
		s.sendUnsupportedErrorResponse(request.Request)
	default:
		// This is a DAP message that go-dap has a struct for, so
		// decoding succeeded, but this function does not know how
		// to handle.
		s.sendInternalErrorResponse(request.GetSeq(), fmt.Sprintf("Unable to process %#v\n", request))
	}
}

func (s *Server) send(message dap.Message) {
	jsonmsg, _ := json.Marshal(message)
	s.log.Debug("[-> to client]", string(jsonmsg))
	// TODO(polina): consider using a channel for all the sends and to have a dedicated
	// goroutine that reads from that channel and sends over the connection.
	// This will avoid blocking on slow network sends.
	s.sendingMu.Lock()
	defer s.sendingMu.Unlock()
	_ = dap.WriteProtocolMessage(s.conn, message)
}

func (s *Server) logToConsole(msg string) {
	s.send(&dap.OutputEvent{
		Event: *newEvent("output"),
		Body: dap.OutputEventBody{
			Output:   msg + "\n",
			Category: "console",
		}})
}

func (s *Server) onInitializeRequest(request *dap.InitializeRequest) {
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

	// TODO(polina): Respond with an error if debug session is in progress?
	response := &dap.InitializeResponse{Response: *newResponse(request.Request)}
	response.Body.SupportsConfigurationDoneRequest = true
	response.Body.SupportsConditionalBreakpoints = true
	response.Body.SupportsDelayedStackTraceLoading = true
	response.Body.SupportTerminateDebuggee = true
	response.Body.SupportsFunctionBreakpoints = true
	response.Body.SupportsExceptionInfoRequest = true
	response.Body.SupportsSetVariable = true
	response.Body.SupportsEvaluateForHovers = true
	response.Body.SupportsClipboardContext = true
	response.Body.SupportsLogPoints = true
	// TODO(polina): support these requests in addition to vscode-go feature parity
	response.Body.SupportsTerminateRequest = false
	response.Body.SupportsRestartRequest = false
	response.Body.SupportsStepBack = false // To be enabled by CapabilitiesEvent based on configuration
	response.Body.SupportsSetExpression = false
	response.Body.SupportsLoadedSourcesRequest = false
	response.Body.SupportsReadMemoryRequest = false
	response.Body.SupportsDisassembleRequest = false
	response.Body.SupportsCancelRequest = false
	s.send(response)
}

func (s *Server) setClientCapabilities(args dap.InitializeRequestArguments) {
	s.clientCapabilities.supportsMemoryReferences = args.SupportsMemoryReferences
	s.clientCapabilities.supportsProgressReporting = args.SupportsProgressReporting
	s.clientCapabilities.supportsRunInTerminalRequest = args.SupportsRunInTerminalRequest
	s.clientCapabilities.supportsVariablePaging = args.SupportsVariablePaging
	s.clientCapabilities.supportsVariableType = args.SupportsVariableType
}

// Default output file pathname for the compiled binary in debug or test modes,
// relative to the current working directory of the server.
const defaultDebugBinary string = "./__debug_bin"

func cleanExeName(name string) string {
	if runtime.GOOS == "windows" && filepath.Ext(name) != ".exe" {
		return name + ".exe"
	}
	return name
}

func (s *Server) onLaunchRequest(request *dap.LaunchRequest) {
	// Validate launch request mode
	mode, ok := request.Arguments["mode"]
	if !ok || mode == "" {
		mode = "debug"
	}
	if !isValidLaunchMode(mode) {
		s.sendErrorResponse(request.Request,
			FailedToLaunch, "Failed to launch",
			fmt.Sprintf("Unsupported 'mode' value %q in debug configuration.", mode))
		return
	}

	// TODO(polina): Respond with an error if debug session is in progress?
	program, ok := request.Arguments["program"].(string)
	if (!ok || program == "") && mode != "replay" { // Only fail on modes requiring a program
		s.sendErrorResponse(request.Request,
			FailedToLaunch, "Failed to launch",
			"The program attribute is missing in debug configuration.")
		return
	}

	backend, ok := request.Arguments["backend"]
	if ok {
		backendParsed, ok := backend.(string)
		if !ok {
			s.sendErrorResponse(request.Request,
				FailedToLaunch, "Failed to launch",
				fmt.Sprintf("'backend' attribute '%v' in debug configuration is not a string.", backend))
			return
		}
		s.config.Debugger.Backend = backendParsed
	} else {
		s.config.Debugger.Backend = "default"
	}

	if mode == "replay" {
		traceDirPath, _ := request.Arguments["traceDirPath"].(string)

		// Validate trace directory
		if traceDirPath == "" {
			s.sendErrorResponse(request.Request,
				FailedToLaunch, "Failed to launch",
				"The 'traceDirPath' attribute is missing in debug configuration.")
			return
		}

		// Assign the rr trace directory path to debugger configuration
		s.config.Debugger.CoreFile = traceDirPath
		s.config.Debugger.Backend = "rr"
	}

	if mode == "core" {
		coreFilePath, _ := request.Arguments["coreFilePath"].(string)

		// Validate core dump path
		if coreFilePath == "" {

			s.sendErrorResponse(request.Request,
				FailedToLaunch, "Failed to launch",
				"The 'coreFilePath' attribute is missing in debug configuration.")
			return
		}

		// Assign the non-empty core file path to debugger configuration. This will
		// trigger a native core file replay instead of an rr trace replay
		s.config.Debugger.CoreFile = coreFilePath
		s.config.Debugger.Backend = "core"
	}

	s.log.Debugf("debug backend is '%s'", s.config.Debugger.Backend)

	// Prepare the debug executable filename, build flags and build it
	if mode == "debug" || mode == "test" {
		output, ok := request.Arguments["output"].(string)
		if !ok || output == "" {
			output = defaultDebugBinary
		}
		output = cleanExeName(output)
		debugbinary, err := filepath.Abs(output)
		if err != nil {
			s.sendInternalErrorResponse(request.Seq, err.Error())
			return
		}

		buildFlags := ""
		buildFlagsArg, ok := request.Arguments["buildFlags"]
		if ok {
			buildFlags, ok = buildFlagsArg.(string)
			if !ok {
				s.sendErrorResponse(request.Request,
					FailedToLaunch, "Failed to launch",
					fmt.Sprintf("'buildFlags' attribute '%v' in debug configuration is not a string.", buildFlagsArg))
				return
			}
		}

		var cmd string
		var out []byte
		// Log debug binary build
		s.log.Debugf("building binary '%s' from '%s' with flags '%v'", debugbinary, program, buildFlags)
		switch mode {
		case "debug":
			cmd, out, err = gobuild.GoBuildCombinedOutput(debugbinary, []string{program}, buildFlags)
		case "test":
			cmd, out, err = gobuild.GoTestBuildCombinedOutput(debugbinary, []string{program}, buildFlags)
		}
		if err != nil {
			s.send(&dap.OutputEvent{
				Event: *newEvent("output"),
				Body: dap.OutputEventBody{
					Output:   fmt.Sprintf("Build Error: %s\n%s (%s)\n", cmd, strings.TrimSpace(string(out)), err.Error()),
					Category: "stderr",
				}})
			s.sendErrorResponse(request.Request,
				FailedToLaunch, "Failed to launch",
				"Build error: Check the debug console for details.")
			return
		}
		program = debugbinary
		s.mu.Lock()
		s.binaryToRemove = debugbinary
		s.mu.Unlock()
	}

	err := s.setLaunchAttachArgs(request)
	if err != nil {
		s.sendErrorResponse(request.Request,
			FailedToLaunch, "Failed to launch",
			err.Error())
		return
	}

	var targetArgs []string
	args, ok := request.Arguments["args"]
	if ok {
		argsParsed, ok := args.([]interface{})
		if !ok {
			s.sendErrorResponse(request.Request,
				FailedToLaunch, "Failed to launch",
				fmt.Sprintf("'args' attribute '%v' in debug configuration is not an array.", args))
			return
		}
		for _, arg := range argsParsed {
			argParsed, ok := arg.(string)
			if !ok {
				s.sendErrorResponse(request.Request,
					FailedToLaunch, "Failed to launch",
					fmt.Sprintf("value '%v' in 'args' attribute in debug configuration is not a string.", arg))
				return
			}
			targetArgs = append(targetArgs, argParsed)
		}
	}

	s.config.ProcessArgs = append([]string{program}, targetArgs...)
	s.config.Debugger.WorkingDir = filepath.Dir(program)

	// Set the WorkingDir for this program to the one specified in the request arguments.
	wd, ok := request.Arguments["cwd"]
	if ok {
		wdParsed, ok := wd.(string)
		if !ok {
			s.sendErrorResponse(request.Request,
				FailedToLaunch, "Failed to launch",
				fmt.Sprintf("'cwd' attribute '%v' in debug configuration is not a string.", wd))
			return
		}
		s.config.Debugger.WorkingDir = wdParsed
	}

	s.log.Debugf("running program in %s\n", s.config.Debugger.WorkingDir)
	if noDebug, ok := request.Arguments["noDebug"].(bool); ok && noDebug {
		s.mu.Lock()
		cmd, err := s.startNoDebugProcess(program, targetArgs, s.config.Debugger.WorkingDir)
		s.mu.Unlock()
		if err != nil {
			s.sendErrorResponse(request.Request, FailedToLaunch, "Failed to launch", err.Error())
			return
		}
		// Skip 'initialized' event, which will prevent the client from sending
		// debug-related requests.
		s.send(&dap.LaunchResponse{Response: *newResponse(request.Request)})

		// Then, block until the program terminates or is stopped.
		if err := cmd.Wait(); err != nil {
			s.log.Debugf("program exited with error: %v", err)
		}
		stopped := false
		s.mu.Lock()
		stopped = s.noDebugProcess == nil // if it was stopped, this should be nil.
		s.noDebugProcess = nil
		s.mu.Unlock()

		if !stopped {
			s.logToConsole(proc.ErrProcessExited{Pid: cmd.ProcessState.Pid(), Status: cmd.ProcessState.ExitCode()}.Error())
			s.send(&dap.TerminatedEvent{Event: *newEvent("terminated")})
		}
		return
	}

	func() {
		s.mu.Lock()
		defer s.mu.Unlock() // Make sure to unlock in case of panic that will become internal error
		s.debugger, err = debugger.New(&s.config.Debugger, s.config.ProcessArgs)
	}()
	if err != nil {
		s.sendErrorResponse(request.Request, FailedToLaunch, "Failed to launch", err.Error())
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

// startNoDebugProcess is called from onLaunchRequest (run goroutine) and
// requires holding mu lock.
func (s *Server) startNoDebugProcess(program string, targetArgs []string, wd string) (*exec.Cmd, error) {
	if s.noDebugProcess != nil {
		return nil, fmt.Errorf("another launch request is in progress")
	}
	cmd := exec.Command(program, targetArgs...)
	cmd.Stdout, cmd.Stderr, cmd.Stdin, cmd.Dir = os.Stdout, os.Stderr, os.Stdin, wd
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	s.noDebugProcess = cmd
	return cmd, nil
}

// stopNoDebugProcess is called from Stop (main goroutine) and
// onDisconnectRequest (run goroutine) and requires holding mu lock.
func (s *Server) stopNoDebugProcess() {
	if s.noDebugProcess == nil {
		// We already handled termination or there was never a process
		return
	}
	if s.noDebugProcess.ProcessState.Exited() {
		s.logToConsole(proc.ErrProcessExited{Pid: s.noDebugProcess.ProcessState.Pid(), Status: s.noDebugProcess.ProcessState.ExitCode()}.Error())
	} else {
		// TODO(hyangah): gracefully terminate the process and its children processes.
		s.logToConsole(fmt.Sprintf("Terminating process %d", s.noDebugProcess.Process.Pid))
		s.noDebugProcess.Process.Kill() // Don't check error. Process killing and self-termination may race.
	}
	s.noDebugProcess = nil
}

// Launch debug sessions support the following modes:
// -- [DEFAULT] "debug" - builds and launches debugger for specified program (similar to 'dlv debug')
//      Required args: program
//      Optional args with default: output, cwd, noDebug
//      Optional args: buildFlags, args
// -- "test" - builds and launches debugger for specified test (similar to 'dlv test')
//      same args as above
// -- "exec" - launches debugger for precompiled binary (similar to 'dlv exec')
//      Required args: program
//      Optional args with default: cwd, noDebug
//      Optional args: args
// -- replay: skips program build and sets the Debugger.CoreFile property based on the
//		Required args: coreFilePath
//      Optional args: program, args
// -- core: skips program build and sets the Debugger.CoreFile property based on the
//      Required args: program, traceDirPath
//      Optional args: args
func isValidLaunchMode(mode interface{}) bool {
	switch mode {
	case "exec", "debug", "test", "replay", "core":
		return true
	}
	return false
}

// Attach debug sessions support the following modes:
// -- [DEFAULT] "local" -- attaches debugger to a local running process
//      Required args: processId
// TODO(polina): support "remote" mode
func isValidAttachMode(mode interface{}) bool {
	return mode == "local"
}

// onDisconnectRequest handles the DisconnectRequest. Per the DAP spec,
// it disconnects the debuggee and signals that the debug adaptor
// (in our case this TCP server) can be terminated.
func (s *Server) onDisconnectRequest(request *dap.DisconnectRequest) {
	defer s.triggerServerStop()
	s.mu.Lock()
	defer s.mu.Unlock()

	var err error
	if s.debugger != nil {
		// We always kill launched programs.
		// In case of attach, we leave the program
		// running by default, which can be
		// overridden by an explicit request to terminate.
		killProcess := s.config.Debugger.AttachPid == 0 || request.Arguments.TerminateDebuggee
		err = s.stopDebugSession(killProcess)
	} else {
		s.stopNoDebugProcess()
	}
	if err != nil {
		s.sendErrorResponse(request.Request, DisconnectError, "Error while disconnecting", err.Error())
	} else {
		s.send(&dap.DisconnectResponse{Response: *newResponse(request.Request)})
	}
	// The debugging session has ended, so we send a terminated event.
	s.send(&dap.TerminatedEvent{
		Event: *newEvent("terminated"),
	})
}

// stopDebugSession is called from Stop (main goroutine) and
// onDisconnectRequest (run goroutine) and requires holding mu lock.
// Returns any detach error other than proc.ErrProcessExited.
func (s *Server) stopDebugSession(killProcess bool) error {
	if s.debugger == nil {
		return nil
	}
	var err error
	var exited error
	// Halting will stop any debugger command that's pending on another
	// per-request goroutine, hence unblocking that goroutine to wrap-up and exit.
	// TODO(polina): Per-request goroutine could still not be done when this one is.
	// To avoid goroutine leaks, we can use a wait group or have the goroutine listen
	// for a stop signal on a dedicated quit channel at suitable points (use context?).
	// Additional clean-up might be especially critical when we support multiple clients.
	state, err := s.debugger.Command(&api.DebuggerCommand{Name: api.Halt}, nil)
	if err == proc.ErrProcessDetached {
		s.log.Debug("halt returned error: ", err)
		return nil
	}
	if err != nil {
		switch err.(type) {
		case proc.ErrProcessExited:
			exited = err
		default:
			s.log.Error("halt returned error: ", err)
			if err.Error() == "no such process" {
				exited = err
			}
		}
	} else if state.Exited {
		exited = proc.ErrProcessExited{Pid: s.debugger.ProcessPid(), Status: state.ExitStatus}
		s.log.Debug("halt returned state: ", exited)
	}
	if exited != nil {
		s.logToConsole(exited.Error())
		s.logToConsole("Detaching")
	} else if killProcess {
		s.logToConsole("Detaching and terminating target process")
	} else {
		s.logToConsole("Detaching without terminating target processs")
	}
	err = s.debugger.Detach(killProcess)
	s.debugger = nil
	if err != nil {
		switch err.(type) {
		case proc.ErrProcessExited:
			s.log.Debug(err)
			s.logToConsole(exited.Error())
			err = nil
		default:
			s.log.Error("detach returned error: ", err)
		}
	}
	return err
}

func (s *Server) isNoDebug() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.noDebugProcess != nil
}

func (s *Server) onSetBreakpointsRequest(request *dap.SetBreakpointsRequest) {
	if s.isNoDebug() {
		s.sendErrorResponse(request.Request, UnableToSetBreakpoints, "Unable to set or clear breakpoints", "running in noDebug mode")
		return
	}

	if request.Arguments.Source.Path == "" {
		s.sendErrorResponse(request.Request, UnableToSetBreakpoints, "Unable to set or clear breakpoints", "empty file path")
		return
	}

	clientPath := request.Arguments.Source.Path
	serverPath := s.toServerPath(clientPath)

	// According to the spec we should "set multiple breakpoints for a single source
	// and clear all previous breakpoints in that source." The simplest way is
	// to clear all and then set all. To maintain state (for hit count conditions)
	// we want to amend existing breakpoints.
	//
	// See https://github.com/golang/vscode-go/issues/163 for details.
	// If a breakpoint:
	// -- exists and not in request => ClearBreakpoint
	// -- exists and in request => AmendBreakpoint
	// -- doesn't exist and in request => SetBreakpoint

	// Get all existing breakpoints that match for this source.
	sourceRequestPrefix := fmt.Sprintf("sourceBp Path=%q ", request.Arguments.Source.Path)
	existingBps := s.getMatchingBreakpoints(sourceRequestPrefix)
	bpAdded := make(map[string]struct{}, len(existingBps))

	// Amend existing breakpoints.
	breakpoints := make([]dap.Breakpoint, len(request.Arguments.Breakpoints))
	for i, want := range request.Arguments.Breakpoints {
		reqString := fmt.Sprintf("%s Line=%d Column=%d", sourceRequestPrefix, want.Line, want.Column)
		var err error
		got, ok := existingBps[reqString]
		if !ok {
			// Skip if the breakpoint does not already exist.
			// These will be created after deleting existing
			// breakpoints to avoid conflicts.
			continue
		}
		if _, ok := bpAdded[reqString]; ok {
			err = fmt.Errorf("breakpoint exists at %q, line: %d, column: %d", request.Arguments.Source.Path, want.Line, want.Column)
		} else {
			got.Cond = want.Condition
			got.HitCond = want.HitCondition
			got.Tracepoint = want.LogMessage != ""
			got.UserData = want.LogMessage
			err = s.debugger.AmendBreakpoint(got)
			bpAdded[reqString] = struct{}{}
		}

		updateBreakpointsResponse(breakpoints, i, err, got, clientPath)
	}

	// Clear existing breakpoints that were not added.
	err := s.clearBreakpoints(existingBps, bpAdded)
	if err != nil {
		s.sendErrorResponse(request.Request, UnableToSetBreakpoints, "Unable to set or clear breakpoints", err.Error())
		return
	}

	for i, want := range request.Arguments.Breakpoints {
		reqString := fmt.Sprintf("%s Line=%d Column=%d", sourceRequestPrefix, want.Line, want.Column)
		if _, ok := existingBps[reqString]; ok {
			continue
		}

		var got *api.Breakpoint
		var err error
		if _, ok := bpAdded[reqString]; ok {
			err = fmt.Errorf("breakpoint exists at %q, line: %d, column: %d", request.Arguments.Source.Path, want.Line, want.Column)
		} else {
			// Create new breakpoints.
			got, err = s.debugger.CreateBreakpoint(
				&api.Breakpoint{
					File:       serverPath,
					Line:       want.Line,
					Cond:       want.Condition,
					HitCond:    want.HitCondition,
					Name:       reqString,
					Tracepoint: want.LogMessage != "",
					UserData:   want.LogMessage,
				})
			bpAdded[reqString] = struct{}{}
		}

		updateBreakpointsResponse(breakpoints, i, err, got, clientPath)
	}

	response := &dap.SetBreakpointsResponse{Response: *newResponse(request.Request)}
	response.Body.Breakpoints = breakpoints

	s.send(response)
}

func updateBreakpointsResponse(breakpoints []dap.Breakpoint, i int, err error, got *api.Breakpoint, path string) {
	breakpoints[i].Verified = (err == nil)
	if err != nil {
		breakpoints[i].Message = err.Error()
	} else {
		breakpoints[i].Id = got.ID
		breakpoints[i].Line = got.Line
		breakpoints[i].Source = dap.Source{Name: filepath.Base(path), Path: path}
	}
}

// functionBpPrefix is the prefix of bp.Name for every breakpoint bp set
// in this request.
const functionBpPrefix = "functionBreakpoint"

func (s *Server) onSetFunctionBreakpointsRequest(request *dap.SetFunctionBreakpointsRequest) {
	if s.noDebugProcess != nil {
		s.sendErrorResponse(request.Request, UnableToSetBreakpoints, "Unable to set or clear breakpoints", "running in noDebug mode")
		return
	}

	// According to the spec, setFunctionBreakpoints "replaces all existing function
	// breakpoints with new function breakpoints." The simplest way is
	// to clear all and then set all. To maintain state (for hit count conditions)
	// we want to amend existing breakpoints.
	//
	// See https://github.com/golang/vscode-go/issues/163 for details.
	// If a breakpoint:
	// -- exists and not in request => ClearBreakpoint
	// -- exists and in request => AmendBreakpoint
	// -- doesn't exist and in request => SetBreakpoint

	// Get all existing function breakpoints.
	existingBps := s.getMatchingBreakpoints(functionBpPrefix)
	bpAdded := make(map[string]struct{}, len(existingBps))
	for _, bp := range existingBps {
		existingBps[bp.Name] = bp
	}

	// Amend any existing breakpoints.
	breakpoints := make([]dap.Breakpoint, len(request.Arguments.Breakpoints))
	for i, want := range request.Arguments.Breakpoints {
		reqString := fmt.Sprintf("%s Name=%s", functionBpPrefix, want.Name)
		var err error
		got, ok := existingBps[reqString]
		if !ok {
			// Skip if the breakpoint does not already exist.
			// These will be created after deleting existing
			// breakpoints to avoid conflicts.
			continue
		}
		if _, ok := bpAdded[reqString]; ok {
			err = fmt.Errorf("breakpoint exists at function %q", want.Name)
		} else {
			got.Cond = want.Condition
			got.HitCond = want.HitCondition
			err = s.debugger.AmendBreakpoint(got)
			bpAdded[reqString] = struct{}{}
		}

		var clientPath string
		if got != nil {
			clientPath = s.toClientPath(got.File)
		}
		updateBreakpointsResponse(breakpoints, i, err, got, clientPath)
	}

	// Clear existing breakpoints that were not added.
	err := s.clearBreakpoints(existingBps, bpAdded)
	if err != nil {
		s.sendErrorResponse(request.Request, UnableToSetBreakpoints, "Unable to set or clear breakpoints", err.Error())
		return
	}

	// Create new breakpoints.
	for i, want := range request.Arguments.Breakpoints {
		reqString := fmt.Sprintf("%s Name=%s", functionBpPrefix, want.Name)
		if _, ok := existingBps[reqString]; ok {
			// Amend existing breakpoints.
			continue
		}

		// Set the function breakpoint breakpoint
		spec, err := locspec.Parse(want.Name)
		if err != nil {
			breakpoints[i].Message = err.Error()
			continue
		}
		if loc, ok := spec.(*locspec.NormalLocationSpec); !ok || loc.FuncBase == nil {
			// Other locations do not make sense in the context of function breakpoints.
			// Regex locations are likely to resolve to multiple places and offset locations
			// are only meaningful at the time the breakpoint was created.
			breakpoints[i].Message = fmt.Sprintf("breakpoint name %q could not be parsed as a function. name must be in the format 'funcName', 'funcName:line' or 'fileName:line'.", want.Name)
			continue
		}

		if want.Name[0] == '.' {
			breakpoints[i].Message = "breakpoint names that are relative paths are not supported."
			continue
		}
		// Find the location of the function name. CreateBreakpoint requires the name to include the base
		// (e.g. main.functionName is supported but not functionName).
		// We first find the location of the function, and then set breakpoints for that location.
		var locs []api.Location
		locs, err = s.debugger.FindLocationSpec(-1, 0, 0, want.Name, spec, true, s.args.substitutePathClientToServer)
		if err != nil {
			breakpoints[i].Message = err.Error()
			continue
		}
		if len(locs) == 0 {
			breakpoints[i].Message = fmt.Sprintf("no location found for %q", want.Name)
			continue
		}
		if len(locs) > 0 {
			s.log.Debugf("multiple locations found for %s", want.Name)
			breakpoints[i].Message = fmt.Sprintf("multiple locations found for %s, function breakpoint is only set for the first location", want.Name)
		}

		// Set breakpoint using the PCs that were found.
		loc := locs[0]
		got, err := s.debugger.CreateBreakpoint(&api.Breakpoint{Addr: loc.PC, Addrs: loc.PCs, Cond: want.Condition, Name: reqString})

		var clientPath string
		if got != nil {
			clientPath = s.toClientPath(got.File)
		}
		updateBreakpointsResponse(breakpoints, i, err, got, clientPath)
	}

	response := &dap.SetFunctionBreakpointsResponse{Response: *newResponse(request.Request)}
	response.Body.Breakpoints = breakpoints

	s.send(response)
}

func (s *Server) clearBreakpoints(existingBps map[string]*api.Breakpoint, bpAdded map[string]struct{}) error {
	for req, bp := range existingBps {
		if _, ok := bpAdded[req]; ok {
			continue
		}
		_, err := s.debugger.ClearBreakpoint(bp)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) getMatchingBreakpoints(prefix string) map[string]*api.Breakpoint {
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

func (s *Server) onSetExceptionBreakpointsRequest(request *dap.SetExceptionBreakpointsRequest) {
	// Unlike what DAP documentation claims, this request is always sent
	// even though we specified no filters at initialization. Handle as no-op.
	s.send(&dap.SetExceptionBreakpointsResponse{Response: *newResponse(request.Request)})
}

func (s *Server) maybeCloseChan(asyncSetupDone chan struct{}) {
	if asyncSetupDone != nil {
		select {
		case <-asyncSetupDone:
			// already closed
		default:
			close(asyncSetupDone)
		}
	}
}

// onConfigurationDoneRequest handles 'configurationDone' request.
// This is an optional request enabled by capability ‘supportsConfigurationDoneRequest’.
// It gets triggered after all the debug requests that followinitalized event,
// so the s.debugger is guaranteed to be set.
func (s *Server) onConfigurationDoneRequest(request *dap.ConfigurationDoneRequest, asyncSetupDone chan struct{}) {
	defer s.maybeCloseChan(asyncSetupDone)
	if s.args.stopOnEntry {
		e := &dap.StoppedEvent{
			Event: *newEvent("stopped"),
			Body:  dap.StoppedEventBody{Reason: "entry", ThreadId: 1, AllThreadsStopped: true},
		}
		s.send(e)
	}
	s.debugger.Target().KeepSteppingBreakpoints = proc.HaltKeepsSteppingBreakpoints | proc.TracepointKeepsSteppingBreakpoints

	s.send(&dap.ConfigurationDoneResponse{Response: *newResponse(request.Request)})
	if !s.args.stopOnEntry {
		s.doRunCommand(api.Continue, asyncSetupDone)
	}
}

// onContinueRequest handles 'continue' request.
// This is a mandatory request to support.
func (s *Server) onContinueRequest(request *dap.ContinueRequest, asyncSetupDone chan struct{}) {
	s.send(&dap.ContinueResponse{
		Response: *newResponse(request.Request),
		Body:     dap.ContinueResponseBody{AllThreadsContinued: true}})
	s.doRunCommand(api.Continue, asyncSetupDone)
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
func (s *Server) onThreadsRequest(request *dap.ThreadsRequest) {
	var err error
	var gs []*proc.G
	var next int
	if s.debugger != nil {
		gs, next, err = s.debugger.Goroutines(0, maxGoroutines)
	}
	threads := make([]dap.Thread, len(gs))

	if err != nil {
		switch err.(type) {
		case proc.ErrProcessExited:
			// If the program exits very quickly, the initial threads request will complete after it has exited.
			// A TerminatedEvent has already been sent. Ignore the err returned in this case.
			s.log.Debug(err)
		default:
			s.send(&dap.OutputEvent{
				Event: *newEvent("output"),
				Body: dap.OutputEventBody{
					Output:   fmt.Sprintf("Unable to retrieve goroutines: %s\n", err.Error()),
					Category: "stderr",
				}})
		}
		threads = []dap.Thread{{Id: 1, Name: "Dummy"}}
	} else if len(threads) == 0 {
		threads = []dap.Thread{{Id: 1, Name: "Dummy"}}
	} else {
		if next >= 0 {
			s.logToConsole(fmt.Sprintf("Too many goroutines, only loaded %d", len(gs)))
		}
		state, err := s.debugger.State( /*nowait*/ true)
		if err != nil {
			s.log.Debug("Unable to get debugger state: ", err)
		}
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
			threads[i].Id = g.ID
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
func (s *Server) onAttachRequest(request *dap.AttachRequest) {
	mode, ok := request.Arguments["mode"]
	if !ok || mode == "" {
		mode = "local"
	}
	if !isValidAttachMode(mode) {
		// TODO(polina): support 'remote' mode that expects a non-nil debugger
		s.sendErrorResponse(request.Request,
			FailedToAttach, "Failed to attach",
			fmt.Sprintf("Unsupported 'mode' value %q in debug configuration", mode))
		return
	}

	if mode == "local" {
		pid, ok := request.Arguments["processId"].(float64)
		if !ok || pid == 0 {
			s.sendErrorResponse(request.Request,
				FailedToAttach, "Failed to attach",
				"The 'processId' attribute is missing in debug configuration")
			return
		}
		s.config.Debugger.AttachPid = int(pid)
		err := s.setLaunchAttachArgs(request)
		if err != nil {
			s.sendErrorResponse(request.Request, FailedToAttach, "Failed to attach", err.Error())
			return
		}
		backend, ok := request.Arguments["backend"]
		if ok {
			backendParsed, ok := backend.(string)
			if !ok {
				s.sendErrorResponse(request.Request,
					FailedToAttach, "Failed to attach",
					fmt.Sprintf("'backend' attribute '%v' in debug configuration is not a string.", backend))
				return
			}
			s.config.Debugger.Backend = backendParsed
		} else {
			s.config.Debugger.Backend = "default"
		}

		func() {
			s.mu.Lock()
			defer s.mu.Unlock() // Make sure to unlock in case of panic that will become internal error
			s.debugger, err = debugger.New(&s.config.Debugger, nil)
		}()
		if err != nil {
			s.sendErrorResponse(request.Request, FailedToAttach, "Failed to attach", err.Error())
			return
		}
	}
	// Notify the client that the debugger is ready to start accepting
	// configuration requests for setting breakpoints, etc. The client
	// will end the configuration sequence with 'configurationDone'.
	s.send(&dap.InitializedEvent{Event: *newEvent("initialized")})
	s.send(&dap.AttachResponse{Response: *newResponse(request.Request)})
}

// onNextRequest handles 'next' request.
// This is a mandatory request to support.
func (s *Server) onNextRequest(request *dap.NextRequest, asyncSetupDone chan struct{}) {
	s.sendStepResponse(request.Arguments.ThreadId, &dap.NextResponse{Response: *newResponse(request.Request)})
	s.doStepCommand(api.Next, request.Arguments.ThreadId, asyncSetupDone)
}

// onStepInRequest handles 'stepIn' request
// This is a mandatory request to support.
func (s *Server) onStepInRequest(request *dap.StepInRequest, asyncSetupDone chan struct{}) {
	s.sendStepResponse(request.Arguments.ThreadId, &dap.StepInResponse{Response: *newResponse(request.Request)})
	s.doStepCommand(api.Step, request.Arguments.ThreadId, asyncSetupDone)
}

// onStepOutRequest handles 'stepOut' request
// This is a mandatory request to support.
func (s *Server) onStepOutRequest(request *dap.StepOutRequest, asyncSetupDone chan struct{}) {
	s.sendStepResponse(request.Arguments.ThreadId, &dap.StepOutResponse{Response: *newResponse(request.Request)})
	s.doStepCommand(api.StepOut, request.Arguments.ThreadId, asyncSetupDone)
}

func (s *Server) sendStepResponse(threadId int, message dap.Message) {
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

func stoppedGoroutineID(state *api.DebuggerState) (id int) {
	if state.SelectedGoroutine != nil {
		id = state.SelectedGoroutine.ID
	} else if state.CurrentThread != nil {
		id = state.CurrentThread.GoroutineID
	}
	return id
}

func stoppedOnBreakpointGoroutineID(state *api.DebuggerState) (id int) {
	for _, th := range state.Threads {
		if bp := th.Breakpoint; bp != nil {
			if !bp.Tracepoint {
				return th.GoroutineID
			}
		}
	}
	return stoppedGoroutineID(state)
}

// doStepCommand is a wrapper around doRunCommand that
// first switches selected goroutine. asyncSetupDone is
// a channel that will be closed to signal that an
// asynchornous command has completed setup or was interrupted
// due to an error, so the server is ready to receive new requests.
func (s *Server) doStepCommand(command string, threadId int, asyncSetupDone chan struct{}) {
	defer s.maybeCloseChan(asyncSetupDone)
	_, err := s.debugger.Command(&api.DebuggerCommand{Name: api.SwitchGoroutine, GoroutineID: threadId}, nil)
	if err != nil {
		s.log.Errorf("Error switching goroutines while stepping: %v", err)
		// If we encounter an error, we will have to send a stopped event
		// since we already sent the step response.
		stopped := &dap.StoppedEvent{Event: *newEvent("stopped")}
		stopped.Body.AllThreadsStopped = true
		if state, err := s.debugger.State(false); err != nil {
			s.log.Errorf("Error retrieving state: %e", err)
		} else {
			stopped.Body.ThreadId = stoppedGoroutineID(state)
		}
		stopped.Body.Reason = "error"
		stopped.Body.Text = err.Error()
		s.send(stopped)
		return
	}
	s.doRunCommand(command, asyncSetupDone)
}

// onPauseRequest handles 'pause' request.
// This is a mandatory request to support.
func (s *Server) onPauseRequest(request *dap.PauseRequest) {
	_, err := s.debugger.Command(&api.DebuggerCommand{Name: api.Halt}, nil)
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
func (s *Server) onStackTraceRequest(request *dap.StackTraceRequest) {
	if s.debugger == nil {
		s.sendErrorResponse(request.Request, UnableToProduceStackTrace, "Unable to produce stack trace", "debugger is nil")
		return
	}

	goroutineID := request.Arguments.ThreadId
	start := request.Arguments.StartFrame
	if start < 0 {
		start = 0
	}
	levels := s.args.stackTraceDepth
	if request.Arguments.Levels > 0 {
		levels = request.Arguments.Levels
	}

	// Since the backend doesn't support paging, we load all frames up to
	// the requested depth and then slice them here per
	// `supportsDelayedStackTraceLoading` capability.
	frames, err := s.debugger.Stacktrace(goroutineID, start+levels-1, 0)
	if err != nil {
		s.sendErrorResponse(request.Request, UnableToProduceStackTrace, "Unable to produce stack trace", err.Error())
		return
	}

	// Determine if the goroutine is a system goroutine.
	isSystemGoroutine := true
	if g, _ := s.debugger.FindGoroutine(goroutineID); g != nil {
		isSystemGoroutine = g.System(s.debugger.Target())
	}

	stackFrames := []dap.StackFrame{} // initialize to empty, since nil is not an accepted response.
	for i := 0; i < levels && i+start < len(frames); i++ {
		frame := frames[start+i]
		loc := &frame.Call
		uniqueStackFrameID := s.stackFrameHandles.create(stackFrame{goroutineID, start + i})
		stackFrame := dap.StackFrame{Id: uniqueStackFrameID, Line: loc.Line, Name: fnName(loc)}
		if loc.File != "<autogenerated>" {
			clientPath := s.toClientPath(loc.File)
			stackFrame.Source = dap.Source{Name: filepath.Base(clientPath), Path: clientPath}
		}
		stackFrame.Column = 0

		packageName := fnPackageName(loc)
		if !isSystemGoroutine && packageName == "runtime" {
			stackFrame.Source.PresentationHint = "deemphasize"
		}
		stackFrames = append(stackFrames, stackFrame)
	}

	totalFrames := len(frames)
	if len(frames) >= start+levels && !frames[len(frames)-1].Bottom {
		// We don't know the exact number of available stack frames, so
		// add an arbitrary number so the client knows to request additional
		// frames.
		totalFrames += s.args.stackTraceDepth
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
func (s *Server) onScopesRequest(request *dap.ScopesRequest) {
	sf, ok := s.stackFrameHandles.get(request.Arguments.FrameId)
	if !ok {
		s.sendErrorResponse(request.Request, UnableToListLocals, "Unable to list locals", fmt.Sprintf("unknown frame id %d", request.Arguments.FrameId))
		return
	}

	goid := sf.(stackFrame).goroutineID
	frame := sf.(stackFrame).frameIndex

	// Check if the function is optimized.
	fn, err := s.debugger.Function(goid, frame, 0, DefaultLoadConfig)
	if fn == nil || err != nil {
		s.sendErrorResponse(request.Request, UnableToListArgs, "Unable to find enclosing function", err.Error())
		return
	}
	suffix := ""
	if fn.Optimized() {
		suffix = " (warning: optimized function)"
	}
	// Retrieve arguments
	args, err := s.debugger.FunctionArguments(goid, frame, 0, DefaultLoadConfig)
	if err != nil {
		s.sendErrorResponse(request.Request, UnableToListArgs, "Unable to list args", err.Error())
		return
	}
	argScope := &fullyQualifiedVariable{&proc.Variable{Name: fmt.Sprintf("Arguments%s", suffix), Children: slicePtrVarToSliceVar(args)}, "", true, 0}

	// Retrieve local variables
	locals, err := s.debugger.LocalVariables(goid, frame, 0, DefaultLoadConfig)
	if err != nil {
		s.sendErrorResponse(request.Request, UnableToListLocals, "Unable to list locals", err.Error())
		return
	}
	locScope := &fullyQualifiedVariable{&proc.Variable{Name: fmt.Sprintf("Locals%s", suffix), Children: slicePtrVarToSliceVar(locals)}, "", true, 0}

	scopeArgs := dap.Scope{Name: argScope.Name, VariablesReference: s.variableHandles.create(argScope)}
	scopeLocals := dap.Scope{Name: locScope.Name, VariablesReference: s.variableHandles.create(locScope)}
	scopes := []dap.Scope{scopeArgs, scopeLocals}

	if s.args.showGlobalVariables {
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
func (s *Server) onVariablesRequest(request *dap.VariablesRequest) {
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

func (s *Server) maybeLoadResliced(v *fullyQualifiedVariable, start, count int) (*fullyQualifiedVariable, error) {
	if start == 0 && count == len(v.Children) {
		// If we have already loaded the correct children,
		// just return the variable.
		return v, nil
	}
	indexedLoadConfig := DefaultLoadConfig
	indexedLoadConfig.MaxArrayValues = count
	newV, err := s.debugger.LoadResliced(v.Variable, start, indexedLoadConfig)
	if err != nil {
		return nil, err
	}
	return &fullyQualifiedVariable{newV, v.fullyQualifiedNameOrExpr, false, start}, nil
}

func getIndexedVariableCount(c *proc.Variable) int {
	indexedVars := 0
	switch c.Kind {
	case reflect.Array, reflect.Slice, reflect.Map:
		indexedVars = int(c.Len)
	}
	return indexedVars
}

// childrenToDAPVariables returns the DAP presentation of the referenced variable's children.
func (s *Server) childrenToDAPVariables(v *fullyQualifiedVariable) ([]dap.Variable, error) {
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
			// a single variable to represet key:value format.
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
func (s *Server) metadataToDAPVariables(v *fullyQualifiedVariable) ([]dap.Variable, error) {
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

		s.log.Debugf("loading %s (type %s) with %s", v.fullyQualifiedNameOrExpr, typeName, loadExpr)
		// We know that this is an array/slice of Uint8 or Int32, so we will load up to MaxStringLen.
		config := DefaultLoadConfig
		config.MaxArrayValues = config.MaxStringLen
		vLoaded, err := s.debugger.EvalVariableInScope(-1, 0, 0, loadExpr, config)
		val := s.convertVariableToString(vLoaded)
		if err == nil {
			// TODO(suzmue): Add evaluate name. Using string(name) will not get the same result because the
			// MaxArrayValues is not auto adjusted in evaluate requests like MaxStringLen is adjusted.
			children = append(children, dap.Variable{
				Name:  "string()",
				Value: val,
				Type:  "string",
			})
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

func (s *Server) getTypeIfSupported(v *proc.Variable) string {
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
func (s *Server) convertVariable(v *proc.Variable, qualifiedNameOrExpr string) (value string, variablesReference int) {
	return s.convertVariableWithOpts(v, qualifiedNameOrExpr, 0)
}

func (s *Server) convertVariableToString(v *proc.Variable) string {
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
func (s *Server) convertVariableWithOpts(v *proc.Variable, qualifiedNameOrExpr string, opts convertVariableFlags) (value string, variablesReference int) {
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
		s.log.Debugf("loading %s (type %s) with %s", qualifiedNameOrExpr, typeName, loadExpr)
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
					s.log.Debugf("loading *(%s) (type %s) with %s", qualifiedNameOrExpr, cTypeName, cLoadExpr)
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
// -- {expression} - evaluates the expression and returns the result as a variable
// -- call {function} - injects a function call and returns the result as a variable
// TODO(polina): users have complained about having to click to expand multi-level
// variables, so consider also adding the following:
// -- print {expression} - return the result as a string like from dlv cli
func (s *Server) onEvaluateRequest(request *dap.EvaluateRequest) {
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
	isCall, err := regexp.MatchString(`^\s*call\s+\S+`, request.Arguments.Expression)
	if err == nil && isCall { // call {expression}
		expr := strings.Replace(request.Arguments.Expression, "call ", "", 1)
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
		exprVar, err := s.debugger.EvalVariableInScope(goid, frame, 0, request.Arguments.Expression, DefaultLoadConfig)
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
					if v, err := s.debugger.EvalVariableInScope(goid, frame, 0, request.Arguments.Expression, loadCfg); err != nil {
						s.log.Debugf("Failed to load more for %v: %v", request.Arguments.Expression, err)
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
		response.Body = dap.EvaluateResponseBody{Result: exprVal, VariablesReference: exprRef, IndexedVariables: getIndexedVariableCount(exprVar), NamedVariables: getNamedVariableCount(exprVar)}
	}
	s.send(response)
}

func (s *Server) doCall(goid, frame int, expr string) (*api.DebuggerState, []*proc.Variable, error) {
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
		GoroutineID:          goid,
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

func (s *Server) sendStoppedEvent(state *api.DebuggerState) {
	stopped := &dap.StoppedEvent{Event: *newEvent("stopped")}
	stopped.Body.AllThreadsStopped = true
	stopped.Body.ThreadId = stoppedGoroutineID(state)
	stopped.Body.Reason = s.debugger.StopReason().String()
	s.send(stopped)
}

// onTerminateRequest sends a not-yet-implemented error response.
// Capability 'supportsTerminateRequest' is not set in 'initialize' response.
func (s *Server) onTerminateRequest(request *dap.TerminateRequest) {
	s.sendNotYetImplementedErrorResponse(request.Request)
}

// onRestartRequest sends a not-yet-implemented error response
// Capability 'supportsRestartRequest' is not set in 'initialize' response.
func (s *Server) onRestartRequest(request *dap.RestartRequest) {
	s.sendNotYetImplementedErrorResponse(request.Request)
}

// onStepBackRequest handles 'stepBack' request.
// This is an optional request enabled by capability ‘supportsStepBackRequest’.
func (s *Server) onStepBackRequest(request *dap.StepBackRequest, asyncSetupDone chan struct{}) {
	s.sendStepResponse(request.Arguments.ThreadId, &dap.StepBackResponse{Response: *newResponse(request.Request)})
	s.doStepCommand(api.ReverseNext, request.Arguments.ThreadId, asyncSetupDone)
}

// onReverseContinueRequest performs a rewind command call up to the previous
// breakpoint or the start of the process
// This is an optional request enabled by capability ‘supportsStepBackRequest’.
func (s *Server) onReverseContinueRequest(request *dap.ReverseContinueRequest, asyncSetupDone chan struct{}) {
	s.send(&dap.ReverseContinueResponse{
		Response: *newResponse(request.Request),
	})
	s.doRunCommand(api.Rewind, asyncSetupDone)
}

// computeEvaluateName finds the named child, and computes its evaluate name.
func (s *Server) computeEvaluateName(v *fullyQualifiedVariable, cname string) (string, error) {
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
func (s *Server) onSetVariableRequest(request *dap.SetVariableRequest) {
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
	evaluated, err := s.debugger.EvalVariableInScope(goid, frame, 0, evaluateName, DefaultLoadConfig)
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
		if err := s.debugger.SetVariableInScope(goid, frame, 0, evaluateName, arg.Value); err != nil {
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
func (s *Server) onSetExpressionRequest(request *dap.SetExpressionRequest) {
	s.sendNotYetImplementedErrorResponse(request.Request)
}

// onLoadedSourcesRequest sends a not-yet-implemented error response.
// Capability 'supportsLoadedSourcesRequest' is not set 'initialize' response.
func (s *Server) onLoadedSourcesRequest(request *dap.LoadedSourcesRequest) {
	s.sendNotYetImplementedErrorResponse(request.Request)
}

// onReadMemoryRequest sends a not-yet-implemented error response.
// Capability 'supportsReadMemoryRequest' is not set 'initialize' response.
func (s *Server) onReadMemoryRequest(request *dap.ReadMemoryRequest) {
	s.sendNotYetImplementedErrorResponse(request.Request)
}

// onDisassembleRequest sends a not-yet-implemented error response.
// Capability 'supportsDisassembleRequest' is not set 'initialize' response.
func (s *Server) onDisassembleRequest(request *dap.DisassembleRequest) {
	s.sendNotYetImplementedErrorResponse(request.Request)
}

// onCancelRequest sends a not-yet-implemented error response.
// Capability 'supportsCancelRequest' is not set 'initialize' response.
func (s *Server) onCancelRequest(request *dap.CancelRequest) {
	s.sendNotYetImplementedErrorResponse(request.Request)
}

// onExceptionInfoRequest handles 'exceptionInfo' requests.
// Capability 'supportsExceptionInfoRequest' is set in 'initialize' response.
func (s *Server) onExceptionInfoRequest(request *dap.ExceptionInfoRequest) {
	goroutineID := request.Arguments.ThreadId
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
	if bpState != nil && bpState.Breakpoint != nil && (bpState.Breakpoint.Name == proc.FatalThrow || bpState.Breakpoint.Name == proc.UnrecoveredPanic) {
		switch bpState.Breakpoint.Name {
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

func (s *Server) stacktrace(goroutineID int, g *proc.G) (string, error) {
	frames, err := s.debugger.Stacktrace(goroutineID, s.args.stackTraceDepth, 0)
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

func (s *Server) throwReason(goroutineID int) (string, error) {
	return s.getExprString("s", goroutineID, 0)
}

func (s *Server) panicReason(goroutineID int) (string, error) {
	return s.getExprString("(*msgs).arg.(data)", goroutineID, 0)
}

func (s *Server) getExprString(expr string, goroutineID, frame int) (string, error) {
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
//   showUser - if true, the error will be shown to the user (e.g. via a visible pop-up)
func (s *Server) sendErrorResponseWithOpts(request dap.Request, id int, summary, details string, showUser bool) {
	er := &dap.ErrorResponse{}
	er.Type = "response"
	er.Command = request.Command
	er.RequestSeq = request.Seq
	er.Success = false
	er.Message = summary
	er.Body.Error.Id = id
	er.Body.Error.Format = fmt.Sprintf("%s: %s", summary, details)
	er.Body.Error.ShowUser = showUser
	s.log.Debug(er.Body.Error.Format)
	s.send(er)
}

// sendErrorResponse sends an error response with default visibility settings.
func (s *Server) sendErrorResponse(request dap.Request, id int, summary, details string) {
	s.sendErrorResponseWithOpts(request, id, summary, details, false /*showUser*/)
}

// sendInternalErrorResponse sends an "internal error" response back to the client.
// We only take a seq here because we don't want to make assumptions about the
// kind of message received by the server that this error is a reply to.
func (s *Server) sendInternalErrorResponse(seq int, details string) {
	er := &dap.ErrorResponse{}
	er.Type = "response"
	er.RequestSeq = seq
	er.Success = false
	er.Message = "Internal Error"
	er.Body.Error.Id = InternalError
	er.Body.Error.Format = fmt.Sprintf("%s: %s", er.Message, details)
	s.log.Debug(er.Body.Error.Format)
	s.send(er)
}

func (s *Server) sendUnsupportedErrorResponse(request dap.Request) {
	s.sendErrorResponse(request, UnsupportedCommand, "Unsupported command",
		fmt.Sprintf("cannot process %q request", request.Command))
}

func (s *Server) sendNotYetImplementedErrorResponse(request dap.Request) {
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

func (s *Server) resetHandlesForStoppedEvent() {
	s.stackFrameHandles.reset()
	s.variableHandles.reset()
	s.exceptionErr = nil
}

func processExited(state *api.DebuggerState, err error) bool {
	_, isexited := err.(proc.ErrProcessExited)
	return isexited || err == nil && state.Exited
}
func (s *Server) setRunning(running bool) {
	s.runningMu.Lock()
	s.running = running
	s.runningMu.Unlock()
}

func (s *Server) isRunning() bool {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()
	return s.running
}

// resume is a helper function to resume the execution
// of the target when the program is halted, but no
// stopped event has been sent.
func (s *Server) resume(command string, waitChannel chan struct{}) (*api.DebuggerState, error) {
	// No other goroutines should be able to try to resume
	// or halt execution while this goroutine is continuing
	// the program.
	// Hold onto resumeMu until the program is running.
	resumeNotify := make(chan struct{}, 1)
	defer s.maybeCloseChan(resumeNotify)
	s.resumeMu.Lock()
	go func() {
		<-resumeNotify
		s.maybeCloseChan(waitChannel)
		s.resumeMu.Unlock()
	}()

	// There may have been a manual halt while the program was
	// stopped. If this happened, do not continue exection of
	// the program.
	if s.debugger.CheckAndClearManualStopRequest() {
		// A halt request came in so we need to CancelNext.
		if err := s.debugger.CancelNext(); err != nil {
			s.log.Error(err)
		}
		return s.debugger.State(false)
	}
	return s.debugger.Command(&api.DebuggerCommand{Name: command}, resumeNotify)
}

// doRunCommand runs a debugger command until it stops on
// termination, error, breakpoint, etc, when an appropriate
// event needs to be sent to the client. asyncSetupDone is
// a channel that will be closed to signal that an
// asynchornous command has completed setup or was interrupted
// due to an error, so the server is ready to receive new requests.
func (s *Server) doRunCommand(command string, asyncSetupDone chan struct{}) {
	state, err := s.run(command, asyncSetupDone)

	if processExited(state, err) {
		s.send(&dap.TerminatedEvent{Event: *newEvent("terminated")})
		return
	}

	stopReason := s.debugger.StopReason()
	file, line := "?", -1
	if state != nil && state.CurrentThread != nil {
		file, line = state.CurrentThread.File, state.CurrentThread.Line
	}
	s.log.Debugf("%q command stopped - reason %q, location %s:%d", command, stopReason, file, line)

	s.resetHandlesForStoppedEvent()
	stopped := &dap.StoppedEvent{Event: *newEvent("stopped")}
	stopped.Body.AllThreadsStopped = true

	if err == nil {
		if stopReason == proc.StopManual {
			if err := s.debugger.CancelNext(); err != nil {
				s.log.Error(err)
			} else {
				state.NextInProgress = false
			}
		}
		// TODO(suzmue): If stopped.Body.ThreadId is not a valid goroutine
		// then the stopped reason does not show up anywhere in the
		// vscode ui.
		stopped.Body.ThreadId = stoppedGoroutineID(state)

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
			stopped.Body.ThreadId = stoppedOnBreakpointGoroutineID(state)
		}
		if state.CurrentThread != nil && state.CurrentThread.Breakpoint != nil {
			switch state.CurrentThread.Breakpoint.Name {
			case proc.FatalThrow:
				stopped.Body.Reason = "exception"
				stopped.Body.Description = "fatal error"
				stopped.Body.Text, _ = s.throwReason(stopped.Body.ThreadId)
			case proc.UnrecoveredPanic:
				stopped.Body.Reason = "exception"
				stopped.Body.Description = "panic"
				stopped.Body.Text, _ = s.panicReason(stopped.Body.ThreadId)
			}
			if strings.HasPrefix(state.CurrentThread.Breakpoint.Name, functionBpPrefix) {
				stopped.Body.Reason = "function breakpoint"
			}
			stopped.Body.HitBreakpointIds = []int{state.CurrentThread.Breakpoint.ID}
		}
	} else {
		s.exceptionErr = err
		s.log.Error("runtime error: ", err)
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
			stopped.Body.ThreadId = stoppedGoroutineID(state)
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

func (s *Server) run(command string, asyncSetupDone chan struct{}) (*api.DebuggerState, error) {
	s.setRunning(true)
	defer s.setRunning(false)

	runningStep := command != api.Continue
	if state, _ := s.debugger.State(true); state != nil {
		// If state.NextInProgress, then we are in the middle of a step,
		// regardless of the command.
		runningStep = runningStep || state.NextInProgress
	}

	var state *api.DebuggerState
	var err error
	for {
		state, err = s.resume(command, asyncSetupDone)
		if state == nil || state.CurrentThread == nil || err != nil {
			break
		}

		istp := s.handleLogPoints(state)
		// Only resume execution if continue was the command used to run the program.
		// Otherwise, the program will go past the step, next, stepout requests.
		// TODO(suzmue): have s.debugger.Command() not clear internal breakpoints when
		// hitting another breakpoint.
		if s.debugger.StopReason() != proc.StopBreakpoint || !istp || (runningStep && !state.NextInProgress) {
			break
		}
		command = api.DirectionCongruentContinue
	}
	return state, err
}

func (s *Server) handleLogPoints(state *api.DebuggerState) bool {
	// istracepoint is true if all breakpoints that are encountered are
	// tracepoints.
	istracepoint := true
	for _, th := range state.Threads {
		if bp := th.Breakpoint; bp != nil {
			s.handleLogPoint(bp)
			if !bp.Tracepoint {
				istracepoint = false
			}
		}
	}
	return istracepoint
}

func (s *Server) handleLogPoint(bp *api.Breakpoint) {
	if !bp.Tracepoint {
		return
	}
	// TODO(suzmue): allow evaluate expressions within log points.
	if msg, ok := bp.UserData.(string); ok {
		s.send(&dap.OutputEvent{
			Event: *newEvent("output"),
			Body: dap.OutputEventBody{
				Category: "stdout",
				Output:   msg + "\n",
			},
		})
	}
}

func (s *Server) toClientPath(path string) string {
	if len(s.args.substitutePathServerToClient) == 0 {
		return path
	}
	clientPath := locspec.SubstitutePath(path, s.args.substitutePathServerToClient)
	if clientPath != path {
		s.log.Debugf("server path=%s converted to client path=%s\n", path, clientPath)
	}
	return clientPath
}

func (s *Server) toServerPath(path string) string {
	if len(s.args.substitutePathClientToServer) == 0 {
		return path
	}
	serverPath := locspec.SubstitutePath(path, s.args.substitutePathClientToServer)
	if serverPath != path {
		s.log.Debugf("client path=%s converted to server path=%s\n", path, serverPath)
	}
	return serverPath
}
