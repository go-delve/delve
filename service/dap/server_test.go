package dap

import (
	"flag"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-delve/delve/pkg/logflags"
	protest "github.com/go-delve/delve/pkg/proc/test"
	"github.com/go-delve/delve/service"
	"github.com/go-delve/delve/service/dap/daptest"
	"github.com/go-delve/delve/service/debugger"
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
		DisconnectChan: disconnectChan,
		Debugger: debugger.Config{
			Backend: "default",
		},
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
		client.StackTraceRequest(1, 0, 20)
		stResp := client.ExpectErrorResponse(t)
		if stResp.Seq != 0 || stResp.RequestSeq != 8 || stResp.Body.Error.Format != "Unable to produce stack trace: unknown goroutine 1" {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=8 Format=\"Unable to produce stack trace: unknown goroutine 1\"", stResp)
		}

		// 9 >> stackTrace, << stackTrace
		client.StackTraceRequest(1, 0, 20)
		stResp = client.ExpectErrorResponse(t)
		if stResp.Seq != 0 || stResp.RequestSeq != 9 || stResp.Body.Error.Id != 2004 {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=9 Id=2004", stResp)
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

		client.StackTraceRequest(1, 0, 20)
		stResp := client.ExpectStackTraceResponse(t)

		if stResp.Body.TotalFrames != 6 {
			t.Errorf("\ngot %#v\nwant TotalFrames=6", stResp.Body.TotalFrames)
		}
		if len(stResp.Body.StackFrames) != 6 {
			t.Errorf("\ngot %#v\nwant len(StackFrames)=6", stResp.Body.StackFrames)
		} else {
			expectFrame := func(got dap.StackFrame, id int, name string, sourceName string, line int) {
				t.Helper()
				if got.Id != id || got.Name != name {
					t.Errorf("\ngot  %#v\nwant Id=%d Name=%s", got, id, name)
				}
				if (sourceName != "" && got.Source.Name != sourceName) || (line > 0 && got.Line != line) {
					t.Errorf("\ngot  %#v\nwant Source.Name=%s Line=%d", got, sourceName, line)
				}
			}
			expectFrame(stResp.Body.StackFrames[0], 1000, "main.Increment", "increment.go", 8)
			expectFrame(stResp.Body.StackFrames[1], 1001, "main.Increment", "increment.go", 11)
			expectFrame(stResp.Body.StackFrames[2], 1002, "main.Increment", "increment.go", 11)
			expectFrame(stResp.Body.StackFrames[3], 1003, "main.main", "increment.go", 17)
			expectFrame(stResp.Body.StackFrames[4], 1004, "runtime.main", "proc.go", -1)
			expectFrame(stResp.Body.StackFrames[5], 1005, "runtime.goexit", "", -1)
		}

		client.ScopesRequest(1000)
		scopes := client.ExpectScopesResponse(t)
		expectScope(t, scopes, 0, "Arguments", 1000)
		expectScope(t, scopes, 1, "Locals", 1001)

		client.VariablesRequest(1000) // Arguments
		args := client.ExpectVariablesResponse(t)
		expectVars(t, args, "Arguments", 2)
		expectVarExact(t, args, 0, "y", "0", 0)
		expectVarExact(t, args, 1, "~r1", "0", 0)

		client.VariablesRequest(1001) // Locals
		locals := client.ExpectVariablesResponse(t)
		expectVars(t, locals, "Locals", 0)

		client.ContinueRequest(1)
		client.ExpectContinueResponse(t)
		// "Continue" is triggered after the response is sent

		client.ExpectTerminatedEvent(t)
		client.DisconnectRequest()
		client.ExpectDisconnectResponse(t)
	})
}

// expectStackFrames is a helper for verifying the values within StackTraceResponse.
//     wantStartLine - file line of the first returned frame (non-positive values are ignored).
//     wantStartID - id of the first frame returned (ignored if wantFrames is 0).
//     wantFrames - number of frames returned.
//     wantTotalFrames - total number of stack frames (StackTraceResponse.Body.TotalFrames).
func expectStackFrames(t *testing.T, got *dap.StackTraceResponse,
	wantStartLine, wantStartID, wantFrames, wantTotalFrames int) {
	t.Helper()
	if got.Body.TotalFrames != wantTotalFrames {
		t.Errorf("\ngot  %#v\nwant TotalFrames=%d", got.Body.TotalFrames, wantTotalFrames)
	}
	if len(got.Body.StackFrames) != wantFrames {
		t.Errorf("\ngot  len(StackFrames)=%d\nwant %d", len(got.Body.StackFrames), wantFrames)
	} else {
		// Verify that frame ids are consecutive numbers starting at wantStartID
		for i := 0; i < wantFrames; i++ {
			if got.Body.StackFrames[i].Id != wantStartID+i {
				t.Errorf("\ngot  %#v\nwant Id=%d", got.Body.StackFrames[i], wantStartID+i)
			}
		}
		// Verify the line corresponding to the first returned frame (if any).
		// This is useful when the first frame is the frame corresponding to the breakpoint at
		// a predefined line. Values < 0 are a signal to skip the check (which can be useful
		// for frames in the third-party code, where we do not control the lines).
		if wantFrames > 0 && wantStartLine > 0 && got.Body.StackFrames[0].Line != wantStartLine {
			t.Errorf("\ngot  Line=%d\nwant %d", got.Body.StackFrames[0].Line, wantStartLine)
		}
	}
}

