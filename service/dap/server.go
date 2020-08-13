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
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"

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
	stackFrameHandles *handlesMap
	// variableHandles maps compound variables to unique references within their stack frame.
	// See also comment for convertVariable.
	variableHandles *handlesMap
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
}

// defaultArgs borrows the defaults for the arguments from the original vscode-go adapter.
var defaultArgs = launchAttachArgs{
	stopOnEntry:     false,
	stackTraceDepth: 50,
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
		variableHandles:   newHandlesMap(),
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
		// TODO: implement this request in V0
		s.onNextRequest(request)
	case *dap.StepInRequest:
		// Required
		// TODO: implement this request in V0
		s.onStepInRequest(request)
	case *dap.StepOutRequest:
		// Required
		// TODO: implement this request in V0
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
		// Required - TODO
		// TODO: implement this request in V0
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

	stop, ok := request.Arguments["stopOnEntry"]
	s.args.stopOnEntry = ok && stop == true

	depth, ok := request.Arguments["stackTraceDepth"].(float64)
	if ok && depth > 0 {
		s.args.stackTraceDepth = int(depth)
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
	if request.Arguments.Source.Path == "" {
		s.log.Error("ERROR: Unable to set breakpoint for empty file path")
	}
	response := &dap.SetBreakpointsResponse{Response: *newResponse(request.Request)}
	response.Body.Breakpoints = make([]dap.Breakpoint, len(request.Arguments.Breakpoints))
	// Only verified breakpoints will be set and reported back in the
	// response. All breakpoints resulting in errors (e.g. duplicates
	// or lines that do not have statements) will be skipped.
	i := 0
	for _, b := range request.Arguments.Breakpoints {
		bp, err := s.debugger.CreateBreakpoint(
			&api.Breakpoint{File: request.Arguments.Source.Path, Line: b.Line})
		if err != nil {
			s.log.Error("ERROR:", err)
			continue
		}
		response.Body.Breakpoints[i].Verified = true
		response.Body.Breakpoints[i].Line = bp.Line
		i++
	}
	response.Body.Breakpoints = response.Body.Breakpoints[:i]
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
		s.doContinue()
	}
}

