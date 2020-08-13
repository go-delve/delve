// Package daptest provides a sample client with utilities
// for DAP mode testing.
package daptest

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"path/filepath"
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

func (c *Client) expectReadProtocolMessage(t *testing.T) dap.Message {
	t.Helper()
	m, err := dap.ReadProtocolMessage(c.reader)
	if err != nil {
		t.Error(err)
	}
	return m
}

func (c *Client) ExpectErrorResponse(t *testing.T) *dap.ErrorResponse {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.ErrorResponse)
}

func (c *Client) expectErrorResponse(t *testing.T, id int, message string) *dap.ErrorResponse {
	t.Helper()
	er := c.expectReadProtocolMessage(t).(*dap.ErrorResponse)
	if er.Body.Error.Id != id || er.Message != message {
		t.Errorf("\ngot %#v\nwant Id=%d Message=%q", er, id, message)
	}
	return er
}

func (c *Client) ExpectNotYetImplementedErrorResponse(t *testing.T) *dap.ErrorResponse {
	t.Helper()
	return c.expectErrorResponse(t, 7777, "Not yet implemented")
}

func (c *Client) ExpectUnsupportedCommandErrorResponse(t *testing.T) *dap.ErrorResponse {
	t.Helper()
	return c.expectErrorResponse(t, 9999, "Unsupported command")
}

func (c *Client) ExpectDisconnectResponse(t *testing.T) *dap.DisconnectResponse {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.DisconnectResponse)
}

func (c *Client) ExpectContinueResponse(t *testing.T) *dap.ContinueResponse {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.ContinueResponse)
}

func (c *Client) ExpectTerminatedEvent(t *testing.T) *dap.TerminatedEvent {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.TerminatedEvent)
}

func (c *Client) ExpectInitializeResponse(t *testing.T) *dap.InitializeResponse {
	t.Helper()
	initResp := c.expectReadProtocolMessage(t).(*dap.InitializeResponse)
	if !initResp.Body.SupportsConfigurationDoneRequest {
		t.Errorf("got %#v, want SupportsConfigurationDoneRequest=true", initResp)
	}
	return initResp
}

func (c *Client) ExpectInitializedEvent(t *testing.T) *dap.InitializedEvent {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.InitializedEvent)
}

func (c *Client) ExpectLaunchResponse(t *testing.T) *dap.LaunchResponse {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.LaunchResponse)
}

func (c *Client) ExpectSetExceptionBreakpointsResponse(t *testing.T) *dap.SetExceptionBreakpointsResponse {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.SetExceptionBreakpointsResponse)
}

func (c *Client) ExpectSetBreakpointsResponse(t *testing.T) *dap.SetBreakpointsResponse {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.SetBreakpointsResponse)
}

func (c *Client) ExpectStoppedEvent(t *testing.T) *dap.StoppedEvent {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.StoppedEvent)
}

func (c *Client) ExpectOutputEvent(t *testing.T) *dap.OutputEvent {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.OutputEvent)
}

func (c *Client) ExpectConfigurationDoneResponse(t *testing.T) *dap.ConfigurationDoneResponse {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.ConfigurationDoneResponse)
}

func (c *Client) ExpectThreadsResponse(t *testing.T) *dap.ThreadsResponse {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.ThreadsResponse)
}

func (c *Client) ExpectStackTraceResponse(t *testing.T) *dap.StackTraceResponse {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.StackTraceResponse)
}

func (c *Client) ExpectScopesResponse(t *testing.T) *dap.ScopesResponse {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.ScopesResponse)
}

func (c *Client) ExpectVariablesResponse(t *testing.T) *dap.VariablesResponse {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.VariablesResponse)
}

func (c *Client) ExpectTerminateResponse(t *testing.T) *dap.TerminateResponse {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.TerminateResponse)
}

func (c *Client) ExpectRestartResponse(t *testing.T) *dap.RestartResponse {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.RestartResponse)
}

func (c *Client) ExpectSetFunctionBreakpointsResponse(t *testing.T) *dap.SetFunctionBreakpointsResponse {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.SetFunctionBreakpointsResponse)
}

func (c *Client) ExpectStepBackResponse(t *testing.T) *dap.StepBackResponse {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.StepBackResponse)
}

func (c *Client) ExpectReverseContinueResponse(t *testing.T) *dap.ReverseContinueResponse {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.ReverseContinueResponse)
}

func (c *Client) ExpectRestartFrameResponse(t *testing.T) *dap.RestartFrameResponse {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.RestartFrameResponse)
}

func (c *Client) ExpectSetExpressionResponse(t *testing.T) *dap.SetExpressionResponse {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.SetExpressionResponse)
}

func (c *Client) ExpectTerminateThreadsResponse(t *testing.T) *dap.TerminateThreadsResponse {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.TerminateThreadsResponse)
}

func (c *Client) ExpectStepInTargetsResponse(t *testing.T) *dap.StepInTargetsResponse {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.StepInTargetsResponse)
}

