package dap

import (
	"flag"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-delve/delve/pkg/logflags"
	protest "github.com/go-delve/delve/pkg/proc/test"
	"github.com/go-delve/delve/service"
	"github.com/go-delve/delve/service/dap/daptest"
	"github.com/google/go-dap"
)

const stopOnEntry bool = true

func TestMain(m *testing.M) {
	var logOutput string
	flag.StringVar(&logOutput, "log-output", "", "configures log output")
	flag.Parse()
	logflags.Setup(logOutput != "", logOutput, "")
	os.Exit(protest.RunTestsWithFixtures(m))
}

// name is for _fixtures/<name>.go
func runTest(t *testing.T, name string, test func(c *daptest.Client, f protest.Fixture)) {
	var buildFlags protest.BuildFlags
	fixture := protest.BuildFixture(name, buildFlags)

	// Start the DAP server.
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	disconnectChan := make(chan struct{})
	server := NewServer(&service.Config{
		Listener:       listener,
		Backend:        "default",
		DisconnectChan: disconnectChan,
	})
	server.Run()
	// Give server time to start listening for clients
	time.Sleep(100 * time.Millisecond)

	var stopOnce sync.Once
	// Run a goroutine that stops the server when disconnectChan is signaled.
	// This helps us test that certain events cause the server to stop as
	// expected.
	go func() {
		<-disconnectChan
		stopOnce.Do(func() { server.Stop() })
	}()

	client := daptest.NewClient(listener.Addr().String())
	defer client.Close()

	defer func() {
		stopOnce.Do(func() { server.Stop() })
	}()

	test(client, fixture)
}