// expectScope is a helper for verifying the values within a ScopesResponse.
//     i - index of the scope within ScopesRespose.Body.Scopes array
//     name - name of the scope
//     varRef - reference to retrieve variables of this scope
func expectScope(t *testing.T, got *dap.ScopesResponse, i int, name string, varRef int) {
	t.Helper()
	if len(got.Body.Scopes) <= i {
		t.Errorf("\ngot  %d\nwant len(Scopes)>%d", len(got.Body.Scopes), i)
	}
	goti := got.Body.Scopes[i]
	if goti.Name != name || goti.VariablesReference != varRef || goti.Expensive {
		t.Errorf("\ngot  %#v\nwant Name=%q VariablesReference=%d Expensive=false", goti, name, varRef)
	}
}

// expectVars is a helper for verifying the number of variables withing a VariablesResponse.
//      parentName - name of the enclosing variable or scope
//      numChildren - number of variables/fields/elements of this variable
func expectVars(t *testing.T, got *dap.VariablesResponse, parentName string, numChildren int) {
	t.Helper()
	if len(got.Body.Variables) != numChildren {
		t.Errorf("\ngot  len(%s)=%d\nwant %d", parentName, len(got.Body.Variables), numChildren)
	}
}

// expectVar is a helper for verifying the values within a VariablesResponse.
//     i - index of the scope within VariablesRespose.Body.Variables array
//     name - name of the variable
//     value - the value of the variable
//     useExactMatch - true if value is to be compared to exactly, false if to be used as regex
//     ref - reference to retrieve children of this variable
func expectVar(t *testing.T, got *dap.VariablesResponse, i int, name, value string, useExactMatch bool, ref int) {
	t.Helper()
	if len(got.Body.Variables) <= i {
		t.Errorf("\ngot  len=%d\nwant len>%d", len(got.Body.Variables), i)
		return
	}
	goti := got.Body.Variables[i]
	if goti.Name != name || goti.VariablesReference != ref {
		t.Errorf("\ngot  %#v\nwant Name=%q VariablesReference=%d ", goti, name, ref)
	}
	matched := false
	if useExactMatch {
		matched = (goti.Value == value)
	} else {
		matched, _ = regexp.MatchString(value, goti.Value)
	}
	if !matched {
		t.Errorf("\ngot  %s=%q\nwant %q", name, goti.Value, value)
	}
}

// expectVarExact is a helper like expectVar that matches value exactly.
func expectVarExact(t *testing.T, got *dap.VariablesResponse, i int, name, value string, ref int) {
	t.Helper()
	expectVar(t, got, i, name, value, true, ref)
}

// expectVarRegex is a helper like expectVar that treats value as a regex.
func expectVarRegex(t *testing.T, got *dap.VariablesResponse, i int, name, value string, ref int) {
	t.Helper()
	expectVar(t, got, i, name, value, false, ref)
}

// TestStackTraceRequest executes to a breakpoint (similarly to TestSetBreakpoint
// that includes more thorough checking of that sequence) and tests different
// good and bad configurations of 'stackTrace' requests.
func TestStackTraceRequest(t *testing.T) {
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		var stResp *dap.StackTraceResponse
		runDebugSessionWithBPs(t, client,
			// Launch
			func() {
				client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
			},
			// Set breakpoints
			fixture.Source, []int{8, 18},
			[]onBreakpoint{
				{ // Stop at line 8
					execute: func() {
						client.StackTraceRequest(1, 0, 0)
						stResp = client.ExpectStackTraceResponse(t)
						expectStackFrames(t, stResp, 8, 1000, 6, 6)

						// Even though the stack frames are the same,
						// repeated requests at the same breakpoint,
						// would assign unique ids to them each time.
						client.StackTraceRequest(1, -100, 0) // Negative startFrame is treated as 0
						stResp = client.ExpectStackTraceResponse(t)
						expectStackFrames(t, stResp, 8, 1006, 6, 6)

						client.StackTraceRequest(1, 3, 0)
						stResp = client.ExpectStackTraceResponse(t)
						expectStackFrames(t, stResp, 17, 1015, 3, 6)

						client.StackTraceRequest(1, 6, 0)
						stResp = client.ExpectStackTraceResponse(t)
						expectStackFrames(t, stResp, -1, -1, 0, 6)

						client.StackTraceRequest(1, 7, 0) // Out of bounds startFrame is capped at len
						stResp = client.ExpectStackTraceResponse(t)
						expectStackFrames(t, stResp, -1, -1, 0, 6)
					},
					disconnect: false,
				},
				{ // Stop at line 18
					execute: func() {
						// Frame ids get reset at each breakpoint.
						client.StackTraceRequest(1, 0, 0)
						stResp = client.ExpectStackTraceResponse(t)
						expectStackFrames(t, stResp, 18, 1000, 3, 3)

						client.StackTraceRequest(1, 0, -100) // Negative levels is treated as 0
						stResp = client.ExpectStackTraceResponse(t)
						expectStackFrames(t, stResp, 18, 1003, 3, 3)

						client.StackTraceRequest(1, 0, 2)
						stResp = client.ExpectStackTraceResponse(t)
						expectStackFrames(t, stResp, 18, 1006, 2, 3)

						client.StackTraceRequest(1, 0, 3)
						stResp = client.ExpectStackTraceResponse(t)
						expectStackFrames(t, stResp, 18, 1009, 3, 3)

						client.StackTraceRequest(1, 0, 4) // Out of bounds levels is capped at len
						stResp = client.ExpectStackTraceResponse(t)
						expectStackFrames(t, stResp, 18, 1012, 3, 3)

						client.StackTraceRequest(1, 1, 2)
						stResp = client.ExpectStackTraceResponse(t)
						expectStackFrames(t, stResp, -1, 1016, 2, 3) // Don't test for runtime line we don't control
					},
					disconnect: false,
				}})
	})
}

