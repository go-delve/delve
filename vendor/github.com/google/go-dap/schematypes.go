// Copyright 2020 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// DO NOT EDIT: This file is auto-generated.
// DAP spec: https://microsoft.github.io/debug-adapter-protocol/specification
// See cmd/gentypes/README.md for additional details.

package dap

// Message is an interface that all DAP message types implement with pointer
// receivers. It's not part of the protocol but is used to enforce static
// typing in Go code.
//
// Note: the DAP type "Message" (which is used in the body of ErrorResponse)
// is renamed to ErrorMessage to avoid collision with this interface.
type Message interface {
	isMessage()
}

// ProtocolMessage: Base class of requests, responses, and events.
type ProtocolMessage struct {
	Seq  int    `json:"seq"`
	Type string `json:"type"`
}

// Request: A client or debug adapter initiated request.
type Request struct {
	ProtocolMessage

	Command string `json:"command"`
}

// Event: A debug adapter initiated event.
type Event struct {
	ProtocolMessage

	Event string `json:"event"`
}

// Response: Response for a request.
type Response struct {
	ProtocolMessage

	RequestSeq int    `json:"request_seq"`
	Success    bool   `json:"success"`
	Command    string `json:"command"`
	Message    string `json:"message,omitempty"`
}

// ErrorResponse: On error (whenever 'success' is false), the body can provide more details.
type ErrorResponse struct {
	Response

	Body ErrorResponseBody `json:"body"`
}

type ErrorResponseBody struct {
	Error ErrorMessage `json:"error,omitempty"`
}

// CancelRequest: The 'cancel' request is used by the frontend to indicate that it is no longer interested in the result produced by a specific request issued earlier.
// This request has a hint characteristic: a debug adapter can only be expected to make a 'best effort' in honouring this request but there are no guarantees.
// The 'cancel' request may return an error if it could not cancel an operation but a frontend should refrain from presenting this error to end users.
// A frontend client should only call this request if the capability 'supportsCancelRequest' is true.
// The request that got canceled still needs to send a response back.
// This can either be a normal result ('success' attribute true) or an error response ('success' attribute false and the 'message' set to 'cancelled').
// Returning partial results from a cancelled request is possible but please note that a frontend client has no generic way for detecting that a response is partial or not.
type CancelRequest struct {
	Request

	Arguments CancelArguments `json:"arguments,omitempty"`
}

// CancelArguments: Arguments for 'cancel' request.
type CancelArguments struct {
	RequestId int `json:"requestId,omitempty"`
}

// CancelResponse: Response to 'cancel' request. This is just an acknowledgement, so no body field is required.
type CancelResponse struct {
	Response
}

// InitializedEvent: This event indicates that the debug adapter is ready to accept configuration requests (e.g. SetBreakpointsRequest, SetExceptionBreakpointsRequest).
// A debug adapter is expected to send this event when it is ready to accept configuration requests (but not before the 'initialize' request has finished).
// The sequence of events/requests is as follows:
// - adapters sends 'initialized' event (after the 'initialize' request has returned)
// - frontend sends zero or more 'setBreakpoints' requests
// - frontend sends one 'setFunctionBreakpoints' request
// - frontend sends a 'setExceptionBreakpoints' request if one or more 'exceptionBreakpointFilters' have been defined (or if 'supportsConfigurationDoneRequest' is not defined or false)
// - frontend sends other future configuration requests
// - frontend sends one 'configurationDone' request to indicate the end of the configuration.
type InitializedEvent struct {
	Event
}

// StoppedEvent: The event indicates that the execution of the debuggee has stopped due to some condition.
// This can be caused by a break point previously set, a stepping action has completed, by executing a debugger statement etc.
type StoppedEvent struct {
	Event

	Body StoppedEventBody `json:"body"`
}

type StoppedEventBody struct {
	Reason            string `json:"reason"`
	Description       string `json:"description,omitempty"`
	ThreadId          int    `json:"threadId,omitempty"`
	PreserveFocusHint bool   `json:"preserveFocusHint,omitempty"`
	Text              string `json:"text,omitempty"`
	AllThreadsStopped bool   `json:"allThreadsStopped,omitempty"`
}

// ContinuedEvent: The event indicates that the execution of the debuggee has continued.
// Please note: a debug adapter is not expected to send this event in response to a request that implies that execution continues, e.g. 'launch' or 'continue'.
// It is only necessary to send a 'continued' event if there was no previous request that implied this.
type ContinuedEvent struct {
	Event

	Body ContinuedEventBody `json:"body"`
}

type ContinuedEventBody struct {
	ThreadId            int  `json:"threadId"`
	AllThreadsContinued bool `json:"allThreadsContinued,omitempty"`
}

// ExitedEvent: The event indicates that the debuggee has exited and returns its exit code.
type ExitedEvent struct {
	Event

	Body ExitedEventBody `json:"body"`
}

type ExitedEventBody struct {
	ExitCode int `json:"exitCode"`
}

// TerminatedEvent: The event indicates that debugging of the debuggee has terminated. This does **not** mean that the debuggee itself has exited.
type TerminatedEvent struct {
	Event

	Body TerminatedEventBody `json:"body,omitempty"`
}

type TerminatedEventBody struct {
	Restart interface{} `json:"restart,omitempty"`
}

// ThreadEvent: The event indicates that a thread has started or exited.
type ThreadEvent struct {
	Event

	Body ThreadEventBody `json:"body"`
}

type ThreadEventBody struct {
	Reason   string `json:"reason"`
	ThreadId int    `json:"threadId"`
}

// OutputEvent: The event indicates that the target has produced some output.
type OutputEvent struct {
	Event

	Body OutputEventBody `json:"body"`
}

type OutputEventBody struct {
	Category           string      `json:"category,omitempty"`
	Output             string      `json:"output"`
	VariablesReference int         `json:"variablesReference,omitempty"`
	Source             Source      `json:"source,omitempty"`
	Line               int         `json:"line,omitempty"`
	Column             int         `json:"column,omitempty"`
	Data               interface{} `json:"data,omitempty"`
}

// BreakpointEvent: The event indicates that some information about a breakpoint has changed.
type BreakpointEvent struct {
	Event

	Body BreakpointEventBody `json:"body"`
}

type BreakpointEventBody struct {
	Reason     string     `json:"reason"`
	Breakpoint Breakpoint `json:"breakpoint"`
}

// ModuleEvent: The event indicates that some information about a module has changed.
type ModuleEvent struct {
	Event

	Body ModuleEventBody `json:"body"`
}

type ModuleEventBody struct {
	Reason string `json:"reason"`
	Module Module `json:"module"`
}

// LoadedSourceEvent: The event indicates that some source has been added, changed, or removed from the set of all loaded sources.
type LoadedSourceEvent struct {
	Event

	Body LoadedSourceEventBody `json:"body"`
}

type LoadedSourceEventBody struct {
	Reason string `json:"reason"`
	Source Source `json:"source"`
}

// ProcessEvent: The event indicates that the debugger has begun debugging a new process. Either one that it has launched, or one that it has attached to.
type ProcessEvent struct {
	Event

	Body ProcessEventBody `json:"body"`
}

type ProcessEventBody struct {
	Name            string `json:"name"`
	SystemProcessId int    `json:"systemProcessId,omitempty"`
	IsLocalProcess  bool   `json:"isLocalProcess,omitempty"`
	StartMethod     string `json:"startMethod,omitempty"`
	PointerSize     int    `json:"pointerSize,omitempty"`
}