func (c *Client) ExpectGotoTargetsResponse(t *testing.T) *dap.GotoTargetsResponse {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.GotoTargetsResponse)
}

func (c *Client) ExpectCompletionsResponse(t *testing.T) *dap.CompletionsResponse {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.CompletionsResponse)
}

func (c *Client) ExpectExceptionInfoResponse(t *testing.T) *dap.ExceptionInfoResponse {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.ExceptionInfoResponse)
}

func (c *Client) ExpectLoadedSourcesResponse(t *testing.T) *dap.LoadedSourcesResponse {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.LoadedSourcesResponse)
}

func (c *Client) ExpectDataBreakpointInfoResponse(t *testing.T) *dap.DataBreakpointInfoResponse {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.DataBreakpointInfoResponse)
}

func (c *Client) ExpectSetDataBreakpointsResponse(t *testing.T) *dap.SetDataBreakpointsResponse {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.SetDataBreakpointsResponse)
}

func (c *Client) ExpectReadMemoryResponse(t *testing.T) *dap.ReadMemoryResponse {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.ReadMemoryResponse)
}

func (c *Client) ExpectDisassembleResponse(t *testing.T) *dap.DisassembleResponse {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.DisassembleResponse)
}

func (c *Client) ExpectCancelResponse(t *testing.T) *dap.CancelResponse {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.CancelResponse)
}

func (c *Client) ExpectBreakpointLocationsResponse(t *testing.T) *dap.BreakpointLocationsResponse {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.BreakpointLocationsResponse)
}

func (c *Client) ExpectModulesResponse(t *testing.T) *dap.ModulesResponse {
	t.Helper()
	return c.expectReadProtocolMessage(t).(*dap.ModulesResponse)
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

// LaunchRequest sends a 'launch' request with the specified args.
func (c *Client) LaunchRequest(mode string, program string, stopOnEntry bool) {
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

// AttachRequest sends an 'attach' request.
func (c *Client) AttachRequest() {
	request := &dap.AttachRequest{Request: *c.newRequest("attach")}
	// TODO(polina): populate meaningful arguments
	c.send(request)
}

// DisconnectRequest sends a 'disconnect' request.
func (c *Client) DisconnectRequest() {
	request := &dap.DisconnectRequest{Request: *c.newRequest("disconnect")}
	c.send(request)
}

// SetBreakpointsRequest sends a 'setBreakpoints' request.
func (c *Client) SetBreakpointsRequest(file string, lines []int) {
	request := &dap.SetBreakpointsRequest{Request: *c.newRequest("setBreakpoints")}
	request.Arguments = dap.SetBreakpointsArguments{
		Source: dap.Source{
			Name: filepath.Base(file),
			Path: file,
		},
		Breakpoints: make([]dap.SourceBreakpoint, len(lines)),
		//sourceModified: false,
	}
	for i, l := range lines {
		request.Arguments.Breakpoints[i].Line = l
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
func (c *Client) NextRequest() {
	request := &dap.NextRequest{Request: *c.newRequest("next")}
	// TODO(polina): arguments
	c.send(request)
}

// StepInRequest sends a 'stepIn' request.
func (c *Client) StepInRequest() {
	request := &dap.NextRequest{Request: *c.newRequest("stepIn")}
	// TODO(polina): arguments
	c.send(request)
}

// StepOutRequest sends a 'stepOut' request.
func (c *Client) StepOutRequest() {
	request := &dap.NextRequest{Request: *c.newRequest("stepOut")}
	// TODO(polina): arguments
	c.send(request)
}

// PauseRequest sends a 'pause' request.
func (c *Client) PauseRequest() {
	request := &dap.NextRequest{Request: *c.newRequest("pause")}
	// TODO(polina): arguments
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

// TeriminateRequest sends a 'terminate' request.
func (c *Client) TerminateRequest() {
	c.send(&dap.TerminateRequest{Request: *c.newRequest("terminate")})
}

// RestartRequest sends a 'restart' request.
func (c *Client) RestartRequest() {
	c.send(&dap.RestartRequest{Request: *c.newRequest("restart")})
}

// SetFunctionBreakpointsRequest sends a 'setFunctionBreakpoints' request.
func (c *Client) SetFunctionBreakpointsRequest() {
	c.send(&dap.SetFunctionBreakpointsRequest{Request: *c.newRequest("setFunctionBreakpoints")})
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
func (c *Client) SetVariableRequest() {
	c.send(&dap.ReverseContinueRequest{Request: *c.newRequest("setVariable")})
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
func (c *Client) EvaluateRequest() {
	c.send(&dap.EvaluateRequest{Request: *c.newRequest("evaluate")})
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
func (c *Client) ExceptionInfoRequest() {
	c.send(&dap.ExceptionInfoRequest{Request: *c.newRequest("exceptionInfo")})
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