// TestScopesAndVariablesRequests executes to a breakpoint and tests different
// configurations of 'scopes' and 'variables' requests.
func TestScopesAndVariablesRequests(t *testing.T) {
	runTest(t, "testvariables", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client,
			// Launch
			func() {
				client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
			},
			// Breakpoints are set within the program
			fixture.Source, []int{},
			[]onBreakpoint{
				{ // Stop at line 62
					execute: func() {
						client.StackTraceRequest(1, 0, 20)
						stack := client.ExpectStackTraceResponse(t)
						expectStackFrames(t, stack, 62, 1000, 4, 4)

						client.ScopesRequest(1000)
						scopes := client.ExpectScopesResponse(t)
						expectScope(t, scopes, 0, "Arguments", 1000)
						expectScope(t, scopes, 1, "Locals", 1001)

						// Arguments

						client.VariablesRequest(1000)
						args := client.ExpectVariablesResponse(t)
						expectVars(t, args, "Arguments", 2)
						expectVarExact(t, args, 0, "baz", `"bazburzum"`, 0)
						expectVarExact(t, args, 1, "bar", `<main.FooBar>`, 1002)
						{
							client.VariablesRequest(1002)
							bar := client.ExpectVariablesResponse(t)
							expectVars(t, bar, "bar", 2)
							expectVarExact(t, bar, 0, "Baz", "10", 0)
							expectVarExact(t, bar, 1, "Bur", `"lorem"`, 0)
						}

						// Locals

						client.VariablesRequest(1001)
						locals := client.ExpectVariablesResponse(t)
						expectVars(t, locals, "Locals", 29)

						// reflect.Kind == Bool
						expectVarExact(t, locals, 13, "b1", "true", 0)
						expectVarExact(t, locals, 14, "b2", "false", 0)
						// reflect.Kind == Int
						expectVarExact(t, locals, 1, "a2", "6", 0)
						expectVarExact(t, locals, 15, "neg", "-1", 0)
						// reflect.Kind == Int8
						expectVarExact(t, locals, 16, "i8", "1", 0)
						// reflect.Kind == Int16 - see testvariables2
						// reflect.Kind == Int32 - see testvariables2
						// reflect.Kind == Int64 - see testvariables2
						// reflect.Kind == Uint
						// reflect.Kind == Uint8
						expectVarExact(t, locals, 17, "u8", "255", 0)
						// reflect.Kind == Uint16
						expectVarExact(t, locals, 18, "u16", "65535", 0)
						// reflect.Kind == Uint32
						expectVarExact(t, locals, 19, "u32", "4294967295", 0)
						// reflect.Kind == Uint64
						expectVarExact(t, locals, 20, "u64", "18446744073709551615", 0)
						// reflect.Kind == Uintptr
						expectVarExact(t, locals, 21, "up", "5", 0)
						// reflect.Kind == Float32
						expectVarExact(t, locals, 22, "f32", "1.2", 0)
						// reflect.Kind == Float64
						expectVarExact(t, locals, 2, "a3", "7.23", 0)
						// reflect.Kind == Complex64
						expectVarExact(t, locals, 23, "c64", "(1 + 2i)", 1011)
						{
							client.VariablesRequest(1011)
							c64 := client.ExpectVariablesResponse(t)
							expectVars(t, c64, "c64", 2)
							expectVarExact(t, c64, 0, "real", "1", 0)
							expectVarExact(t, c64, 1, "imaginary", "2", 0)
						}
						// reflect.Kind == Complex128
						expectVarExact(t, locals, 24, "c128", "(2 + 3i)", 1012)
						{
							client.VariablesRequest(1012)
							c128 := client.ExpectVariablesResponse(t)
							expectVars(t, c128, "c128", 2)
							expectVarExact(t, c128, 0, "real", "2", 0)
							expectVarExact(t, c128, 1, "imaginary", "3", 0)
						}
						// reflect.Kind == Array
						expectVarExact(t, locals, 3, "a4", "<[2]int>", 1003)
						{
							client.VariablesRequest(1003)
							a4 := client.ExpectVariablesResponse(t)
							expectVars(t, a4, "a4", 2)
							expectVarExact(t, a4, 0, "[0]", "1", 0)
							expectVarExact(t, a4, 1, "[1]", "2", 0)
						}
						expectVarExact(t, locals, 10, "a11", "<[3]main.FooBar>", 1008)
						{
							client.VariablesRequest(1008)
							a11 := client.ExpectVariablesResponse(t)
							expectVars(t, a11, "a11", 3)
							expectVarExact(t, a11, 0, "[0]", "<main.FooBar>", 1016)
							expectVarExact(t, a11, 1, "[1]", "<main.FooBar>", 1017)
							{
								client.VariablesRequest(1017)
								a11_1 := client.ExpectVariablesResponse(t)
								expectVars(t, a11_1, "a11[1]", 2)
								expectVarExact(t, a11_1, 0, "Baz", "2", 0)
								expectVarExact(t, a11_1, 1, "Bur", `"b"`, 0)

							}
							expectVarExact(t, a11, 2, "[2]", "<main.FooBar>", 1018)
						}

						// reflect.Kind == Chan - see testvariables2
						// reflect.Kind == Func - see testvariables2
						// reflect.Kind == Interface - see testvariables2
						// reflect.Kind == Map - see testvariables2
						// reflect.Kind == Ptr
						expectVarRegex(t, locals, 6, "a7", "<\\*main\\.FooBar>\\(0x[0-9a-f]+\\)", 1006)
						{
							client.VariablesRequest(1006)
							a7 := client.ExpectVariablesResponse(t)
							expectVars(t, a7, "a7", 1)
							expectVarExact(t, a7, 0, "", "<main.FooBar>", 1019)
							{
								client.VariablesRequest(1019)
								a7val := client.ExpectVariablesResponse(t)
								expectVars(t, a7val, "*a7", 2)
								expectVarExact(t, a7val, 0, "Baz", "5", 0)
								expectVarExact(t, a7val, 1, "Bur", `"strum"`, 0)
							}
						}
						// TODO(polina): how to test for "nil" (without type) and "void"?
						expectVarExact(t, locals, 8, "a9", "nil <*main.FooBar>", 0)
						// reflect.Kind == Slice
						expectVarExact(t, locals, 4, "a5", "<[]int> (length: 5, cap: 5)", 1004)
						{
							client.VariablesRequest(1004)
							a5 := client.ExpectVariablesResponse(t)
							expectVars(t, a5, "a5", 5)
							expectVarExact(t, a5, 0, "[0]", "1", 0)
							expectVarExact(t, a5, 4, "[4]", "5", 0)
						}
						expectVarExact(t, locals, 11, "a12", "<[]main.FooBar> (length: 2, cap: 2)", 1009)
						{
							client.VariablesRequest(1009)
							a12 := client.ExpectVariablesResponse(t)
							expectVars(t, a12, "a12", 2)
							expectVarExact(t, a12, 0, "[0]", "<main.FooBar>", 1020)
							expectVarExact(t, a12, 1, "[1]", "<main.FooBar>", 1021)
							{
								client.VariablesRequest(1021)
								a12_1 := client.ExpectVariablesResponse(t)
								expectVars(t, a12_1, "a12[1]", 2)
								expectVarExact(t, a12_1, 0, "Baz", "5", 0)
								expectVarExact(t, a12_1, 1, "Bur", `"e"`, 0)
							}
						}
						expectVarExact(t, locals, 12, "a13", "<[]*main.FooBar> (length: 3, cap: 3)", 1010)
						{
							client.VariablesRequest(1010)
							a13 := client.ExpectVariablesResponse(t)
							expectVars(t, a13, "a13", 3)
							expectVarRegex(t, a13, 0, "[0]", "<\\*main\\.FooBar>\\(0x[0-9a-f]+\\)", 1022)
							expectVarRegex(t, a13, 1, "[1]", "<\\*main\\.FooBar>\\(0x[0-9a-f]+\\)", 1023)
							expectVarRegex(t, a13, 2, "[2]", "<\\*main\\.FooBar>\\(0x[0-9a-f]+\\)", 1024)
							{
								client.VariablesRequest(1024)
								a13_2 := client.ExpectVariablesResponse(t)
								expectVars(t, a13_2, "a13[2]", 1)
								expectVarExact(t, a13_2, 0, "", "<main.FooBar>", 1025)
								{
									client.VariablesRequest(1025)
									val := client.ExpectVariablesResponse(t)
									expectVars(t, val, "*a13[2]", 2)
									expectVarExact(t, val, 0, "Baz", "8", 0)
									expectVarExact(t, val, 1, "Bur", `"h"`, 0)
								}
							}
						}
						// reflect.Kind == String
						expectVarExact(t, locals, 0, "a1", `"foofoofoofoofoofoo"`, 0)
						expectVarExact(t, locals, 9, "a10", `"ofo"`, 0)
						// reflect.Kind == Struct
						expectVarExact(t, locals, 5, "a6", "<main.FooBar>", 1005)
						{
							client.VariablesRequest(1005)
							a6 := client.ExpectVariablesResponse(t)
							expectVars(t, a6, "a6", 2)
							expectVarExact(t, a6, 0, "Baz", "8", 0)
							expectVarExact(t, a6, 1, "Bur", `"word"`, 0)
						}
						expectVarExact(t, locals, 7, "a8", "<main.FooBar2>", 1007)
						{
							client.VariablesRequest(1007)
							a8 := client.ExpectVariablesResponse(t)
							expectVars(t, a8, "a8", 2)
							expectVarExact(t, a8, 0, "Bur", "10", 0)
							expectVarExact(t, a8, 1, "Baz", `"feh"`, 0)
						}
						// reflect.Kind == UnsafePointer - see testvariables2
					},
					disconnect: false,
				},
				{ // Stop at line 25
					execute: func() {
						// Frame ids get reset at each breakpoint.
						client.StackTraceRequest(1, 0, 20)
						stack := client.ExpectStackTraceResponse(t)
						expectStackFrames(t, stack, 25, 1000, 5, 5)

						client.ScopesRequest(1000)
						scopes := client.ExpectScopesResponse(t)
						expectScope(t, scopes, 0, "Arguments", 1000)
						expectScope(t, scopes, 1, "Locals", 1001)

						client.ScopesRequest(1111)
						erres := client.ExpectErrorResponse(t)
						if erres.Body.Error.Format != "Unable to list locals: unknown frame id 1111" {
							t.Errorf("\ngot %#v\nwant Format=\"Unable to list locals: unknown frame id 1111\"", erres)
						}

						client.VariablesRequest(1000) // Arguments
						args := client.ExpectVariablesResponse(t)
						expectVars(t, args, "Arguments", 0)

						client.VariablesRequest(1001) // Locals
						locals := client.ExpectVariablesResponse(t)
						expectVars(t, locals, "Locals", 1)
						expectVarExact(t, locals, 0, "a1", `"bur"`, 0)

						client.VariablesRequest(7777)
						erres = client.ExpectErrorResponse(t)
						if erres.Body.Error.Format != "Unable to lookup variable: unknown reference 7777" {
							t.Errorf("\ngot %#v\nwant Format=\"Unable to lookup variable: unknown reference 7777\"", erres)
						}
					},
					disconnect: false,
				}})
	})
}