// CapabilitiesEvent: The event indicates that one or more capabilities have changed.
// Since the capabilities are dependent on the frontend and its UI, it might not be possible to change that at random times (or too late).
// Consequently this event has a hint characteristic: a frontend can only be expected to make a 'best effort' in honouring individual capabilities but there are no guarantees.
// Only changed capabilities need to be included, all other capabilities keep their values.
type CapabilitiesEvent struct {
	Event

	Body CapabilitiesEventBody `json:"body"`
}

type CapabilitiesEventBody struct {
	Capabilities Capabilities `json:"capabilities"`
}

// RunInTerminalRequest: This request is sent from the debug adapter to the client to run a command in a terminal. This is typically used to launch the debuggee in a terminal provided by the client.
type RunInTerminalRequest struct {
	Request

	Arguments RunInTerminalRequestArguments `json:"arguments"`
}

// RunInTerminalRequestArguments: Arguments for 'runInTerminal' request.
type RunInTerminalRequestArguments struct {
	Kind  string                 `json:"kind,omitempty"`
	Title string                 `json:"title,omitempty"`
	Cwd   string                 `json:"cwd"`
	Args  []string               `json:"args"`
	Env   map[string]interface{} `json:"env,omitempty"`
}

// RunInTerminalResponse: Response to 'runInTerminal' request.
type RunInTerminalResponse struct {
	Response

	Body RunInTerminalResponseBody `json:"body"`
}

type RunInTerminalResponseBody struct {
	ProcessId      int `json:"processId,omitempty"`
	ShellProcessId int `json:"shellProcessId,omitempty"`
}

// InitializeRequest: The 'initialize' request is sent as the first request from the client to the debug adapter in order to configure it with client capabilities and to retrieve capabilities from the debug adapter.
// Until the debug adapter has responded to with an 'initialize' response, the client must not send any additional requests or events to the debug adapter. In addition the debug adapter is not allowed to send any requests or events to the client until it has responded with an 'initialize' response.
// The 'initialize' request may only be sent once.
type InitializeRequest struct {
	Request

	Arguments InitializeRequestArguments `json:"arguments"`
}

// InitializeRequestArguments: Arguments for 'initialize' request.
type InitializeRequestArguments struct {
	ClientID                     string `json:"clientID,omitempty"`
	ClientName                   string `json:"clientName,omitempty"`
	AdapterID                    string `json:"adapterID"`
	Locale                       string `json:"locale,omitempty"`
	LinesStartAt1                bool   `json:"linesStartAt1,omitempty"`
	ColumnsStartAt1              bool   `json:"columnsStartAt1,omitempty"`
	PathFormat                   string `json:"pathFormat,omitempty"`
	SupportsVariableType         bool   `json:"supportsVariableType,omitempty"`
	SupportsVariablePaging       bool   `json:"supportsVariablePaging,omitempty"`
	SupportsRunInTerminalRequest bool   `json:"supportsRunInTerminalRequest,omitempty"`
	SupportsMemoryReferences     bool   `json:"supportsMemoryReferences,omitempty"`
}

// InitializeResponse: Response to 'initialize' request.
type InitializeResponse struct {
	Response

	Body Capabilities `json:"body,omitempty"`
}

// ConfigurationDoneRequest: The client of the debug protocol must send this request at the end of the sequence of configuration requests (which was started by the 'initialized' event).
type ConfigurationDoneRequest struct {
	Request

	Arguments ConfigurationDoneArguments `json:"arguments,omitempty"`
}

// ConfigurationDoneArguments: Arguments for 'configurationDone' request.
type ConfigurationDoneArguments struct {
}

// ConfigurationDoneResponse: Response to 'configurationDone' request. This is just an acknowledgement, so no body field is required.
type ConfigurationDoneResponse struct {
	Response
}

// LaunchRequest: The launch request is sent from the client to the debug adapter to start the debuggee with or without debugging (if 'noDebug' is true). Since launching is debugger/runtime specific, the arguments for this request are not part of this specification.
type LaunchRequest struct {
	Request

	Arguments map[string]interface{} `json:"arguments"`
}

// LaunchResponse: Response to 'launch' request. This is just an acknowledgement, so no body field is required.
type LaunchResponse struct {
	Response
}

// AttachRequest: The attach request is sent from the client to the debug adapter to attach to a debuggee that is already running. Since attaching is debugger/runtime specific, the arguments for this request are not part of this specification.
type AttachRequest struct {
	Request

	Arguments AttachRequestArguments `json:"arguments"`
}

// AttachRequestArguments: Arguments for 'attach' request. Additional attributes are implementation specific.
type AttachRequestArguments struct {
	Restart interface{} `json:"__restart,omitempty"`
}

// AttachResponse: Response to 'attach' request. This is just an acknowledgement, so no body field is required.
type AttachResponse struct {
	Response
}

// RestartRequest: Restarts a debug session. If the capability 'supportsRestartRequest' is missing or has the value false,
// the client will implement 'restart' by terminating the debug adapter first and then launching it anew.
// A debug adapter can override this default behaviour by implementing a restart request
// and setting the capability 'supportsRestartRequest' to true.
type RestartRequest struct {
	Request

	Arguments RestartArguments `json:"arguments,omitempty"`
}

// RestartArguments: Arguments for 'restart' request.
type RestartArguments struct {
}

// RestartResponse: Response to 'restart' request. This is just an acknowledgement, so no body field is required.
type RestartResponse struct {
	Response
}

// DisconnectRequest: The 'disconnect' request is sent from the client to the debug adapter in order to stop debugging. It asks the debug adapter to disconnect from the debuggee and to terminate the debug adapter. If the debuggee has been started with the 'launch' request, the 'disconnect' request terminates the debuggee. If the 'attach' request was used to connect to the debuggee, 'disconnect' does not terminate the debuggee. This behavior can be controlled with the 'terminateDebuggee' argument (if supported by the debug adapter).
type DisconnectRequest struct {
	Request

	Arguments DisconnectArguments `json:"arguments,omitempty"`
}

// DisconnectArguments: Arguments for 'disconnect' request.
type DisconnectArguments struct {
	Restart           bool `json:"restart,omitempty"`
	TerminateDebuggee bool `json:"terminateDebuggee,omitempty"`
}

// DisconnectResponse: Response to 'disconnect' request. This is just an acknowledgement, so no body field is required.
type DisconnectResponse struct {
	Response
}

// TerminateRequest: The 'terminate' request is sent from the client to the debug adapter in order to give the debuggee a chance for terminating itself.
type TerminateRequest struct {
	Request

	Arguments TerminateArguments `json:"arguments,omitempty"`
}

// TerminateArguments: Arguments for 'terminate' request.
type TerminateArguments struct {
	Restart bool `json:"restart,omitempty"`
}

// TerminateResponse: Response to 'terminate' request. This is just an acknowledgement, so no body field is required.
type TerminateResponse struct {
	Response
}

// BreakpointLocationsRequest: The 'breakpointLocations' request returns all possible locations for source breakpoints in a given range.
type BreakpointLocationsRequest struct {
	Request

	Arguments BreakpointLocationsArguments `json:"arguments,omitempty"`
}

// BreakpointLocationsArguments: Arguments for 'breakpointLocations' request.
type BreakpointLocationsArguments struct {
	Source    Source `json:"source"`
	Line      int    `json:"line"`
	Column    int    `json:"column,omitempty"`
	EndLine   int    `json:"endLine,omitempty"`
	EndColumn int    `json:"endColumn,omitempty"`
}

