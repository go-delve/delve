package dap

import (
	"flag"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-delve/delve/pkg/goversion"
	"github.com/go-delve/delve/pkg/logflags"
	protest "github.com/go-delve/delve/pkg/proc/test"
	"github.com/go-delve/delve/service"
	"github.com/go-delve/delve/service/dap/daptest"
	"github.com/go-delve/delve/service/debugger"
	"github.com/google/go-dap"
)

const stopOnEntry bool = true
const hasChildren bool = true
const noChildren bool = false

var testBackend string

func TestMain(m *testing.M) {
	var logOutput string
	flag.StringVar(&logOutput, "log-output", "", "configures log output")
	flag.Parse()
	logflags.Setup(logOutput != "", logOutput, "")
	protest.DefaultTestBackend(&testBackend)
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
		if contResp.Seq != 0 || contResp.RequestSeq != 10 || !contResp.Body.AllThreadsContinued {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=10 Body.AllThreadsContinued=true", contResp)
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
		if len(scopes.Body.Scopes) > 2 {
			t.Errorf("\ngot  %#v\nwant len(Scopes)=2 (Arguments & Locals)", scopes)
		}
		expectScope(t, scopes, 0, "Arguments", 1000)
		expectScope(t, scopes, 1, "Locals", 1001)

		client.VariablesRequest(1000) // Arguments
		args := client.ExpectVariablesResponse(t)
		expectChildren(t, args, "Arguments", 2)
		expectVarExact(t, args, 0, "y", "0", noChildren)
		expectVarExact(t, args, 1, "~r1", "0", noChildren)

		client.VariablesRequest(1001) // Locals
		locals := client.ExpectVariablesResponse(t)
		expectChildren(t, locals, "Locals", 0)

		client.ContinueRequest(1)
		ctResp := client.ExpectContinueResponse(t)
		if !ctResp.Body.AllThreadsContinued {
			t.Errorf("\ngot  %#v\nwant AllThreadsContinued=true", ctResp.Body)
		}
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

// expectChildren is a helper for verifying the number of variables within a VariablesResponse.
//      parentName - pseudoname of the enclosing variable or scope (used for error message only)
//      numChildren - number of variables/fields/elements of this variable
func expectChildren(t *testing.T, got *dap.VariablesResponse, parentName string, numChildren int) {
	t.Helper()
	if len(got.Body.Variables) != numChildren {
		t.Errorf("\ngot  len(%s)=%d (children=%#v)\nwant len=%d", parentName, len(got.Body.Variables), got.Body.Variables, numChildren)
	}
}

// expectVar is a helper for verifying the values within a VariablesResponse.
//     i - index of the variable within VariablesRespose.Body.Variables array (-1 will search all vars for a match)
//     name - name of the variable
//     value - the value of the variable
//     useExactMatch - true if value is to be compared to exactly, false if to be used as regex
//     hasRef - true if the variable should have children and therefore a non-0 variable reference
//     ref - reference to retrieve children of this variable (0 if none)
func expectVar(t *testing.T, got *dap.VariablesResponse, i int, name, value string, useExactMatch, hasRef bool) (ref int) {
	t.Helper()
	if len(got.Body.Variables) <= i {
		t.Errorf("\ngot  len=%d (children=%#v)\nwant len>%d", len(got.Body.Variables), got.Body.Variables, i)
		return
	}
	if i < 0 {
		for vi, v := range got.Body.Variables {
			if v.Name == name {
				i = vi
				break
			}
		}
	}
	if i < 0 {
		t.Errorf("\ngot  %#v\nwant Variables[i].Name=%q", got, name)
		return 0
	}

	goti := got.Body.Variables[i]
	if goti.Name != name || (goti.VariablesReference > 0) != hasRef {
		t.Errorf("\ngot  %#v\nwant Name=%q hasRef=%t", goti, name, hasRef)
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
	return goti.VariablesReference
}

// expectVarExact is a helper like expectVar that matches value exactly.
func expectVarExact(t *testing.T, got *dap.VariablesResponse, i int, name, value string, hasRef bool) (ref int) {
	t.Helper()
	return expectVar(t, got, i, name, value, true, hasRef)
}

// expectVarRegex is a helper like expectVar that treats value as a regex.
func expectVarRegex(t *testing.T, got *dap.VariablesResponse, i int, name, value string, hasRef bool) (ref int) {
	t.Helper()
	return expectVar(t, got, i, name, value, false, hasRef)
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
			[]onBreakpoint{{
				// Stop at line 8
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
			}, {
				// Stop at line 18
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
				client.LaunchRequestWithArgs(map[string]interface{}{
					"mode": "exec", "program": fixture.Path, "showGlobalVariables": true,
				})
			},
			// Breakpoints are set within the program
			fixture.Source, []int{},
			[]onBreakpoint{{
				// Stop at line 62
				execute: func() {
					client.StackTraceRequest(1, 0, 20)
					stack := client.ExpectStackTraceResponse(t)

					startLineno := 62
					if runtime.GOOS == "windows" && goversion.VersionAfterOrEqual(runtime.Version(), 1, 15) {
						// Go1.15 on windows inserts a NOP after the call to
						// runtime.Breakpoint and marks it as line 61 (same as the
						// runtime.Breakpoint call).
						startLineno = 61
					}

					expectStackFrames(t, stack, startLineno, 1000, 4, 4)

					client.ScopesRequest(1000)
					scopes := client.ExpectScopesResponse(t)
					expectScope(t, scopes, 0, "Arguments", 1000)
					expectScope(t, scopes, 1, "Locals", 1001)
					expectScope(t, scopes, 2, "Globals (package main)", 1002)

					// Arguments

					client.VariablesRequest(1000)
					args := client.ExpectVariablesResponse(t)
					expectChildren(t, args, "Arguments", 2)
					expectVarExact(t, args, 0, "baz", `"bazburzum"`, noChildren)
					ref := expectVarExact(t, args, 1, "bar", `<main.FooBar>`, hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						bar := client.ExpectVariablesResponse(t)
						expectChildren(t, bar, "bar", 2)
						expectVarExact(t, bar, 0, "Baz", "10", noChildren)
						expectVarExact(t, bar, 1, "Bur", `"lorem"`, noChildren)
					}

					// Globals

					client.VariablesRequest(1002)
					globals := client.ExpectVariablesResponse(t)
					expectVarExact(t, globals, 0, "p1", "10", noChildren)

					// Locals

					client.VariablesRequest(1001)
					locals := client.ExpectVariablesResponse(t)
					expectChildren(t, locals, "Locals", 29)

					// reflect.Kind == Bool
					expectVarExact(t, locals, -1, "b1", "true", noChildren)
					expectVarExact(t, locals, -1, "b2", "false", noChildren)
					// reflect.Kind == Int
					expectVarExact(t, locals, -1, "a2", "6", noChildren)
					expectVarExact(t, locals, -1, "neg", "-1", noChildren)
					// reflect.Kind == Int8
					expectVarExact(t, locals, -1, "i8", "1", noChildren)
					// reflect.Kind == Int16 - see testvariables2
					// reflect.Kind == Int32 - see testvariables2
					// reflect.Kind == Int64 - see testvariables2
					// reflect.Kind == Uint
					// reflect.Kind == Uint8
					expectVarExact(t, locals, -1, "u8", "255", noChildren)
					// reflect.Kind == Uint16
					expectVarExact(t, locals, -1, "u16", "65535", noChildren)
					// reflect.Kind == Uint32
					expectVarExact(t, locals, -1, "u32", "4294967295", noChildren)
					// reflect.Kind == Uint64
					expectVarExact(t, locals, -1, "u64", "18446744073709551615", noChildren)
					// reflect.Kind == Uintptr
					expectVarExact(t, locals, -1, "up", "5", noChildren)
					// reflect.Kind == Float32
					expectVarExact(t, locals, -1, "f32", "1.2", noChildren)
					// reflect.Kind == Float64
					expectVarExact(t, locals, -1, "a3", "7.23", noChildren)
					// reflect.Kind == Complex64
					ref = expectVarExact(t, locals, -1, "c64", "(1 + 2i)", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						c64 := client.ExpectVariablesResponse(t)
						expectChildren(t, c64, "c64", 2)
						expectVarExact(t, c64, 0, "real", "1", noChildren)
						expectVarExact(t, c64, 1, "imaginary", "2", noChildren)
					}
					// reflect.Kind == Complex128
					ref = expectVarExact(t, locals, -1, "c128", "(2 + 3i)", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						c128 := client.ExpectVariablesResponse(t)
						expectChildren(t, c128, "c128", 2)
						expectVarExact(t, c128, 0, "real", "2", noChildren)
						expectVarExact(t, c128, 1, "imaginary", "3", noChildren)
					}
					// reflect.Kind == Array
					ref = expectVarExact(t, locals, -1, "a4", "<[2]int>", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						a4 := client.ExpectVariablesResponse(t)
						expectChildren(t, a4, "a4", 2)
						expectVarExact(t, a4, 0, "[0]", "1", noChildren)
						expectVarExact(t, a4, 1, "[1]", "2", noChildren)
					}
					ref = expectVarExact(t, locals, -1, "a11", "<[3]main.FooBar>", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						a11 := client.ExpectVariablesResponse(t)
						expectChildren(t, a11, "a11", 3)
						expectVarExact(t, a11, 0, "[0]", "<main.FooBar>", hasChildren)
						ref = expectVarExact(t, a11, 1, "[1]", "<main.FooBar>", hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							a11_1 := client.ExpectVariablesResponse(t)
							expectChildren(t, a11_1, "a11[1]", 2)
							expectVarExact(t, a11_1, 0, "Baz", "2", noChildren)
							expectVarExact(t, a11_1, 1, "Bur", `"b"`, noChildren)

						}
						expectVarExact(t, a11, 2, "[2]", "<main.FooBar>", hasChildren)
					}

					// reflect.Kind == Chan - see testvariables2
					// reflect.Kind == Func - see testvariables2
					// reflect.Kind == Interface - see testvariables2
					// reflect.Kind == Map - see testvariables2
					// reflect.Kind == Ptr
					ref = expectVarRegex(t, locals, -1, "a7", "<\\*main\\.FooBar>\\(0x[0-9a-f]+\\)", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						a7 := client.ExpectVariablesResponse(t)
						expectChildren(t, a7, "a7", 1)
						ref = expectVarExact(t, a7, 0, "", "<main.FooBar>", hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							a7val := client.ExpectVariablesResponse(t)
							expectChildren(t, a7val, "*a7", 2)
							expectVarExact(t, a7val, 0, "Baz", "5", noChildren)
							expectVarExact(t, a7val, 1, "Bur", `"strum"`, noChildren)
						}
					}
					// TODO(polina): how to test for "nil" (without type) and "void"?
					expectVarExact(t, locals, -1, "a9", "nil <*main.FooBar>", noChildren)
					// reflect.Kind == Slice
					ref = expectVarExact(t, locals, -1, "a5", "<[]int> (length: 5, cap: 5)", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						a5 := client.ExpectVariablesResponse(t)
						expectChildren(t, a5, "a5", 5)
						expectVarExact(t, a5, 0, "[0]", "1", noChildren)
						expectVarExact(t, a5, 4, "[4]", "5", noChildren)
					}
					ref = expectVarExact(t, locals, -1, "a12", "<[]main.FooBar> (length: 2, cap: 2)", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						a12 := client.ExpectVariablesResponse(t)
						expectChildren(t, a12, "a12", 2)
						expectVarExact(t, a12, 0, "[0]", "<main.FooBar>", hasChildren)
						ref = expectVarExact(t, a12, 1, "[1]", "<main.FooBar>", hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							a12_1 := client.ExpectVariablesResponse(t)
							expectChildren(t, a12_1, "a12[1]", 2)
							expectVarExact(t, a12_1, 0, "Baz", "5", noChildren)
							expectVarExact(t, a12_1, 1, "Bur", `"e"`, noChildren)
						}
					}
					ref = expectVarExact(t, locals, -1, "a13", "<[]*main.FooBar> (length: 3, cap: 3)", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						a13 := client.ExpectVariablesResponse(t)
						expectChildren(t, a13, "a13", 3)
						expectVarRegex(t, a13, 0, "[0]", "<\\*main\\.FooBar>\\(0x[0-9a-f]+\\)", hasChildren)
						expectVarRegex(t, a13, 1, "[1]", "<\\*main\\.FooBar>\\(0x[0-9a-f]+\\)", hasChildren)
						ref = expectVarRegex(t, a13, 2, "[2]", "<\\*main\\.FooBar>\\(0x[0-9a-f]+\\)", hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							a13_2 := client.ExpectVariablesResponse(t)
							expectChildren(t, a13_2, "a13[2]", 1)
							ref = expectVarExact(t, a13_2, 0, "", "<main.FooBar>", hasChildren)
							if ref > 0 {
								client.VariablesRequest(ref)
								val := client.ExpectVariablesResponse(t)
								expectChildren(t, val, "*a13[2]", 2)
								expectVarExact(t, val, 0, "Baz", "8", noChildren)
								expectVarExact(t, val, 1, "Bur", `"h"`, noChildren)
							}
						}
					}
					// reflect.Kind == String
					expectVarExact(t, locals, -1, "a1", `"foofoofoofoofoofoo"`, noChildren)
					expectVarExact(t, locals, -1, "a10", `"ofo"`, noChildren)
					// reflect.Kind == Struct
					ref = expectVarExact(t, locals, -1, "a6", "<main.FooBar>", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						a6 := client.ExpectVariablesResponse(t)
						expectChildren(t, a6, "a6", 2)
						expectVarExact(t, a6, 0, "Baz", "8", noChildren)
						expectVarExact(t, a6, 1, "Bur", `"word"`, noChildren)
					}
					ref = expectVarExact(t, locals, -1, "a8", "<main.FooBar2>", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						a8 := client.ExpectVariablesResponse(t)
						expectChildren(t, a8, "a8", 2)
						expectVarExact(t, a8, 0, "Bur", "10", noChildren)
						expectVarExact(t, a8, 1, "Baz", `"feh"`, noChildren)
					}
					// reflect.Kind == UnsafePointer - see testvariables2
				},
				disconnect: false,
			}, {
				// Stop at line 25
				execute: func() {
					// Frame ids get reset at each breakpoint.
					client.StackTraceRequest(1, 0, 20)
					stack := client.ExpectStackTraceResponse(t)
					expectStackFrames(t, stack, 25, 1000, 5, 5)

					client.ScopesRequest(1000)
					scopes := client.ExpectScopesResponse(t)
					expectScope(t, scopes, 0, "Arguments", 1000)
					expectScope(t, scopes, 1, "Locals", 1001)
					expectScope(t, scopes, 2, "Globals (package main)", 1002)

					client.ScopesRequest(1111)
					erres := client.ExpectErrorResponse(t)
					if erres.Body.Error.Format != "Unable to list locals: unknown frame id 1111" {
						t.Errorf("\ngot %#v\nwant Format=\"Unable to list locals: unknown frame id 1111\"", erres)
					}

					client.VariablesRequest(1000) // Arguments
					args := client.ExpectVariablesResponse(t)
					expectChildren(t, args, "Arguments", 0)

					client.VariablesRequest(1001) // Locals
					locals := client.ExpectVariablesResponse(t)
					expectChildren(t, locals, "Locals", 1)
					expectVarExact(t, locals, -1, "a1", `"bur"`, noChildren)

					client.VariablesRequest(1002) // Globals
					globals := client.ExpectVariablesResponse(t)
					expectVarExact(t, globals, 0, "p1", "10", noChildren)

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
			[]onBreakpoint{{
				// Stop at line 317
				execute: func() {
					client.StackTraceRequest(1, 0, 20)
					stack := client.ExpectStackTraceResponse(t)
					expectStackFrames(t, stack, 317, 1000, 3, 3)

					client.ScopesRequest(1000)
					scopes := client.ExpectScopesResponse(t)
					expectScope(t, scopes, 0, "Arguments", 1000)
					expectScope(t, scopes, 1, "Locals", 1001)
				},
				disconnect: false,
			}, {
				// Stop at line 322
				execute: func() {
					client.StackTraceRequest(1, 0, 20)
					stack := client.ExpectStackTraceResponse(t)
					expectStackFrames(t, stack, 322, 1000, 3, 3)

					client.ScopesRequest(1000)
					scopes := client.ExpectScopesResponse(t)
					if len(scopes.Body.Scopes) > 2 {
						t.Errorf("\ngot  %#v\nwant len(scopes)=2 (Argumes & Locals)", scopes)
					}
					expectScope(t, scopes, 0, "Arguments", 1000)
					expectScope(t, scopes, 1, "Locals", 1001)

					// Arguments

					client.VariablesRequest(1000)
					args := client.ExpectVariablesResponse(t)
					expectChildren(t, args, "Arguments", 0)

					// Locals

					client.VariablesRequest(1001)
					locals := client.ExpectVariablesResponse(t)

					// reflect.Kind == Bool - see testvariables
					// reflect.Kind == Int - see testvariables
					// reflect.Kind == Int8
					expectVarExact(t, locals, -1, "ni8", "-5", noChildren)
					// reflect.Kind == Int16
					expectVarExact(t, locals, -1, "ni16", "-5", noChildren)
					// reflect.Kind == Int32
					expectVarExact(t, locals, -1, "ni32", "-5", noChildren)
					// reflect.Kind == Int64
					expectVarExact(t, locals, -1, "ni64", "-5", noChildren)
					// reflect.Kind == Uint
					// reflect.Kind == Uint8 - see testvariables
					// reflect.Kind == Uint16 - see testvariables
					// reflect.Kind == Uint32 - see testvariables
					// reflect.Kind == Uint64 - see testvariables
					// reflect.Kind == Uintptr - see testvariables
					// reflect.Kind == Float32 - see testvariables
					// reflect.Kind == Float64
					expectVarExact(t, locals, -1, "pinf", "+Inf", noChildren)
					expectVarExact(t, locals, -1, "ninf", "-Inf", noChildren)
					expectVarExact(t, locals, -1, "nan", "NaN", noChildren)
					// reflect.Kind == Complex64 - see testvariables
					// reflect.Kind == Complex128 - see testvariables
					// reflect.Kind == Array
					expectVarExact(t, locals, -1, "a0", "<[0]int>", noChildren)
					// reflect.Kind == Chan
					ref := expectVarExact(t, locals, -1, "ch1", "<chan int>", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						ch1 := client.ExpectVariablesResponse(t)
						expectChildren(t, ch1, "ch1", 11)
						expectVarExact(t, ch1, 0, "qcount", "4", noChildren)
						expectVarExact(t, ch1, 10, "lock", "<runtime.mutex>", hasChildren)
					}
					expectVarExact(t, locals, -1, "chnil", "nil <chan int>", noChildren)
					// reflect.Kind == Func
					expectVarExact(t, locals, -1, "fn1", "main.afunc", noChildren)
					expectVarExact(t, locals, -1, "fn2", "<main.functype>", noChildren)
					// reflect.Kind == Interface
					expectVarExact(t, locals, -1, "ifacenil", "nil <interface {}>", noChildren)
					ref = expectVarExact(t, locals, -1, "iface2", "<interface {}(string)>", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						iface2 := client.ExpectVariablesResponse(t)
						expectChildren(t, iface2, "iface2", 1)
						expectVarExact(t, iface2, 0, "data", `"test"`, noChildren)
					}
					ref = expectVarExact(t, locals, -1, "iface4", "<interface {}([]go/constant.Value)>", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						iface4 := client.ExpectVariablesResponse(t)
						expectChildren(t, iface4, "iface4", 1)
						ref = expectVarExact(t, iface4, 0, "data", "<[]go/constant.Value> (length: 1, cap: 1)", hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							iface4data := client.ExpectVariablesResponse(t)
							expectChildren(t, iface4data, "iface4.data", 1)
							expectVarExact(t, iface4data, 0, "[0]", "<go/constant.Value(go/constant.int64Val)>", hasChildren)

						}
					}
					expectVarExact(t, locals, -1, "errnil", "nil <error>", noChildren)
					expectVarExact(t, locals, -1, "err1", "<error(*main.astruct)>", hasChildren)
					// reflect.Kind == Map
					expectVarExact(t, locals, -1, "mnil", "nil <map[string]main.astruct>", noChildren)
					ref = expectVarExact(t, locals, -1, "m2", "<map[int]*main.astruct> (length: 1)", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						m2 := client.ExpectVariablesResponse(t)
						expectChildren(t, m2, "m2", 1)
						ref = expectVarRegex(t, m2, 0, "1", "<\\*main\\.astruct>\\(0x[0-9a-f]+\\)", hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							m2_1 := client.ExpectVariablesResponse(t)
							expectChildren(t, m2_1, "m2[1]", 1)
							ref = expectVarExact(t, m2_1, 0, "", "<main.astruct>", hasChildren)
							if ref > 0 {
								client.VariablesRequest(ref)
								m2_1val := client.ExpectVariablesResponse(t)
								expectChildren(t, m2_1val, "*m2[1]", 2)
								expectVarExact(t, m2_1val, 0, "A", "10", noChildren)
								expectVarExact(t, m2_1val, 1, "B", "11", noChildren)
							}
						}
					}
					ref = expectVarExact(t, locals, -1, "m3", "<map[main.astruct]int> (length: 2)", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						m3 := client.ExpectVariablesResponse(t)
						expectChildren(t, m3, "m3", 2)
						ref = expectVarExact(t, m3, 0, "<main.astruct>[0]", "42", hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							m3_0 := client.ExpectVariablesResponse(t)
							expectChildren(t, m3_0, "m3[0]", 2)
							expectVarExact(t, m3_0, 0, "A", "1", noChildren)
							expectVarExact(t, m3_0, 1, "B", "1", noChildren)
						}
						ref = expectVarExact(t, m3, 1, "<main.astruct>[1]", "43", hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							m3_1 := client.ExpectVariablesResponse(t)
							expectChildren(t, m3_1, "m3[1]", 2)
							expectVarExact(t, m3_1, 0, "A", "2", noChildren)
							expectVarExact(t, m3_1, 1, "B", "2", noChildren)
						}
					}
					ref = expectVarExact(t, locals, -1, "m4", "<map[main.astruct]main.astruct> (length: 2)", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						m4 := client.ExpectVariablesResponse(t)
						expectChildren(t, m4, "m4", 4)
						expectVarExact(t, m4, 0, "[key 0]", "<main.astruct>", hasChildren)
						expectVarExact(t, m4, 1, "[val 0]", "<main.astruct>", hasChildren)
						ref = expectVarExact(t, m4, 2, "[key 1]", "<main.astruct>", hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							m4Key1 := client.ExpectVariablesResponse(t)
							expectChildren(t, m4Key1, "m4Key1", 2)
							expectVarExact(t, m4Key1, 0, "A", "2", noChildren)
							expectVarExact(t, m4Key1, 1, "B", "2", noChildren)
						}
						ref = expectVarExact(t, m4, 3, "[val 1]", "<main.astruct>", hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							m4Val1 := client.ExpectVariablesResponse(t)
							expectChildren(t, m4Val1, "m4Val1", 2)
							expectVarExact(t, m4Val1, 0, "A", "22", noChildren)
							expectVarExact(t, m4Val1, 1, "B", "22", noChildren)
						}
					}
					expectVarExact(t, locals, -1, "emptymap", "<map[string]string> (length: 0)", noChildren)
					// reflect.Kind == Ptr - see testvariables
					// reflect.Kind == Slice
					ref = expectVarExact(t, locals, -1, "zsslice", "<[]struct {}> (length: 3, cap: 3)", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						zsslice := client.ExpectVariablesResponse(t)
						expectChildren(t, zsslice, "zsslice", 3)
					}
					expectVarExact(t, locals, -1, "emptyslice", "<[]string> (length: 0, cap: 0)", noChildren)
					expectVarExact(t, locals, -1, "nilslice", "nil <[]int>", noChildren)
					// reflect.Kind == String
					expectVarExact(t, locals, -1, "longstr", "\"very long string 0123456789a0123456789b0123456789c0123456789d012...+73 more\"", noChildren)
					// reflect.Kind == Struct
					expectVarExact(t, locals, -1, "zsvar", "<struct {}>", noChildren)
					// reflect.Kind == UnsafePointer
					// TODO(polina): how do I test for unsafe.Pointer(nil)?
					expectVarRegex(t, locals, -1, "upnil", "unsafe\\.Pointer\\(0x0\\)", noChildren)
					expectVarRegex(t, locals, -1, "up1", "unsafe\\.Pointer\\(0x[0-9a-f]+\\)", noChildren)

					// Test unreadable variable
					ref = expectVarExact(t, locals, -1, "unread", "<*int>(0x3039)", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						val := client.ExpectVariablesResponse(t)
						expectChildren(t, val, "*unread", 1)
						expectVarRegex(t, val, 0, "", "unreadable <.+>", noChildren)
					}

					// Test that variables are not yet loaded completely.
					ref = expectVarExact(t, locals, -1, "m1", "<map[string]main.astruct> (length: 66)", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						m1 := client.ExpectVariablesResponse(t)
						expectChildren(t, m1, "m1", 64) // TODO(polina): should be 66.
					}
				},
				disconnect: true,
			}})
	})
}

// TestGlobalScopeAndVariables launches the program with showGlobalVariables
// arg set, executes to a breakpoint in the main package and tests that global
// package main variables got loaded. It then steps into a function
// in another package and tests that globals scope got updated to those vars.
func TestGlobalScopeAndVariables(t *testing.T) {
	runTest(t, "consts", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client,
			// Launch
			func() {
				client.LaunchRequestWithArgs(map[string]interface{}{
					"mode": "exec", "program": fixture.Path, "showGlobalVariables": true,
				})
			},
			// Breakpoints are set within the program
			fixture.Source, []int{},
			[]onBreakpoint{{
				// Stop at line 36
				execute: func() {
					client.StackTraceRequest(1, 0, 20)
					stack := client.ExpectStackTraceResponse(t)
					expectStackFrames(t, stack, 36, 1000, 3, 3)

					client.ScopesRequest(1000)
					scopes := client.ExpectScopesResponse(t)
					expectScope(t, scopes, 0, "Arguments", 1000)
					expectScope(t, scopes, 1, "Locals", 1001)
					expectScope(t, scopes, 2, "Globals (package main)", 1002)

					client.VariablesRequest(1002)
					client.ExpectVariablesResponse(t)
					// The program has no user-defined globals.
					// Depending on the Go version, there might
					// be some runtime globals (e.g. main..inittask)
					// so testing for the total number is too fragile.

					// Step into pkg.AnotherMethod()
					client.StepInRequest(1)
					client.ExpectStepInResponse(t)
					client.ExpectStoppedEvent(t)

					client.StackTraceRequest(1, 0, 20)
					stack = client.ExpectStackTraceResponse(t)
					expectStackFrames(t, stack, 14, 1000, 4, 4)

					client.ScopesRequest(1000)
					scopes = client.ExpectScopesResponse(t)
					expectScope(t, scopes, 0, "Arguments", 1000)
					expectScope(t, scopes, 1, "Locals", 1001)
					expectScope(t, scopes, 2, "Globals (package github.com/go-delve/delve/_fixtures/internal/dir0/pkg)", 1002)

					client.VariablesRequest(1002)
					globals := client.ExpectVariablesResponse(t)
					expectChildren(t, globals, "Globals", 1)
					ref := expectVarExact(t, globals, 0, "SomeVar", "<github.com/go-delve/delve/_fixtures/internal/dir0/pkg.SomeType>", hasChildren)

					if ref > 0 {
						client.VariablesRequest(ref)
						somevar := client.ExpectVariablesResponse(t)
						expectChildren(t, somevar, "SomeVar", 1)
						expectVarExact(t, somevar, 0, "X", "0", noChildren)
					}
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
			[]onBreakpoint{{ // Stop at line 8
				execute: func() {
					client.StackTraceRequest(1, 0, 0)
					stResp = client.ExpectStackTraceResponse(t)
					expectStackFrames(t, stResp, 8, 1000, 2, 2)
				},
				disconnect: false,
			}})
	})
}

func TestNextAndStep(t *testing.T) {
	runTest(t, "testinline", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client,
			// Launch
			func() {
				client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
			},
			// Set breakpoints
			fixture.Source, []int{11},
			[]onBreakpoint{{ // Stop at line 11
				execute: func() {
					handleStop(t, client, 1, 11)

					expectStop := func(line int) {
						t.Helper()
						se := client.ExpectStoppedEvent(t)
						if se.Body.Reason != "step" || se.Body.ThreadId != 1 || !se.Body.AllThreadsStopped {
							t.Errorf("got %#v, want Reason=\"step\", ThreadId=1, AllThreadsStopped=true", se)
						}
						handleStop(t, client, 1, line)
					}

					client.StepOutRequest(1)
					client.ExpectStepOutResponse(t)
					expectStop(18)

					client.NextRequest(1)
					client.ExpectNextResponse(t)
					expectStop(19)

					client.StepInRequest(1)
					client.ExpectStepInResponse(t)
					expectStop(5)

					client.NextRequest(-10000 /*this is ignored*/)
					client.ExpectNextResponse(t)
					expectStop(6)
				},
				disconnect: false,
			}})
	})
}

func TestBadAccess(t *testing.T) {
	if runtime.GOOS != "darwin" || testBackend != "lldb" {
		t.Skip("not applicable")
	}
	runTest(t, "issue2078", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client,
			// Launch
			func() {
				client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
			},
			// Set breakpoints
			fixture.Source, []int{4},
			[]onBreakpoint{{ // Stop at line 4
				execute: func() {
					handleStop(t, client, 1, 4)

					expectStoppedOnError := func(errorPrefix string) {
						t.Helper()
						se := client.ExpectStoppedEvent(t)
						if se.Body.ThreadId != 1 || se.Body.Reason != "runtime error" || !strings.HasPrefix(se.Body.Text, errorPrefix) {
							t.Errorf("\ngot  %#v\nwant ThreadId=1 Reason=\"runtime error\" Text=\"%s\"", se, errorPrefix)
						}
						oe := client.ExpectOutputEvent(t)
						if oe.Body.Category != "stderr" || !strings.HasPrefix(oe.Body.Output, "ERROR: "+errorPrefix) {
							t.Errorf("\ngot  %#v\nwant Category=\"stderr\" Output=\"%s ...\"", oe, errorPrefix)
						}
					}

					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)
					expectStoppedOnError("invalid memory address or nil pointer dereference")

					client.NextRequest(1)
					client.ExpectNextResponse(t)
					expectStoppedOnError("invalid memory address or nil pointer dereference")

					client.NextRequest(1)
					client.ExpectNextResponse(t)
					expectStoppedOnError("next while nexting")

					client.StepInRequest(1)
					client.ExpectStepInResponse(t)
					expectStoppedOnError("next while nexting")

					client.StepOutRequest(1)
					client.ExpectStepOutResponse(t)
					expectStoppedOnError("next while nexting")
				},
				disconnect: true,
			}})
	})
}