// TestStopOnEntry emulates the message exchange that can be observed with
// VS Code for the most basic debug session with "stopOnEntry" enabled:
// - User selects "Start Debugging":  1 >> initialize
//                                 :  1 << initialize
//                                 :  2 >> launch
//                                 :    << initialized event
//                                 :  2 << launch
//                                 :  3 >> setBreakpoints (empty)
//                                 :  3 << setBreakpoints
//                                 :  4 >> setExceptionBreakpoints (empty)
//                                 :  4 << setExceptionBreakpoints
//                                 :  5 >> configurationDone
// - Program stops upon launching  :    << stopped event
//                                 :  5 << configurationDone
//                                 :  6 >> threads
//                                 :  6 << threads (Dummy)
//                                 :  7 >> threads
//                                 :  7 << threads (Dummy)
//                                 :  8 >> stackTrace
//                                 :  8 << stackTrace (Unable to produce stack trace)
//                                 :  9 >> stackTrace
//                                 :  9 << stackTrace (Unable to produce stack trace)
// - User selects "Continue"       : 10 >> continue
//                                 : 10 << continue
// - Program runs to completion    :    << terminated event
//                                 : 11 >> disconnect
//                                 : 11 << disconnect
// This test exhaustively tests Seq and RequestSeq on all messages from the
// server. Other tests do not necessarily need to repeat all these checks.
func TestStopOnEntry(t *testing.T) {
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		// 1 >> initialize, << initialize
		client.InitializeRequest()
		initResp := client.ExpectInitializeResponse(t)
		if initResp.Seq != 0 || initResp.RequestSeq != 1 {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=1", initResp)
		}

		// 2 >> launch, << initialized, << launch
		client.LaunchRequest("exec", fixture.Path, stopOnEntry)
		initEvent := client.ExpectInitializedEvent(t)
		if initEvent.Seq != 0 {
			t.Errorf("\ngot %#v\nwant Seq=0", initEvent)
		}
		launchResp := client.ExpectLaunchResponse(t)
		if launchResp.Seq != 0 || launchResp.RequestSeq != 2 {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=2", launchResp)
		}

		// 3 >> setBreakpoints, << setBreakpoints
		client.SetBreakpointsRequest(fixture.Source, nil)
		sbpResp := client.ExpectSetBreakpointsResponse(t)
		if sbpResp.Seq != 0 || sbpResp.RequestSeq != 3 || len(sbpResp.Body.Breakpoints) != 0 {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=3, len(Breakpoints)=0", sbpResp)
		}

		// 4 >> setExceptionBreakpoints, << setExceptionBreakpoints
		client.SetExceptionBreakpointsRequest()
		sebpResp := client.ExpectSetExceptionBreakpointsResponse(t)
		if sebpResp.Seq != 0 || sebpResp.RequestSeq != 4 {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=4", sebpResp)
		}

		// 5 >> configurationDone, << stopped, << configurationDone
		client.ConfigurationDoneRequest()
		stopEvent := client.ExpectStoppedEvent(t)
		if stopEvent.Seq != 0 ||
			stopEvent.Body.Reason != "entry" ||
			stopEvent.Body.ThreadId != 1 ||
			!stopEvent.Body.AllThreadsStopped {
			t.Errorf("\ngot %#v\nwant Seq=0, Body={Reason=\"entry\", ThreadId=1, AllThreadsStopped=true}", stopEvent)
		}
		cdResp := client.ExpectConfigurationDoneResponse(t)
		if cdResp.Seq != 0 || cdResp.RequestSeq != 5 {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=5", cdResp)
		}

		// 6 >> threads, << threads
		client.ThreadsRequest()
		tResp := client.ExpectThreadsResponse(t)
		if tResp.Seq != 0 || tResp.RequestSeq != 6 || len(tResp.Body.Threads) != 1 {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=6 len(Threads)=1", tResp)
		}
		if tResp.Body.Threads[0].Id != 1 || tResp.Body.Threads[0].Name != "Dummy" {
			t.Errorf("\ngot %#v\nwant Id=1, Name=\"Dummy\"", tResp)
		}

		// 7 >> threads, << threads
		client.ThreadsRequest()
		tResp = client.ExpectThreadsResponse(t)
		if tResp.Seq != 0 || tResp.RequestSeq != 7 || len(tResp.Body.Threads) != 1 {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=7 len(Threads)=1", tResp)
		}

		// 8 >> stackTrace, << stackTrace
		client.StackTraceRequest()
		stResp := client.ExpectErrorResponse(t)
		if stResp.Seq != 0 || stResp.RequestSeq != 8 || stResp.Message != "Unsupported command" {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=8 Message=\"Unsupported command\"", stResp)
		}

		// 9 >> stackTrace, << stackTrace
		client.StackTraceRequest()
		stResp = client.ExpectErrorResponse(t)
		if stResp.Seq != 0 || stResp.RequestSeq != 9 || stResp.Message != "Unsupported command" {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=9 Message=\"Unsupported command\"", stResp)
		}

		// 10 >> continue, << continue, << terminated
		client.ContinueRequest(1)
		contResp := client.ExpectContinueResponse(t)
		if contResp.Seq != 0 || contResp.RequestSeq != 10 {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=10", contResp)
		}
		termEvent := client.ExpectTerminatedEvent(t)
		if termEvent.Seq != 0 {
			t.Errorf("\ngot %#v\nwant Seq=0", termEvent)
		}

		// 11 >> disconnect, << disconnect
		client.DisconnectRequest()
		dResp := client.ExpectDisconnectResponse(t)
		if dResp.Seq != 0 || dResp.RequestSeq != 11 {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=11", dResp)
		}
	})
}

// Like the test above, except the program is configured to continue on entry.
func TestContinueOnEntry(t *testing.T) {
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		// 1 >> initialize, << initialize
		client.InitializeRequest()
		client.ExpectInitializeResponse(t)

		// 2 >> launch, << initialized, << launch
		client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
		client.ExpectInitializedEvent(t)
		client.ExpectLaunchResponse(t)

		// 3 >> setBreakpoints, << setBreakpoints
		client.SetBreakpointsRequest(fixture.Source, nil)
		client.ExpectSetBreakpointsResponse(t)

		// 4 >> setExceptionBreakpoints, << setExceptionBreakpoints
		client.SetExceptionBreakpointsRequest()
		client.ExpectSetExceptionBreakpointsResponse(t)

		// 5 >> configurationDone, << configurationDone
		client.ConfigurationDoneRequest()
		client.ExpectConfigurationDoneResponse(t)
		// "Continue" happens behind the scenes

		// For now continue is blocking and runs until a stop or
		// termination. But once we upgrade the server to be async,
		// a simultaneous threads request can be made while continue
		// is running. Note that vscode-go just keeps track of the
		// continue state and would just return a dummy response
		// without talking to debugger if continue was in progress.
		// TODO(polina): test this once it is possible

		client.ExpectTerminatedEvent(t)

		// It is possible for the program to terminate before the initial
		// threads request is processed.

		// 6 >> threads, << threads
		client.ThreadsRequest()
		tResp := client.ExpectThreadsResponse(t)
		if tResp.Seq != 0 || tResp.RequestSeq != 6 || len(tResp.Body.Threads) != 0 {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=6 len(Threads)=0", tResp)
		}

		// 7 >> disconnect, << disconnect
		client.DisconnectRequest()
		dResp := client.ExpectDisconnectResponse(t)
		if dResp.Seq != 0 || dResp.RequestSeq != 7 {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=7", dResp)
		}
	})
}