// BreakpointLocationsResponse: Response to 'breakpointLocations' request.
// Contains possible locations for source breakpoints.
type BreakpointLocationsResponse struct {
	Response

	Body BreakpointLocationsResponseBody `json:"body"`
}

type BreakpointLocationsResponseBody struct {
	Breakpoints []BreakpointLocation `json:"breakpoints"`
}

// SetBreakpointsRequest: Sets multiple breakpoints for a single source and clears all previous breakpoints in that source.
// To clear all breakpoint for a source, specify an empty array.
// When a breakpoint is hit, a 'stopped' event (with reason 'breakpoint') is generated.
type SetBreakpointsRequest struct {
	Request

	Arguments SetBreakpointsArguments `json:"arguments"`
}

// SetBreakpointsArguments: Arguments for 'setBreakpoints' request.
type SetBreakpointsArguments struct {
	Source         Source             `json:"source"`
	Breakpoints    []SourceBreakpoint `json:"breakpoints,omitempty"`
	Lines          []int              `json:"lines,omitempty"`
	SourceModified bool               `json:"sourceModified,omitempty"`
}

// SetBreakpointsResponse: Response to 'setBreakpoints' request.
// Returned is information about each breakpoint created by this request.
// This includes the actual code location and whether the breakpoint could be verified.
// The breakpoints returned are in the same order as the elements of the 'breakpoints'
// (or the deprecated 'lines') array in the arguments.
type SetBreakpointsResponse struct {
	Response

	Body SetBreakpointsResponseBody `json:"body"`
}

type SetBreakpointsResponseBody struct {
	Breakpoints []Breakpoint `json:"breakpoints"`
}

// SetFunctionBreakpointsRequest: Replaces all existing function breakpoints with new function breakpoints.
// To clear all function breakpoints, specify an empty array.
// When a function breakpoint is hit, a 'stopped' event (with reason 'function breakpoint') is generated.
type SetFunctionBreakpointsRequest struct {
	Request

	Arguments SetFunctionBreakpointsArguments `json:"arguments"`
}

// SetFunctionBreakpointsArguments: Arguments for 'setFunctionBreakpoints' request.
type SetFunctionBreakpointsArguments struct {
	Breakpoints []FunctionBreakpoint `json:"breakpoints"`
}

// SetFunctionBreakpointsResponse: Response to 'setFunctionBreakpoints' request.
// Returned is information about each breakpoint created by this request.
type SetFunctionBreakpointsResponse struct {
	Response

	Body SetFunctionBreakpointsResponseBody `json:"body"`
}

type SetFunctionBreakpointsResponseBody struct {
	Breakpoints []Breakpoint `json:"breakpoints"`
}

// SetExceptionBreakpointsRequest: The request configures the debuggers response to thrown exceptions. If an exception is configured to break, a 'stopped' event is fired (with reason 'exception').
type SetExceptionBreakpointsRequest struct {
	Request

	Arguments SetExceptionBreakpointsArguments `json:"arguments"`
}

// SetExceptionBreakpointsArguments: Arguments for 'setExceptionBreakpoints' request.
type SetExceptionBreakpointsArguments struct {
	Filters          []string           `json:"filters"`
	ExceptionOptions []ExceptionOptions `json:"exceptionOptions,omitempty"`
}

// SetExceptionBreakpointsResponse: Response to 'setExceptionBreakpoints' request. This is just an acknowledgement, so no body field is required.
type SetExceptionBreakpointsResponse struct {
	Response
}

// DataBreakpointInfoRequest: Obtains information on a possible data breakpoint that could be set on an expression or variable.
type DataBreakpointInfoRequest struct {
	Request

	Arguments DataBreakpointInfoArguments `json:"arguments"`
}

// DataBreakpointInfoArguments: Arguments for 'dataBreakpointInfo' request.
type DataBreakpointInfoArguments struct {
	VariablesReference int    `json:"variablesReference,omitempty"`
	Name               string `json:"name"`
}

// DataBreakpointInfoResponse: Response to 'dataBreakpointInfo' request.
type DataBreakpointInfoResponse struct {
	Response

	Body DataBreakpointInfoResponseBody `json:"body"`
}

type DataBreakpointInfoResponseBody struct {
	DataId      interface{}                `json:"dataId"`
	Description string                     `json:"description"`
	AccessTypes []DataBreakpointAccessType `json:"accessTypes,omitempty"`
	CanPersist  bool                       `json:"canPersist,omitempty"`
}

// SetDataBreakpointsRequest: Replaces all existing data breakpoints with new data breakpoints.
// To clear all data breakpoints, specify an empty array.
// When a data breakpoint is hit, a 'stopped' event (with reason 'data breakpoint') is generated.
type SetDataBreakpointsRequest struct {
	Request

	Arguments SetDataBreakpointsArguments `json:"arguments"`
}

// SetDataBreakpointsArguments: Arguments for 'setDataBreakpoints' request.
type SetDataBreakpointsArguments struct {
	Breakpoints []DataBreakpoint `json:"breakpoints"`
}

// SetDataBreakpointsResponse: Response to 'setDataBreakpoints' request.
// Returned is information about each breakpoint created by this request.
type SetDataBreakpointsResponse struct {
	Response

	Body SetDataBreakpointsResponseBody `json:"body"`
}

type SetDataBreakpointsResponseBody struct {
	Breakpoints []Breakpoint `json:"breakpoints"`
}

// ContinueRequest: The request starts the debuggee to run again.
type ContinueRequest struct {
	Request

	Arguments ContinueArguments `json:"arguments"`
}

// ContinueArguments: Arguments for 'continue' request.
type ContinueArguments struct {
	ThreadId int `json:"threadId"`
}

// ContinueResponse: Response to 'continue' request.
type ContinueResponse struct {
	Response

	Body ContinueResponseBody `json:"body"`
}

type ContinueResponseBody struct {
	AllThreadsContinued bool `json:"allThreadsContinued,omitempty"`
}

// NextRequest: The request starts the debuggee to run again for one step.
// The debug adapter first sends the response and then a 'stopped' event (with reason 'step') after the step has completed.
type NextRequest struct {
	Request

	Arguments NextArguments `json:"arguments"`
}

// NextArguments: Arguments for 'next' request.
type NextArguments struct {
	ThreadId int `json:"threadId"`
}

// NextResponse: Response to 'next' request. This is just an acknowledgement, so no body field is required.
type NextResponse struct {
	Response
}

// StepInRequest: The request starts the debuggee to step into a function/method if possible.
// If it cannot step into a target, 'stepIn' behaves like 'next'.
// The debug adapter first sends the response and then a 'stopped' event (with reason 'step') after the step has completed.
// If there are multiple function/method calls (or other targets) on the source line,
// the optional argument 'targetId' can be used to control into which target the 'stepIn' should occur.
// The list of possible targets for a given source line can be retrieved via the 'stepInTargets' request.
type StepInRequest struct {
	Request

	Arguments StepInArguments `json:"arguments"`
}

// StepInArguments: Arguments for 'stepIn' request.
type StepInArguments struct {
	ThreadId int `json:"threadId"`
	TargetId int `json:"targetId,omitempty"`
}

// StepInResponse: Response to 'stepIn' request. This is just an acknowledgement, so no body field is required.
type StepInResponse struct {
	Response
}

// StepOutRequest: The request starts the debuggee to run again for one step.
// The debug adapter first sends the response and then a 'stopped' event (with reason 'step') after the step has completed.
type StepOutRequest struct {
	Request

	Arguments StepOutArguments `json:"arguments"`
}

