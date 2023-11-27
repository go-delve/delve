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
	return NewClientFromConn(conn)
}

// NewClientFromConn creates a new Client with the given TCP connection.
// Call Close to close the connection.
func NewClientFromConn(conn net.Conn) *Client {
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
	if er.Body.Error != nil && er.Body.Error.ShowUser {
		t.Errorf("\ngot %#v\nwant ShowUser=false", er)
	}
	return er
}

func (c *Client) ExpectVisibleErrorResponse(t *testing.T) *dap.ErrorResponse {
	t.Helper()
	er := c.ExpectErrorResponse(t)
	if er.Body.Error == nil || !er.Body.Error.ShowUser {
		t.Errorf("\ngot %#v\nwant ShowUser=true", er)
	}
	return er
}

func (c *Client) ExpectErrorResponseWith(t *testing.T, id int, message string, showUser bool) *dap.ErrorResponse {
	t.Helper()
	er := c.ExpectErrorResponse(t)
	if er.Body.Error == nil {
		t.Errorf("got nil, want Id=%d Format=%q ShowUser=%v", id, message, showUser)
		return er
	}
	if matched, _ := regexp.MatchString(message, er.Body.Error.Format); !matched || er.Body.Error.Id != id || er.Body.Error.ShowUser != showUser {
		t.Errorf("got %#v, want Id=%d Format=%q ShowUser=%v", er, id, message, showUser)
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
		SupportsExceptionInfoRequest:     true,
		SupportsSetVariable:              true,
		SupportsFunctionBreakpoints:      true,
		SupportsInstructionBreakpoints:   true,
		SupportsEvaluateForHovers:        true,
		SupportsClipboardContext:         true,
		SupportsSteppingGranularity:      true,
		SupportsLogPoints:                true,
		SupportsDisassembleRequest:       true,
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
	return c.ExpectErrorResponseWith(t, 7777, "Not yet implemented", false)
}

func (c *Client) ExpectUnsupportedCommandErrorResponse(t *testing.T) *dap.ErrorResponse {
	t.Helper()
	return c.ExpectErrorResponseWith(t, 9999, "Unsupported command", false)
}

func (c *Client) ExpectCapabilitiesEventSupportTerminateDebuggee(t *testing.T) *dap.CapabilitiesEvent {
	t.Helper()
	e := c.ExpectCapabilitiesEvent(t)
	if !e.Body.Capabilities.SupportTerminateDebuggee {
		t.Errorf("\ngot %#v\nwant SupportTerminateDebuggee=true", e.Body.Capabilities.SupportTerminateDebuggee)
	}
	return e
}

func (c *Client) ExpectOutputEventRegex(t *testing.T, want string) *dap.OutputEvent {
	t.Helper()
	e := c.ExpectOutputEvent(t)
	if matched, _ := regexp.MatchString(want, e.Body.Output); !matched {
		t.Errorf("\ngot %#v\nwant Output=%q", e, want)
	}
	return e
}

const ProcessExited = `Process [0-9]+ has exited with status %s\n`

func (c *Client) ExpectOutputEventProcessExited(t *testing.T, status int) *dap.OutputEvent {
	t.Helper()
	// We sometimes fail to return the correct exit status on Linux, so allow -1 here as well.
	return c.ExpectOutputEventRegex(t, fmt.Sprintf(ProcessExited, fmt.Sprintf("(%d|-1)", status)))
}

func (c *Client) ExpectOutputEventProcessExitedAnyStatus(t *testing.T) *dap.OutputEvent {
	t.Helper()
	return c.ExpectOutputEventRegex(t, fmt.Sprintf(ProcessExited, `-?\d+`))
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

func (c *Client) ExpectOutputEventTerminating(t *testing.T) *dap.OutputEvent {
	t.Helper()
	return c.ExpectOutputEventRegex(t, `Terminating process [0-9]+\n`)
}

const ClosingClient = "Closing client session, but leaving multi-client DAP server at .+:[0-9]+ with debuggee %s\n"

func (c *Client) ExpectOutputEventClosingClient(t *testing.T, status string) *dap.OutputEvent {
	t.Helper()
	return c.ExpectOutputEventRegex(t, fmt.Sprintf(ClosingClient, status))
}

func (c *Client) CheckStopLocation(t *testing.T, thread int, name string, line interface{}) {
	t.Helper()
	c.StackTraceRequest(thread, 0, 20)
	st := c.ExpectStackTraceResponse(t)
	if len(st.Body.StackFrames) < 1 {
		t.Errorf("\ngot  %#v\nwant len(stackframes) => 1", st)
	} else {
		switch line := line.(type) {
		case int:
			if line != -1 && st.Body.StackFrames[0].Line != line {
				t.Errorf("\ngot  %#v\nwant Line=%d", st, line)
			}
		case []int:
			found := false
			for _, line := range line {
				if st.Body.StackFrames[0].Line == line {
					found = true
				}
			}
			if !found {
				t.Errorf("\ngot  %#v\nwant Line=%v", st, line)
			}
		}
		if st.Body.StackFrames[0].Name != name {
			t.Errorf("\ngot  %#v\nwant Name=%q", st, name)
		}
	}
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

func toRawMessage(in interface{}) json.RawMessage {
	out, _ := json.Marshal(in)
	return out
}

// LaunchRequest sends a 'launch' request with the specified args.
func (c *Client) LaunchRequest(mode, program string, stopOnEntry bool) {
	request := &dap.LaunchRequest{Request: *c.newRequest("launch")}
	request.Arguments = toRawMessage(map[string]interface{}{
		"request":     "launch",
		"mode":        mode,
		"program":     program,
		"stopOnEntry": stopOnEntry,
	})
	c.send(request)
}

// LaunchRequestWithArgs takes a map of untyped implementation-specific
// arguments to send a 'launch' request. This version can be used to
// test for values of unexpected types or unspecified values.
func (c *Client) LaunchRequestWithArgs(arguments map[string]interface{}) {
	request := &dap.LaunchRequest{Request: *c.newRequest("launch")}
	request.Arguments = toRawMessage(arguments)
	c.send(request)
}

// AttachRequest sends an 'attach' request with the specified
// arguments.
func (c *Client) AttachRequest(arguments map[string]interface{}) {
	request := &dap.AttachRequest{Request: *c.newRequest("attach")}
	request.Arguments = toRawMessage(arguments)
	c.send(request)
}

// DisconnectRequest sends a 'disconnect' request.
func (c *Client) DisconnectRequest() {
	request := &dap.DisconnectRequest{Request: *c.newRequest("disconnect")}
	c.send(request)
}

// DisconnectRequestWithKillOption sends a 'disconnect' request with an option to specify
// `terminateDebuggee`.
func (c *Client) DisconnectRequestWithKillOption(kill bool) {
	request := &dap.DisconnectRequest{Request: *c.newRequest("disconnect")}
	request.Arguments = &dap.DisconnectArguments{
		TerminateDebuggee: kill,
	}
	c.send(request)
}

// SetBreakpointsRequest sends a 'setBreakpoints' request.
func (c *Client) SetBreakpointsRequest(file string, lines []int) {
	c.SetBreakpointsRequestWithArgs(file, lines, nil, nil, nil)
}

// SetBreakpointsRequestWithArgs sends a 'setBreakpoints' request with an option to
// specify conditions, hit conditions, and log messages.
func (c *Client) SetBreakpointsRequestWithArgs(file string, lines []int, conditions, hitConditions, logMessages map[int]string) {
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
		if cond, ok := conditions[l]; ok {
			request.Arguments.Breakpoints[i].Condition = cond
		}
		if hitCond, ok := hitConditions[l]; ok {
			request.Arguments.Breakpoints[i].HitCondition = hitCond
		}
		if logMessage, ok := logMessages[l]; ok {
			request.Arguments.Breakpoints[i].LogMessage = logMessage
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

// NextInstructionRequest sends a 'next' request with granularity 'instruction'.
func (c *Client) NextInstructionRequest(thread int) {
	request := &dap.NextRequest{Request: *c.newRequest("next")}
	request.Arguments.ThreadId = thread
	request.Arguments.Granularity = "instruction"
	c.send(request)
}

// StepInRequest sends a 'stepIn' request.
func (c *Client) StepInRequest(thread int) {
	request := &dap.StepInRequest{Request: *c.newRequest("stepIn")}
	request.Arguments.ThreadId = thread
	c.send(request)
}

// StepInInstructionRequest sends a 'stepIn' request with granularity 'instruction'.
func (c *Client) StepInInstructionRequest(thread int) {
	request := &dap.StepInRequest{Request: *c.newRequest("stepIn")}
	request.Arguments.ThreadId = thread
	request.Arguments.Granularity = "instruction"
	c.send(request)
}

// StepOutRequest sends a 'stepOut' request.
func (c *Client) StepOutRequest(thread int) {
	request := &dap.StepOutRequest{Request: *c.newRequest("stepOut")}
	request.Arguments.ThreadId = thread
	c.send(request)
}

// StepOutInstructionRequest sends a 'stepOut' request with granularity 'instruction'.
func (c *Client) StepOutInstructionRequest(thread int) {
	request := &dap.StepOutRequest{Request: *c.newRequest("stepOut")}
	request.Arguments.ThreadId = thread
	request.Arguments.Granularity = "instruction"
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

// TerminateRequest sends a 'terminate' request.
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

// SetInstructionBreakpointsRequest sends a 'setInstructionBreakpoints' request.
func (c *Client) SetInstructionBreakpointsRequest(breakpoints []dap.InstructionBreakpoint) {
	c.send(&dap.SetInstructionBreakpointsRequest{
		Request: *c.newRequest("setInstructionBreakpoints"),
		Arguments: dap.SetInstructionBreakpointsArguments{
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
func (c *Client) DisassembleRequest(memoryReference string, instructionOffset, instructionCount int) {
	c.send(&dap.DisassembleRequest{
		Request: *c.newRequest("disassemble"),
		Arguments: dap.DisassembleArguments{
			MemoryReference:   memoryReference,
			Offset:            0,
			InstructionOffset: instructionOffset,
			InstructionCount:  instructionCount,
			ResolveSymbols:    false,
		},
	})
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
