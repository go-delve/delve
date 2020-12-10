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
	"encoding/json"
	"fmt"
	"go/constant"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	"github.com/go-delve/delve/pkg/gobuild"
	"github.com/go-delve/delve/pkg/logflags"
	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/service"
	"github.com/go-delve/delve/service/api"
	"github.com/go-delve/delve/service/debugger"
	"github.com/google/go-dap"
	"github.com/sirupsen/logrus"
)

// Server implements a DAP server that can accept a single client for
// a single debug session. It does not support restarting.
// The server operates via two goroutines:
// (1) Main goroutine where the server is created via NewServer(),
// started via Run() and stopped via Stop().
// (2) Run goroutine started from Run() that accepts a client connection,
// reads, decodes and processes each request, issuing commands to the
// underlying debugger and sending back events and responses.
// TODO(polina): make it asynchronous (i.e. launch goroutine per request)
type Server struct {
	// config is all the information necessary to start the debugger and server.
	config *service.Config
	// listener is used to accept the client connection.
	listener net.Listener
	// conn is the accepted client connection.
	conn net.Conn
	// stopChan is closed when the server is Stop()-ed. This can be used to signal
	// to goroutines run by the server that it's time to quit.
	stopChan chan struct{}
	// reader is used to read requests from the connection.
	reader *bufio.Reader
	// debugger is the underlying debugger service.
	debugger *debugger.Debugger
	// log is used for structured logging.
	log *logrus.Entry
	// binaryToRemove is the compiled binary to be removed on disconnect.
	binaryToRemove string
	// stackFrameHandles maps frames of each goroutine to unique ids across all goroutines.
	// Reset at every stop.
	stackFrameHandles *handlesMap
	// variableHandles maps compound variables to unique references within their stack frame.
	// Reset at every stop.
	// See also comment for convertVariable.
	variableHandles *variablesHandlesMap
	// args tracks special settings for handling debug session requests.
	args launchAttachArgs
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
}

// defaultArgs borrows the defaults for the arguments from the original vscode-go adapter.
var defaultArgs = launchAttachArgs{
	stopOnEntry:         false,
	stackTraceDepth:     50,
	showGlobalVariables: false,
}

// NewServer creates a new DAP Server. It takes an opened Listener
// via config and assumes its ownership. config.disconnectChan has to be set;
// it will be closed by the server when the client disconnects or requests
// shutdown. Once disconnectChan is closed, Server.Stop() must be called.
func NewServer(config *service.Config) *Server {
	logger := logflags.DAPLogger()
	logflags.WriteDAPListeningMessage(config.Listener.Addr().String())
	logger.Debug("DAP server pid = ", os.Getpid())
	return &Server{
		config:            config,
		listener:          config.Listener,
		stopChan:          make(chan struct{}),
		log:               logger,
		stackFrameHandles: newHandlesMap(),
		variableHandles:   newVariablesHandlesMap(),
		args:              defaultArgs,
	}
}

// Stop stops the DAP debugger service, closes the listener and the client
// connection. It shuts down the underlying debugger and kills the target
// process if it was launched by it. This method mustn't be called more than
// once.
func (s *Server) Stop() {
	s.listener.Close()
	close(s.stopChan)
	if s.conn != nil {
		// Unless Stop() was called after serveDAPCodec()
		// returned, this will result in closed connection error
		// on next read, breaking out of the read loop and
		// allowing the run goroutine to exit.
		s.conn.Close()
	}
	if s.debugger != nil {
		kill := s.config.Debugger.AttachPid == 0
		if err := s.debugger.Detach(kill); err != nil {
			s.log.Error(err)
		}
	}
}

// signalDisconnect closes config.DisconnectChan if not nil, which
// signals that the client disconnected or there was a client
// connection failure. Since the server currently services only one
// client, this can be used as a signal to the entire server via
// Stop(). The function safeguards agaist closing the channel more
// than once and can be called multiple times. It is not thread-safe
// and is currently only called from the run goroutine.
// TODO(polina): lock this when we add more goroutines that could call
// this when we support asynchronous request-response communication.
func (s *Server) signalDisconnect() {
	// Avoid accidentally closing the channel twice and causing a panic, when
	// this function is called more than once. For example, we could have the
	// following sequence of events:
	// -- run goroutine: calls onDisconnectRequest()
	// -- run goroutine: calls signalDisconnect()
	// -- main goroutine: calls Stop()
	// -- main goroutine: Stop() closes client connection
	// -- run goroutine: serveDAPCodec() gets "closed network connection"
	// -- run goroutine: serveDAPCodec() returns
	// -- run goroutine: serveDAPCodec calls signalDisconnect()
	if s.config.DisconnectChan != nil {
		close(s.config.DisconnectChan)
		s.config.DisconnectChan = nil
	}
	if s.binaryToRemove != "" {
		gobuild.Remove(s.binaryToRemove)
	}
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
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.stopChan:
			default:
				s.log.Errorf("Error accepting client connection: %s\n", err)
			}
			s.signalDisconnect()
			return
		}
		s.conn = conn
		s.serveDAPCodec()
	}()
}