// StepOutArguments: Arguments for 'stepOut' request.
type StepOutArguments struct {
	ThreadId int `json:"threadId"`
}

// StepOutResponse: Response to 'stepOut' request. This is just an acknowledgement, so no body field is required.
type StepOutResponse struct {
	Response
}

// StepBackRequest: The request starts the debuggee to run one step backwards.
// The debug adapter first sends the response and then a 'stopped' event (with reason 'step') after the step has completed. Clients should only call this request if the capability 'supportsStepBack' is true.
type StepBackRequest struct {
	Request

	Arguments StepBackArguments `json:"arguments"`
}

// StepBackArguments: Arguments for 'stepBack' request.
type StepBackArguments struct {
	ThreadId int `json:"threadId"`
}

// StepBackResponse: Response to 'stepBack' request. This is just an acknowledgement, so no body field is required.
type StepBackResponse struct {
	Response
}

// ReverseContinueRequest: The request starts the debuggee to run backward. Clients should only call this request if the capability 'supportsStepBack' is true.
type ReverseContinueRequest struct {
	Request

	Arguments ReverseContinueArguments `json:"arguments"`
}

// ReverseContinueArguments: Arguments for 'reverseContinue' request.
type ReverseContinueArguments struct {
	ThreadId int `json:"threadId"`
}

// ReverseContinueResponse: Response to 'reverseContinue' request. This is just an acknowledgement, so no body field is required.
type ReverseContinueResponse struct {
	Response
}

// RestartFrameRequest: The request restarts execution of the specified stackframe.
// The debug adapter first sends the response and then a 'stopped' event (with reason 'restart') after the restart has completed.
type RestartFrameRequest struct {
	Request

	Arguments RestartFrameArguments `json:"arguments"`
}

// RestartFrameArguments: Arguments for 'restartFrame' request.
type RestartFrameArguments struct {
	FrameId int `json:"frameId"`
}

// RestartFrameResponse: Response to 'restartFrame' request. This is just an acknowledgement, so no body field is required.
type RestartFrameResponse struct {
	Response
}

// GotoRequest: The request sets the location where the debuggee will continue to run.
// This makes it possible to skip the execution of code or to executed code again.
// The code between the current location and the goto target is not executed but skipped.
// The debug adapter first sends the response and then a 'stopped' event with reason 'goto'.
type GotoRequest struct {
	Request

	Arguments GotoArguments `json:"arguments"`
}

// GotoArguments: Arguments for 'goto' request.
type GotoArguments struct {
	ThreadId int `json:"threadId"`
	TargetId int `json:"targetId"`
}

// GotoResponse: Response to 'goto' request. This is just an acknowledgement, so no body field is required.
type GotoResponse struct {
	Response
}

// PauseRequest: The request suspends the debuggee.
// The debug adapter first sends the response and then a 'stopped' event (with reason 'pause') after the thread has been paused successfully.
type PauseRequest struct {
	Request

	Arguments PauseArguments `json:"arguments"`
}

// PauseArguments: Arguments for 'pause' request.
type PauseArguments struct {
	ThreadId int `json:"threadId"`
}

// PauseResponse: Response to 'pause' request. This is just an acknowledgement, so no body field is required.
type PauseResponse struct {
	Response
}

// StackTraceRequest: The request returns a stacktrace from the current execution state.
type StackTraceRequest struct {
	Request

	Arguments StackTraceArguments `json:"arguments"`
}

// StackTraceArguments: Arguments for 'stackTrace' request.
type StackTraceArguments struct {
	ThreadId   int              `json:"threadId"`
	StartFrame int              `json:"startFrame,omitempty"`
	Levels     int              `json:"levels,omitempty"`
	Format     StackFrameFormat `json:"format,omitempty"`
}

// StackTraceResponse: Response to 'stackTrace' request.
type StackTraceResponse struct {
	Response

	Body StackTraceResponseBody `json:"body"`
}

type StackTraceResponseBody struct {
	StackFrames []StackFrame `json:"stackFrames"`
	TotalFrames int          `json:"totalFrames,omitempty"`
}

// ScopesRequest: The request returns the variable scopes for a given stackframe ID.
type ScopesRequest struct {
	Request

	Arguments ScopesArguments `json:"arguments"`
}

// ScopesArguments: Arguments for 'scopes' request.
type ScopesArguments struct {
	FrameId int `json:"frameId"`
}

// ScopesResponse: Response to 'scopes' request.
type ScopesResponse struct {
	Response

	Body ScopesResponseBody `json:"body"`
}

type ScopesResponseBody struct {
	Scopes []Scope `json:"scopes"`
}

// VariablesRequest: Retrieves all child variables for the given variable reference.
// An optional filter can be used to limit the fetched children to either named or indexed children.
type VariablesRequest struct {
	Request

	Arguments VariablesArguments `json:"arguments"`
}

// VariablesArguments: Arguments for 'variables' request.
type VariablesArguments struct {
	VariablesReference int         `json:"variablesReference"`
	Filter             string      `json:"filter,omitempty"`
	Start              int         `json:"start,omitempty"`
	Count              int         `json:"count,omitempty"`
	Format             ValueFormat `json:"format,omitempty"`
}

// VariablesResponse: Response to 'variables' request.
type VariablesResponse struct {
	Response

	Body VariablesResponseBody `json:"body"`
}

type VariablesResponseBody struct {
	Variables []Variable `json:"variables"`
}

// SetVariableRequest: Set the variable with the given name in the variable container to a new value.
type SetVariableRequest struct {
	Request

	Arguments SetVariableArguments `json:"arguments"`
}

// SetVariableArguments: Arguments for 'setVariable' request.
type SetVariableArguments struct {
	VariablesReference int         `json:"variablesReference"`
	Name               string      `json:"name"`
	Value              string      `json:"value"`
	Format             ValueFormat `json:"format,omitempty"`
}

// SetVariableResponse: Response to 'setVariable' request.
type SetVariableResponse struct {
	Response

	Body SetVariableResponseBody `json:"body"`
}

type SetVariableResponseBody struct {
	Value              string `json:"value"`
	Type               string `json:"type,omitempty"`
	VariablesReference int    `json:"variablesReference,omitempty"`
	NamedVariables     int    `json:"namedVariables,omitempty"`
	IndexedVariables   int    `json:"indexedVariables,omitempty"`
}

// SourceRequest: The request retrieves the source code for a given source reference.
type SourceRequest struct {
	Request

	Arguments SourceArguments `json:"arguments"`
}

// SourceArguments: Arguments for 'source' request.
type SourceArguments struct {
	Source          Source `json:"source,omitempty"`
	SourceReference int    `json:"sourceReference"`
}

// SourceResponse: Response to 'source' request.
type SourceResponse struct {
	Response

	Body SourceResponseBody `json:"body"`
}

type SourceResponseBody struct {
	Content  string `json:"content"`
	MimeType string `json:"mimeType,omitempty"`
}

// ThreadsRequest: The request retrieves a list of all threads.
type ThreadsRequest struct {
	Request
}

// ThreadsResponse: Response to 'threads' request.
type ThreadsResponse struct {
	Response

	Body ThreadsResponseBody `json:"body"`
}

type ThreadsResponseBody struct {
	Threads []Thread `json:"threads"`
}

// TerminateThreadsRequest: The request terminates the threads with the given ids.
type TerminateThreadsRequest struct {
	Request

	Arguments TerminateThreadsArguments `json:"arguments"`
}

// TerminateThreadsArguments: Arguments for 'terminateThreads' request.
type TerminateThreadsArguments struct {
	ThreadIds []int `json:"threadIds,omitempty"`
}