func TestPanicBreakpointOnContinue(t *testing.T) {
	runTest(t, "panic", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client,
			// Launch
			func() {
				client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
			},
			// Set breakpoints
			fixture.Source, []int{5},
			[]onBreakpoint{{
				execute: func() {
					handleStop(t, client, 1, 5)

					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)

					se := client.ExpectStoppedEvent(t)
					if se.Body.ThreadId != 1 || se.Body.Reason != "panic" {
						t.Errorf("\ngot  %#v\nwant ThreadId=1 Reason=\"panic\"", se)
					}
				},
				disconnect: true,
			}})
	})
}

func TestPanicBreakpointOnNext(t *testing.T) {
	runTest(t, "panic", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client,
			// Launch
			func() {
				client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
			},
			// Set breakpoints
			fixture.Source, []int{5},
			[]onBreakpoint{{
				execute: func() {
					handleStop(t, client, 1, 5)

					// Send a next request until the panic breakpoint is hit.
					// We expect to hit the unrecovered panic breakpoint and
					// return a "panic" stopped event.
					// In Go 1.13, 'next' will step into the defer in the runtime
					// main function. This will take multiple steps to reach the
					// unrecovered panic breakpoint.
					for {
						client.NextRequest(1)
						client.ExpectNextResponse(t)

						m, err := client.ReadMessage()
						if err != nil {
							t.Error(err)
						}
						if se, ok := m.(*dap.StoppedEvent); ok {
							if se.Body.ThreadId == 1 && se.Body.Reason == "panic" {
								// Successfully reached the unrecovered panic breakpoint.
								break
							} else if se.Body.ThreadId != 1 || se.Body.Reason != "step" {
								t.Errorf("\ngot  %#v\nexpected ThreadId=1 Reason=\"panic\" or ThreadId=1 Reason=\"step\"", se)
							}
						} else if _, ok := m.(*dap.TerminatedEvent); ok {
							t.Errorf("\nended program without \"panic\" event.")
							break
						}
					}
				},
				disconnect: true,
			}})
	})
}

