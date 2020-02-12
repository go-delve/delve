// Copyright 2019 Google LLC
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

// This file contains utilities for decoding JSON-encoded bytes into DAP message.

package dap

import (
	"encoding/json"
	"fmt"
)

// DecodeProtocolMessageFieldError describes which JSON attribute
// has an unsupported value that the decoding cannot handle.
type DecodeProtocolMessageFieldError struct {
	SubType    string
	FieldName  string
	FieldValue string
}

func (e *DecodeProtocolMessageFieldError) Error() string {
	return fmt.Sprintf("%s %s '%s' is not supported", e.SubType, e.FieldName, e.FieldValue)
}

// DecodeProtocolMessage parses the JSON-encoded data and returns the result of
// the appropriate type within the ProtocolMessage hierarchy. If message type,
// command, etc cannot be cast, returns DecodeProtocolMessageFieldError.
// See also godoc for json.Unmarshal, which is used for underlying decoding.
func DecodeProtocolMessage(data []byte) (Message, error) {
	var protomsg ProtocolMessage
	if err := json.Unmarshal(data, &protomsg); err != nil {
		return nil, err
	}
	switch protomsg.Type {
	case "request":
		return decodeRequest(data)
	case "response":
		return decodeResponse(data)
	case "event":
		return decodeEvent(data)
	default:
		return nil, &DecodeProtocolMessageFieldError{"ProtocolMessage", "type", protomsg.Type}
	}
}

type messageCtor func() Message

// decodeRequest determines what request type in the ProtocolMessage hierarchy
// data corresponds to and uses json.Unmarshal to populate the corresponding
// struct to be returned.
func decodeRequest(data []byte) (Message, error) {
	var r Request
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	if ctor, ok := requestCtor[r.Command]; ok {
		requestPtr := ctor()
		err := json.Unmarshal(data, requestPtr)
		return requestPtr, err
	}
	return nil, &DecodeProtocolMessageFieldError{"Request", "command", r.Command}
}

// Mapping of request commands and corresponding struct constructors that
// can be passed to json.Unmarshal.
var requestCtor = map[string]messageCtor{
	"cancel":                  func() Message { return &CancelRequest{} },
	"runInTerminal":           func() Message { return &RunInTerminalRequest{} },
	"initialize":              func() Message { return &InitializeRequest{} },
	"configurationDone":       func() Message { return &ConfigurationDoneRequest{} },
	"launch":                  func() Message { return &LaunchRequest{} },
	"attach":                  func() Message { return &AttachRequest{} },
	"restart":                 func() Message { return &RestartRequest{} },
	"disconnect":              func() Message { return &DisconnectRequest{} },
	"terminate":               func() Message { return &TerminateRequest{} },
	"breakpointLocations":     func() Message { return &BreakpointLocationsRequest{} },
	"setBreakpoints":          func() Message { return &SetBreakpointsRequest{} },
	"setFunctionBreakpoints":  func() Message { return &SetFunctionBreakpointsRequest{} },
	"setExceptionBreakpoints": func() Message { return &SetExceptionBreakpointsRequest{} },
	"dataBreakpointInfo":      func() Message { return &DataBreakpointInfoRequest{} },
	"setDataBreakpoints":      func() Message { return &SetDataBreakpointsRequest{} },
	"continue":                func() Message { return &ContinueRequest{} },
	"next":                    func() Message { return &NextRequest{} },
	"stepIn":                  func() Message { return &StepInRequest{} },
	"stepOut":                 func() Message { return &StepOutRequest{} },
	"stepBack":                func() Message { return &StepBackRequest{} },
	"reverseContinue":         func() Message { return &ReverseContinueRequest{} },
	"restartFrame":            func() Message { return &RestartFrameRequest{} },
	"goto":                    func() Message { return &GotoRequest{} },
	"pause":                   func() Message { return &PauseRequest{} },
	"stackTrace":              func() Message { return &StackTraceRequest{} },
	"scopes":                  func() Message { return &ScopesRequest{} },
	"variables":               func() Message { return &VariablesRequest{} },
	"setVariable":             func() Message { return &SetVariableRequest{} },
	"source":                  func() Message { return &SourceRequest{} },
	"threads":                 func() Message { return &ThreadsRequest{} },
	"terminateThreads":        func() Message { return &TerminateThreadsRequest{} },
	"modules":                 func() Message { return &ModulesRequest{} },
	"loadedSources":           func() Message { return &LoadedSourcesRequest{} },
	"evaluate":                func() Message { return &EvaluateRequest{} },
	"setExpression":           func() Message { return &SetExpressionRequest{} },
	"stepInTargets":           func() Message { return &StepInTargetsRequest{} },
	"gotoTargets":             func() Message { return &GotoTargetsRequest{} },
	"completions":             func() Message { return &CompletionsRequest{} },
	"exceptionInfo":           func() Message { return &ExceptionInfoRequest{} },
	"readMemory":              func() Message { return &ReadMemoryRequest{} },
	"disassemble":             func() Message { return &DisassembleRequest{} },
}