// TestScopesAndVariablesRequests2 executes to a breakpoint and tests different
// configurations of 'scopes' and 'variables' requests.
func TestScopesAndVariablesRequests2(t *testing.T) {
	runTest(t, "testvariables2", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client,
			// Launch
			func() {
				client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
			},
			// Breakpoints are set within the program
			fixture.Source, []int{},
			[]onBreakpoint{
				{ // Stop at line 316
					execute: func() {
						client.StackTraceRequest(1, 0, 20)
						stack := client.ExpectStackTraceResponse(t)
						expectStackFrames(t, stack, 316, 1000, 3, 3)

						client.ScopesRequest(1000)
						scopes := client.ExpectScopesResponse(t)
						expectScope(t, scopes, 0, "Arguments", 1000)
						expectScope(t, scopes, 1, "Locals", 1001)

						// Arguments

						client.VariablesRequest(1000)
						args := client.ExpectVariablesResponse(t)
						expectVars(t, args, "Arguments", 0)

						// Locals

						client.VariablesRequest(1001)
						locals := client.ExpectVariablesResponse(t)
						expectVars(t, locals, "Locals", 93)

						// reflect.Kind == Bool - see testvariables
						// reflect.Kind == Int - see testvariables
						// reflect.Kind == Int8
						expectVarExact(t, locals, 65, "ni8", "-5", 0)
						// reflect.Kind == Int16
						expectVarExact(t, locals, 66, "ni16", "-5", 0)
						// reflect.Kind == Int32
						expectVarExact(t, locals, 67, "ni32", "-5", 0)
						// reflect.Kind == Int64
						expectVarExact(t, locals, 68, "ni64", "-5", 0)
						// reflect.Kind == Uint
						// reflect.Kind == Uint8 - see testvariables
						// reflect.Kind == Uint16 - see testvariables
						// reflect.Kind == Uint32 - see testvariables
						// reflect.Kind == Uint64 - see testvariables
						// reflect.Kind == Uintptr - see testvariables
						// reflect.Kind == Float32 - see testvariables
						// reflect.Kind == Float64
						expectVarExact(t, locals, 69, "pinf", "+Inf", 0)
						expectVarExact(t, locals, 70, "ninf", "-Inf", 0)
						expectVarExact(t, locals, 71, "nan", "NaN", 0)
						// reflect.Kind == Complex64 - see testvariables
						// reflect.Kind == Complex128 - see testvariables
						// reflect.Kind == Array
						expectVarExact(t, locals, 7, "a0", "<[0]int>", 0)
						// reflect.Kind == Chan
						expectVarExact(t, locals, 19, "ch1", "<chan int>", 1009)
						{
							client.VariablesRequest(1009)
							ch1 := client.ExpectVariablesResponse(t)
							expectVars(t, ch1, "ch1", 11)
							expectVarExact(t, ch1, 0, "qcount", "4", 0)
							expectVarExact(t, ch1, 10, "lock", "<runtime.mutex>", 1060)
						}
						expectVarExact(t, locals, 20, "chnil", "nil <chan int>", 0)
						// reflect.Kind == Func
						expectVarExact(t, locals, 15, "fn1", "main.afunc", 0)
						expectVarExact(t, locals, 16, "fn2", "<main.functype>", 0)
						// reflect.Kind == Interface
						expectVarExact(t, locals, 40, "ifacenil", "nil <interface {}>", 0)
						expectVarExact(t, locals, 37, "iface2", "<interface {}>", 1018)
						{
							client.VariablesRequest(1018)
							iface2 := client.ExpectVariablesResponse(t)
							expectVars(t, iface2, "iface2", 1)
							expectVarExact(t, iface2, 0, "data", `"test"`, 0)
						}
						expectVarExact(t, locals, 39, "iface4", "<interface {}>", 1020)
						{
							client.VariablesRequest(1020)
							iface4 := client.ExpectVariablesResponse(t)
							expectVars(t, iface4, "iface4", 1)
							expectVarExact(t, iface4, 0, "data", "<[]go/constant.Value> (length: 1, cap: 1)", 1061)
							{
								client.VariablesRequest(1061)
								iface4data := client.ExpectVariablesResponse(t)
								expectVars(t, iface4data, "iface4.data", 1)
								expectVarExact(t, iface4data, 0, "[0]", "<go/constant.Value>", 1062)

							}
						}
						// reflect.Kind == Map
						expectVarExact(t, locals, 22, "mnil", "nil <map[string]main.astruct>", 0)
						expectVarExact(t, locals, 23, "m2", "<map[int]*main.astruct> (length: 1)", 1011)
						{
							client.VariablesRequest(1011)
							m2 := client.ExpectVariablesResponse(t)
							expectVars(t, m2, "m2", 1)
							expectVarRegex(t, m2, 0, "1", "<\\*main\\.astruct>\\(0x[0-9a-f]+\\)", 1063)
							{
								client.VariablesRequest(1063)
								m2_1 := client.ExpectVariablesResponse(t)
								expectVars(t, m2_1, "m2[1]", 1)
								expectVarExact(t, m2_1, 0, "", "<main.astruct>", 1064)
								{
									client.VariablesRequest(1064)
									m2_1val := client.ExpectVariablesResponse(t)
									expectVars(t, m2_1val, "*m2[1]", 2)
									expectVarExact(t, m2_1val, 0, "A", "10", 0)
									expectVarExact(t, m2_1val, 1, "B", "11", 0)
								}
							}
						}
						expectVarExact(t, locals, 24, "m3", "<map[main.astruct]int> (length: 2)", 1012)
						{
							client.VariablesRequest(1012)
							m3 := client.ExpectVariablesResponse(t)
							expectVars(t, m3, "m3", 2)
							expectVarExact(t, m3, 0, "<main.astruct>", "42", 1065)
							{
								client.VariablesRequest(1065)
								m3_0 := client.ExpectVariablesResponse(t)
								expectVars(t, m3_0, "m3[0]", 2)
								expectVarExact(t, m3_0, 0, "A", "1", 0)
								expectVarExact(t, m3_0, 1, "B", "1", 0)
							}
							expectVarExact(t, m3, 1, "<main.astruct>", "43", 1066)
							{
								client.VariablesRequest(1066)
								m3_1 := client.ExpectVariablesResponse(t)
								expectVars(t, m3_1, "m3[1]", 2)
								expectVarExact(t, m3_1, 0, "A", "2", 0)
								expectVarExact(t, m3_1, 1, "B", "2", 0)
							}
						}
						expectVarExact(t, locals, 25, "m4", "<map[main.astruct]main.astruct> (length: 2)", 1013)
						{
							client.VariablesRequest(1013)
							m4 := client.ExpectVariablesResponse(t)
							expectVars(t, m4, "m4", 4)
							expectVarExact(t, m4, 0, "[key 0]", "<main.astruct>", 1067)
							expectVarExact(t, m4, 1, "[val 0]", "<main.astruct>", 1068)
							expectVarExact(t, m4, 2, "[key 1]", "<main.astruct>", 1069)
							{
								client.VariablesRequest(1069)
								m4_key1 := client.ExpectVariablesResponse(t)
								expectVars(t, m4_key1, "m4_key1", 2)
								expectVarExact(t, m4_key1, 0, "A", "2", 0)
								expectVarExact(t, m4_key1, 1, "B", "2", 0)
							}
							expectVarExact(t, m4, 3, "[val 1]", "<main.astruct>", 1070)
							{
								client.VariablesRequest(1070)
								m4_val1 := client.ExpectVariablesResponse(t)
								expectVars(t, m4_val1, "m4_val1", 2)
								expectVarExact(t, m4_val1, 0, "A", "22", 0)
								expectVarExact(t, m4_val1, 1, "B", "22", 0)
							}
						}
						expectVarExact(t, locals, 80, "emptymap", "<map[string]string> (length: 0)", 0)
						// reflect.Kind == Ptr - see testvariables
						// reflect.Kind == Slice
						expectVarExact(t, locals, 76, "zsslice", "<[]struct {}> (length: 3, cap: 3)", 1045)
						{
							client.VariablesRequest(1045)
							zsslice := client.ExpectVariablesResponse(t)
							expectVars(t, zsslice, "zsslice", 3)
						}
						expectVarExact(t, locals, 79, "emptyslice", "<[]string> (length: 0, cap: 0)", 0)
						expectVarExact(t, locals, 17, "nilslice", "nil <[]int>", 0)
						// reflect.Kind == String
						expectVarExact(t, locals, 85, "longstr", "\"very long string 0123456789a0123456789b0123456789c0123456789d012...+73 more\"", 0)
						// reflect.Kind == Struct
						expectVarExact(t, locals, 75, "zsvar", "<struct {}>", 0)
						// reflect.Kind == UnsafePointer
						// TODO(polina): how do I test for unsafe.Pointer(nil)?
						expectVarRegex(t, locals, 26, "upnil", "unsafe\\.Pointer\\(0x0\\)", 0)
						expectVarRegex(t, locals, 27, "up1", "unsafe\\.Pointer\\(0x[0-9a-f]+\\)", 0)

						// Test that variables are not yet loaded completely.
						expectVarExact(t, locals, 21, "m1", "<map[string]main.astruct> (length: 66)", 1010)
						{
							client.VariablesRequest(1010)
							m1 := client.ExpectVariablesResponse(t)
							expectVars(t, m1, "m1", 64) // TODO(polina): should be 66.
						}
					},
					disconnect: false,
				},
				{ // Stop at line 321
					execute: func() {
						// Frame ids get reset at each breakpoint.
						client.StackTraceRequest(1, 0, 20)
						stack := client.ExpectStackTraceResponse(t)
						expectStackFrames(t, stack, 321, 1000, 3, 3)

						client.ScopesRequest(1000)
						scopes := client.ExpectScopesResponse(t)
						expectScope(t, scopes, 0, "Arguments", 1000)
						expectScope(t, scopes, 1, "Locals", 1001)

						client.VariablesRequest(1000) // Arguments
						args := client.ExpectVariablesResponse(t)
						expectVars(t, args, "Arguments", 0)

						client.VariablesRequest(1001) // Locals
						locals := client.ExpectVariablesResponse(t)
						expectVars(t, locals, "Locals", 92)

						// There is stack overflow at the end of the program.
						// Exist before that.
						client.DisconnectRequest()
						client.ExpectDisconnectResponse(t)
					},
					disconnect: true,
				}})
	})
}