// TestSetBreakpoint corresponds to a debug session that is configured to
// continue on entry with a pre-set breakpoint.
func TestSetBreakpoint(t *testing.T) {
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		client.InitializeRequest()
		client.ExpectInitializeResponse(t)

		client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
		client.ExpectInitializedEvent(t)
		client.ExpectLaunchResponse(t)

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
		client.ExpectConfigurationDoneResponse(t)
		// This triggers "continue"

		// TODO(polina): add a no-op threads request
		// with dummy response here once server becomes async
		// to match what happens in VS Code.

		stopEvent1 := client.ExpectStoppedEvent(t)
		if stopEvent1.Body.Reason != "breakpoint" ||
			stopEvent1.Body.ThreadId != 1 ||
			!stopEvent1.Body.AllThreadsStopped {
			t.Errorf("got %#v, want Body={Reason=\"breakpoint\", ThreadId=1, AllThreadsStopped=true}", stopEvent1)
		}

		client.ThreadsRequest()
		tResp := client.ExpectThreadsResponse(t)
		if len(tResp.Body.Threads) < 2 { // 1 main + runtime
			t.Errorf("\ngot  %#v\nwant len(Threads)>1", tResp.Body.Threads)
		}
		// TODO(polina): can we reliably test for these values?
		wantMain := dap.Thread{Id: 1, Name: "main.Increment"}
		wantRuntime := dap.Thread{Id: 2, Name: "runtime.gopark"}
		for _, got := range tResp.Body.Threads {
			if !reflect.DeepEqual(got, wantMain) && !strings.HasPrefix(got.Name, "runtime") {
				t.Errorf("\ngot  %#v\nwant []dap.Thread{%#v, %#v, ...}", tResp.Body.Threads, wantMain, wantRuntime)
			}
		}

		// TODO(polina): add other status checking requests
		// that are not yet supported (stackTrace, scopes, variables)

		client.ContinueRequest(1)
		client.ExpectContinueResponse(t)
		// "Continue" is triggered after the response is sent

		client.ExpectTerminatedEvent(t)
		client.DisconnectRequest()
		client.ExpectDisconnectResponse(t)
	})
}

// runDebugSesion is a helper for executing the standard init and shutdown
// sequences for a program that does not stop on entry
// while specifying unique launch criteria via parameters.
func runDebugSession(t *testing.T, client *daptest.Client, launchRequest func()) {
	client.InitializeRequest()
	client.ExpectInitializeResponse(t)

	launchRequest()
	client.ExpectInitializedEvent(t)
	client.ExpectLaunchResponse(t)

	// Skip no-op setBreakpoints
	// Skip no-op setExceptionBreakpoints

	client.ConfigurationDoneRequest()
	client.ExpectConfigurationDoneResponse(t)

	// Program automatically continues to completion

	client.ExpectTerminatedEvent(t)
	client.DisconnectRequest()
	client.ExpectDisconnectResponse(t)
}

func TestLaunchDebugRequest(t *testing.T) {
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		// We reuse the harness that builds, but ignore the actual binary.
		runDebugSession(t, client, func() {
			// Use the default output directory.
			client.LaunchRequestWithArgs(map[string]interface{}{
				"mode": "debug", "program": fixture.Source})
		})
	})
}

