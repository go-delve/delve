// Package daptest provides a sample client with utilities
// for DAP mode testing.
package daptest

import (
	"bufio"
	"encoding/json"
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
	return c
}

// Close closes the client connection.
func (c *Client) Close() {
	c.conn.Close()
}

func (c *Client) send(request dap.Message) {
	jsonmsg, _ := json.Marshal(request)
	fmt.Println("[client -> server]", string(jsonmsg))
	dap.WriteProtocolMessage(c.conn, request)
}

func (c *Client) ExpectDisconnectResponse(t *testing.T) *dap.DisconnectResponse {
	m, err := dap.ReadProtocolMessage(c.reader)
	if err != nil {
		t.Error(err)
	}
	return m.(*dap.DisconnectResponse)
}

func (c *Client) ExpectErrorResponse(t *testing.T) *dap.ErrorResponse {
	m, err := dap.ReadProtocolMessage(c.reader)
	if err != nil {
		t.Error(err)
	}
	return m.(*dap.ErrorResponse)
}

func (c *Client) ExpectContinueResponse(t *testing.T) *dap.ContinueResponse {
	m, err := dap.ReadProtocolMessage(c.reader)
	if err != nil {
		t.Error(err)
	}
	return m.(*dap.ContinueResponse)
}

func (c *Client) ExpectTerminatedEvent(t *testing.T) *dap.TerminatedEvent {
	m, err := dap.ReadProtocolMessage(c.reader)
	if err != nil {
		t.Error(err)
	}
	return m.(*dap.TerminatedEvent)
}

func (c *Client) ExpectInitializeResponse(t *testing.T) *dap.InitializeResponse {
	m, err := dap.ReadProtocolMessage(c.reader)
	if err != nil {
		t.Error(err)
	}
	initResp := m.(*dap.InitializeResponse)
	if !initResp.Body.SupportsConfigurationDoneRequest {
		t.Errorf("got %#v, want SupportsConfigurationDoneRequest=true", initResp)
	}
	return initResp
}

func (c *Client) ExpectInitializedEvent(t *testing.T) *dap.InitializedEvent {
	m, err := dap.ReadProtocolMessage(c.reader)
	if err != nil {
		t.Error(err)
	}
	return m.(*dap.InitializedEvent)
}

func (c *Client) ExpectLaunchResponse(t *testing.T) *dap.LaunchResponse {
	m, err := dap.ReadProtocolMessage(c.reader)
	if err != nil {
		t.Error(err)
	}
	return m.(*dap.LaunchResponse)
}

func (c *Client) ExpectSetExceptionBreakpointsResponse(t *testing.T) *dap.SetExceptionBreakpointsResponse {
	m, err := dap.ReadProtocolMessage(c.reader)
	if err != nil {
		t.Error(err)
	}
	return m.(*dap.SetExceptionBreakpointsResponse)
}

func (c *Client) ExpectSetBreakpointsResponse(t *testing.T) *dap.SetBreakpointsResponse {
	m, err := dap.ReadProtocolMessage(c.reader)
	if err != nil {
		t.Error(err)
	}
	return m.(*dap.SetBreakpointsResponse)
}

func (c *Client) ExpectStoppedEvent(t *testing.T) *dap.StoppedEvent {
	m, err := dap.ReadProtocolMessage(c.reader)
	if err != nil {
		t.Error(err)
	}
	return m.(*dap.StoppedEvent)
}

func (c *Client) ExpectConfigurationDoneResponse(t *testing.T) *dap.ConfigurationDoneResponse {
	m, err := dap.ReadProtocolMessage(c.reader)
	if err != nil {
		t.Error(err)
	}
	return m.(*dap.ConfigurationDoneResponse)
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

// LaunchRequest sends a 'launch' request.
func (c *Client) LaunchRequest(program string, stopOnEntry bool) {
	request := &dap.LaunchRequest{Request: *c.newRequest("launch")}
	request.Arguments = map[string]interface{}{
		"request":     "launch",
		"mode":        "exec",
		"program":     program,
		"stopOnEntry": stopOnEntry,
	}
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

// UnknownRequest triggers dap.DecodeProtocolMessageFieldError.
func (c *Client) UnkownRequest() {
	request := c.newRequest("unknown")
	c.send(request)
}

// UnkownProtocolMessage triggers dap.DecodeProtocolMessageFieldError.
func (c *Client) UnkownProtocolMessage() {
	m := &dap.ProtocolMessage{}
	m.Seq = -1
	m.Type = "unknown"
	c.send(m)
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