// TestScopesAndVariablesRequests3 executes to a breakpoint and tests
// a corner case where a variable is of invalid interface type before
// initialtization.
func TestScopesAndVariablesRequests3(t *testing.T) {
	runTest(t, "testvariables3", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client,
			// Launch
			func() {
				client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
			},
			fixture.Source, []int{18},
			[]onBreakpoint{
				{ // Stop at line 18
					execute: func() {
						client.StackTraceRequest(1, 0, 20)
						stack := client.ExpectStackTraceResponse(t)
						expectStackFrames(t, stack, 18, 1000, 3, 3)

						client.ScopesRequest(1000)
						scopes := client.ExpectScopesResponse(t)
						expectScope(t, scopes, 0, "Arguments", 1000)
						expectScope(t, scopes, 1, "Locals", 1001)

						client.VariablesRequest(1001)
						locals := client.ExpectVariablesResponse(t)
						expectVars(t, locals, "Locals", 1)
						expectVarExact(t, locals, 0, "foo", "(unreadable invalid interface type: key not found)", 0)
					},
					disconnect: false,
				}})
	})
}

// Tests that 'stackTraceDepth' from LaunchRequest is parsed and passed to
// stacktrace requests handlers.
func TestLaunchRequestWithStackTraceDepth(t *testing.T) {
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		var stResp *dap.StackTraceResponse
		runDebugSessionWithBPs(t, client,
			// Launch
			func() {
				client.LaunchRequestWithArgs(map[string]interface{}{
					"mode": "exec", "program": fixture.Path, "stackTraceDepth": 1,
				})
			},
			// Set breakpoints
			fixture.Source, []int{8},
			[]onBreakpoint{
				{ // Stop at line 8
					execute: func() {
						client.StackTraceRequest(1, 0, 0)
						stResp = client.ExpectStackTraceResponse(t)
						expectStackFrames(t, stResp, 8, 1000, 2, 2)
					},
					disconnect: false,
				}})
	})
}

