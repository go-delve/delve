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
	"path/filepath"

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
	// stopOnEntry is set to automatically stop the debugee after start.
	stopOnEntry bool
	// binaryToRemove is the compiled binary to be removed on disconnect.
	binaryToRemove string
}

// NewServer creates a new DAP Server. It takes an opened Listener
// via config and assumes its ownership. config.disconnectChan has to be set;
// it will be closed by the server when the client disconnects or requests
// shutdown. Once disconnectChan is closed, Server.Stop() must be called.
func NewServer(config *service.Config) *Server {
	logger := logflags.DAPLogger()
	logflags.WriteDAPListeningMessage(config.Listener.Addr().String())
	return &Server{
		config:   config,
		listener: config.Listener,
		stopChan: make(chan struct{}),
		log:      logger,
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
		kill := s.config.AttachPid == 0
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
		s.onInitializeRequest(request)
	case *dap.LaunchRequest:
		s.onLaunchRequest(request)
	case *dap.AttachRequest:
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.DisconnectRequest:
		s.onDisconnectRequest(request)
	case *dap.TerminateRequest:
		s.onTerminateRequest(request)
	case *dap.RestartRequest:
		s.onRestartRequest(request)
	case *dap.SetBreakpointsRequest:
		s.onSetBreakpointsRequest(request)
	case *dap.SetFunctionBreakpointsRequest:
		s.onSetFunctionBreakpointsRequest(request)
	case *dap.SetExceptionBreakpointsRequest:
		s.onSetExceptionBreakpointsRequest(request)
	case *dap.ConfigurationDoneRequest:
		s.onConfigurationDoneRequest(request)
	case *dap.ContinueRequest:
		s.onContinueRequest(request)
	case *dap.NextRequest:
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.StepInRequest:
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.StepOutRequest:
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.StepBackRequest:
		s.onStepBackRequest(request)
	case *dap.ReverseContinueRequest:
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.RestartFrameRequest:
		s.onRestartFrameRequest(request)
	case *dap.GotoRequest:
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.PauseRequest:
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.StackTraceRequest:
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.ScopesRequest:
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.VariablesRequest:
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.SetVariableRequest:
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.SetExpressionRequest:
		s.onSetExpressionRequest(request)
	case *dap.SourceRequest:
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.ThreadsRequest:
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.TerminateThreadsRequest:
		s.onTerminateThreadsRequest(request)
	case *dap.EvaluateRequest:
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.StepInTargetsRequest:
		s.onStepInTargetsRequest(request)
	case *dap.GotoTargetsRequest:
		s.onGotoTargetsRequest(request)
	case *dap.CompletionsRequest:
		s.onCompletionsRequest(request)
	case *dap.ExceptionInfoRequest:
		s.onExceptionInfoRequest(request)
	case *dap.LoadedSourcesRequest:
		s.onLoadedSourcesRequest(request)
	case *dap.DataBreakpointInfoRequest:
		s.sendUnsupportedErrorResponse(request.Request)
	case *dap.SetDataBreakpointsRequest:
		s.onSetDataBreakpointsRequest(request)
	case *dap.ReadMemoryRequest:
		s.onReadMemoryRequest(request)
	case *dap.DisassembleRequest:
		s.onDisassembleRequest(request)
	case *dap.CancelRequest:
		s.onCancelRequest(request)
	case *dap.BreakpointLocationsRequest:
		s.onBreakpointLocationsRequest(request)
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
	s.send(response)
}

// Output path for the compiled binary in debug or test modes.
const debugBinary string = "./__debug_bin"

func (s *Server) onLaunchRequest(request *dap.LaunchRequest) {
	// TODO(polina): Respond with an error if debug session is in progress?

	program, ok := request.Arguments["program"].(string)
	if !ok || program == "" {
		s.sendErrorResponse(request.Request,
			FailedToContinue, "Failed to launch",
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

		buildFlags, ok := request.Arguments["buildFlags"].(string)
		if !ok {
			buildFlags = ""
		}

		switch mode {
		case "debug":
			err = gobuild.GoBuild(debugname, []string{program}, buildFlags)
		case "test":
			err = gobuild.GoTestBuild(debugname, []string{program}, buildFlags)
		}
		if err != nil {
			s.sendErrorResponse(request.Request,
				FailedToContinue, "Failed to launch",
				fmt.Sprintf("Build error: %s", err.Error()))
			return
		}
		program = debugname
		s.binaryToRemove = debugname
	}

	// TODO(polina): support "remote" mode
	if mode != "exec" && mode != "debug" && mode != "test" {
		s.sendErrorResponse(request.Request,
			FailedToContinue, "Failed to launch",
			fmt.Sprintf("Unsupported 'mode' value %q in debug configuration.", mode))
		return
	}

	stop, ok := request.Arguments["stopOnEntry"]
	s.stopOnEntry = (ok && stop == true)

	// TODO(polina): support target args
	s.config.ProcessArgs = []string{program}
	s.config.WorkingDir = filepath.Dir(program)

	config := &debugger.Config{
		WorkingDir:           s.config.WorkingDir,
		AttachPid:            0,
		CoreFile:             "",
		Backend:              s.config.Backend,
		Foreground:           s.config.Foreground,
		DebugInfoDirectories: s.config.DebugInfoDirectories,
		CheckGoVersion:       s.config.CheckGoVersion,
	}
	var err error
	if s.debugger, err = debugger.New(config, s.config.ProcessArgs); err != nil {
		s.sendErrorResponse(request.Request,
			FailedToContinue, "Failed to launch", err.Error())
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
		kill := s.config.AttachPid == 0
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
	// even though we specified no filters at initializatin. Handle as no-op.
	s.send(&dap.SetExceptionBreakpointsResponse{Response: *newResponse(request.Request)})
}

func (s *Server) onConfigurationDoneRequest(request *dap.ConfigurationDoneRequest) {
	if s.stopOnEntry {
		e := &dap.StoppedEvent{
			Event: *newEvent("stopped"),
			Body:  dap.StoppedEventBody{Reason: "breakpoint", ThreadId: 1, AllThreadsStopped: true},
		}
		s.send(e)
	}
	s.send(&dap.ConfigurationDoneResponse{Response: *newResponse(request.Request)})
	if !s.stopOnEntry {
		s.doContinue()
	}
}

func (s *Server) onContinueRequest(request *dap.ContinueRequest) {
	s.send(&dap.ContinueResponse{Response: *newResponse(request.Request)})
	s.doContinue()
}

//
// The rest of the handlers below are no-ops because the adaptor
// does not support these messages. We choose no-op over an error response
// because that is the default behavior in vscode-debugadapter-node
// that many other adaptors, including vscode-go, inherit from.
//

// onTerminateRequest is no-op because this adaptor does not support
// the 'terminate' request (supportsTerminateRequest is false in
// 'initialize' response).
func (s *Server) onTerminateRequest(request *dap.TerminateRequest) {
	s.send(&dap.TerminateResponse{Response: *newResponse(request.Request)})
}

// onRestartRequest is no-op because this adaptor does not support
// the 'restart' request (supportsRestartRequest is false in
// 'initialize' response).
func (s *Server) onRestartRequest(request *dap.RestartRequest) {
	s.send(&dap.RestartResponse{Response: *newResponse(request.Request)})
}

// onSetFunctionBreakpointsRequest is no-op because this adaptor does not support
// the 'setFunctionBreakpoints' request (supportsSetFunctionBreakpoints is false in
// 'initialize' response).
func (s *Server) onSetFunctionBreakpointsRequest(request *dap.SetFunctionBreakpointsRequest) {
	s.send(&dap.SetFunctionBreakpointsResponse{Response: *newResponse(request.Request)})
}

// onStepBackRequest is no-op because this adaptor does not support
// the 'stepBack' request (supportsStepBack is false in
// 'initialize' response).
func (s *Server) onStepBackRequest(request *dap.StepBackRequest) {
	s.send(&dap.StepBackResponse{Response: *newResponse(request.Request)})
}

// onRestartFrameRequest is no-op because this adaptor does not support
// the 'restartFrame' request (supportsRestartFrame is false in
// 'initialize' response).
func (s *Server) onRestartFrameRequest(request *dap.RestartFrameRequest) {
	s.send(&dap.RestartFrameResponse{Response: *newResponse(request.Request)})
}

// onSetExpression is no-op because this adaptor does not support
// the 'setExpression' request (supportsSetExpression is false in
// 'initialize' response).
func (s *Server) onSetExpressionRequest(request *dap.SetExpressionRequest) {
	s.send(&dap.SetExpressionResponse{Response: *newResponse(request.Request)})
}

// onTerminateThreadsRequest is no-op because this adaptor does not support
// the 'terminateThreads' request (supportsTerminateThreadsRequest is false in
// 'initialize' response).
func (s *Server) onTerminateThreadsRequest(request *dap.TerminateThreadsRequest) {
	s.send(&dap.TerminateThreadsResponse{Response: *newResponse(request.Request)})
}

// onStepInTargetsRequest is no-op because this adaptor does not support
// the 'stepInTargets' request (supportsStepInTargetsRequest is false in
// 'initialize' response).
func (s *Server) onStepInTargetsRequest(request *dap.StepInTargetsRequest) {
	s.send(&dap.StepInTargetsResponse{Response: *newResponse(request.Request)})
}

// onGotoTargetsRequest is no-op because this adaptor does not support
// the 'gotoTargets' request (supportsGotoTargetsRequest is false in
// 'initialize' response).
func (s *Server) onGotoTargetsRequest(request *dap.GotoTargetsRequest) {
	s.send(&dap.GotoTargetsResponse{Response: *newResponse(request.Request)})
}

// onCompletionsRequest is no-op because this adaptor does not support
// the 'completions' request (supportsCompletionsRequest is false in
// 'initialize' response).
func (s *Server) onCompletionsRequest(request *dap.CompletionsRequest) {
	s.send(&dap.CompletionsResponse{Response: *newResponse(request.Request)})
}

// onExceptionInfoRequest is no-op because this adaptor does not support
// the 'exceptionInfo' request (supportsExceptionInfoRequest is false in
// 'initialize' response).
func (s *Server) onExceptionInfoRequest(request *dap.ExceptionInfoRequest) {
	s.send(&dap.ExceptionInfoResponse{Response: *newResponse(request.Request)})
}

// onLoadedSourcesRequest is no-op because this adaptor does not support
// the 'loadedSources' request (supportsLoadedSourcesRequest is false in
// 'initialize' response).
func (s *Server) onLoadedSourcesRequest(request *dap.LoadedSourcesRequest) {
	s.send(&dap.LoadedSourcesResponse{Response: *newResponse(request.Request)})
}

// onSetDataBreakpointsRequest is no-op because this adaptor does not support
// the 'setDataBreakpoints' request (supportsDataBreakpoints is false in
// 'initialize' response).
func (s *Server) onSetDataBreakpointsRequest(request *dap.SetDataBreakpointsRequest) {
	s.send(&dap.SetDataBreakpointsResponse{Response: *newResponse(request.Request)})
}

// onReadMemoryRequest is no-op because this adaptor does not support
// the 'readMemory' request (supportsReadMemoryRequest is false in
// 'initialize' response).
func (s *Server) onReadMemoryRequest(request *dap.ReadMemoryRequest) {
	s.send(&dap.ReadMemoryResponse{Response: *newResponse(request.Request)})
}

// onDisassembleRequest is no-op because this adaptor does not support
// the 'disassemble' request (supportsDisassembleRequest is false in
// 'initialize' response).
func (s *Server) onDisassembleRequest(request *dap.DisassembleRequest) {
	s.send(&dap.DisassembleResponse{Response: *newResponse(request.Request)})
}

// onCancelRequest is no-op because this adaptor does not support
// the 'cancel' request (supportsCancelRequest is false in
// 'initialize' response).
func (s *Server) onCancelRequest(request *dap.CancelRequest) {
	s.send(&dap.CancelResponse{Response: *newResponse(request.Request)})
}

// onBreakpointLocationsRequest is no-op because this adaptor does not support
// the 'breakpointLocations' request (supportsBreakpointLocationsRequest is false in
// 'initialize' response).
func (s *Server) onBreakpointLocationsRequest(request *dap.BreakpointLocationsRequest) {
	s.send(&dap.BreakpointLocationsResponse{Response: *newResponse(request.Request)})
}

func (s *Server) sendErrorResponse(request dap.Request, id int, summary string, details string) {
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
		s.log.Error(err)
		switch err.(type) {
		case proc.ErrProcessExited:
			e := &dap.TerminatedEvent{Event: *newEvent("terminated")}
			s.send(e)
		default:
		}
		return
	}
	if state.Exited {
		e := &dap.TerminatedEvent{Event: *newEvent("terminated")}
		s.send(e)
	} else {
		e := &dap.StoppedEvent{Event: *newEvent("stopped")}
		// TODO(polina): differentiate between breakpoint and pause on halt.
		e.Body.Reason = "breakpoint"
		e.Body.AllThreadsStopped = true
		e.Body.ThreadId = state.SelectedGoroutine.ID
		s.send(e)
	}
}
