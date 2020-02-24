package dap

import (
	"flag"
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

func TestStopOnEntry(t *testing.T) {
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		// This test exhaustively tests Seq and RequestSeq on all messages from the
		// server. Other tests shouldn't necessarily repeat these checks.
		client.InitializeRequest()
		initResp := client.ExpectInitializeResponse(t)
		if initResp.Seq != 0 || initResp.RequestSeq != 0 {
			t.Errorf("got %#v, want Seq=0, RequestSeq=0", initResp)
		}

		client.LaunchRequest(fixture.Path, true /*stopOnEntry*/)
		initEv := client.ExpectInitializedEvent(t)
		if initEv.Seq != 0 {
			t.Errorf("got %#v, want Seq=0", initEv)
		}

		launchResp := client.ExpectLaunchResponse(t)
		if launchResp.Seq != 0 || launchResp.RequestSeq != 1 {
			t.Errorf("got %#v, want Seq=0, RequestSeq=1", launchResp)
		}

		client.SetExceptionBreakpointsRequest()
		sResp := client.ExpectSetExceptionBreakpointsResponse(t)
		if sResp.Seq != 0 || sResp.RequestSeq != 2 {
			t.Errorf("got %#v, want Seq=0, RequestSeq=2", sResp)
		}

		client.ConfigurationDoneRequest()
		stopEvent := client.ExpectStoppedEvent(t)
		if stopEvent.Seq != 0 ||
			stopEvent.Body.Reason != "breakpoint" ||
			stopEvent.Body.ThreadId != 1 ||
			!stopEvent.Body.AllThreadsStopped {
			t.Errorf("got %#v, want Seq=0, Body={Reason=\"breakpoint\", ThreadId=1, AllThreadsStopped=true}", stopEvent)
		}

		cdResp := client.ExpectConfigurationDoneResponse(t)
		if cdResp.Seq != 0 || cdResp.RequestSeq != 3 {
			t.Errorf("got %#v, want Seq=0, RequestSeq=3", cdResp)
		}

		client.ContinueRequest(1)
		contResp := client.ExpectContinueResponse(t)
		if contResp.Seq != 0 || contResp.RequestSeq != 4 {
			t.Errorf("got %#v, want Seq=0, RequestSeq=4", contResp)
		}

		termEv := client.ExpectTerminatedEvent(t)
		if termEv.Seq != 0 {
			t.Errorf("got %#v, want Seq=0", termEv)
		}

		client.DisconnectRequest()
		dResp := client.ExpectDisconnectResponse(t)
		if dResp.Seq != 0 || dResp.RequestSeq != 5 {
			t.Errorf("got %#v, want Seq=0, RequestSeq=5", dResp)
		}
	})
}

func TestSetBreakpoint(t *testing.T) {
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		client.InitializeRequest()
		client.ExpectInitializeResponse(t)

		client.LaunchRequest(fixture.Path, false /*stopOnEntry*/)
		client.ExpectInitializedEvent(t)
		launchResp := client.ExpectLaunchResponse(t)
		if launchResp.RequestSeq != 1 {
			t.Errorf("got %#v, want RequestSeq=1", launchResp)
		}

		client.SetBreakpointsRequest(fixture.Source, []int{8, 100})
		sResp := client.ExpectSetBreakpointsResponse(t)
		if len(sResp.Body.Breakpoints) != 1 {
			t.Errorf("got %#v, want len(Breakpoints)=1", sResp)
		}
		bkpt0 := sResp.Body.Breakpoints[0]
		if !bkpt0.Verified || bkpt0.Line != 8 {
			t.Errorf("got breakpoints[0] = %#v, want Verified=true, Line=8", bkpt0)
		}

		client.SetExceptionBreakpointsRequest()
		client.ExpectSetExceptionBreakpointsResponse(t)

		client.ConfigurationDoneRequest()
		cdResp := client.ExpectConfigurationDoneResponse(t)
		if cdResp.RequestSeq != 4 {
			t.Errorf("got %#v, want RequestSeq=4", cdResp)
		}

		client.ContinueRequest(1)
		stopEvent1 := client.ExpectStoppedEvent(t)
		if stopEvent1.Body.Reason != "breakpoint" ||
			stopEvent1.Body.ThreadId != 1 ||
			!stopEvent1.Body.AllThreadsStopped {
			t.Errorf("got %#v, want Body={Reason=\"breakpoint\", ThreadId=1, AllThreadsStopped=true}", stopEvent1)
		}
		client.ExpectContinueResponse(t)

		client.ContinueRequest(1)
		client.ExpectTerminatedEvent(t)
		client.ExpectContinueResponse(t)

		client.DisconnectRequest()
		client.ExpectDisconnectResponse(t)
	})
}

func TestBadLaunchRequests(t *testing.T) {
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		client.LaunchRequest("", true)

		expectFailedToLaunch := func(response *dap.ErrorResponse, seq int) {
			t.Helper()
			if response.RequestSeq != seq {
				t.Errorf("RequestSeq got %d, want %d", seq, response.RequestSeq)
			}
			if response.Command != "launch" {
				t.Errorf("Command got %q, want \"launch\"", response.Command)
			}
			if response.Message != "Failed to launch" {
				t.Errorf("Message got %q, want \"Failed to launch\"", response.Message)
			}
			if response.Body.Error.Id != 3000 {
				t.Errorf("Id got %d, want 3000", response.Body.Error.Id)
			}
		}

		resp := client.ExpectErrorResponse(t)
		expectFailedToLaunch(resp, 0)
		// Test for the DAP-specific detailed error message.
		wantErrorFormat := "Failed to launch: The program attribute is missing in debug configuration."
		if resp.Body.Error.Format != wantErrorFormat {
			t.Errorf("got %q, want %q", resp.Body.Error.Format, wantErrorFormat)
		}

		// Skip detailed message checks for potentially different OS-specific errors.
		client.LaunchRequest(fixture.Path+"_does_not_exist", false)
		expectFailedToLaunch(client.ExpectErrorResponse(t), 1)

		client.LaunchRequest(fixture.Source, true) // Not an executable
		expectFailedToLaunch(client.ExpectErrorResponse(t), 2)

		// We failed to launch the program. Make sure shutdown still works.
		client.DisconnectRequest()
		dresp := client.ExpectDisconnectResponse(t)
		if dresp.RequestSeq != 3 {
			t.Errorf("got %#v, want RequestSeq=3", dresp)
		}
	})
}