// onBreakpoint specifies what the test harness should simulate at
// a stopped breakpoint. First execute() is to be called to test
// specified editor-driven or user-driven requests. Then if
// disconnect is true, the test harness will abort the program
// execution. Otherwise, a continue will be issued and the
// program will continue to the next breakpoint or termination.
type onBreakpoint struct {
	execute    func()
	disconnect bool
}

// runDebugSessionWithBPs is a helper for executing the common init and shutdown
// sequences for a program that does not stop on entry
// while specifying breakpoints and unique launch criteria via parameters.
//     launchRequest - a function that sends a launch request, so the test author
//                     has full control of its arguments. Note that he rest of
//                     the test sequence assumes that stopOneEntry is false.
//     breakpoints   - list of lines, where breakpoints are to be set
//     onBreakpoints - list of test sequences to execute at each of the set breakpoints.
func runDebugSessionWithBPs(t *testing.T, client *daptest.Client, launchRequest func(), source string, breakpoints []int, onBPs []onBreakpoint) {
	client.InitializeRequest()
	client.ExpectInitializeResponse(t)

	launchRequest()
	client.ExpectInitializedEvent(t)
	client.ExpectLaunchResponse(t)

	client.SetBreakpointsRequest(source, breakpoints)
	client.ExpectSetBreakpointsResponse(t)

	// Skip no-op setExceptionBreakpoints

	client.ConfigurationDoneRequest()
	client.ExpectConfigurationDoneResponse(t)

	// Program automatically continues to breakpoint or completion

	for _, onBP := range onBPs {
		client.ExpectStoppedEvent(t)
		onBP.execute()
		if onBP.disconnect {
			client.DisconnectRequest()
			client.ExpectDisconnectResponse(t)
			return
		}
		client.ContinueRequest(1)
		client.ExpectContinueResponse(t)
		// "Continue" is triggered after the response is sent
	}

	client.ExpectTerminatedEvent(t)
	client.DisconnectRequest()
	client.ExpectDisconnectResponse(t)
}