// TerminateThreadsResponse: Response to 'terminateThreads' request. This is just an acknowledgement, so no body field is required.
type TerminateThreadsResponse struct {
	Response
}

// ModulesRequest: Modules can be retrieved from the debug adapter with the ModulesRequest which can either return all modules or a range of modules to support paging.
type ModulesRequest struct {
	Request

	Arguments ModulesArguments `json:"arguments"`
}

// ModulesArguments: Arguments for 'modules' request.
type ModulesArguments struct {
	StartModule int `json:"startModule,omitempty"`
	ModuleCount int `json:"moduleCount,omitempty"`
}

// ModulesResponse: Response to 'modules' request.
type ModulesResponse struct {
	Response

	Body ModulesResponseBody `json:"body"`
}

type ModulesResponseBody struct {
	Modules      []Module `json:"modules"`
	TotalModules int      `json:"totalModules,omitempty"`
}

// LoadedSourcesRequest: Retrieves the set of all sources currently loaded by the debugged process.
type LoadedSourcesRequest struct {
	Request

	Arguments LoadedSourcesArguments `json:"arguments,omitempty"`
}

// LoadedSourcesArguments: Arguments for 'loadedSources' request.
type LoadedSourcesArguments struct {
}

// LoadedSourcesResponse: Response to 'loadedSources' request.
type LoadedSourcesResponse struct {
	Response

	Body LoadedSourcesResponseBody `json:"body"`
}

type LoadedSourcesResponseBody struct {
	Sources []Source `json:"sources"`
}

// EvaluateRequest: Evaluates the given expression in the context of the top most stack frame.
// The expression has access to any variables and arguments that are in scope.
type EvaluateRequest struct {
	Request

	Arguments EvaluateArguments `json:"arguments"`
}

// EvaluateArguments: Arguments for 'evaluate' request.
type EvaluateArguments struct {
	Expression string      `json:"expression"`
	FrameId    int         `json:"frameId,omitempty"`
	Context    string      `json:"context,omitempty"`
	Format     ValueFormat `json:"format,omitempty"`
}

// EvaluateResponse: Response to 'evaluate' request.
type EvaluateResponse struct {
	Response

	Body EvaluateResponseBody `json:"body"`
}

type EvaluateResponseBody struct {
	Result             string                   `json:"result"`
	Type               string                   `json:"type,omitempty"`
	PresentationHint   VariablePresentationHint `json:"presentationHint,omitempty"`
	VariablesReference int                      `json:"variablesReference"`
	NamedVariables     int                      `json:"namedVariables,omitempty"`
	IndexedVariables   int                      `json:"indexedVariables,omitempty"`
	MemoryReference    string                   `json:"memoryReference,omitempty"`
}

// SetExpressionRequest: Evaluates the given 'value' expression and assigns it to the 'expression' which must be a modifiable l-value.
// The expressions have access to any variables and arguments that are in scope of the specified frame.
type SetExpressionRequest struct {
	Request

	Arguments SetExpressionArguments `json:"arguments"`
}

// SetExpressionArguments: Arguments for 'setExpression' request.
type SetExpressionArguments struct {
	Expression string      `json:"expression"`
	Value      string      `json:"value"`
	FrameId    int         `json:"frameId,omitempty"`
	Format     ValueFormat `json:"format,omitempty"`
}

// SetExpressionResponse: Response to 'setExpression' request.
type SetExpressionResponse struct {
	Response

	Body SetExpressionResponseBody `json:"body"`
}

type SetExpressionResponseBody struct {
	Value              string                   `json:"value"`
	Type               string                   `json:"type,omitempty"`
	PresentationHint   VariablePresentationHint `json:"presentationHint,omitempty"`
	VariablesReference int                      `json:"variablesReference,omitempty"`
	NamedVariables     int                      `json:"namedVariables,omitempty"`
	IndexedVariables   int                      `json:"indexedVariables,omitempty"`
}

// StepInTargetsRequest: This request retrieves the possible stepIn targets for the specified stack frame.
// These targets can be used in the 'stepIn' request.
// The StepInTargets may only be called if the 'supportsStepInTargetsRequest' capability exists and is true.
type StepInTargetsRequest struct {
	Request

	Arguments StepInTargetsArguments `json:"arguments"`
}

// StepInTargetsArguments: Arguments for 'stepInTargets' request.
type StepInTargetsArguments struct {
	FrameId int `json:"frameId"`
}

// StepInTargetsResponse: Response to 'stepInTargets' request.
type StepInTargetsResponse struct {
	Response

	Body StepInTargetsResponseBody `json:"body"`
}

type StepInTargetsResponseBody struct {
	Targets []StepInTarget `json:"targets"`
}

// GotoTargetsRequest: This request retrieves the possible goto targets for the specified source location.
// These targets can be used in the 'goto' request.
// The GotoTargets request may only be called if the 'supportsGotoTargetsRequest' capability exists and is true.
type GotoTargetsRequest struct {
	Request

	Arguments GotoTargetsArguments `json:"arguments"`
}

// GotoTargetsArguments: Arguments for 'gotoTargets' request.
type GotoTargetsArguments struct {
	Source Source `json:"source"`
	Line   int    `json:"line"`
	Column int    `json:"column,omitempty"`
}

// GotoTargetsResponse: Response to 'gotoTargets' request.
type GotoTargetsResponse struct {
	Response

	Body GotoTargetsResponseBody `json:"body"`
}

type GotoTargetsResponseBody struct {
	Targets []GotoTarget `json:"targets"`
}

// CompletionsRequest: Returns a list of possible completions for a given caret position and text.
// The CompletionsRequest may only be called if the 'supportsCompletionsRequest' capability exists and is true.
type CompletionsRequest struct {
	Request

	Arguments CompletionsArguments `json:"arguments"`
}

// CompletionsArguments: Arguments for 'completions' request.
type CompletionsArguments struct {
	FrameId int    `json:"frameId,omitempty"`
	Text    string `json:"text"`
	Column  int    `json:"column"`
	Line    int    `json:"line,omitempty"`
}

// CompletionsResponse: Response to 'completions' request.
type CompletionsResponse struct {
	Response

	Body CompletionsResponseBody `json:"body"`
}

type CompletionsResponseBody struct {
	Targets []CompletionItem `json:"targets"`
}

// ExceptionInfoRequest: Retrieves the details of the exception that caused this event to be raised.
type ExceptionInfoRequest struct {
	Request

	Arguments ExceptionInfoArguments `json:"arguments"`
}

// ExceptionInfoArguments: Arguments for 'exceptionInfo' request.
type ExceptionInfoArguments struct {
	ThreadId int `json:"threadId"`
}

// ExceptionInfoResponse: Response to 'exceptionInfo' request.
type ExceptionInfoResponse struct {
	Response

	Body ExceptionInfoResponseBody `json:"body"`
}

type ExceptionInfoResponseBody struct {
	ExceptionId string             `json:"exceptionId"`
	Description string             `json:"description,omitempty"`
	BreakMode   ExceptionBreakMode `json:"breakMode"`
	Details     ExceptionDetails   `json:"details,omitempty"`
}

// ReadMemoryRequest: Reads bytes from memory at the provided location.
type ReadMemoryRequest struct {
	Request

	Arguments ReadMemoryArguments `json:"arguments"`
}

// ReadMemoryArguments: Arguments for 'readMemory' request.
type ReadMemoryArguments struct {
	MemoryReference string `json:"memoryReference"`
	Offset          int    `json:"offset,omitempty"`
	Count           int    `json:"count"`
}

