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
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/go-delve/delve/pkg/gobuild"
	"github.com/go-delve/delve/pkg/locspec"
	"github.com/go-delve/delve/pkg/logflags"
	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/service"
	"github.com/go-delve/delve/service/api"
	"github.com/go-delve/delve/service/debugger"
	"github.com/google/go-dap"
	"github.com/sirupsen/logrus"
)

// Server implements a DAP server that can accept a single client for
// a single debug session (for now). It does yet not support restarting.
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
// reads, decodes and processes each request, issuing commands to the
// underlying debugger and sending back events and responses.
// This gorouitne sends a stop-server signal via config.DisconnecChan
// when encounering a client connection error or responding to
// a DAP disconnect request.
//
// TODO(polina): add another layer of per-client goroutines to support multiple clients
// TODO(polina): make it asynchronous (i.e. launch goroutine per request)
type Server struct {
	// config is all the information necessary to start the debugger and server.
	config *service.Config
	// listener is used to accept the client connection.
	listener net.Listener
	// stopTriggered is closed when the server is Stop()-ed. This can be used to signal
	// to goroutines run by the server that it's time to quit.
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

// DefaultLoadConfig controls how variables are loaded from the target's memory, borrowing the
// default value from the original vscode-go debug adapter and rpc server.
// TODO(polina): Support setting config via launch/attach args or only rely on on-demand loading?
var DefaultLoadConfig = proc.LoadConfig{
	FollowPointers:     true,
	MaxVariableRecurse: 1,
	MaxStringLen:       64,
	MaxArrayValues:     64,
	MaxStructFields:    -1,
}

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
	if s.conn != nil {
		// Unless Stop() was called after serveDAPCodec()
		// returned, this will result in closed connection error
		// on next read, breaking out of the read loop and
		// allowing the run goroutine to exit.
		_ = s.conn.Close()
	}
	if s.debugger != nil {
		killProcess := s.config.Debugger.AttachPid == 0
		s.stopDebugSession(killProcess)
	} else {
		s.stopNoDebugProcess()
	}
	// The binary is no longer in use by the debugger. It is safe to remove it.
	if s.binaryToRemove != "" {
		gobuild.Remove(s.binaryToRemove)
		s.binaryToRemove = ""
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
// TODO(polina): lock this when we add more goroutines that could call
// this when we support asynchronous request-response communication.
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
		// TODO(polina): Differentiate between errors and handle them
		// gracefully. For example,
		// -- "Request command 'foo' is not supported" means we
		// potentially got some new DAP request that we do not yet have
		// decoding support for, so we can respond with an ErrorResponse.
		// TODO(polina): to support this add Seq to
		// dap.DecodeProtocolMessageFieldError.
		if err != nil {
			select {
			case <-s.stopTriggered:
			default:
				if err != io.EOF {
					s.log.Error("DAP error: ", err)
				}
				s.triggerServerStop()
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
			s.log.Errorf("stacktrace from recovered panic:\n%s\n", debug.Stack())
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
	// TODO(polina): Respond with an error if debug session is in progress?
	response := &dap.InitializeResponse{Response: *newResponse(request.Request)}
	response.Body.SupportsConfigurationDoneRequest = true
	response.Body.SupportsConditionalBreakpoints = true
	response.Body.SupportsDelayedStackTraceLoading = true
	response.Body.SupportTerminateDebuggee = true
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
	if !ok || program == "" {
		s.sendErrorResponse(request.Request,
			FailedToLaunch, "Failed to launch",
			"The program attribute is missing in debug configuration.")
		return
	}

	if mode == "debug" || mode == "test" {
		output, ok := request.Arguments["output"].(string)
		if !ok || output == "" {
			output = cleanExeName(defaultDebugBinary)
		}
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

		s.log.Debugf("building binary at %s", debugbinary)
		var out []byte
		switch mode {
		case "debug":
			out, err = gobuild.GoBuildCombinedOutput(debugbinary, []string{program}, buildFlags)
		case "test":
			out, err = gobuild.GoTestBuildCombinedOutput(debugbinary, []string{program}, buildFlags)
		}
		if err != nil {
			s.sendErrorResponse(request.Request,
				FailedToLaunch, "Failed to launch",
				fmt.Sprintf("Build error: %s (%s)", strings.TrimSpace(string(out)), err.Error()))
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

// TODO(polina): support "remote" mode
func isValidLaunchMode(launchMode interface{}) bool {
	switch launchMode {
	case "exec", "debug", "test":
		return true
	}

	return false
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
	state, err := s.debugger.Command(&api.DebuggerCommand{Name: api.Halt}, nil)
	if err == proc.ErrProcessDetached {
		s.log.Debug(err)
		return nil
	}
	if err != nil {
		switch err.(type) {
		case proc.ErrProcessExited:
			exited = err
		default:
			s.log.Error(err)
		}
	} else if state.Exited {
		exited = proc.ErrProcessExited{Pid: s.debugger.ProcessPid(), Status: state.ExitStatus}
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
			s.log.Error(err)
		}
	}
	return err
}

func (s *Server) onSetBreakpointsRequest(request *dap.SetBreakpointsRequest) {
	if s.noDebugProcess != nil {
		s.sendErrorResponse(request.Request, UnableToSetBreakpoints, "Unable to set or clear breakpoints", "running in noDebug mode")
		return
	}
	// TODO(polina): handle this while running by halting first.

	if request.Arguments.Source.Path == "" {
		s.sendErrorResponse(request.Request, UnableToSetBreakpoints, "Unable to set or clear breakpoints", "empty file path")
		return
	}

	clientPath := request.Arguments.Source.Path
	serverPath := s.toServerPath(clientPath)

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
		if bp.File != serverPath {
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
			&api.Breakpoint{File: serverPath, Line: want.Line, Cond: want.Condition})
		response.Body.Breakpoints[i].Verified = (err == nil)
		if err != nil {
			response.Body.Breakpoints[i].Line = want.Line
			response.Body.Breakpoints[i].Message = err.Error()
		} else {
			response.Body.Breakpoints[i].Id = got.ID
			response.Body.Breakpoints[i].Line = got.Line
			response.Body.Breakpoints[i].Source = dap.Source{Name: request.Arguments.Source.Name, Path: clientPath}
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
	if s.debugger != nil && s.args.stopOnEntry {
		e := &dap.StoppedEvent{
			Event: *newEvent("stopped"),
			Body:  dap.StoppedEventBody{Reason: "entry", ThreadId: 1, AllThreadsStopped: true},
		}
		s.send(e)
	}
	s.send(&dap.ConfigurationDoneResponse{Response: *newResponse(request.Request)})
	if s.debugger != nil && !s.args.stopOnEntry {
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
		case proc.ErrProcessExited:
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

// onAttachRequest handles 'attach' request.
// This is a mandatory request to support.
func (s *Server) onAttachRequest(request *dap.AttachRequest) {
	mode, ok := request.Arguments["mode"]
	if !ok || mode == "" {
		mode = "local"
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
		func() {
			s.mu.Lock()
			defer s.mu.Unlock() // Make sure to unlock in case of panic that will become internal error
			s.debugger, err = debugger.New(&s.config.Debugger, nil)
		}()
		if err != nil {
			s.sendErrorResponse(request.Request, FailedToAttach, "Failed to attach", err.Error())
			return
		}
	} else {
		// TODO(polina): support 'remote' mode with 'host' and 'port'
		s.sendErrorResponse(request.Request,
			FailedToAttach, "Failed to attach",
			fmt.Sprintf("Unsupported 'mode' value %q in debug configuration", mode))
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
func (s *Server) onNextRequest(request *dap.NextRequest) {
	s.send(&dap.NextResponse{Response: *newResponse(request.Request)})
	s.doStepCommand(api.Next, request.Arguments.ThreadId)
}

// onStepInRequest handles 'stepIn' request
// This is a mandatory request to support.
func (s *Server) onStepInRequest(request *dap.StepInRequest) {
	s.send(&dap.StepInResponse{Response: *newResponse(request.Request)})
	s.doStepCommand(api.Step, request.Arguments.ThreadId)
}

// onStepOutRequest handles 'stepOut' request
// This is a mandatory request to support.
func (s *Server) onStepOutRequest(request *dap.StepOutRequest) {
	s.send(&dap.StepOutResponse{Response: *newResponse(request.Request)})
	s.doStepCommand(api.StepOut, request.Arguments.ThreadId)
}

func stoppedGoroutineID(state *api.DebuggerState) (id int) {
	if state.SelectedGoroutine != nil {
		id = state.SelectedGoroutine.ID
	} else if state.CurrentThread != nil {
		id = state.CurrentThread.GoroutineID
	}
	return id
}

func (s *Server) doStepCommand(command string, threadId int) {
	// Use SwitchGoroutine to change the current goroutine.
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
	s.doCommand(command)
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
			clientPath := s.toClientPath(loc.File)
			stackFrames[i].Source = dap.Source{Name: filepath.Base(clientPath), Path: clientPath}
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

	// Retrieve arguments
	args, err := s.debugger.FunctionArguments(goid, frame, 0, DefaultLoadConfig)
	if err != nil {
		s.sendErrorResponse(request.Request, UnableToListArgs, "Unable to list args", err.Error())
		return
	}
	argScope := &fullyQualifiedVariable{&proc.Variable{Name: "Arguments", Children: slicePtrVarToSliceVar(args)}, "", true}

	// Retrieve local variables
	locals, err := s.debugger.LocalVariables(goid, frame, 0, DefaultLoadConfig)
	if err != nil {
		s.sendErrorResponse(request.Request, UnableToListLocals, "Unable to list locals", err.Error())
		return
	}
	locScope := &fullyQualifiedVariable{&proc.Variable{Name: "Locals", Children: slicePtrVarToSliceVar(locals)}, "", true}

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
		}, currPkg, true}
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
			// If key or value or both are scalars, we can use
			// a single variable to represet key:value format.
			// Otherwise, we must return separate variables for both.
			if keyref > 0 && valref > 0 { // Both are not scalars
				keyvar := dap.Variable{
					Name:               fmt.Sprintf("[key %d]", kvIndex),
					EvaluateName:       keyexpr,
					Value:              key,
					VariablesReference: keyref,
				}
				valvar := dap.Variable{
					Name:               fmt.Sprintf("[val %d]", kvIndex),
					EvaluateName:       valexpr,
					Value:              val,
					VariablesReference: valref,
				}
				children = append(children, keyvar, valvar)
			} else { // At least one is a scalar
				kvvar := dap.Variable{
					Name:         key,
					EvaluateName: valexpr,
					Value:        val,
				}
				if keyref != 0 { // key is a type to be expanded
					if len(key) > DefaultLoadConfig.MaxStringLen {
						// Truncate and make unique
						kvvar.Name = fmt.Sprintf("%s... @ %#x", key[0:DefaultLoadConfig.MaxStringLen], keyv.Addr)
					}
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
			cfqname := fmt.Sprintf("%s[%d]", v.fullyQualifiedNameOrExpr, i)
			cvalue, cvarref := s.convertVariable(&v.Children[i], cfqname)
			children[i] = dap.Variable{
				Name:               fmt.Sprintf("[%d]", i),
				EvaluateName:       cfqname,
				Value:              cvalue,
				VariablesReference: cvarref,
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
				Value:              cvalue,
				VariablesReference: cvarref,
			}
		}
	}
	response := &dap.VariablesResponse{
		Response: *newResponse(request.Request),
		Body:     dap.VariablesResponseBody{Variables: children},
	}
	s.send(response)
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
	return s.convertVariableWithOpts(v, qualifiedNameOrExpr, false)
}

func (s *Server) convertVariableToString(v *proc.Variable) string {
	val, _ := s.convertVariableWithOpts(v, "", true)
	return val
}

// convertVariableWithOpts allows to skip reference generation in case all we need is
// a string representation of the variable.
func (s *Server) convertVariableWithOpts(v *proc.Variable, qualifiedNameOrExpr string, skipRef bool) (value string, variablesReference int) {
	maybeCreateVariableHandle := func(v *proc.Variable) int {
		if skipRef {
			return 0
		}
		return s.variableHandles.create(&fullyQualifiedVariable{v, qualifiedNameOrExpr, false /*not a scope*/})
	}
	value = api.ConvertVar(v).SinglelineString()
	if v.Unreadable != nil {
		return
	}
	typeName := api.PrettyTypeName(v.DwarfType)

	// As per https://github.com/go-delve/delve/blob/master/Documentation/api/ClientHowto.md#looking-into-variables,
	// some of the types might be fully or partially not loaded based on LoadConfig. For now, clearly
	// communicate when values are not fully loaded. TODO(polina): look into loading on demand.

	switch v.Kind {
	case reflect.UnsafePointer:
		// Skip child reference
	case reflect.Ptr:
		if v.DwarfType != nil && len(v.Children) > 0 && v.Children[0].Addr != 0 && v.Children[0].Kind != reflect.Invalid {
			if v.Children[0].OnlyAddr {
				value = "(not loaded) " + value
			} else {
				variablesReference = maybeCreateVariableHandle(v)
			}
		}
	case reflect.Slice, reflect.Array:
		if v.Len > int64(len(v.Children)) { // Not fully loaded
			value = fmt.Sprintf("(loaded %d/%d) ", len(v.Children), v.Len) + value
		}
		if v.Base != 0 && len(v.Children) > 0 {
			variablesReference = maybeCreateVariableHandle(v)
		}
	case reflect.Map:
		if v.Len > int64(len(v.Children)/2) { // Not fully loaded
			value = fmt.Sprintf("(loaded %d/%d) ", len(v.Children)/2, v.Len) + value
		}
		if v.Base != 0 && len(v.Children) > 0 {
			variablesReference = maybeCreateVariableHandle(v)
		}
	case reflect.String:
		// TODO(polina): implement auto-loading here
	case reflect.Interface:
		if v.Addr != 0 && len(v.Children) > 0 && v.Children[0].Kind != reflect.Invalid && v.Children[0].Addr != 0 {
			if v.Children[0].OnlyAddr { // Not fully loaded
				// We might be loading variables from the frame that's not topmost, so use
				// frame-independent address-based expression, not fully-qualified name.
				loadExpr := fmt.Sprintf("*(*%q)(%#x)", typeName, v.Addr)
				s.log.Debugf("loading %s (type %s) with %s", qualifiedNameOrExpr, typeName, loadExpr)
				vLoaded, err := s.debugger.EvalVariableInScope(-1, 0, 0, loadExpr, DefaultLoadConfig)
				if err != nil {
					value += fmt.Sprintf(" - FAILED TO LOAD: %s", err)
				} else {
					v.Children = vLoaded.Children
					value = api.ConvertVar(v).SinglelineString()
				}
			}
			// Provide a reference to the child only if it fully loaded
			if !v.Children[0].OnlyAddr {
				variablesReference = maybeCreateVariableHandle(v)
			}
		}
	case reflect.Struct:
		if v.Len > int64(len(v.Children)) { // Not fully loaded
			value = fmt.Sprintf("(loaded %d/%d) ", len(v.Children), v.Len) + value
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
	showErrorToUser := request.Arguments.Context != "watch" && request.Arguments.Context != "repl"
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
			ReturnInfoLoadConfig: api.LoadConfigFromProc(&DefaultLoadConfig),
			Expr:                 strings.Replace(request.Arguments.Expression, "call ", "", 1),
			UnsafeCall:           false,
			GoroutineID:          goid,
		}, nil)
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
				retVars, err = s.debugger.FindThreadReturnValues(t.ID, DefaultLoadConfig)
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
			s.resetHandlesForStoppedEvent()
			stopped := &dap.StoppedEvent{Event: *newEvent("stopped")}
			stopped.Body.AllThreadsStopped = true
			stopped.Body.ThreadId = stoppedGoroutineID(state)
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
				VariablesReference: s.variableHandles.create(&fullyQualifiedVariable{retVarsAsVar, "", false /*not a scope*/}),
			}
		}
	} else { // {expression}
		exprVar, err := s.debugger.EvalVariableInScope(goid, frame, 0, request.Arguments.Expression, DefaultLoadConfig)
		if err != nil {
			s.sendErrorResponseWithOpts(request.Request, UnableToEvaluateExpression, "Unable to evaluate expression", err.Error(), showErrorToUser)
			return
		}
		// TODO(polina): as far as I can tell, evaluateName is ignored by vscode for expression variables.
		// Should it be skipped alltogether for all levels?
		exprVal, exprRef := s.convertVariable(exprVar, fmt.Sprintf("(%s)", request.Arguments.Expression))
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

func (s *Server) resetHandlesForStoppedEvent() {
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

	state, err := s.debugger.Command(&api.DebuggerCommand{Name: command}, nil)
	if _, isexited := err.(proc.ErrProcessExited); isexited || err == nil && state.Exited {
		e := &dap.TerminatedEvent{Event: *newEvent("terminated")}
		s.send(e)
		return
	}

	s.resetHandlesForStoppedEvent()
	stopped := &dap.StoppedEvent{Event: *newEvent("stopped")}
	stopped.Body.AllThreadsStopped = true

	if err == nil {
		stopped.Body.ThreadId = stoppedGoroutineID(state)

		switch s.debugger.StopReason() {
		case proc.StopNextFinished:
			stopped.Body.Reason = "step"
		default:
			stopped.Body.Reason = "breakpoint"
		}
		if state.CurrentThread != nil && state.CurrentThread.Breakpoint != nil {
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
			stopped.Body.ThreadId = stoppedGoroutineID(state)
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