func TestFatalThrowBreakpoint(t *testing.T) {
	runTest(t, "testdeadlock", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client,
			// Launch
			func() {
				client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
			},
			// Set breakpoints
			fixture.Source, []int{3},
			[]onBreakpoint{{
				execute: func() {
					handleStop(t, client, 1, 3)

					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)

					se := client.ExpectStoppedEvent(t)
					if se.Body.Reason != "fatal error" {
						t.Errorf("\ngot  %#v\nwant Reason=\"fatal error\"", se)
					}
				},
				disconnect: true,
			}})
	})
}

// handleStop covers the standard sequence of reqeusts issued by
// a client at a breakpoint or another non-terminal stop event.
// The details have been tested by other tests,
// so this is just a sanity check.
func handleStop(t *testing.T, client *daptest.Client, thread, line int) {
	t.Helper()
	client.ThreadsRequest()
	client.ExpectThreadsResponse(t)

	client.StackTraceRequest(thread, 0, 20)
	st := client.ExpectStackTraceResponse(t)
	if len(st.Body.StackFrames) < 1 || st.Body.StackFrames[0].Line != line {
		t.Errorf("\ngot  %#v\nwant Line=%d", st, line)
	}

	client.ScopesRequest(1000)
	client.ExpectScopesResponse(t)

	client.VariablesRequest(1000) // Arguments
	client.ExpectVariablesResponse(t)
	client.VariablesRequest(1001) // Locals
	client.ExpectVariablesResponse(t)
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

	// TODO(polina): See if we can make this more like withTestProcessArgs in proc_test:
	// a single function pointer gets called here and then if it wants to continue it calls
	// client.ContinueRequest/client.ExpectContinueResponse/client.ExpectStoppedEvent
	// (possibly using a helper function).
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
