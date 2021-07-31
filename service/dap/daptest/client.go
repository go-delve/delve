// Package daptest provides a sample client with utilities
// for DAP mode testing.
package daptest

//go:generate go run ./gen/main.go -o ./resp.go

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"

	"github.com/google/go-dap"
)

// Client is a debugger service client that uses Debug Adaptor Protocol.
// It does not (yet?) implement service.Client interface.
// All client methods are synchronous.
type Client struct {
	conn   net.Conn
	reader *bufio.Reader
	// seq is used to track the sequence number of each
	// requests that the client sends to the server
	seq int
}

// NewClient creates a new Client over a TCP connection.
// Call Close() to close the connection.
func NewClient(addr string) *Client {
	fmt.Println("Connecting to server at:", addr)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		log.Fatal("dialing:", err)
	}
	c := &Client{conn: conn, reader: bufio.NewReader(conn)}
	c.seq = 1 // match VS Code numbering
	return c
}

// Close closes the client connection.
func (c *Client) Close() {
	c.conn.Close()
}

func (c *Client) send(request dap.Message) {
	dap.WriteProtocolMessage(c.conn, request)
}

func (c *Client) ReadMessage() (dap.Message, error) {
	return dap.ReadProtocolMessage(c.reader)
}

func (c *Client) ExpectMessage(t *testing.T) dap.Message {
	t.Helper()
	m, err := dap.ReadProtocolMessage(c.reader)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func (c *Client) ExpectInvisibleErrorResponse(t *testing.T) *dap.ErrorResponse {
	t.Helper()
	er := c.ExpectErrorResponse(t)
	if er.Body.Error.ShowUser {
		t.Errorf("\ngot %#v\nwant ShowUser=false", er)
	}
	return er
}

func (c *Client) ExpectVisibleErrorResponse(t *testing.T) *dap.ErrorResponse {
	t.Helper()
	er := c.ExpectErrorResponse(t)
	if !er.Body.Error.ShowUser {
		t.Errorf("\ngot %#v\nwant ShowUser=true", er)
	}
	return er
}

func (c *Client) expectErrorResponse(t *testing.T, id int, message string) *dap.ErrorResponse {
	t.Helper()
	er := c.ExpectErrorResponse(t)
	if er.Body.Error.Id != id || er.Message != message {
		t.Errorf("\ngot %#v\nwant Id=%d Message=%q", er, id, message)
	}
	return er
}

func (c *Client) ExpectInitializeResponseAndCapabilities(t *testing.T) *dap.InitializeResponse {
	t.Helper()
	initResp := c.ExpectInitializeResponse(t)
	wantCapabilities := dap.Capabilities{
		// the values set by dap.(*Server).onInitializeRequest.
		SupportsConfigurationDoneRequest: true,
		SupportsConditionalBreakpoints:   true,
		SupportsDelayedStackTraceLoading: true,
		SupportTerminateDebuggee:         true,
		SupportsExceptionInfoRequest:     true,
		SupportsSetVariable:              true,
		SupportsFunctionBreakpoints:      true,
		SupportsEvaluateForHovers:        true,
		SupportsClipboardContext:         true,
		SupportsLogPoints:                true,
	}
	if !reflect.DeepEqual(initResp.Body, wantCapabilities) {
		t.Errorf("capabilities in initializeResponse: got %+v, want %v", pretty(initResp.Body), pretty(wantCapabilities))
	}
	return initResp
}

func pretty(v interface{}) string {
	s, _ := json.MarshalIndent(v, "", "\t")
	return string(s)
}

func (c *Client) ExpectNotYetImplementedErrorResponse(t *testing.T) *dap.ErrorResponse {
	t.Helper()
	return c.expectErrorResponse(t, 7777, "Not yet implemented")
}

func (c *Client) ExpectUnsupportedCommandErrorResponse(t *testing.T) *dap.ErrorResponse {
	t.Helper()
	return c.expectErrorResponse(t, 9999, "Unsupported command")
}

func (c *Client) ExpectOutputEventRegex(t *testing.T, want string) *dap.OutputEvent {
	t.Helper()
	e := c.ExpectOutputEvent(t)
	if matched, _ := regexp.MatchString(want, e.Body.Output); !matched {
		t.Errorf("\ngot %#v\nwant Output=%q", e, want)
	}
	return e
}

func (c *Client) ExpectOutputEventProcessExited(t *testing.T, status int) *dap.OutputEvent {
	t.Helper()
	return c.ExpectOutputEventRegex(t, fmt.Sprintf(`Process [0-9]+ has exited with status %d\n`, status))
}

func (c *Client) ExpectOutputEventDetaching(t *testing.T) *dap.OutputEvent {
	t.Helper()
	return c.ExpectOutputEventRegex(t, `Detaching\n`)
}

func (c *Client) ExpectOutputEventDetachingKill(t *testing.T) *dap.OutputEvent {
	t.Helper()
	return c.ExpectOutputEventRegex(t, `Detaching and terminating target process\n`)
}

func (c *Client) ExpectOutputEventDetachingNoKill(t *testing.T) *dap.OutputEvent {
	t.Helper()
	return c.ExpectOutputEventRegex(t, `Detaching without terminating target process\n`)
}

// InitializeRequest sends an 'initialize' request.
func (c *Client) InitializeRequest() {
	request := &dap.InitializeRequest{Request: *c.newRequest("initialize")}
	request.Arguments = dap.InitializeRequestArguments{
		AdapterID:                    "go",
		PathFormat:                   "path",
		LinesStartAt1:                true,
		ColumnsStartAt1:              true,
		SupportsVariableType:         true,
		SupportsVariablePaging:       true,
		SupportsRunInTerminalRequest: true,
		Locale:                       "en-us",
	}
	c.send(request)
}

// InitializeRequestWithArgs sends an 'initialize' request with specified arguments.
func (c *Client) InitializeRequestWithArgs(args dap.InitializeRequestArguments) {
	request := &dap.InitializeRequest{Request: *c.newRequest("initialize")}
	request.Arguments = args
	c.send(request)
}

// LaunchRequest sends a 'launch' request with the specified args.
func (c *Client) LaunchRequest(mode, program string, stopOnEntry bool) {
	request := &dap.LaunchRequest{Request: *c.newRequest("launch")}
	request.Arguments = map[string]interface{}{
		"request":     "launch",
		"mode":        mode,
		"program":     program,
		"stopOnEntry": stopOnEntry,
	}
	c.send(request)
}

// LaunchRequestWithArgs takes a map of untyped implementation-specific
// arguments to send a 'launch' request. This version can be used to
// test for values of unexpected types or unspecified values.
func (c *Client) LaunchRequestWithArgs(arguments map[string]interface{}) {
	request := &dap.LaunchRequest{Request: *c.newRequest("launch")}
	request.Arguments = arguments
	c.send(request)
}

// AttachRequest sends an 'attach' request with the specified
// arguments.
func (c *Client) AttachRequest(arguments map[string]interface{}) {
	request := &dap.AttachRequest{Request: *c.newRequest("attach")}
	request.Arguments = arguments
	c.send(request)
}

// DisconnectRequest sends a 'disconnect' request.
func (c *Client) DisconnectRequest() {
	request := &dap.DisconnectRequest{Request: *c.newRequest("disconnect")}
	c.send(request)
}

// DisconnectRequest sends a 'disconnect' request with an option to specify
// `terminateDebuggee`.
func (c *Client) DisconnectRequestWithKillOption(kill bool) {
	request := &dap.DisconnectRequest{Request: *c.newRequest("disconnect")}
	request.Arguments.TerminateDebuggee = kill
	c.send(request)
}

// SetBreakpointsRequest sends a 'setBreakpoints' request.
func (c *Client) SetBreakpointsRequest(file string, lines []int) {
	c.SetConditionalBreakpointsRequest(file, lines, nil)
}

// SetBreakpointsRequest sends a 'setBreakpoints' request with conditions.
func (c *Client) SetConditionalBreakpointsRequest(file string, lines []int, conditions map[int]string) {
	request := &dap.SetBreakpointsRequest{Request: *c.newRequest("setBreakpoints")}
	request.Arguments = dap.SetBreakpointsArguments{
		Source: dap.Source{
			Name: filepath.Base(file),
			Path: file,
		},
		Breakpoints: make([]dap.SourceBreakpoint, len(lines)),
	}
	for i, l := range lines {
		request.Arguments.Breakpoints[i].Line = l
		cond, ok := conditions[l]
		if ok {
			request.Arguments.Breakpoints[i].Condition = cond
		}
	}
	c.send(request)
}

// SetBreakpointsRequest sends a 'setBreakpoints' request with conditions.
func (c *Client) SetHitConditionalBreakpointsRequest(file string, lines []int, conditions map[int]string) {
	request := &dap.SetBreakpointsRequest{Request: *c.newRequest("setBreakpoints")}
	request.Arguments = dap.SetBreakpointsArguments{
		Source: dap.Source{
			Name: filepath.Base(file),
			Path: file,
		},
		Breakpoints: make([]dap.SourceBreakpoint, len(lines)),
	}
	for i, l := range lines {
		request.Arguments.Breakpoints[i].Line = l
		cond, ok := conditions[l]
		if ok {
			request.Arguments.Breakpoints[i].HitCondition = cond
		}
	}
	c.send(request)
}

// SetLogpointsRequest sends a 'setBreakpoints' request with logMessages.
func (c *Client) SetLogpointsRequest(file string, lines []int, logMessages map[int]string) {
	request := &dap.SetBreakpointsRequest{Request: *c.newRequest("setBreakpoints")}
	request.Arguments = dap.SetBreakpointsArguments{
		Source: dap.Source{
			Name: filepath.Base(file),
			Path: file,
		},
		Breakpoints: make([]dap.SourceBreakpoint, len(lines)),
	}
	for i, l := range lines {
		request.Arguments.Breakpoints[i].Line = l
		msg, ok := logMessages[l]
		if ok {
			request.Arguments.Breakpoints[i].LogMessage = msg
		}
	}
	c.send(request)
}

// SetExceptionBreakpointsRequest sends a 'setExceptionBreakpoints' request.
func (c *Client) SetExceptionBreakpointsRequest() {
	request := &dap.SetBreakpointsRequest{Request: *c.newRequest("setExceptionBreakpoints")}
	c.send(request)
}

// ConfigurationDoneRequest sends a 'configurationDone' request.
func (c *Client) ConfigurationDoneRequest() {
	request := &dap.ConfigurationDoneRequest{Request: *c.newRequest("configurationDone")}
	c.send(request)
}

// ContinueRequest sends a 'continue' request.
func (c *Client) ContinueRequest(thread int) {
	request := &dap.ContinueRequest{Request: *c.newRequest("continue")}
	request.Arguments.ThreadId = thread
	c.send(request)
}

// NextRequest sends a 'next' request.
func (c *Client) NextRequest(thread int) {
	request := &dap.NextRequest{Request: *c.newRequest("next")}
	request.Arguments.ThreadId = thread
	c.send(request)
}

// StepInRequest sends a 'stepIn' request.
func (c *Client) StepInRequest(thread int) {
	request := &dap.NextRequest{Request: *c.newRequest("stepIn")}
	request.Arguments.ThreadId = thread
	c.send(request)
}

// StepOutRequest sends a 'stepOut' request.
func (c *Client) StepOutRequest(thread int) {
	request := &dap.NextRequest{Request: *c.newRequest("stepOut")}
	request.Arguments.ThreadId = thread
	c.send(request)
}

// PauseRequest sends a 'pause' request.
func (c *Client) PauseRequest(threadId int) {
	request := &dap.PauseRequest{Request: *c.newRequest("pause")}
	request.Arguments.ThreadId = threadId
	c.send(request)
}

// ThreadsRequest sends a 'threads' request.
func (c *Client) ThreadsRequest() {
	request := &dap.ThreadsRequest{Request: *c.newRequest("threads")}
	c.send(request)
}

// StackTraceRequest sends a 'stackTrace' request.
func (c *Client) StackTraceRequest(threadID, startFrame, levels int) {
	request := &dap.StackTraceRequest{Request: *c.newRequest("stackTrace")}
	request.Arguments.ThreadId = threadID
	request.Arguments.StartFrame = startFrame
	request.Arguments.Levels = levels
	c.send(request)
}

// ScopesRequest sends a 'scopes' request.
func (c *Client) ScopesRequest(frameID int) {
	request := &dap.ScopesRequest{Request: *c.newRequest("scopes")}
	request.Arguments.FrameId = frameID
	c.send(request)
}

// VariablesRequest sends a 'variables' request.
func (c *Client) VariablesRequest(variablesReference int) {
	request := &dap.VariablesRequest{Request: *c.newRequest("variables")}
	request.Arguments.VariablesReference = variablesReference
	c.send(request)
}

// IndexedVariablesRequest sends a 'variables' request.
func (c *Client) IndexedVariablesRequest(variablesReference, start, count int) {
	request := &dap.VariablesRequest{Request: *c.newRequest("variables")}
	request.Arguments.VariablesReference = variablesReference
	request.Arguments.Filter = "indexed"
	request.Arguments.Start = start
	request.Arguments.Count = count
	c.send(request)
}

// NamedVariablesRequest sends a 'variables' request.
func (c *Client) NamedVariablesRequest(variablesReference int) {
	request := &dap.VariablesRequest{Request: *c.newRequest("variables")}
	request.Arguments.VariablesReference = variablesReference
	request.Arguments.Filter = "named"
	c.send(request)
}

// TeriminateRequest sends a 'terminate' request.
func (c *Client) TerminateRequest() {
	c.send(&dap.TerminateRequest{Request: *c.newRequest("terminate")})
}

// RestartRequest sends a 'restart' request.
func (c *Client) RestartRequest() {
	c.send(&dap.RestartRequest{Request: *c.newRequest("restart")})
}

// SetFunctionBreakpointsRequest sends a 'setFunctionBreakpoints' request.
func (c *Client) SetFunctionBreakpointsRequest(breakpoints []dap.FunctionBreakpoint) {
	c.send(&dap.SetFunctionBreakpointsRequest{
		Request: *c.newRequest("setFunctionBreakpoints"),
		Arguments: dap.SetFunctionBreakpointsArguments{
			Breakpoints: breakpoints,
		},
	})
}

// StepBackRequest sends a 'stepBack' request.
func (c *Client) StepBackRequest() {
	c.send(&dap.StepBackRequest{Request: *c.newRequest("stepBack")})
}

// ReverseContinueRequest sends a 'reverseContinue' request.
func (c *Client) ReverseContinueRequest() {
	c.send(&dap.ReverseContinueRequest{Request: *c.newRequest("reverseContinue")})
}

// SetVariableRequest sends a 'setVariable' request.
func (c *Client) SetVariableRequest(variablesRef int, name, value string) {
	request := &dap.SetVariableRequest{Request: *c.newRequest("setVariable")}
	request.Arguments.VariablesReference = variablesRef
	request.Arguments.Name = name
	request.Arguments.Value = value
	c.send(request)
}

// RestartFrameRequest sends a 'restartFrame' request.
func (c *Client) RestartFrameRequest() {
	c.send(&dap.RestartFrameRequest{Request: *c.newRequest("restartFrame")})
}

// GotoRequest sends a 'goto' request.
func (c *Client) GotoRequest() {
	c.send(&dap.GotoRequest{Request: *c.newRequest("goto")})
}

// SetExpressionRequest sends a 'setExpression' request.
func (c *Client) SetExpressionRequest() {
	c.send(&dap.SetExpressionRequest{Request: *c.newRequest("setExpression")})
}

// SourceRequest sends a 'source' request.
func (c *Client) SourceRequest() {
	c.send(&dap.SourceRequest{Request: *c.newRequest("source")})
}

// TerminateThreadsRequest sends a 'terminateThreads' request.
func (c *Client) TerminateThreadsRequest() {
	c.send(&dap.TerminateThreadsRequest{Request: *c.newRequest("terminateThreads")})
}

// EvaluateRequest sends a 'evaluate' request.
func (c *Client) EvaluateRequest(expr string, fid int, context string) {
	request := &dap.EvaluateRequest{Request: *c.newRequest("evaluate")}
	request.Arguments.Expression = expr
	request.Arguments.FrameId = fid
	request.Arguments.Context = context
	c.send(request)
}

// StepInTargetsRequest sends a 'stepInTargets' request.
func (c *Client) StepInTargetsRequest() {
	c.send(&dap.StepInTargetsRequest{Request: *c.newRequest("stepInTargets")})
}

// GotoTargetsRequest sends a 'gotoTargets' request.
func (c *Client) GotoTargetsRequest() {
	c.send(&dap.GotoTargetsRequest{Request: *c.newRequest("gotoTargets")})
}

// CompletionsRequest sends a 'completions' request.
func (c *Client) CompletionsRequest() {
	c.send(&dap.CompletionsRequest{Request: *c.newRequest("completions")})
}

// ExceptionInfoRequest sends a 'exceptionInfo' request.
func (c *Client) ExceptionInfoRequest(threadID int) {
	request := &dap.ExceptionInfoRequest{Request: *c.newRequest("exceptionInfo")}
	request.Arguments.ThreadId = threadID
	c.send(request)
}

// LoadedSourcesRequest sends a 'loadedSources' request.
func (c *Client) LoadedSourcesRequest() {
	c.send(&dap.LoadedSourcesRequest{Request: *c.newRequest("loadedSources")})
}

// DataBreakpointInfoRequest sends a 'dataBreakpointInfo' request.
func (c *Client) DataBreakpointInfoRequest() {
	c.send(&dap.DataBreakpointInfoRequest{Request: *c.newRequest("dataBreakpointInfo")})
}

// SetDataBreakpointsRequest sends a 'setDataBreakpoints' request.
func (c *Client) SetDataBreakpointsRequest() {
	c.send(&dap.SetDataBreakpointsRequest{Request: *c.newRequest("setDataBreakpoints")})
}

// ReadMemoryRequest sends a 'readMemory' request.
func (c *Client) ReadMemoryRequest() {
	c.send(&dap.ReadMemoryRequest{Request: *c.newRequest("readMemory")})
}

// DisassembleRequest sends a 'disassemble' request.
func (c *Client) DisassembleRequest() {
	c.send(&dap.DisassembleRequest{Request: *c.newRequest("disassemble")})
}

// CancelRequest sends a 'cancel' request.
func (c *Client) CancelRequest() {
	c.send(&dap.CancelRequest{Request: *c.newRequest("cancel")})
}

// BreakpointLocationsRequest sends a 'breakpointLocations' request.
func (c *Client) BreakpointLocationsRequest() {
	c.send(&dap.BreakpointLocationsRequest{Request: *c.newRequest("breakpointLocations")})
}

// ModulesRequest sends a 'modules' request.
func (c *Client) ModulesRequest() {
	c.send(&dap.ModulesRequest{Request: *c.newRequest("modules")})
}

// UnknownRequest triggers dap.DecodeProtocolMessageFieldError.
func (c *Client) UnknownRequest() {
	request := c.newRequest("unknown")
	c.send(request)
}

// UnknownEvent triggers dap.DecodeProtocolMessageFieldError.
func (c *Client) UnknownEvent() {
	event := &dap.Event{}
	event.Type = "event"
	event.Seq = -1
	event.Event = "unknown"
	c.send(event)
}

// BadRequest triggers an unmarshal error.
func (c *Client) BadRequest() {
	content := []byte("{malformedString}")
	contentLengthHeaderFmt := "Content-Length: %d\r\n\r\n"
	header := fmt.Sprintf(contentLengthHeaderFmt, len(content))
	c.conn.Write([]byte(header))
	c.conn.Write(content)
}

// KnownEvent passes decode checks, but delve has no 'case' to
// handle it. This behaves the same way a new request type
// added to go-dap, but not to delve.
func (c *Client) KnownEvent() {
	event := &dap.Event{}
	event.Type = "event"
	event.Seq = -1
	event.Event = "terminated"
	c.send(event)
}

func (c *Client) newRequest(command string) *dap.Request {
	request := &dap.Request{}
	request.Type = "request"
	request.Command = command
	request.Seq = c.seq
	c.seq++
	return request
}