// ReadMemoryResponse: Response to 'readMemory' request.
type ReadMemoryResponse struct {
	Response

	Body ReadMemoryResponseBody `json:"body,omitempty"`
}

type ReadMemoryResponseBody struct {
	Address         string `json:"address"`
	UnreadableBytes int    `json:"unreadableBytes,omitempty"`
	Data            string `json:"data,omitempty"`
}

// DisassembleRequest: Disassembles code stored at the provided location.
type DisassembleRequest struct {
	Request

	Arguments DisassembleArguments `json:"arguments"`
}

// DisassembleArguments: Arguments for 'disassemble' request.
type DisassembleArguments struct {
	MemoryReference   string `json:"memoryReference"`
	Offset            int    `json:"offset,omitempty"`
	InstructionOffset int    `json:"instructionOffset,omitempty"`
	InstructionCount  int    `json:"instructionCount"`
	ResolveSymbols    bool   `json:"resolveSymbols,omitempty"`
}

// DisassembleResponse: Response to 'disassemble' request.
type DisassembleResponse struct {
	Response

	Body DisassembleResponseBody `json:"body,omitempty"`
}

type DisassembleResponseBody struct {
	Instructions []DisassembledInstruction `json:"instructions"`
}

// Capabilities: Information about the capabilities of a debug adapter.
type Capabilities struct {
	SupportsConfigurationDoneRequest   bool                         `json:"supportsConfigurationDoneRequest,omitempty"`
	SupportsFunctionBreakpoints        bool                         `json:"supportsFunctionBreakpoints,omitempty"`
	SupportsConditionalBreakpoints     bool                         `json:"supportsConditionalBreakpoints,omitempty"`
	SupportsHitConditionalBreakpoints  bool                         `json:"supportsHitConditionalBreakpoints,omitempty"`
	SupportsEvaluateForHovers          bool                         `json:"supportsEvaluateForHovers,omitempty"`
	ExceptionBreakpointFilters         []ExceptionBreakpointsFilter `json:"exceptionBreakpointFilters,omitempty"`
	SupportsStepBack                   bool                         `json:"supportsStepBack,omitempty"`
	SupportsSetVariable                bool                         `json:"supportsSetVariable,omitempty"`
	SupportsRestartFrame               bool                         `json:"supportsRestartFrame,omitempty"`
	SupportsGotoTargetsRequest         bool                         `json:"supportsGotoTargetsRequest,omitempty"`
	SupportsStepInTargetsRequest       bool                         `json:"supportsStepInTargetsRequest,omitempty"`
	SupportsCompletionsRequest         bool                         `json:"supportsCompletionsRequest,omitempty"`
	CompletionTriggerCharacters        []string                     `json:"completionTriggerCharacters,omitempty"`
	SupportsModulesRequest             bool                         `json:"supportsModulesRequest,omitempty"`
	AdditionalModuleColumns            []ColumnDescriptor           `json:"additionalModuleColumns,omitempty"`
	SupportedChecksumAlgorithms        []ChecksumAlgorithm          `json:"supportedChecksumAlgorithms,omitempty"`
	SupportsRestartRequest             bool                         `json:"supportsRestartRequest,omitempty"`
	SupportsExceptionOptions           bool                         `json:"supportsExceptionOptions,omitempty"`
	SupportsValueFormattingOptions     bool                         `json:"supportsValueFormattingOptions,omitempty"`
	SupportsExceptionInfoRequest       bool                         `json:"supportsExceptionInfoRequest,omitempty"`
	SupportTerminateDebuggee           bool                         `json:"supportTerminateDebuggee,omitempty"`
	SupportsDelayedStackTraceLoading   bool                         `json:"supportsDelayedStackTraceLoading,omitempty"`
	SupportsLoadedSourcesRequest       bool                         `json:"supportsLoadedSourcesRequest,omitempty"`
	SupportsLogPoints                  bool                         `json:"supportsLogPoints,omitempty"`
	SupportsTerminateThreadsRequest    bool                         `json:"supportsTerminateThreadsRequest,omitempty"`
	SupportsSetExpression              bool                         `json:"supportsSetExpression,omitempty"`
	SupportsTerminateRequest           bool                         `json:"supportsTerminateRequest,omitempty"`
	SupportsDataBreakpoints            bool                         `json:"supportsDataBreakpoints,omitempty"`
	SupportsReadMemoryRequest          bool                         `json:"supportsReadMemoryRequest,omitempty"`
	SupportsDisassembleRequest         bool                         `json:"supportsDisassembleRequest,omitempty"`
	SupportsCancelRequest              bool                         `json:"supportsCancelRequest,omitempty"`
	SupportsBreakpointLocationsRequest bool                         `json:"supportsBreakpointLocationsRequest,omitempty"`
}

// ExceptionBreakpointsFilter: An ExceptionBreakpointsFilter is shown in the UI as an option for configuring how exceptions are dealt with.
type ExceptionBreakpointsFilter struct {
	Filter  string `json:"filter"`
	Label   string `json:"label"`
	Default bool   `json:"default,omitempty"`
}

// ErrorMessage: A structured message object. Used to return errors from requests.
type ErrorMessage struct {
	Id            int               `json:"id"`
	Format        string            `json:"format"`
	Variables     map[string]string `json:"variables,omitempty"`
	SendTelemetry bool              `json:"sendTelemetry,omitempty"`
	ShowUser      bool              `json:"showUser,omitempty"`
	Url           string            `json:"url,omitempty"`
	UrlLabel      string            `json:"urlLabel,omitempty"`
}

// Module: A Module object represents a row in the modules view.
// Two attributes are mandatory: an id identifies a module in the modules view and is used in a ModuleEvent for identifying a module for adding, updating or deleting.
// The name is used to minimally render the module in the UI.
//
// Additional attributes can be added to the module. They will show up in the module View if they have a corresponding ColumnDescriptor.
//
// To avoid an unnecessary proliferation of additional attributes with similar semantics but different names
// we recommend to re-use attributes from the 'recommended' list below first, and only introduce new attributes if nothing appropriate could be found.
type Module struct {
	Id             interface{} `json:"id"`
	Name           string      `json:"name"`
	Path           string      `json:"path,omitempty"`
	IsOptimized    bool        `json:"isOptimized,omitempty"`
	IsUserCode     bool        `json:"isUserCode,omitempty"`
	Version        string      `json:"version,omitempty"`
	SymbolStatus   string      `json:"symbolStatus,omitempty"`
	SymbolFilePath string      `json:"symbolFilePath,omitempty"`
	DateTimeStamp  string      `json:"dateTimeStamp,omitempty"`
	AddressRange   string      `json:"addressRange,omitempty"`
}

// ColumnDescriptor: A ColumnDescriptor specifies what module attribute to show in a column of the ModulesView, how to format it, and what the column's label should be.
// It is only used if the underlying UI actually supports this level of customization.
type ColumnDescriptor struct {
	AttributeName string `json:"attributeName"`
	Label         string `json:"label"`
	Format        string `json:"format,omitempty"`
	Type          string `json:"type,omitempty"`
	Width         int    `json:"width,omitempty"`
}

// ModulesViewDescriptor: The ModulesViewDescriptor is the container for all declarative configuration options of a ModuleView.
// For now it only specifies the columns to be shown in the modules view.
type ModulesViewDescriptor struct {
	Columns []ColumnDescriptor `json:"columns"`
}

// Thread: A Thread
type Thread struct {
	Id   int    `json:"id"`
	Name string `json:"name"`
}