func TestLaunchTestRequest(t *testing.T) {
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSession(t, client, func() {
			// We reuse the harness that builds, but ignore the actual binary.
			fixtures := protest.FindFixturesDir()
			testdir, _ := filepath.Abs(filepath.Join(fixtures, "buildtest"))
			client.LaunchRequestWithArgs(map[string]interface{}{
				"mode": "test", "program": testdir, "output": "__mytestdir"})
		})
	})
}

func TestBadLaunchRequests(t *testing.T) {
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		seqCnt := 1
		expectFailedToLaunch := func(response *dap.ErrorResponse) {
			t.Helper()
			if response.RequestSeq != seqCnt {
				t.Errorf("RequestSeq got %d, want %d", seqCnt, response.RequestSeq)
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
			seqCnt++
		}

		expectFailedToLaunchWithMessage := func(response *dap.ErrorResponse, errmsg string) {
			t.Helper()
			expectFailedToLaunch(response)
			if response.Body.Error.Format != errmsg {
				t.Errorf("\ngot  %q\nwant %q", response.Body.Error.Format, errmsg)
			}
		}

		// Test for the DAP-specific detailed error message.
		client.LaunchRequest("exec", "", stopOnEntry)
		expectFailedToLaunchWithMessage(client.ExpectErrorResponse(t),
			"Failed to launch: The program attribute is missing in debug configuration.")

		client.LaunchRequestWithArgs(map[string]interface{}{"program": 12345})
		expectFailedToLaunchWithMessage(client.ExpectErrorResponse(t),
			"Failed to launch: The program attribute is missing in debug configuration.")

		client.LaunchRequestWithArgs(map[string]interface{}{"program": nil})
		expectFailedToLaunchWithMessage(client.ExpectErrorResponse(t),
			"Failed to launch: The program attribute is missing in debug configuration.")

		client.LaunchRequestWithArgs(map[string]interface{}{})
		expectFailedToLaunchWithMessage(client.ExpectErrorResponse(t),
			"Failed to launch: The program attribute is missing in debug configuration.")

		client.LaunchRequest("remote", fixture.Path, stopOnEntry)
		expectFailedToLaunchWithMessage(client.ExpectErrorResponse(t),
			"Failed to launch: Unsupported 'mode' value \"remote\" in debug configuration.")

		client.LaunchRequest("notamode", fixture.Path, stopOnEntry)
		expectFailedToLaunchWithMessage(client.ExpectErrorResponse(t),
			"Failed to launch: Unsupported 'mode' value \"notamode\" in debug configuration.")

		client.LaunchRequestWithArgs(map[string]interface{}{"mode": 12345, "program": fixture.Path})
		expectFailedToLaunchWithMessage(client.ExpectErrorResponse(t),
			"Failed to launch: Unsupported 'mode' value %!q(float64=12345) in debug configuration.")

		// Skip detailed message checks for potentially different OS-specific errors.
		client.LaunchRequest("exec", fixture.Path+"_does_not_exist", stopOnEntry)
		expectFailedToLaunch(client.ExpectErrorResponse(t))

		client.LaunchRequest("debug", fixture.Path+"_does_not_exist", stopOnEntry)
		expectFailedToLaunch(client.ExpectErrorResponse(t)) // Build error

		client.LaunchRequest("exec", fixture.Source, stopOnEntry)
		expectFailedToLaunch(client.ExpectErrorResponse(t)) // Not an executable

		// We failed to launch the program. Make sure shutdown still works.
		client.DisconnectRequest()
		dresp := client.ExpectDisconnectResponse(t)
		if dresp.RequestSeq != seqCnt {
			t.Errorf("got %#v, want RequestSeq=%d", dresp, seqCnt)
		}
	})
}

func TestBadlyFormattedMessageToServer(t *testing.T) {
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		// Send a badly formatted message to the server, and expect it to close the
		// connection.
		client.UnknownRequest()
		time.Sleep(100 * time.Millisecond)

		_, err := client.ReadMessage()

		if err != io.EOF {
			t.Errorf("got err=%v, want io.EOF", err)
		}
	})
}