func (s *Server) onContinueRequest(request *dap.ContinueRequest) {
	s.send(&dap.ContinueResponse{Response: *newResponse(request.Request)})
	s.doContinue()
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
		for i, g := range gs {
			threads[i].Id = g.ID
			if loc := g.UserCurrentLoc; loc.Function != nil {
				threads[i].Name = loc.Function.Name()
			} else {
				threads[i].Name = fmt.Sprintf("%s@%d", loc.File, loc.Line)
			}
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

// onNextRequest sends a not-yet-implemented error response.
// This is a mandatory request to support.
func (s *Server) onNextRequest(request *dap.NextRequest) { // TODO V0
	s.sendNotYetImplementedErrorResponse(request.Request)
}

// onStepInRequest sends a not-yet-implemented error response.
// This is a mandatory request to support.
func (s *Server) onStepInRequest(request *dap.StepInRequest) { // TODO V0
	s.sendNotYetImplementedErrorResponse(request.Request)
}

// onStepOutRequest sends a not-yet-implemented error response.
// This is a mandatory request to support.
func (s *Server) onStepOutRequest(request *dap.StepOutRequest) { // TODO V0
	s.sendNotYetImplementedErrorResponse(request.Request)
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
	locs, err := s.debugger.Stacktrace(goroutineID, s.args.stackTraceDepth, 0, nil /*skip locals & args*/)
	if err != nil {
		s.sendErrorResponse(request.Request, UnableToProduceStackTrace, "Unable to produce stack trace", err.Error())
		return
	}

	stackFrames := make([]dap.StackFrame, len(locs))
	for i, loc := range locs {
		uniqueStackFrameID := s.stackFrameHandles.create(stackFrame{goroutineID, i})
		stackFrames[i] = dap.StackFrame{Id: uniqueStackFrameID, Line: loc.Line}
		stackFrames[i].Name = loc.Function.Name()
		if loc.File != "<autogenerated>" {
			stackFrames[i].Source = dap.Source{Name: filepath.Base(loc.File), Path: loc.File}
		}
		stackFrames[i].Column = 0
	}
	if request.Arguments.StartFrame > 0 {
		stackFrames = stackFrames[min(request.Arguments.StartFrame, len(stackFrames)):]
	}
	if request.Arguments.Levels > 0 {
		stackFrames = stackFrames[:min(request.Arguments.Levels, len(stackFrames))]
	}
	response := &dap.StackTraceResponse{
		Response: *newResponse(request.Request),
		Body:     dap.StackTraceResponseBody{StackFrames: stackFrames, TotalFrames: len(locs)},
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

	scope := api.EvalScope{GoroutineID: sf.(stackFrame).goroutineID, Frame: sf.(stackFrame).frameIndex}
	// TODO(polina): Support setting config via launch/attach args
	cfg := proc.LoadConfig{FollowPointers: true, MaxVariableRecurse: 1, MaxStringLen: 64, MaxArrayValues: 64, MaxStructFields: -1}

	// Retrieve arguments
	args, err := s.debugger.FunctionArguments(scope, cfg)
	if err != nil {
		s.sendErrorResponse(request.Request, UnableToListArgs, "Unable to list args", err.Error())
		return
	}
	argScope := api.Variable{Name: "Arguments", Children: args}

	// Retrieve local variables
	locals, err := s.debugger.LocalVariables(scope, cfg)
	if err != nil {
		s.sendErrorResponse(request.Request, UnableToListLocals, "Unable to list local vars", err.Error())
		return
	}
	locScope := api.Variable{Name: "Locals", Children: locals}

	// TODO(polina): Annotate shadowed variables
	// TODO(polina): Retrieve global variables

	scopeArgs := dap.Scope{Name: argScope.Name, VariablesReference: s.variableHandles.create(argScope)}
	scopeLocals := dap.Scope{Name: locScope.Name, VariablesReference: s.variableHandles.create(locScope)}
	scopes := []dap.Scope{scopeArgs, scopeLocals}

	response := &dap.ScopesResponse{
		Response: *newResponse(request.Request),
		Body:     dap.ScopesResponseBody{Scopes: scopes},
	}
	s.send(response)
}

// onVariablesRequest handles 'variables' requests.
// This is a mandatory request to support.
func (s *Server) onVariablesRequest(request *dap.VariablesRequest) {
	variable, ok := s.variableHandles.get(request.Arguments.VariablesReference)
	if !ok {
		s.sendErrorResponse(request.Request, UnableToLookupVariable, "Unable to lookup variable", fmt.Sprintf("unknown reference %d", request.Arguments.VariablesReference))
		return
	}
	v := variable.(api.Variable)
	children := make([]dap.Variable, 0)
	// TODO(polina): check and handle if variable loaded incompletely
	// https://github.com/go-delve/delve/blob/master/Documentation/api/ClientHowto.md#looking-into-variables

	switch v.Kind {
	case reflect.Map:
		for i := 0; i < len(v.Children); i += 2 {
			// A map will have twice as many children as there are key-value elements.
			kvIndex := i / 2
			// Process children in pairs: even indices are map keys, odd indices are values.
			key, keyref := s.convertVariable(v.Children[i])
			val, valref := s.convertVariable(v.Children[i+1])
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
		for i, c := range v.Children {
			value, varref := s.convertVariable(c)
			children[i] = dap.Variable{
				Name:               fmt.Sprintf("[%d]", i),
				Value:              value,
				VariablesReference: varref,
			}
		}
	default:
		children = make([]dap.Variable, len(v.Children))
		for i, c := range v.Children {
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

// convertVariable converts api.Variable to dap.Variable value and reference.
// Variable reference is used to keep track of the children associated with each
// variable. It is shared with the host via a scopes response and is an index to
// the s.variableHandles map, so it can be referenced from a subsequent variables
// request. A positive reference signals the host that another variables request
// can be issued to get the elements of the compound variable. As a custom, a zero
// reference, reminiscent of a zero pointer, is used to indicate that a scalar
// variable cannot be "dereferenced" to get its elements (as there are none).
func (s *Server) convertVariable(v api.Variable) (value string, variablesReference int) {
	if v.Unreadable != "" {
		value = fmt.Sprintf("unreadable <%s>", v.Unreadable)
		return
	}
	switch v.Kind {
	case reflect.UnsafePointer:
		if len(v.Children) == 0 {
			value = "unsafe.Pointer(nil)"
		} else {
			value = fmt.Sprintf("unsafe.Pointer(%#x)", v.Children[0].Addr)
		}
	case reflect.Ptr:
		if v.Type == "" || len(v.Children) == 0 {
			value = "nil"
		} else if v.Children[0].Addr == 0 {
			value = "nil <" + v.Type + ">"
		} else if v.Children[0].Type == "void" {
			value = "void"
		} else {
			value = fmt.Sprintf("<%s>(%#x)", v.Type, v.Children[0].Addr)
			variablesReference = s.variableHandles.create(v)
		}
	case reflect.Array:
		value = "<" + v.Type + ">"
		if len(v.Children) > 0 {
			variablesReference = s.variableHandles.create(v)
		}
	case reflect.Slice:
		if v.Base == 0 {
			value = "nil <" + v.Type + ">"
		} else {
			value = fmt.Sprintf("<%s> (length: %d, cap: %d)", v.Type, v.Len, v.Cap)
			if len(v.Children) > 0 {
				variablesReference = s.variableHandles.create(v)
			}
		}
	case reflect.Map:
		if v.Base == 0 {
			value = "nil <" + v.Type + ">"
		} else {
			value = fmt.Sprintf("<%s> (length: %d)", v.Type, v.Len)
			if len(v.Children) > 0 {
				variablesReference = s.variableHandles.create(v)
			}
		}
	case reflect.String:
		lenNotLoaded := v.Len - int64(len(v.Value))
		vvalue := v.Value
		if lenNotLoaded > 0 {
			vvalue += fmt.Sprintf("...+%d more", lenNotLoaded)
		}
		value = fmt.Sprintf("%q", vvalue)
	case reflect.Chan:
		if len(v.Children) == 0 {
			value = "nil <" + v.Type + ">"
		} else {
			value = "<" + v.Type + ">"
			variablesReference = s.variableHandles.create(v)
		}
	case reflect.Interface:
		if len(v.Children) == 0 || v.Children[0].Kind == reflect.Invalid && v.Children[0].Addr == 0 {
			value = "nil <" + v.Type + ">"
		} else {
			value = "<" + v.Type + ">"
			variablesReference = s.variableHandles.create(v)
		}
	default: // Struct, complex, scalar
		if v.Value != "" {
			value = v.Value
		} else {
			value = "<" + v.Type + ">"
		}
		if len(v.Children) > 0 {
			variablesReference = s.variableHandles.create(v)
		}
	}
	return
}

// onEvaluateRequest sends a not-yet-implemented error response.
// This is a mandatory request to support.
func (s *Server) onEvaluateRequest(request *dap.EvaluateRequest) { // TODO V0
	s.sendNotYetImplementedErrorResponse(request.Request)
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

func (s *Server) sendErrorResponse(request dap.Request, id int, summary, details string) {
	er := &dap.ErrorResponse{}
	er.Type = "response"
	er.Command = request.Command
	er.RequestSeq = request.Seq
	er.Success = false
	er.Message = summary
	er.Body.Error.Id = id
	er.Body.Error.Format = fmt.Sprintf("%s: %s", summary, details)
	s.log.Error(er.Body.Error.Format)
	s.send(er)
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

func (s *Server) doContinue() {
	if s.debugger == nil {
		return
	}
	state, err := s.debugger.Command(&api.DebuggerCommand{Name: api.Continue})
	if err != nil {
		s.handleStopOnError(err)
	} else {
		s.handleStop(state, "breakpoint")
	}
}

func (s *Server) clearProcessStateHandles() {
	s.stackFrameHandles.reset()
	s.variableHandles.reset()
}

// handleStopOnError resets the stage for refreshing debuggee state
// and sends an apropriate event to the client, followed by
// an output event with the details of the error.
func (s *Server) handleStopOnError(err error) {
	s.log.Error("runtime error: ", err)
	s.clearProcessStateHandles()

	switch err.(type) {
	case proc.ErrProcessExited:
		e := &dap.TerminatedEvent{Event: *newEvent("terminated")}
		s.send(e)
	default:
		e := &dap.StoppedEvent{Event: *newEvent("stopped")}

		e.Body.AllThreadsStopped = true
		e.Body.Reason = "runtime error"
		e.Body.Text = err.Error()
		// Special case in the spirit of https://github.com/microsoft/vscode-go/issues/1903
		if e.Body.Text == "bad access" {
			e.Body.Text = fmt.Sprintf(
				"%s\n%s",
				"invalid memory address or nil pointer dereference [signal SIGSEGV: segmentation violation]",
				"Unable to propogate EXC_BAD_ACCESS signal to target process and panic (see https://github.com/go-delve/delve/issues/852)")
		}
		state, err := s.debugger.State( /*nowait*/ true)
		if err == nil {
			e.Body.ThreadId = state.CurrentThread.GoroutineID
		}
		s.send(e)

		// TODO(polina): according to the spec, the extra 'text' is supposed to show up in the UI (e.g. on hover),
		// but so far I am unable to get this to work in vscode - see https://github.com/microsoft/vscode/issues/104475.
		// Options to explore:
		//   - supporting ExceptionInfo request
		//   - virtual variable scope for Exception that shows the message (details here: https://github.com/microsoft/vscode/issues/3101)
		// In the meantime, provide the extra details by outputing an error message.
		// {"body":{"category":"stdout","output":"API server listening at: 127.0.0.1:11973\n"}}
		s.send(&dap.OutputEvent{
			Event: *newEvent("output"),
			Body: dap.OutputEventBody{
				Output:   fmt.Sprintf("ERROR: %s", e.Body.Text),
				Category: "stderr",
			}})
	}
}

// handleStop resets the stage for refreshing debuggee state
// and sends an apropriate event to the client when execution stops
// due to normal causes (termination, breakpoint, step, etc).
func (s *Server) handleStop(state *api.DebuggerState, reason string) {
	s.clearProcessStateHandles()

	if state.Exited {
		e := &dap.TerminatedEvent{Event: *newEvent("terminated")}
		s.send(e)
	} else {
		e := &dap.StoppedEvent{Event: *newEvent("stopped")}
		e.Body.Reason = reason
		e.Body.AllThreadsStopped = true
		e.Body.ThreadId = state.SelectedGoroutine.ID
		s.send(e)
	}
}