// Source: A Source is a descriptor for source code. It is returned from the debug adapter as part of a StackFrame and it is used by clients when specifying breakpoints.
type Source struct {
	Name             string      `json:"name,omitempty"`
	Path             string      `json:"path,omitempty"`
	SourceReference  int         `json:"sourceReference,omitempty"`
	PresentationHint string      `json:"presentationHint,omitempty"`
	Origin           string      `json:"origin,omitempty"`
	Sources          []Source    `json:"sources,omitempty"`
	AdapterData      interface{} `json:"adapterData,omitempty"`
	Checksums        []Checksum  `json:"checksums,omitempty"`
}

// StackFrame: A Stackframe contains the source location.
type StackFrame struct {
	Id                          int         `json:"id"`
	Name                        string      `json:"name"`
	Source                      Source      `json:"source,omitempty"`
	Line                        int         `json:"line"`
	Column                      int         `json:"column"`
	EndLine                     int         `json:"endLine,omitempty"`
	EndColumn                   int         `json:"endColumn,omitempty"`
	InstructionPointerReference string      `json:"instructionPointerReference,omitempty"`
	ModuleId                    interface{} `json:"moduleId,omitempty"`
	PresentationHint            string      `json:"presentationHint,omitempty"`
}

// Scope: A Scope is a named container for variables. Optionally a scope can map to a source or a range within a source.
type Scope struct {
	Name               string `json:"name"`
	PresentationHint   string `json:"presentationHint,omitempty"`
	VariablesReference int    `json:"variablesReference"`
	NamedVariables     int    `json:"namedVariables,omitempty"`
	IndexedVariables   int    `json:"indexedVariables,omitempty"`
	Expensive          bool   `json:"expensive"`
	Source             Source `json:"source,omitempty"`
	Line               int    `json:"line,omitempty"`
	Column             int    `json:"column,omitempty"`
	EndLine            int    `json:"endLine,omitempty"`
	EndColumn          int    `json:"endColumn,omitempty"`
}

// Variable: A Variable is a name/value pair.
// Optionally a variable can have a 'type' that is shown if space permits or when hovering over the variable's name.
// An optional 'kind' is used to render additional properties of the variable, e.g. different icons can be used to indicate that a variable is public or private.
// If the value is structured (has children), a handle is provided to retrieve the children with the VariablesRequest.
// If the number of named or indexed children is large, the numbers should be returned via the optional 'namedVariables' and 'indexedVariables' attributes.
// The client can use this optional information to present the children in a paged UI and fetch them in chunks.
type Variable struct {
	Name               string                   `json:"name"`
	Value              string                   `json:"value"`
	Type               string                   `json:"type,omitempty"`
	PresentationHint   VariablePresentationHint `json:"presentationHint,omitempty"`
	EvaluateName       string                   `json:"evaluateName,omitempty"`
	VariablesReference int                      `json:"variablesReference"`
	NamedVariables     int                      `json:"namedVariables,omitempty"`
	IndexedVariables   int                      `json:"indexedVariables,omitempty"`
	MemoryReference    string                   `json:"memoryReference,omitempty"`
}

// VariablePresentationHint: Optional properties of a variable that can be used to determine how to render the variable in the UI.
type VariablePresentationHint struct {
	Kind       string   `json:"kind,omitempty"`
	Attributes []string `json:"attributes,omitempty"`
	Visibility string   `json:"visibility,omitempty"`
}

// BreakpointLocation: Properties of a breakpoint location returned from the 'breakpointLocations' request.
type BreakpointLocation struct {
	Line      int `json:"line"`
	Column    int `json:"column,omitempty"`
	EndLine   int `json:"endLine,omitempty"`
	EndColumn int `json:"endColumn,omitempty"`
}

// SourceBreakpoint: Properties of a breakpoint or logpoint passed to the setBreakpoints request.
type SourceBreakpoint struct {
	Line         int    `json:"line"`
	Column       int    `json:"column,omitempty"`
	Condition    string `json:"condition,omitempty"`
	HitCondition string `json:"hitCondition,omitempty"`
	LogMessage   string `json:"logMessage,omitempty"`
}

// FunctionBreakpoint: Properties of a breakpoint passed to the setFunctionBreakpoints request.
type FunctionBreakpoint struct {
	Name         string `json:"name"`
	Condition    string `json:"condition,omitempty"`
	HitCondition string `json:"hitCondition,omitempty"`
}

// DataBreakpointAccessType: This enumeration defines all possible access types for data breakpoints.
type DataBreakpointAccessType string

// DataBreakpoint: Properties of a data breakpoint passed to the setDataBreakpoints request.
type DataBreakpoint struct {
	DataId       string                   `json:"dataId"`
	AccessType   DataBreakpointAccessType `json:"accessType,omitempty"`
	Condition    string                   `json:"condition,omitempty"`
	HitCondition string                   `json:"hitCondition,omitempty"`
}

// Breakpoint: Information about a Breakpoint created in setBreakpoints or setFunctionBreakpoints.
type Breakpoint struct {
	Id        int    `json:"id,omitempty"`
	Verified  bool   `json:"verified"`
	Message   string `json:"message,omitempty"`
	Source    Source `json:"source,omitempty"`
	Line      int    `json:"line,omitempty"`
	Column    int    `json:"column,omitempty"`
	EndLine   int    `json:"endLine,omitempty"`
	EndColumn int    `json:"endColumn,omitempty"`
}

// StepInTarget: A StepInTarget can be used in the 'stepIn' request and determines into which single target the stepIn request should step.
type StepInTarget struct {
	Id    int    `json:"id"`
	Label string `json:"label"`
}

// GotoTarget: A GotoTarget describes a code location that can be used as a target in the 'goto' request.
// The possible goto targets can be determined via the 'gotoTargets' request.
type GotoTarget struct {
	Id                          int    `json:"id"`
	Label                       string `json:"label"`
	Line                        int    `json:"line"`
	Column                      int    `json:"column,omitempty"`
	EndLine                     int    `json:"endLine,omitempty"`
	EndColumn                   int    `json:"endColumn,omitempty"`
	InstructionPointerReference string `json:"instructionPointerReference,omitempty"`
}

// CompletionItem: CompletionItems are the suggestions returned from the CompletionsRequest.
type CompletionItem struct {
	Label    string             `json:"label"`
	Text     string             `json:"text,omitempty"`
	SortText string             `json:"sortText,omitempty"`
	Type     CompletionItemType `json:"type,omitempty"`
	Start    int                `json:"start,omitempty"`
	Length   int                `json:"length,omitempty"`
}

// CompletionItemType: Some predefined types for the CompletionItem. Please note that not all clients have specific icons for all of them.
type CompletionItemType string

// ChecksumAlgorithm: Names of checksum algorithms that may be supported by a debug adapter.
type ChecksumAlgorithm string

// Checksum: The checksum of an item calculated by the specified algorithm.
type Checksum struct {
	Algorithm ChecksumAlgorithm `json:"algorithm"`
	Checksum  string            `json:"checksum"`
}

// ValueFormat: Provides formatting information for a value.
type ValueFormat struct {
	Hex bool `json:"hex,omitempty"`
}

// StackFrameFormat: Provides formatting information for a stack frame.
type StackFrameFormat struct {
	ValueFormat

	Parameters      bool `json:"parameters,omitempty"`
	ParameterTypes  bool `json:"parameterTypes,omitempty"`
	ParameterNames  bool `json:"parameterNames,omitempty"`
	ParameterValues bool `json:"parameterValues,omitempty"`
	Line            bool `json:"line,omitempty"`
	Module          bool `json:"module,omitempty"`
	IncludeAll      bool `json:"includeAll,omitempty"`
}