// runDebugSesion is a helper for executing the standard init and shutdown
// sequences for a program that does not stop on entry
// while specifying unique launch criteria via parameters.
func runDebugSession(t *testing.T, client *daptest.Client, launchRequest func()) {
	runDebugSessionWithBPs(t, client, launchRequest, "", nil, nil)
}

func TestLaunchDebugRequest(t *testing.T) {
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		// We reuse the harness that builds, but ignore the built binary,
		// only relying on the source to be built in response to LaunchRequest.
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
			// We reuse the harness that builds, but ignore the built binary,
			// only relying on the source to be built in response to LaunchRequest.
			fixtures := protest.FindFixturesDir()
			testdir, _ := filepath.Abs(filepath.Join(fixtures, "buildtest"))
			client.LaunchRequestWithArgs(map[string]interface{}{
				"mode": "test", "program": testdir, "output": "__mytestdir"})
		})
	})
}

// Tests that 'args' from LaunchRequest are parsed and passed to the target
// program. The target program exits without an error on success, and
// panics on error, causing an unexpected StoppedEvent instead of
// Terminated Event.
func TestLaunchRequestWithArgs(t *testing.T) {
	runTest(t, "testargs", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSession(t, client, func() {
			client.LaunchRequestWithArgs(map[string]interface{}{
				"mode": "exec", "program": fixture.Path,
				"args": []string{"test", "pass flag"}})
		})
	})
}

// Tests that 'buildFlags' from LaunchRequest are parsed and passed to the
// compiler. The target program exits without an error on success, and
// panics on error, causing an unexpected StoppedEvent instead of
// TerminatedEvent.
func TestLaunchRequestWithBuildFlags(t *testing.T) {
	runTest(t, "buildflagtest", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSession(t, client, func() {
			// We reuse the harness that builds, but ignore the built binary,
			// only relying on the source to be built in response to LaunchRequest.
			client.LaunchRequestWithArgs(map[string]interface{}{
				"mode": "debug", "program": fixture.Source,
				"buildFlags": "-ldflags '-X main.Hello=World'"})
		})
	})
}