// serveDAPCodec reads and decodes requests from the client
// until it encounters an error or EOF, when it sends
// the disconnect signal and returns.
func (s *Server) serveDAPCodec() {
	defer s.signalDisconnect()
	s.reader = bufio.NewReader(s.conn)
	for {
		request, err := dap.ReadProtocolMessage(s.reader)
		// TODO(polina): Differentiate between errors and handle them
		// gracefully. For example,
		// -- "Request command 'foo' is not supported" means we
		// potentially got some new DAP request that we do not yet have
		// decoding support for, so we can respond with an ErrorResponse.
		// TODO(polina): to support this add Seq to
		// dap.DecodeProtocolMessageFieldError.
		if err != nil {
			stopRequested := false
			select {
			case <-s.stopChan:
				stopRequested = true
			default:
			}
			if err != io.EOF && !stopRequested {
				s.log.Error("DAP error: ", err)
			}
			return
		}
		s.handleRequest(request)
	}
}

func (s *Server) handleRequest(request dap.Message) {
	defer func() {
		// In case a handler panics, we catch the panic and send an error response
		// back to the client.
		if ierr := recover(); ierr != nil {
			s.sendInternalErrorResponse(request.GetSeq(), fmt.Sprintf("%v", ierr))
		}
	}()

	jsonmsg, _ := json.Marshal(request)
	s.log.Debug("[<- from client]", string(jsonmsg))

	switch request := request.(type) {
	case *dap.InitializeRequest:
		// Required
		s.onInitializeRequest(request)
	case *dap.LaunchRequest:
		// Required
		s.onLaunchRequest(request)
	case *dap.AttachRequest:
		// Required
		// TODO: implement this request in V0
		s.onAttachRequest(request)
	case *dap.DisconnectRequest:
		// Required
		s.onDisconnectRequest(request)
	case *dap.TerminateRequest:
		// Optional (capability ‘supportsTerminateRequest‘)
		// TODO: implement this request in V1
		s.onTerminateRequest(request)
	case *dap.RestartRequest:
		// Optional (capability ‘supportsRestartRequest’)
		// TODO: implement this request in V1
		s.onRestartRequest(request)
	case *dap.SetBreakpointsRequest:
		// Required
		s.onSetBreakpointsRequest(request)
	case *dap.SetFunctionBreakpointsRequest:
		// Optional (capability ‘supportsFunctionBreakpoints’)
		// TODO: implement this request in V1
		s.onSetFunctionBreakpointsRequest(request)
	case *dap.SetExceptionBreakpointsRequest:
		// Optional (capability ‘exceptionBreakpointFilters’)
		s.onSetExceptionBreakpointsRequest(request)
	case *dap.ConfigurationDoneRequest:
		// Optional (capability ‘supportsConfigurationDoneRequest’)
		// Supported by vscode-go
		s.onConfigurationDoneRequest(request)
	case *dap.ContinueRequest:
		// Required
		s.onContinueRequest(request)
	case *dap.NextRequest:
		// Required
		s.onNextRequest(request)
	case *dap.StepInRequest:
		// Required
		s.onStepInRequest(request)
	case *dap.StepOutRequest:
		// Required
		s.onStepOutRequest(request)
	case *dap.StepBackRequest:
		// Optional (capability ‘supportsStepBack’)
		// TODO: implement this request in V1
		s.onStepBackRequest(request)
	case *dap.ReverseContinueRequest:
		// Optional (capability ‘supportsStepBack’)
		// TODO: implement this request in V1
		s.onReverseContinueRequest(request)
	case *dap.RestartFrameRequest:
		// Optional (capability ’supportsRestartFrame’)
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.GotoRequest:
		// Optional (capability ‘supportsGotoTargetsRequest’)
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.PauseRequest:
		// Required
		// TODO: implement this request in V0
		s.onPauseRequest(request)
	case *dap.StackTraceRequest:
		// Required
		s.onStackTraceRequest(request)
	case *dap.ScopesRequest:
		// Required
		s.onScopesRequest(request)
	case *dap.VariablesRequest:
		// Required
		s.onVariablesRequest(request)
	case *dap.SetVariableRequest:
		// Optional (capability ‘supportsSetVariable’)
		// Supported by vscode-go
		// TODO: implement this request in V0
		s.onSetVariableRequest(request)
	case *dap.SetExpressionRequest:
		// Optional (capability ‘supportsSetExpression’)
		// TODO: implement this request in V1
		s.onSetExpressionRequest(request)
	case *dap.SourceRequest:
		// Required
		// This does not make sense in the context of Go as
		// the source cannot be a string eval'ed at runtime.
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.ThreadsRequest:
		// Required
		s.onThreadsRequest(request)
	case *dap.TerminateThreadsRequest:
		// Optional (capability ‘supportsTerminateThreadsRequest’)
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.EvaluateRequest:
		// Required
		s.onEvaluateRequest(request)
	case *dap.StepInTargetsRequest:
		// Optional (capability ‘supportsStepInTargetsRequest’)
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.GotoTargetsRequest:
		// Optional (capability ‘supportsGotoTargetsRequest’)
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.CompletionsRequest:
		// Optional (capability ‘supportsCompletionsRequest’)
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.ExceptionInfoRequest:
		// Optional (capability ‘supportsExceptionInfoRequest’)
		// TODO: does this request make sense for delve?
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.LoadedSourcesRequest:
		// Optional (capability ‘supportsLoadedSourcesRequest’)
		// TODO: implement this request in V1
		s.onLoadedSourcesRequest(request)
	case *dap.DataBreakpointInfoRequest:
		// Optional (capability ‘supportsDataBreakpoints’)
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.SetDataBreakpointsRequest:
		// Optional (capability ‘supportsDataBreakpoints’)
		s.sendUnsupportedErrorResponse(request.Request)
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
	dap.WriteProtocolMessage(s.conn, message)
}

func (s *Server) onInitializeRequest(request *dap.InitializeRequest) {
	// TODO(polina): Respond with an error if debug session is in progress?
	response := &dap.InitializeResponse{Response: *newResponse(request.Request)}
	response.Body.SupportsConfigurationDoneRequest = true
	response.Body.SupportsConditionalBreakpoints = true
	response.Body.SupportsDelayedStackTraceLoading = true
	// TODO(polina): support this to match vscode-go functionality
	response.Body.SupportsSetVariable = false
	// TODO(polina): support these requests in addition to vscode-go feature parity
	response.Body.SupportsTerminateRequest = false
	response.Body.SupportsRestartRequest = false
	response.Body.SupportsFunctionBreakpoints = false
	response.Body.SupportsStepBack = false
	response.Body.SupportsSetExpression = false
	response.Body.SupportsLoadedSourcesRequest = false
	response.Body.SupportsReadMemoryRequest = false
	response.Body.SupportsDisassembleRequest = false
	response.Body.SupportsCancelRequest = false
	s.send(response)
}

// Output path for the compiled binary in debug or test modes.
const debugBinary string = "./__debug_bin"

func (s *Server) onLaunchRequest(request *dap.LaunchRequest) {
	// TODO(polina): Respond with an error if debug session is in progress?

	program, ok := request.Arguments["program"].(string)
	if !ok || program == "" {
		s.sendErrorResponse(request.Request,
			FailedToLaunch, "Failed to launch",
			"The program attribute is missing in debug configuration.")
		return
	}

	mode, ok := request.Arguments["mode"]
	if !ok || mode == "" {
		mode = "debug"
	}

	if mode == "debug" || mode == "test" {
		output, ok := request.Arguments["output"].(string)
		if !ok || output == "" {
			output = debugBinary
		}
		debugname, err := filepath.Abs(output)
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

		switch mode {
		case "debug":
			err = gobuild.GoBuild(debugname, []string{program}, buildFlags)
		case "test":
			err = gobuild.GoTestBuild(debugname, []string{program}, buildFlags)
		}
		if err != nil {
			s.sendErrorResponse(request.Request,
				FailedToLaunch, "Failed to launch",
				fmt.Sprintf("Build error: %s", err.Error()))
			return
		}
		program = debugname
		s.binaryToRemove = debugname
	}

	// TODO(polina): support "remote" mode
	if mode != "exec" && mode != "debug" && mode != "test" {
		s.sendErrorResponse(request.Request,
			FailedToLaunch, "Failed to launch",
			fmt.Sprintf("Unsupported 'mode' value %q in debug configuration.", mode))
		return
	}

	// If user-specified, overwrite the defaults for optional args.
	stop, ok := request.Arguments["stopOnEntry"].(bool)
	if ok {
		s.args.stopOnEntry = stop
	}
	depth, ok := request.Arguments["stackTraceDepth"].(float64)
	if ok && depth > 0 {
		s.args.stackTraceDepth = int(depth)
	}
	globals, ok := request.Arguments["showGlobalVariables"].(bool)
	if ok {
		s.args.showGlobalVariables = globals
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

	var err error
	if s.debugger, err = debugger.New(&s.config.Debugger, s.config.ProcessArgs); err != nil {
		s.sendErrorResponse(request.Request,
			FailedToLaunch, "Failed to launch", err.Error())
		return
	}

	// Notify the client that the debugger is ready to start accepting
	// configuration requests for setting breakpoints, etc. The client
	// will end the configuration sequence with 'configurationDone'.
	s.send(&dap.InitializedEvent{Event: *newEvent("initialized")})
	s.send(&dap.LaunchResponse{Response: *newResponse(request.Request)})
}

// onDisconnectRequest handles the DisconnectRequest. Per the DAP spec,
// it disconnects the debuggee and signals that the debug adaptor
// (in our case this TCP server) can be terminated.
func (s *Server) onDisconnectRequest(request *dap.DisconnectRequest) {
	s.send(&dap.DisconnectResponse{Response: *newResponse(request.Request)})
	if s.debugger != nil {
		_, err := s.debugger.Command(&api.DebuggerCommand{Name: api.Halt})
		if err != nil {
			s.log.Error(err)
		}
		kill := s.config.Debugger.AttachPid == 0
		err = s.debugger.Detach(kill)
		if err != nil {
			s.log.Error(err)
		}
	}
	// TODO(polina): make thread-safe when handlers become asynchronous.
	s.signalDisconnect()
}

func (s *Server) onSetBreakpointsRequest(request *dap.SetBreakpointsRequest) {
	// TODO(polina): handle this while running by halting first.

	if request.Arguments.Source.Path == "" {
		s.sendErrorResponse(request.Request, UnableToSetBreakpoints, "Unable to set or clear breakpoints", "empty file path")
		return
	}

	// According to the spec we should "set multiple breakpoints for a single source
	// and clear all previous breakpoints in that source." The simplest way is
	// to clear all and then set all.
	//
	// TODO(polina): should we optimize this as follows?
	// See https://github.com/golang/vscode-go/issues/163 for details.
	// If a breakpoint:
	// -- exists and not in request => ClearBreakpoint
	// -- exists and in request => AmendBreakpoint
	// -- doesn't exist and in request => SetBreakpoint

	// Clear all existing breakpoints in the file.
	existing := s.debugger.Breakpoints()
	for _, bp := range existing {
		// Skip special breakpoints such as for panic.
		if bp.ID < 0 {
			continue
		}
		// Skip other source files.
		// TODO(polina): should this be normalized because of different OSes?
		if bp.File != request.Arguments.Source.Path {
			continue
		}
		_, err := s.debugger.ClearBreakpoint(bp)
		if err != nil {
			s.sendErrorResponse(request.Request, UnableToSetBreakpoints, "Unable to set or clear breakpoints", err.Error())
			return
		}
	}

	// Set all requested breakpoints.
	response := &dap.SetBreakpointsResponse{Response: *newResponse(request.Request)}
	response.Body.Breakpoints = make([]dap.Breakpoint, len(request.Arguments.Breakpoints))
	for i, want := range request.Arguments.Breakpoints {
		got, err := s.debugger.CreateBreakpoint(
			&api.Breakpoint{File: request.Arguments.Source.Path, Line: want.Line, Cond: want.Condition})
		response.Body.Breakpoints[i].Verified = (err == nil)
		if err != nil {
			response.Body.Breakpoints[i].Line = want.Line
			response.Body.Breakpoints[i].Message = err.Error()
		} else {
			response.Body.Breakpoints[i].Line = got.Line
		}
	}
	s.send(response)
}

func (s *Server) onSetExceptionBreakpointsRequest(request *dap.SetExceptionBreakpointsRequest) {
	// Unlike what DAP documentation claims, this request is always sent
	// even though we specified no filters at initialization. Handle as no-op.
	s.send(&dap.SetExceptionBreakpointsResponse{Response: *newResponse(request.Request)})
}

func (s *Server) onConfigurationDoneRequest(request *dap.ConfigurationDoneRequest) {
	if s.args.stopOnEntry {
		e := &dap.StoppedEvent{
			Event: *newEvent("stopped"),
			Body:  dap.StoppedEventBody{Reason: "entry", ThreadId: 1, AllThreadsStopped: true},
		}
		s.send(e)
	}
	s.send(&dap.ConfigurationDoneResponse{Response: *newResponse(request.Request)})
	if !s.args.stopOnEntry {
		s.doCommand(api.Continue)
	}
}

func (s *Server) onContinueRequest(request *dap.ContinueRequest) {
	s.send(&dap.ContinueResponse{
		Response: *newResponse(request.Request),
		Body:     dap.ContinueResponseBody{AllThreadsContinued: true}})
	s.doCommand(api.Continue)
}

func fnName(loc *proc.Location) string {
	if loc.Fn == nil {
		return "???"
	}
	return loc.Fn.Name
}

func (s *Server) onThreadsRequest(request *dap.ThreadsRequest) {
	if s.debugger == nil {
		s.sendErrorResponse(request.Request, UnableToDisplayThreads, "Unable to display threads", "debugger is nil")
		return
	}
	gs, _, err := s.debugger.Goroutines(0, 0)
	if err != nil {
		switch err.(type) {
		case *proc.ErrProcessExited:
			// If the program exits very quickly, the initial threads request will complete after it has exited.
			// A TerminatedEvent has already been sent. Ignore the err returned in this case.
			s.send(&dap.ThreadsResponse{Response: *newResponse(request.Request)})
		default:
			s.sendErrorResponse(request.Request, UnableToDisplayThreads, "Unable to display threads", err.Error())
		}
		return
	}

	threads := make([]dap.Thread, len(gs))
	if len(threads) == 0 {
		// Depending on the debug session stage, goroutines information
		// might not be available. However, the DAP spec states that
		// "even if a debug adapter does not support multiple threads,
		// it must implement the threads request and return a single
		// (dummy) thread".
		threads = []dap.Thread{{Id: 1, Name: "Dummy"}}
	} else {
		state, err := s.debugger.State( /*nowait*/ true)
		if err != nil {
			s.sendErrorResponse(request.Request, UnableToDisplayThreads, "Unable to display threads", err.Error())
			return
		}
		s.debugger.LockTarget()
		defer s.debugger.UnlockTarget()

		for i, g := range gs {
			selected := ""
			if state.SelectedGoroutine != nil && g.ID == state.SelectedGoroutine.ID {
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

// onAttachRequest sends a not-yet-implemented error response.
// This is a mandatory request to support.
func (s *Server) onAttachRequest(request *dap.AttachRequest) { // TODO V0
	s.sendNotYetImplementedErrorResponse(request.Request)
}

// onNextRequest handles 'next' request.
// This is a mandatory request to support.
func (s *Server) onNextRequest(request *dap.NextRequest) {
	// This ignores threadId argument to match the original vscode-go implementation.
	// TODO(polina): use SwitchGoroutine to change the current goroutine.
	s.send(&dap.NextResponse{Response: *newResponse(request.Request)})
	s.doCommand(api.Next)
}

// onStepInRequest handles 'stepIn' request
// This is a mandatory request to support.
func (s *Server) onStepInRequest(request *dap.StepInRequest) {
	// This ignores threadId argument to match the original vscode-go implementation.
	// TODO(polina): use SwitchGoroutine to change the current goroutine.
	s.send(&dap.StepInResponse{Response: *newResponse(request.Request)})
	s.doCommand(api.Step)
}

// onStepOutRequest handles 'stepOut' request
// This is a mandatory request to support.
func (s *Server) onStepOutRequest(request *dap.StepOutRequest) {
	// This ignores threadId argument to match the original vscode-go implementation.
	// TODO(polina): use SwitchGoroutine to change the current goroutine.
	s.send(&dap.StepOutResponse{Response: *newResponse(request.Request)})
	s.doCommand(api.StepOut)
}

// onPauseRequest sends a not-yet-implemented error response.
// This is a mandatory request to support.
func (s *Server) onPauseRequest(request *dap.PauseRequest) { // TODO V0
	s.sendNotYetImplementedErrorResponse(request.Request)
}

// stackFrame represents the index of a frame within
// the context of a stack of a specific goroutine.
type stackFrame struct {
	goroutineID int
	frameIndex  int
}

// onStackTraceRequest handles ‘stackTrace’ requests.
// This is a mandatory request to support.
func (s *Server) onStackTraceRequest(request *dap.StackTraceRequest) {
	goroutineID := request.Arguments.ThreadId
	frames, err := s.debugger.Stacktrace(goroutineID, s.args.stackTraceDepth, 0)
	if err != nil {
		s.sendErrorResponse(request.Request, UnableToProduceStackTrace, "Unable to produce stack trace", err.Error())
		return
	}

	stackFrames := make([]dap.StackFrame, len(frames))
	for i, frame := range frames {
		loc := &frame.Call
		uniqueStackFrameID := s.stackFrameHandles.create(stackFrame{goroutineID, i})
		stackFrames[i] = dap.StackFrame{Id: uniqueStackFrameID, Line: loc.Line, Name: fnName(loc)}
		if loc.File != "<autogenerated>" {
			stackFrames[i].Source = dap.Source{Name: filepath.Base(loc.File), Path: loc.File}
		}
		stackFrames[i].Column = 0
	}
	// Since the backend doesn't support paging, we load all frames up to
	// pre-configured depth every time and then slice them here per
	// `supportsDelayedStackTraceLoading` capability.
	// TODO(polina): consider optimizing this, so subsequent stack requests
	// slice already loaded frames and handles instead of reloading every time.
	if request.Arguments.StartFrame > 0 {
		stackFrames = stackFrames[min(request.Arguments.StartFrame, len(stackFrames)):]
	}
	if request.Arguments.Levels > 0 {
		stackFrames = stackFrames[:min(request.Arguments.Levels, len(stackFrames))]
	}
	response := &dap.StackTraceResponse{
		Response: *newResponse(request.Request),
		Body:     dap.StackTraceResponseBody{StackFrames: stackFrames, TotalFrames: len(frames)},
	}
	s.send(response)
}

// onScopesRequest handles 'scopes' requests.
// This is a mandatory request to support.
func (s *Server) onScopesRequest(request *dap.ScopesRequest) {
	sf, ok := s.stackFrameHandles.get(request.Arguments.FrameId)
	if !ok {
		s.sendErrorResponse(request.Request, UnableToListLocals, "Unable to list locals", fmt.Sprintf("unknown frame id %d", request.Arguments.FrameId))
		return
	}

	goid := sf.(stackFrame).goroutineID
	frame := sf.(stackFrame).frameIndex
	// TODO(polina): Support setting config via launch/attach args
	cfg := proc.LoadConfig{FollowPointers: true, MaxVariableRecurse: 1, MaxStringLen: 64, MaxArrayValues: 64, MaxStructFields: -1}

	// Retrieve arguments
	args, err := s.debugger.FunctionArguments(goid, frame, 0, cfg)
	if err != nil {
		s.sendErrorResponse(request.Request, UnableToListArgs, "Unable to list args", err.Error())
		return
	}
	argScope := &proc.Variable{Name: "Arguments", Children: slicePtrVarToSliceVar(args)}

	// Retrieve local variables
	locals, err := s.debugger.LocalVariables(goid, frame, 0, cfg)
	if err != nil {
		s.sendErrorResponse(request.Request, UnableToListLocals, "Unable to list locals", err.Error())
		return
	}
	locScope := &proc.Variable{Name: "Locals", Children: slicePtrVarToSliceVar(locals)}

	// TODO(polina): Annotate shadowed variables

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
		globals, err := s.debugger.PackageVariables(currPkgFilter, cfg)
		if err != nil {
			s.sendErrorResponse(request.Request, UnableToListGlobals, "Unable to list globals", err.Error())
			return
		}
		// Remove package prefix from the fully-qualified variable names.
		// We will include the package info once in the name of the scope instead.
		for i, g := range globals {
			globals[i].Name = strings.TrimPrefix(g.Name, currPkg+".")
		}

		globScope := &proc.Variable{
			Name:     fmt.Sprintf("Globals (package %s)", currPkg),
			Children: slicePtrVarToSliceVar(globals),
		}
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
	v, ok := s.variableHandles.get(request.Arguments.VariablesReference)
	if !ok {
		s.sendErrorResponse(request.Request, UnableToLookupVariable, "Unable to lookup variable", fmt.Sprintf("unknown reference %d", request.Arguments.VariablesReference))
		return
	}
	children := make([]dap.Variable, 0)
	// TODO(polina): check and handle if variable loaded incompletely
	// https://github.com/go-delve/delve/blob/master/Documentation/api/ClientHowto.md#looking-into-variables

	switch v.Kind {
	case reflect.Map:
		for i := 0; i < len(v.Children); i += 2 {
			// A map will have twice as many children as there are key-value elements.
			kvIndex := i / 2
			// Process children in pairs: even indices are map keys, odd indices are values.
			key, keyref := s.convertVariable(&v.Children[i])
			val, valref := s.convertVariable(&v.Children[i+1])
			// If key or value or both are scalars, we can use
			// a single variable to represet key:value format.
			// Otherwise, we must return separate variables for both.
			if keyref > 0 && valref > 0 { // Both are not scalars
				keyvar := dap.Variable{
					Name:               fmt.Sprintf("[key %d]", kvIndex),
					Value:              key,
					VariablesReference: keyref,
				}
				valvar := dap.Variable{
					Name:               fmt.Sprintf("[val %d]", kvIndex),
					Value:              val,
					VariablesReference: valref,
				}
				children = append(children, keyvar, valvar)
			} else { // At least one is a scalar
				kvvar := dap.Variable{
					Name:  key,
					Value: val,
				}
				if keyref != 0 { // key is a type to be expanded
					kvvar.Name = fmt.Sprintf("%s[%d]", kvvar.Name, kvIndex) // Make the name unique
					kvvar.VariablesReference = keyref
				} else if valref != 0 { // val is a type to be expanded
					kvvar.VariablesReference = valref
				}
				children = append(children, kvvar)
			}
		}
	case reflect.Slice, reflect.Array:
		children = make([]dap.Variable, len(v.Children))
		for i := range v.Children {
			c := &v.Children[i]
			value, varref := s.convertVariable(c)
			children[i] = dap.Variable{
				Name:               fmt.Sprintf("[%d]", i),
				Value:              value,
				VariablesReference: varref,
			}
		}
	default:
		children = make([]dap.Variable, len(v.Children))
		for i := range v.Children {
			c := &v.Children[i]
			value, variablesReference := s.convertVariable(c)
			children[i] = dap.Variable{
				Name:               c.Name,
				Value:              value,
				VariablesReference: variablesReference,
			}
		}
	}
	response := &dap.VariablesResponse{
		Response: *newResponse(request.Request),
		Body:     dap.VariablesResponseBody{Variables: children},
		// TODO(polina): support evaluateName field
	}
	s.send(response)
}

// convertVariable converts proc.Variable to dap.Variable value and reference.
// Variable reference is used to keep track of the children associated with each
// variable. It is shared with the host via scopes or evaluate response and is an index
// into the s.variableHandles map, used to look up variables and their children on
// subsequent variables requests. A positive reference signals the host that another
// variables request can be issued to get the elements of the compound variable. As a
// custom, a zero reference, reminiscent of a zero pointer, is used to indicate that
// a scalar variable cannot be "dereferenced" to get its elements (as there are none).
func (s *Server) convertVariable(v *proc.Variable) (value string, variablesReference int) {
	return s.convertVariableWithOpts(v, false)
}

func (s *Server) convertVariableToString(v *proc.Variable) string {
	val, _ := s.convertVariableWithOpts(v, true)
	return val
}

// convertVarialbeWithOpts allows to skip reference generation in case all we need is
// a string representation of the variable.
func (s *Server) convertVariableWithOpts(v *proc.Variable, skipRef bool) (value string, variablesReference int) {
	maybeCreateVariableHandle := func(v *proc.Variable) int {
		if skipRef {
			return 0
		}
		return s.variableHandles.create(v)
	}
	if v.Unreadable != nil {
		value = fmt.Sprintf("unreadable <%v>", v.Unreadable)
		return
	}
	typeName := api.PrettyTypeName(v.DwarfType)
	switch v.Kind {
	case reflect.UnsafePointer:
		if len(v.Children) == 0 {
			value = "unsafe.Pointer(nil)"
		} else {
			value = fmt.Sprintf("unsafe.Pointer(%#x)", v.Children[0].Addr)
		}
	case reflect.Ptr:
		if v.DwarfType == nil || len(v.Children) == 0 {
			value = "nil"
		} else if v.Children[0].Addr == 0 {
			value = "nil <" + typeName + ">"
		} else if v.Children[0].Kind == reflect.Invalid {
			value = "void"
		} else {
			value = fmt.Sprintf("<%s>(%#x)", typeName, v.Children[0].Addr)
			variablesReference = maybeCreateVariableHandle(v)
		}
	case reflect.Array:
		value = "<" + typeName + ">"
		if len(v.Children) > 0 {
			variablesReference = maybeCreateVariableHandle(v)
		}
	case reflect.Slice:
		if v.Base == 0 {
			value = "nil <" + typeName + ">"
		} else {
			value = fmt.Sprintf("<%s> (length: %d, cap: %d)", typeName, v.Len, v.Cap)
			if len(v.Children) > 0 {
				variablesReference = maybeCreateVariableHandle(v)
			}
		}
	case reflect.Map:
		if v.Base == 0 {
			value = "nil <" + typeName + ">"
		} else {
			value = fmt.Sprintf("<%s> (length: %d)", typeName, v.Len)
			if len(v.Children) > 0 {
				variablesReference = maybeCreateVariableHandle(v)
			}
		}
	case reflect.String:
		vvalue := constant.StringVal(v.Value)
		lenNotLoaded := v.Len - int64(len(vvalue))
		if lenNotLoaded > 0 {
			vvalue += fmt.Sprintf("...+%d more", lenNotLoaded)
		}
		value = fmt.Sprintf("%q", vvalue)
	case reflect.Chan:
		if len(v.Children) == 0 {
			value = "nil <" + typeName + ">"
		} else {
			value = "<" + typeName + ">"
			variablesReference = maybeCreateVariableHandle(v)
		}
	case reflect.Interface:
		if v.Addr == 0 {
			// An escaped interface variable that points to nil: this shouldn't
			// happen in normal code but can happen if the variable is out of scope,
			// such as if an interface variable has been captured by a
			// closure and replaced by a pointer to interface, and the pointer
			// happens to contain 0.
			value = "nil"
		} else if len(v.Children) == 0 || v.Children[0].Kind == reflect.Invalid && v.Children[0].Addr == 0 {
			value = "nil <" + typeName + ">"
		} else {
			value = "<" + typeName + "(" + v.Children[0].TypeString() + ")" + ">"
			// TODO(polina): should we remove one level of indirection and skip "data"?
			// Then we will have:
			// Before:
			//   i: <interface{}(int)>
			//      data: 123
			// After:
			//   i: <interface{}(int)> 123
			// Before:
			//   i: <interface{}(main.MyStruct)>
			//      data: <main.MyStruct>
			//         field1: ...
			// After:
			//   i: <interface{}(main.MyStruct)>
			//      field1: ...
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
	default: // Struct, complex, scalar
		vvalue := api.VariableValueAsString(v)
		if vvalue != "" {
			value = vvalue
		} else {
			value = "<" + typeName + ">"
		}
		if len(v.Children) > 0 {
			variablesReference = maybeCreateVariableHandle(v)
		}
	}
	return
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
	showErrorToUser := request.Arguments.Context != "watch"
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
	// TODO(polina): Support config settings via launch/attach args vs auto-loading?
	apiCfg := &api.LoadConfig{FollowPointers: true, MaxVariableRecurse: 1, MaxStringLen: 64, MaxArrayValues: 64, MaxStructFields: -1}
	prcCfg := proc.LoadConfig{FollowPointers: true, MaxVariableRecurse: 1, MaxStringLen: 64, MaxArrayValues: 64, MaxStructFields: -1}

	response := &dap.EvaluateResponse{Response: *newResponse(request.Request)}
	isCall, err := regexp.MatchString(`^\s*call\s+\S+`, request.Arguments.Expression)
	if err == nil && isCall { // call {expression}
		// This call might be evaluated in the context of the frame that is not topmost
		// if the editor is set to view the variables for one of the parent frames.
		// If the call expression refers to any of these variables, unlike regular
		// expressions, it will evaluate them in the context of the topmost frame,
		// and the user will get an unexpected result or an unexpected symbol error.
		// We prevent this but disallowing any frames other than topmost.
		if frame > 0 {
			s.sendErrorResponseWithOpts(request.Request, UnableToEvaluateExpression, "Unable to evaluate expression", "call is only supported with topmost stack frame", showErrorToUser)
			return
		}
		stateBeforeCall, err := s.debugger.State( /*nowait*/ true)
		if err != nil {
			s.sendErrorResponseWithOpts(request.Request, UnableToEvaluateExpression, "Unable to evaluate expression", err.Error(), showErrorToUser)
			return
		}
		state, err := s.debugger.Command(&api.DebuggerCommand{
			Name:                 api.Call,
			ReturnInfoLoadConfig: apiCfg,
			Expr:                 strings.Replace(request.Arguments.Expression, "call ", "", 1),
			UnsafeCall:           false,
			GoroutineID:          goid,
		})
		if _, isexited := err.(proc.ErrProcessExited); isexited || err == nil && state.Exited {
			e := &dap.TerminatedEvent{Event: *newEvent("terminated")}
			s.send(e)
			return
		}
		if err != nil {
			s.sendErrorResponseWithOpts(request.Request, UnableToEvaluateExpression, "Unable to evaluate expression", err.Error(), showErrorToUser)
			return
		}
		// After the call is done, the goroutine where we injected the call should
		// return to the original stopped line with return values. However,
		// it is not guaranteed to be selected due to the possibility of the
		// of simultaenous breakpoints. Therefore, we check all threads.
		var retVars []*proc.Variable
		for _, t := range state.Threads {
			if t.GoroutineID == stateBeforeCall.SelectedGoroutine.ID &&
				t.Line == stateBeforeCall.SelectedGoroutine.CurrentLoc.Line && t.CallReturn {
				// The call completed. Get the return values.
				retVars, err = s.debugger.FindThreadReturnValues(t.ID, prcCfg)
				if err != nil {
					s.sendErrorResponseWithOpts(request.Request, UnableToEvaluateExpression, "Unable to evaluate expression", err.Error(), showErrorToUser)
					return
				}
				break
			}
		}
		if retVars == nil {
			// The call got interrupted by a stop (e.g. breakpoint in injected
			// function call or in another goroutine)
			s.resetHandlesForStop()
			stopped := &dap.StoppedEvent{Event: *newEvent("stopped")}
			stopped.Body.AllThreadsStopped = true
			if state.SelectedGoroutine != nil {
				stopped.Body.ThreadId = state.SelectedGoroutine.ID
			}
			stopped.Body.Reason = s.debugger.StopReason().String()
			s.send(stopped)
			// TODO(polina): once this is asynchronous, we could wait to reply until the user
			// continues, call ends, original stop point is hit and return values are available.
			s.sendErrorResponseWithOpts(request.Request, UnableToEvaluateExpression, "Unable to evaluate expression", "call stopped", showErrorToUser)
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
				VariablesReference: s.variableHandles.create(retVarsAsVar),
			}
		}
	} else { // {expression}
		exprVar, err := s.debugger.EvalVariableInScope(goid, frame, 0, request.Arguments.Expression, prcCfg)
		if err != nil {
			s.sendErrorResponseWithOpts(request.Request, UnableToEvaluateExpression, "Unable to evaluate expression", err.Error(), showErrorToUser)
			return
		}
		exprVal, exprRef := s.convertVariable(exprVar)
		response.Body = dap.EvaluateResponseBody{Result: exprVal, VariablesReference: exprRef}
	}
	s.send(response)
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

// onSetFunctionBreakpointsRequest sends a not-yet-implemented error response.
// Capability 'supportsFunctionBreakpoints' is not set 'initialize' response.
func (s *Server) onSetFunctionBreakpointsRequest(request *dap.SetFunctionBreakpointsRequest) {
	s.sendNotYetImplementedErrorResponse(request.Request)
}

// onStepBackRequest sends a not-yet-implemented error response.
// Capability 'supportsStepBack' is not set 'initialize' response.
func (s *Server) onStepBackRequest(request *dap.StepBackRequest) {
	s.sendNotYetImplementedErrorResponse(request.Request)
}

// onReverseContinueRequest sends a not-yet-implemented error response.
// Capability 'supportsStepBack' is not set 'initialize' response.
func (s *Server) onReverseContinueRequest(request *dap.ReverseContinueRequest) {
	s.sendNotYetImplementedErrorResponse(request.Request)
}

// onSetVariableRequest sends a not-yet-implemented error response.
// Capability 'supportsSetVariable' is not set 'initialize' response.
func (s *Server) onSetVariableRequest(request *dap.SetVariableRequest) { // TODO V0
	s.sendNotYetImplementedErrorResponse(request.Request)
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

// sendERrorResponseWithOpts offers configuration options.
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
	s.log.Error(er.Body.Error.Format)
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
	s.log.Error(er.Body.Error.Format)
	s.send(er)
}

func (s *Server) sendUnsupportedErrorResponse(request dap.Request) {
	s.sendErrorResponse(request, UnsupportedCommand, "Unsupported command",
		fmt.Sprintf("cannot process '%s' request", request.Command))
}

func (s *Server) sendNotYetImplementedErrorResponse(request dap.Request) {
	s.sendErrorResponse(request, NotYetImplemented, "Not yet implemented",
		fmt.Sprintf("cannot process '%s' request", request.Command))
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
Unable to propogate EXC_BAD_ACCESS signal to target process and panic (see https://github.com/go-delve/delve/issues/852)`

func (s *Server) resetHandlesForStop() {
	s.stackFrameHandles.reset()
	s.variableHandles.reset()
}

// doCommand runs a debugger command until it stops on
// termination, error, breakpoint, etc, when an appropriate
// event needs to be sent to the client.
func (s *Server) doCommand(command string) {
	if s.debugger == nil {
		return
	}

	state, err := s.debugger.Command(&api.DebuggerCommand{Name: command})
	if _, isexited := err.(proc.ErrProcessExited); isexited || err == nil && state.Exited {
		e := &dap.TerminatedEvent{Event: *newEvent("terminated")}
		s.send(e)
		return
	}

	s.resetHandlesForStop()
	stopped := &dap.StoppedEvent{Event: *newEvent("stopped")}
	stopped.Body.AllThreadsStopped = true

	if err == nil {
		if state.SelectedGoroutine != nil {
			stopped.Body.ThreadId = state.SelectedGoroutine.ID
		}

		switch s.debugger.StopReason() {
		case proc.StopNextFinished:
			stopped.Body.Reason = "step"
		default:
			stopped.Body.Reason = "breakpoint"
		}
		if state.CurrentThread.Breakpoint != nil {
			switch state.CurrentThread.Breakpoint.Name {
			case proc.FatalThrow:
				stopped.Body.Reason = "fatal error"
			case proc.UnrecoveredPanic:
				stopped.Body.Reason = "panic"
			}
		}
		s.send(stopped)
	} else {
		s.log.Error("runtime error: ", err)
		stopped.Body.Reason = "runtime error"
		stopped.Body.Text = err.Error()
		// Special case in the spirit of https://github.com/microsoft/vscode-go/issues/1903
		if stopped.Body.Text == "bad access" {
			stopped.Body.Text = BetterBadAccessError
		}
		state, err := s.debugger.State( /*nowait*/ true)
		if err == nil {
			stopped.Body.ThreadId = state.CurrentThread.GoroutineID
		}
		s.send(stopped)

		// TODO(polina): according to the spec, the extra 'text' is supposed to show up in the UI (e.g. on hover),
		// but so far I am unable to get this to work in vscode - see https://github.com/microsoft/vscode/issues/104475.
		// Options to explore:
		//   - supporting ExceptionInfo request
		//   - virtual variable scope for Exception that shows the message (details here: https://github.com/microsoft/vscode/issues/3101)
		// In the meantime, provide the extra details by outputing an error message.
		s.send(&dap.OutputEvent{
			Event: *newEvent("output"),
			Body: dap.OutputEventBody{
				Output:   fmt.Sprintf("ERROR: %s\n", stopped.Body.Text),
				Category: "stderr",
			}})
	}
}
