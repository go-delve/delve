package dap

import (
	"bytes"
	"flag"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/go-delve/delve/pkg/logflags"
	protest "github.com/go-delve/delve/pkg/proc/test"
	"github.com/go-delve/delve/service"
	"github.com/go-delve/delve/service/dap/daptest"
	"github.com/google/go-dap"
)

func TestMain(m *testing.M) {
	var logOutput string
	flag.StringVar(&logOutput, "log-output", "", "configures log output")
	flag.Parse()
	logflags.Setup(logOutput != "", logOutput, "")
	os.Exit(protest.RunTestsWithFixtures(m))
}

func startDAPServer(t *testing.T) (server *Server, addr string) {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	server = NewServer(&service.Config{
		Listener:       listener,
		Backend:        "default",
		DisconnectChan: nil,
	})
	server.Run()
	// Give server time to start listening for clients
	time.Sleep(100 * time.Millisecond)
	return server, listener.Addr().String()
}

func expectMessage(t *testing.T, client *daptest.Client, want []byte) {
	t.Helper()
	got, err := client.ReadBaseMessage()
	if err != nil {
		t.Error(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("\ngot  %q\nwant %q", got, want)
	}
}

// name is for _fixtures/<name>.go
func runTest(t *testing.T, name string, test func(c *daptest.Client, f protest.Fixture)) {
	var buildFlags protest.BuildFlags
	fixture := protest.BuildFixture(name, buildFlags)

	server, addr := startDAPServer(t)
	defer server.Stop()
	client := daptest.NewClient(addr)
	defer client.Close()

	test(client, fixture)
}

// TODO(polina): instead of hardcoding message bytes,
// add methods to client to receive, decode and verify responses.

func TestStopOnEntry(t *testing.T) {
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		client.InitializeRequest()
		expectMessage(t, client, []byte(`{"seq":0,"type":"response","request_seq":0,"success":true,"command":"initialize","body":{"supportsConfigurationDoneRequest":true}}`))

		client.LaunchRequest(fixture.Path, true /*stopOnEntry*/)
		expectMessage(t, client, []byte(`{"seq":0,"type":"event","event":"initialized"}`))
		expectMessage(t, client, []byte(`{"seq":0,"type":"response","request_seq":1,"success":true,"command":"launch"}`))

		client.SetExceptionBreakpointsRequest()
		expectMessage(t, client, []byte(`{"seq":0,"type":"response","request_seq":2,"success":true,"command":"setExceptionBreakpoints"}`))

		client.ConfigurationDoneRequest()
		expectMessage(t, client, []byte(`{"seq":0,"type":"event","event":"stopped","body":{"reason":"breakpoint","threadId":1,"allThreadsStopped":true}}`))
		expectMessage(t, client, []byte(`{"seq":0,"type":"response","request_seq":3,"success":true,"command":"configurationDone"}`))

		client.ContinueRequest(1)
		expectMessage(t, client, []byte(`{"seq":0,"type":"response","request_seq":4,"success":true,"command":"continue","body":{}}`))
		expectMessage(t, client, []byte(`{"seq":0,"type":"event","event":"terminated","body":{}}`))

		client.DisconnectRequest()
		expectMessage(t, client, []byte(`{"seq":0,"type":"response","request_seq":5,"success":true,"command":"disconnect"}`))
	})
}

func TestSetBreakpoint(t *testing.T) {
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		client.InitializeRequest()
		expectMessage(t, client, []byte(`{"seq":0,"type":"response","request_seq":0,"success":true,"command":"initialize","body":{"supportsConfigurationDoneRequest":true}}`))

		client.LaunchRequest(fixture.Path, false /*stopOnEntry*/)
		expectMessage(t, client, []byte(`{"seq":0,"type":"event","event":"initialized"}`))
		expectMessage(t, client, []byte(`{"seq":0,"type":"response","request_seq":1,"success":true,"command":"launch"}`))

		client.SetBreakpointsRequest(fixture.Source, []int{8, 100})
		expectMessage(t, client, []byte(`{"seq":0,"type":"response","request_seq":2,"success":true,"command":"setBreakpoints","body":{"breakpoints":[{"verified":true,"source":{},"line":8}]}}`))

		client.SetExceptionBreakpointsRequest()
		expectMessage(t, client, []byte(`{"seq":0,"type":"response","request_seq":3,"success":true,"command":"setExceptionBreakpoints"}`))

		client.ConfigurationDoneRequest()
		expectMessage(t, client, []byte(`{"seq":0,"type":"response","request_seq":4,"success":true,"command":"configurationDone"}`))

		client.ContinueRequest(1)
		expectMessage(t, client, []byte(`{"seq":0,"type":"event","event":"stopped","body":{"reason":"breakpoint","threadId":1,"allThreadsStopped":true}}`))
		expectMessage(t, client, []byte(`{"seq":0,"type":"response","request_seq":5,"success":true,"command":"continue","body":{}}`))

		client.ContinueRequest(1)
		expectMessage(t, client, []byte(`{"seq":0,"type":"event","event":"terminated","body":{}}`))
		expectMessage(t, client, []byte(`{"seq":0,"type":"response","request_seq":6,"success":true,"command":"continue","body":{}}`))

		client.DisconnectRequest()
		expectMessage(t, client, []byte(`{"seq":0,"type":"response","request_seq":7,"success":true,"command":"disconnect"}`))
	})
}

func expectErrorResponse(t *testing.T, client *daptest.Client, requestSeq int, command string, message string, id int) *dap.ErrorResponse {
	t.Helper()
	response, err := client.ReadErrorResponse()
	if err != nil {
		t.Error(err)
		return nil
	}
	got := response.(*dap.ErrorResponse)
	if got.RequestSeq != requestSeq || got.Command != command || got.Message != message || got.Body.Error.Id != id {
		want := fmt.Sprintf("{RequestSeq: %d, Command: %q, Message: %q, Id: %d}", requestSeq, command, message, id)
		t.Errorf("\ngot  %#v\nwant %s", got, want)
		return nil
	}
	return got
}

func TestBadLaunchRequests(t *testing.T) {
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		client.LaunchRequest("", true)
		// Test for the DAP-specific detailed error message.
		want := "Failed to launch: The program attribute is missing in debug configuration."
		if got := expectErrorResponse(t, client, 0, "launch", "Failed to launch", 3000); got != nil && got.Body.Error.Format != want {
			t.Errorf("got %q, want %q", got.Body.Error.Format, want)
		}

		// Skip detailed message checks for potentially different OS-specific errors.
		client.LaunchRequest(fixture.Path+"_does_not_exist", false)
		expectErrorResponse(t, client, 1, "launch", "Failed to launch", 3000)

		client.LaunchRequest(fixture.Source, true) // Not an executable
		expectErrorResponse(t, client, 2, "launch", "Failed to launch", 3000)

		// We failed to launch the program. Make sure shutdown still works.
		client.DisconnectRequest()
		expectMessage(t, client, []byte(`{"seq":0,"type":"response","request_seq":3,"success":true,"command":"disconnect"}`))
	})
}