func TestUnupportedCommandResponses(t *testing.T) {
	var got *dap.ErrorResponse
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		seqCnt := 1
		expectUnsupportedCommand := func(cmd string) {
			t.Helper()
			got = client.ExpectUnsupportedCommandErrorResponse(t)
			if got.RequestSeq != seqCnt || got.Command != cmd {
				t.Errorf("\ngot  %#v\nwant RequestSeq=%d Command=%s", got, seqCnt, cmd)
			}
			seqCnt++
		}

		client.RestartFrameRequest()
		expectUnsupportedCommand("restartFrame")

		client.GotoRequest()
		expectUnsupportedCommand("goto")

		client.SourceRequest()
		expectUnsupportedCommand("source")

		client.TerminateThreadsRequest()
		expectUnsupportedCommand("terminateThreads")

		client.StepInTargetsRequest()
		expectUnsupportedCommand("stepInTargets")

		client.GotoTargetsRequest()
		expectUnsupportedCommand("gotoTargets")

		client.CompletionsRequest()
		expectUnsupportedCommand("completions")

		client.ExceptionInfoRequest()
		expectUnsupportedCommand("exceptionInfo")

		client.DataBreakpointInfoRequest()
		expectUnsupportedCommand("dataBreakpointInfo")

		client.SetDataBreakpointsRequest()
		expectUnsupportedCommand("setDataBreakpoints")

		client.BreakpointLocationsRequest()
		expectUnsupportedCommand("breakpointLocations")

		client.ModulesRequest()
		expectUnsupportedCommand("modules")
	})
}

func TestRequiredNotYetImplementedResponses(t *testing.T) {
	var got *dap.ErrorResponse
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		seqCnt := 1
		expectNotYetImplemented := func(cmd string) {
			t.Helper()
			got = client.ExpectNotYetImplementedErrorResponse(t)
			if got.RequestSeq != seqCnt || got.Command != cmd {
				t.Errorf("\ngot  %#v\nwant RequestSeq=%d Command=%s", got, seqCnt, cmd)
			}
			seqCnt++
		}

		client.AttachRequest()
		expectNotYetImplemented("attach")

		client.NextRequest()
		expectNotYetImplemented("next")

		client.StepInRequest()
		expectNotYetImplemented("stepIn")

		client.StepOutRequest()
		expectNotYetImplemented("stepOut")

		client.PauseRequest()
		expectNotYetImplemented("pause")

		client.EvaluateRequest()
		expectNotYetImplemented("evaluate")
	})
}

func TestOptionalNotYetImplementedResponses(t *testing.T) {
	var got *dap.ErrorResponse
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		seqCnt := 1
		expectNotYetImplemented := func(cmd string) {
			t.Helper()
			got = client.ExpectNotYetImplementedErrorResponse(t)
			if got.RequestSeq != seqCnt || got.Command != cmd {
				t.Errorf("\ngot  %#v\nwant RequestSeq=%d Command=%s", got, seqCnt, cmd)
			}
			seqCnt++
		}

		client.TerminateRequest()
		expectNotYetImplemented("terminate")

		client.RestartRequest()
		expectNotYetImplemented("restart")

		client.SetFunctionBreakpointsRequest()
		expectNotYetImplemented("setFunctionBreakpoints")

		client.StepBackRequest()
		expectNotYetImplemented("stepBack")

		client.ReverseContinueRequest()
		expectNotYetImplemented("reverseContinue")

		client.SetVariableRequest()
		expectNotYetImplemented("setVariable")

		client.SetExpressionRequest()
		expectNotYetImplemented("setExpression")

		client.LoadedSourcesRequest()
		expectNotYetImplemented("loadedSources")

		client.ReadMemoryRequest()
		expectNotYetImplemented("readMemory")

		client.DisassembleRequest()
		expectNotYetImplemented("disassemble")

		client.CancelRequest()
		expectNotYetImplemented("cancel")
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

		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "exec", "program": fixture.Path, "args": nil})
		expectFailedToLaunchWithMessage(client.ExpectErrorResponse(t),
			"Failed to launch: 'args' attribute '<nil>' in debug configuration is not an array.")

		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "exec", "program": fixture.Path, "args": 12345})
		expectFailedToLaunchWithMessage(client.ExpectErrorResponse(t),
			"Failed to launch: 'args' attribute '12345' in debug configuration is not an array.")

		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "exec", "program": fixture.Path, "args": []int{1, 2}})
		expectFailedToLaunchWithMessage(client.ExpectErrorResponse(t),
			"Failed to launch: value '1' in 'args' attribute in debug configuration is not a string.")

		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "debug", "program": fixture.Source, "buildFlags": 123})
		expectFailedToLaunchWithMessage(client.ExpectErrorResponse(t),
			"Failed to launch: 'buildFlags' attribute '123' in debug configuration is not a string.")

		// Skip detailed message checks for potentially different OS-specific errors.
		client.LaunchRequest("exec", fixture.Path+"_does_not_exist", stopOnEntry)
		expectFailedToLaunch(client.ExpectErrorResponse(t))

		client.LaunchRequest("debug", fixture.Path+"_does_not_exist", stopOnEntry)
		expectFailedToLaunch(client.ExpectErrorResponse(t)) // Build error

		client.LaunchRequest("exec", fixture.Source, stopOnEntry)
		expectFailedToLaunch(client.ExpectErrorResponse(t)) // Not an executable

		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "debug", "program": fixture.Source, "buildFlags": "123"})
		expectFailedToLaunch(client.ExpectErrorResponse(t)) // Build error

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