// ExceptionOptions: An ExceptionOptions assigns configuration options to a set of exceptions.
type ExceptionOptions struct {
	Path      []ExceptionPathSegment `json:"path,omitempty"`
	BreakMode ExceptionBreakMode     `json:"breakMode"`
}

// ExceptionBreakMode: This enumeration defines all possible conditions when a thrown exception should result in a break.
// never: never breaks,
// always: always breaks,
// unhandled: breaks when exception unhandled,
// userUnhandled: breaks if the exception is not handled by user code.
type ExceptionBreakMode string

// ExceptionPathSegment: An ExceptionPathSegment represents a segment in a path that is used to match leafs or nodes in a tree of exceptions. If a segment consists of more than one name, it matches the names provided if 'negate' is false or missing or it matches anything except the names provided if 'negate' is true.
type ExceptionPathSegment struct {
	Negate bool     `json:"negate,omitempty"`
	Names  []string `json:"names"`
}

// ExceptionDetails: Detailed information about an exception that has occurred.
type ExceptionDetails struct {
	Message        string             `json:"message,omitempty"`
	TypeName       string             `json:"typeName,omitempty"`
	FullTypeName   string             `json:"fullTypeName,omitempty"`
	EvaluateName   string             `json:"evaluateName,omitempty"`
	StackTrace     string             `json:"stackTrace,omitempty"`
	InnerException []ExceptionDetails `json:"innerException,omitempty"`
}

// DisassembledInstruction: Represents a single disassembled instruction.
type DisassembledInstruction struct {
	Address          string `json:"address"`
	InstructionBytes string `json:"instructionBytes,omitempty"`
	Instruction      string `json:"instruction"`
	Symbol           string `json:"symbol,omitempty"`
	Location         Source `json:"location,omitempty"`
	Line             int    `json:"line,omitempty"`
	Column           int    `json:"column,omitempty"`
	EndLine          int    `json:"endLine,omitempty"`
	EndColumn        int    `json:"endColumn,omitempty"`
}

func (*ProtocolMessage) isMessage()                 {}
func (*Request) isMessage()                         {}
func (*Event) isMessage()                           {}
func (*Response) isMessage()                        {}
func (*ErrorResponse) isMessage()                   {}
func (*CancelRequest) isMessage()                   {}
func (*CancelResponse) isMessage()                  {}
func (*InitializedEvent) isMessage()                {}
func (*StoppedEvent) isMessage()                    {}
func (*ContinuedEvent) isMessage()                  {}
func (*ExitedEvent) isMessage()                     {}
func (*TerminatedEvent) isMessage()                 {}
func (*ThreadEvent) isMessage()                     {}
func (*OutputEvent) isMessage()                     {}
func (*BreakpointEvent) isMessage()                 {}
func (*ModuleEvent) isMessage()                     {}
func (*LoadedSourceEvent) isMessage()               {}
func (*ProcessEvent) isMessage()                    {}
func (*CapabilitiesEvent) isMessage()               {}
func (*RunInTerminalRequest) isMessage()            {}
func (*RunInTerminalResponse) isMessage()           {}
func (*InitializeRequest) isMessage()               {}
func (*InitializeResponse) isMessage()              {}
func (*ConfigurationDoneRequest) isMessage()        {}
func (*ConfigurationDoneResponse) isMessage()       {}
func (*LaunchRequest) isMessage()                   {}
func (*LaunchResponse) isMessage()                  {}
func (*AttachRequest) isMessage()                   {}
func (*AttachResponse) isMessage()                  {}
func (*RestartRequest) isMessage()                  {}
func (*RestartResponse) isMessage()                 {}
func (*DisconnectRequest) isMessage()               {}
func (*DisconnectResponse) isMessage()              {}
func (*TerminateRequest) isMessage()                {}
func (*TerminateResponse) isMessage()               {}
func (*BreakpointLocationsRequest) isMessage()      {}
func (*BreakpointLocationsResponse) isMessage()     {}
func (*SetBreakpointsRequest) isMessage()           {}
func (*SetBreakpointsResponse) isMessage()          {}
func (*SetFunctionBreakpointsRequest) isMessage()   {}
func (*SetFunctionBreakpointsResponse) isMessage()  {}
func (*SetExceptionBreakpointsRequest) isMessage()  {}
func (*SetExceptionBreakpointsResponse) isMessage() {}
func (*DataBreakpointInfoRequest) isMessage()       {}
func (*DataBreakpointInfoResponse) isMessage()      {}
func (*SetDataBreakpointsRequest) isMessage()       {}
func (*SetDataBreakpointsResponse) isMessage()      {}
func (*ContinueRequest) isMessage()                 {}
func (*ContinueResponse) isMessage()                {}
func (*NextRequest) isMessage()                     {}
func (*NextResponse) isMessage()                    {}
func (*StepInRequest) isMessage()                   {}
func (*StepInResponse) isMessage()                  {}
func (*StepOutRequest) isMessage()                  {}
func (*StepOutResponse) isMessage()                 {}
func (*StepBackRequest) isMessage()                 {}
func (*StepBackResponse) isMessage()                {}
func (*ReverseContinueRequest) isMessage()          {}
func (*ReverseContinueResponse) isMessage()         {}
func (*RestartFrameRequest) isMessage()             {}
func (*RestartFrameResponse) isMessage()            {}
func (*GotoRequest) isMessage()                     {}
func (*GotoResponse) isMessage()                    {}
func (*PauseRequest) isMessage()                    {}
func (*PauseResponse) isMessage()                   {}
func (*StackTraceRequest) isMessage()               {}
func (*StackTraceResponse) isMessage()              {}
func (*ScopesRequest) isMessage()                   {}
func (*ScopesResponse) isMessage()                  {}
func (*VariablesRequest) isMessage()                {}
func (*VariablesResponse) isMessage()               {}
func (*SetVariableRequest) isMessage()              {}
func (*SetVariableResponse) isMessage()             {}
func (*SourceRequest) isMessage()                   {}
func (*SourceResponse) isMessage()                  {}
func (*ThreadsRequest) isMessage()                  {}
func (*ThreadsResponse) isMessage()                 {}
func (*TerminateThreadsRequest) isMessage()         {}
func (*TerminateThreadsResponse) isMessage()        {}
func (*ModulesRequest) isMessage()                  {}
func (*ModulesResponse) isMessage()                 {}
func (*LoadedSourcesRequest) isMessage()            {}
func (*LoadedSourcesResponse) isMessage()           {}
func (*EvaluateRequest) isMessage()                 {}
func (*EvaluateResponse) isMessage()                {}
func (*SetExpressionRequest) isMessage()            {}
func (*SetExpressionResponse) isMessage()           {}
func (*StepInTargetsRequest) isMessage()            {}
func (*StepInTargetsResponse) isMessage()           {}
func (*GotoTargetsRequest) isMessage()              {}
func (*GotoTargetsResponse) isMessage()             {}
func (*CompletionsRequest) isMessage()              {}
func (*CompletionsResponse) isMessage()             {}
func (*ExceptionInfoRequest) isMessage()            {}
func (*ExceptionInfoResponse) isMessage()           {}
func (*ReadMemoryRequest) isMessage()               {}
func (*ReadMemoryResponse) isMessage()              {}
func (*DisassembleRequest) isMessage()              {}
func (*DisassembleResponse) isMessage()             {}
