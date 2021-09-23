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

// This file contains utilities for decoding JSON-encoded bytes into DAP message.

package dap

import (
	"encoding/json"
	"fmt"
)

// DecodeProtocolMessageFieldError describes which JSON attribute
// has an unsupported value that the decoding cannot handle.
type DecodeProtocolMessageFieldError struct {
	Seq        int
	SubType    string
	FieldName  string
	FieldValue string
}

func (e *DecodeProtocolMessageFieldError) Error() string {
	return fmt.Sprintf("%s %s '%s' is not supported (seq: %d)", e.SubType, e.FieldName, e.FieldValue, e.Seq)
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
		return nil, &DecodeProtocolMessageFieldError{protomsg.GetSeq(), "ProtocolMessage", "type", protomsg.Type}
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
	return nil, &DecodeProtocolMessageFieldError{r.GetSeq(), "Request", "command", r.Command}
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
	return nil, &DecodeProtocolMessageFieldError{r.GetSeq(), "Response", "command", r.Command}
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
	return nil, &DecodeProtocolMessageFieldError{e.GetSeq(), "Event", "event", e.Event}
}