// decodeResponse determines what response type in the ProtocolMessage hierarchy
// data corresponds to and uses json.Unmarshal to populate the corresponding
// struct to be returned.
func decodeResponse(data []byte) (Message, error) {
	var r Response
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	if !r.Success {
		var er ErrorResponse
		err := json.Unmarshal(data, &er)
		return &er, err
	}
	if ctor, ok := responseCtor[r.Command]; ok {
		responsePtr := ctor()
		err := json.Unmarshal(data, responsePtr)
		return responsePtr, err
	}
	return nil, &DecodeProtocolMessageFieldError{"Response", "command", r.Command}
}

// Mapping of response commands and corresponding struct constructors that
// can be passed to json.Unmarshal.
var responseCtor = map[string]messageCtor{
	"cancel":                  func() Message { return &CancelResponse{} },
	"runInTerminal":           func() Message { return &RunInTerminalResponse{} },
	"initialize":              func() Message { return &InitializeResponse{} },
	"configurationDone":       func() Message { return &ConfigurationDoneResponse{} },
	"launch":                  func() Message { return &LaunchResponse{} },
	"attach":                  func() Message { return &AttachResponse{} },
	"restart":                 func() Message { return &RestartResponse{} },
	"disconnect":              func() Message { return &DisconnectResponse{} },
	"terminate":               func() Message { return &TerminateResponse{} },
	"breakpointLocations":     func() Message { return &BreakpointLocationsResponse{} },
	"setBreakpoints":          func() Message { return &SetBreakpointsResponse{} },
	"setFunctionBreakpoints":  func() Message { return &SetFunctionBreakpointsResponse{} },
	"setExceptionBreakpoints": func() Message { return &SetExceptionBreakpointsResponse{} },
	"dataBreakpointInfo":      func() Message { return &DataBreakpointInfoResponse{} },
	"setDataBreakpoints":      func() Message { return &SetDataBreakpointsResponse{} },
	"continue":                func() Message { return &ContinueResponse{} },
	"next":                    func() Message { return &NextResponse{} },
	"stepIn":                  func() Message { return &StepInResponse{} },
	"stepOut":                 func() Message { return &StepOutResponse{} },
	"stepBack":                func() Message { return &StepBackResponse{} },
	"reverseContinue":         func() Message { return &ReverseContinueResponse{} },
	"restartFrame":            func() Message { return &RestartFrameResponse{} },
	"goto":                    func() Message { return &GotoResponse{} },
	"pause":                   func() Message { return &PauseResponse{} },
	"stackTrace":              func() Message { return &StackTraceResponse{} },
	"scopes":                  func() Message { return &ScopesResponse{} },
	"variables":               func() Message { return &VariablesResponse{} },
	"setVariable":             func() Message { return &SetVariableResponse{} },
	"source":                  func() Message { return &SourceResponse{} },
	"threads":                 func() Message { return &ThreadsResponse{} },
	"terminateThreads":        func() Message { return &TerminateThreadsResponse{} },
	"modules":                 func() Message { return &ModulesResponse{} },
	"loadedSources":           func() Message { return &LoadedSourcesResponse{} },
	"evaluate":                func() Message { return &EvaluateResponse{} },
	"setExpression":           func() Message { return &SetExpressionResponse{} },
	"stepInTargets":           func() Message { return &StepInTargetsResponse{} },
	"gotoTargets":             func() Message { return &GotoTargetsResponse{} },
	"completions":             func() Message { return &CompletionsResponse{} },
	"exceptionInfo":           func() Message { return &ExceptionInfoResponse{} },
	"readMemory":              func() Message { return &ReadMemoryResponse{} },
	"disassemble":             func() Message { return &DisassembleResponse{} },
}

// decodeEvent determines what event type in the ProtocolMessage hierarchy
// data corresponds to and uses json.Unmarshal to populate the corresponding
// struct to be returned.
func decodeEvent(data []byte) (Message, error) {
	var e Event
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, err
	}
	if ctor, ok := eventCtor[e.Event]; ok {
		eventPtr := ctor()
		err := json.Unmarshal(data, eventPtr)
		return eventPtr, err
	}
	return nil, &DecodeProtocolMessageFieldError{"Event", "event", e.Event}
}

// Mapping of event ids and corresponding struct constructors that
// can be passed to json.Unmarshal.
var eventCtor = map[string]messageCtor{
	"initialized":  func() Message { return &InitializedEvent{} },
	"stopped":      func() Message { return &StoppedEvent{} },
	"continued":    func() Message { return &ContinuedEvent{} },
	"exited":       func() Message { return &ExitedEvent{} },
	"terminated":   func() Message { return &TerminatedEvent{} },
	"thread":       func() Message { return &ThreadEvent{} },
	"output":       func() Message { return &OutputEvent{} },
	"breakpoint":   func() Message { return &BreakpointEvent{} },
	"module":       func() Message { return &ModuleEvent{} },
	"loadedSource": func() Message { return &LoadedSourceEvent{} },
	"process":      func() Message { return &ProcessEvent{} },
	"capabilities": func() Message { return &CapabilitiesEvent{} },
}
