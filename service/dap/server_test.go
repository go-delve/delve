package dap

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"path/filepath"
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
	var buildFlags protest.BuildFlags = protest.AllNonOptimized
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

// TestLaunchStopOnEntry emulates the message exchange that can be observed with
// VS Code for the most basic launch debug session with "stopOnEntry" enabled:
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
//                                 :  8 << error (Unable to produce stack trace)
//                                 :  9 >> stackTrace
//                                 :  9 << error (Unable to produce stack trace)
// - User evaluates bad expression : 10 >> evaluate
//                                 : 10 << error (unable to find function context)
// - User evaluates good expression: 11 >> evaluate
//                                 : 11 << evaluate
// - User selects "Continue"       : 12 >> continue
//                                 : 12 << continue
// - Program runs to completion    :    << terminated event
//                                 : 13 >> disconnect
//                                 : 13 << disconnect
// This test exhaustively tests Seq and RequestSeq on all messages from the
// server. Other tests do not necessarily need to repeat all these checks.
func TestLaunchStopOnEntry(t *testing.T) {
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

		// 8 >> stackTrace, << error
		client.StackTraceRequest(1, 0, 20)
		stResp := client.ExpectErrorResponse(t)
		if stResp.Seq != 0 || stResp.RequestSeq != 8 || stResp.Body.Error.Format != "Unable to produce stack trace: unknown goroutine 1" {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=8 Format=\"Unable to produce stack trace: unknown goroutine 1\"", stResp)
		}

		// 9 >> stackTrace, << error
		client.StackTraceRequest(1, 0, 20)
		stResp = client.ExpectErrorResponse(t)
		if stResp.Seq != 0 || stResp.RequestSeq != 9 || stResp.Body.Error.Id != 2004 {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=9 Id=2004", stResp)
		}

		// 10 >> evaluate, << error
		client.EvaluateRequest("foo", 0 /*no frame specified*/, "repl")
		erResp := client.ExpectVisibleErrorResponse(t)
		if erResp.Seq != 0 || erResp.RequestSeq != 10 || erResp.Body.Error.Id != 2009 {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=10 Id=2009", erResp)
		}

		// 11 >> evaluate, << evaluate
		client.EvaluateRequest("1+1", 0 /*no frame specified*/, "repl")
		evResp := client.ExpectEvaluateResponse(t)
		if evResp.Seq != 0 || evResp.RequestSeq != 11 || evResp.Body.Result != "2" {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=10 Result=2", evResp)
		}

		// 12 >> continue, << continue, << terminated
		client.ContinueRequest(1)
		contResp := client.ExpectContinueResponse(t)
		if contResp.Seq != 0 || contResp.RequestSeq != 12 || !contResp.Body.AllThreadsContinued {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=12 Body.AllThreadsContinued=true", contResp)
		}
		termEvent := client.ExpectTerminatedEvent(t)
		if termEvent.Seq != 0 {
			t.Errorf("\ngot %#v\nwant Seq=0", termEvent)
		}

		// 13 >> disconnect, << disconnect
		client.DisconnectRequest()
		dResp := client.ExpectDisconnectResponse(t)
		if dResp.Seq != 0 || dResp.RequestSeq != 13 {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=13", dResp)
		}
	})
}

// TestAttachStopOnEntry is like TestLaunchStopOnEntry, but with attach request.
func TestAttachStopOnEntry(t *testing.T) {
	if runtime.GOOS == "freebsd" {
		t.SkipNow()
	}
	runTest(t, "loopprog", func(client *daptest.Client, fixture protest.Fixture) {
		// Start the program to attach to
		cmd := exec.Command(fixture.Path)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		// Wait for output.
		// This will give the target process time to initialize the runtime before we attach,
		// so we can rely on having goroutines when they are requested on attach.
		scanOut := bufio.NewScanner(stdout)
		scanOut.Scan()
		if scanOut.Text() != "past main" {
			t.Errorf("expected loopprog.go to output \"past main\"")
		}

		// 1 >> initialize, << initialize
		client.InitializeRequest()
		initResp := client.ExpectInitializeResponse(t)
		if initResp.Seq != 0 || initResp.RequestSeq != 1 {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=1", initResp)
		}

		// 2 >> attach, << initialized, << attach
		client.AttachRequest(
			map[string]interface{}{"mode": "local", "processId": cmd.Process.Pid, "stopOnEntry": true})
		initEvent := client.ExpectInitializedEvent(t)
		if initEvent.Seq != 0 {
			t.Errorf("\ngot %#v\nwant Seq=0", initEvent)
		}
		attachResp := client.ExpectAttachResponse(t)
		if attachResp.Seq != 0 || attachResp.RequestSeq != 2 {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=2", attachResp)
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
		// Expect main goroutine plus runtime at this point.
		if tResp.Seq != 0 || tResp.RequestSeq != 6 || len(tResp.Body.Threads) < 2 {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=6 len(Threads)>1", tResp)
		}

		// 7 >> threads, << threads
		client.ThreadsRequest()
		client.ExpectThreadsResponse(t)

		// 8 >> stackTrace, << response
		client.StackTraceRequest(1, 0, 20)
		client.ExpectStackTraceResponse(t)

		// 9 >> stackTrace, << response
		client.StackTraceRequest(1, 0, 20)
		client.ExpectStackTraceResponse(t)

		// 10 >> evaluate, << error
		client.EvaluateRequest("foo", 0 /*no frame specified*/, "repl")
		erResp := client.ExpectVisibleErrorResponse(t)
		if erResp.Seq != 0 || erResp.RequestSeq != 10 || erResp.Body.Error.Id != 2009 {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=10 Id=2009", erResp)
		}

		// 11 >> evaluate, << evaluate
		client.EvaluateRequest("1+1", 0 /*no frame specified*/, "repl")
		evResp := client.ExpectEvaluateResponse(t)
		if evResp.Seq != 0 || evResp.RequestSeq != 11 || evResp.Body.Result != "2" {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=10 Result=2", evResp)
		}

		// We cannot contiue here like in the launch case becase the program
		// will never terminate. Since we won't receive a terminate event and
		// there are no breakpoints to stop on, we just detach.

		// TODO(polina): once https://github.com/go-delve/delve/issues/2259 is
		// fixed, test with kill=false.

		// 12 >> disconnect, << disconnect
		client.DisconnectRequestWithKillOption(true)
		dResp := client.ExpectDisconnectResponse(t)
		if dResp.Seq != 0 || dResp.RequestSeq != 12 {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=12", dResp)
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

// TestPreSetBreakpoint corresponds to a debug session that is configured to
// continue on entry with a pre-set breakpoint.
func TestPreSetBreakpoint(t *testing.T) {
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		client.InitializeRequest()
		client.ExpectInitializeResponse(t)

		client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
		client.ExpectInitializedEvent(t)
		client.ExpectLaunchResponse(t)

		client.SetBreakpointsRequest(fixture.Source, []int{8})
		sResp := client.ExpectSetBreakpointsResponse(t)
		if len(sResp.Body.Breakpoints) != 1 {
			t.Errorf("got %#v, want len(Breakpoints)=1", sResp)
		}
		bkpt0 := sResp.Body.Breakpoints[0]
		if !bkpt0.Verified || bkpt0.Line != 8 || bkpt0.Id != 1 || bkpt0.Source.Name != filepath.Base(fixture.Source) || bkpt0.Source.Path != fixture.Source {
			t.Errorf("got breakpoints[0] = %#v, want Verified=true, Line=8, Id=1, Path=%q", bkpt0, fixture.Source)
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
		reMain, _ := regexp.Compile(`\* \[Go 1\] main.Increment \(Thread [0-9]+\)`)
		wantMain := dap.Thread{Id: 1, Name: "* [Go 1] main.Increment (Thread ...)"}
		wantRuntime := dap.Thread{Id: 2, Name: "[Go 2] runtime.gopark"}
		for _, got := range tResp.Body.Threads {
			if got.Id != 1 && !reMain.MatchString(got.Name) && !strings.Contains(got.Name, "runtime") {
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
		expectVarExact(t, args, 0, "y", "y", "0", noChildren)
		expectVarExact(t, args, 1, "~r1", "", "0", noChildren)

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
//     wantStartName - name of the first returned frame (ignored if "")
//     wantStartLine - file line of the first returned frame (ignored if <0).
//     wantStartID - id of the first frame returned (ignored if wantFrames is 0).
//     wantFrames - number of frames returned (length of StackTraceResponse.Body.StackFrames array).
//     wantTotalFrames - total number of stack frames available (StackTraceResponse.Body.TotalFrames).
func expectStackFrames(t *testing.T, got *dap.StackTraceResponse,
	wantStartName string, wantStartLine, wantStartID, wantFrames, wantTotalFrames int) {
	t.Helper()
	expectStackFramesNamed("", t, got, wantStartName, wantStartLine, wantStartID, wantFrames, wantTotalFrames)
}

func expectStackFramesNamed(testName string, t *testing.T, got *dap.StackTraceResponse,
	wantStartName string, wantStartLine, wantStartID, wantFrames, wantTotalFrames int) {
	t.Helper()
	if got.Body.TotalFrames != wantTotalFrames {
		t.Errorf("%s\ngot  %#v\nwant TotalFrames=%d", testName, got.Body.TotalFrames, wantTotalFrames)
	}
	if len(got.Body.StackFrames) != wantFrames {
		t.Errorf("%s\ngot  len(StackFrames)=%d\nwant %d", testName, len(got.Body.StackFrames), wantFrames)
	} else {
		// Verify that frame ids are consecutive numbers starting at wantStartID
		for i := 0; i < wantFrames; i++ {
			if got.Body.StackFrames[i].Id != wantStartID+i {
				t.Errorf("%s\ngot  %#v\nwant Id=%d", testName, got.Body.StackFrames[i], wantStartID+i)
			}
		}
		// Verify the name and line corresponding to the first returned frame (if any).
		// This is useful when the first frame is the frame corresponding to the breakpoint at
		// a predefined line. Line values < 0 are a signal to skip the check (which can be useful
		// for frames in the third-party code, where we do not control the lines).
		if wantFrames > 0 && wantStartLine > 0 && got.Body.StackFrames[0].Line != wantStartLine {
			t.Errorf("%s\ngot  Line=%d\nwant %d", testName, got.Body.StackFrames[0].Line, wantStartLine)
		}
		if wantFrames > 0 && wantStartName != "" && got.Body.StackFrames[0].Name != wantStartName {
			t.Errorf("%s\ngot  Name=%s\nwant %s", testName, got.Body.StackFrames[0].Name, wantStartName)
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
//     evalName - fully qualified variable name or alternative expression to load this variable
//     value - the value of the variable
//     useExactMatch - true if name, evalName and value are to be compared to exactly, false if to be used as regex
//     hasRef - true if the variable should have children and therefore a non-0 variable reference
//     ref - reference to retrieve children of this variable (0 if none)
func expectVar(t *testing.T, got *dap.VariablesResponse, i int, name, evalName, value string, useExactMatch, hasRef bool) (ref int) {
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
	matchedName := false
	if useExactMatch {
		matchedName = (goti.Name == name)
	} else {
		matchedName, _ = regexp.MatchString(name, goti.Name)
	}
	if !matchedName || (goti.VariablesReference > 0) != hasRef {
		t.Errorf("\ngot  %#v\nwant Name=%q hasRef=%t", goti, name, hasRef)
	}
	matchedEvalName := false
	if useExactMatch {
		matchedEvalName = (goti.EvaluateName == evalName)
	} else {
		matchedEvalName, _ = regexp.MatchString(evalName, goti.EvaluateName)
	}
	if !matchedEvalName {
		t.Errorf("\ngot  %q\nwant EvaluateName=%q", goti.EvaluateName, evalName)
	}
	matchedValue := false
	if useExactMatch {
		matchedValue = (goti.Value == value)
	} else {
		matchedValue, _ = regexp.MatchString(value, goti.Value)
	}
	if !matchedValue {
		t.Errorf("\ngot  %s=%q\nwant %q", name, goti.Value, value)
	}
	return goti.VariablesReference
}

// expectVarExact is a helper like expectVar that matches value exactly.
func expectVarExact(t *testing.T, got *dap.VariablesResponse, i int, name, evalName, value string, hasRef bool) (ref int) {
	t.Helper()
	return expectVar(t, got, i, name, evalName, value, true, hasRef)
}

// expectVarRegex is a helper like expectVar that treats value, evalName or name as a regex.
func expectVarRegex(t *testing.T, got *dap.VariablesResponse, i int, name, evalName, value string, hasRef bool) (ref int) {
	t.Helper()
	return expectVar(t, got, i, name, evalName, value, false, hasRef)
}

// validateEvaluateName issues an evaluate request with evaluateName of a variable and
// confirms that it succeeds and returns the same variable record as the original.
func validateEvaluateName(t *testing.T, client *daptest.Client, got *dap.VariablesResponse, i int) {
	t.Helper()
	original := got.Body.Variables[i]
	client.EvaluateRequest(original.EvaluateName, 1000, "this context will be ignored")
	validated := client.ExpectEvaluateResponse(t)
	if original.VariablesReference == 0 && validated.Body.VariablesReference != 0 ||
		original.VariablesReference != 0 && validated.Body.VariablesReference == 0 {
		t.Errorf("\ngot  varref=%d\nwant %d", validated.Body.VariablesReference, original.VariablesReference)
	}
	// The variable might not be fully loaded, and when we reload it with an expression
	// more of the subvalues might be revealed, so we must match the loaded prefix only.
	if strings.Contains(original.Value, "...") {
		origLoaded := strings.Split(original.Value, "...")[0]
		if !strings.HasPrefix(validated.Body.Result, origLoaded) {
			t.Errorf("\ngot  value=%q\nwant %q", validated.Body.Result, original.Value)
		}
	} else if original.Value != validated.Body.Result {
		t.Errorf("\ngot  value=%q\nwant %q", validated.Body.Result, original.Value)
	}
}

// TestStackTraceRequest executes to a breakpoint and tests different
// good and bad configurations of 'stackTrace' requests.
func TestStackTraceRequest(t *testing.T) {
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		var stResp *dap.StackTraceResponse
		const StartHandle = 1000 // from handles.go
		runDebugSessionWithBPs(t, client, "launch",
			// Launch
			func() {
				client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
			},
			// Set breakpoints
			fixture.Source, []int{8, 18},
			[]onBreakpoint{{
				// Stop at line 8
				execute: func() {
					// Even though the stack frames do not change,
					// repeated requests at the same breakpoint
					// would assign next block of unique ids to them each time.
					const NumFrames = 6
					reqIndex := -1
					frameID := func(frameIndex int) int {
						reqIndex++
						return startHandle + NumFrames*reqIndex + frameIndex
					}

					tests := map[string]struct {
						startFrame          int
						levels              int
						wantStartName       string
						wantStartLine       int
						wantStartFrame      int
						wantFramesReturned  int
						wantFramesAvailable int
					}{
						"all frame levels from 0 to NumFrames":    {0, NumFrames, "main.Increment", 8, 0, NumFrames, NumFrames},
						"subset of frames from 1 to -1":           {1, NumFrames - 1, "main.Increment", 11, 1, NumFrames - 1, NumFrames},
						"load stack in pages: first half":         {0, NumFrames / 2, "main.Increment", 8, 0, NumFrames / 2, NumFrames},
						"load stack in pages: second half":        {NumFrames / 2, NumFrames, "main.main", 17, NumFrames / 2, NumFrames / 2, NumFrames},
						"zero levels means all levels":            {0, 0, "main.Increment", 8, 0, NumFrames, NumFrames},
						"zero levels means all remaining levels":  {NumFrames / 2, 0, "main.main", 17, NumFrames / 2, NumFrames / 2, NumFrames},
						"negative levels treated as 0 (all)":      {0, -10, "main.Increment", 8, 0, NumFrames, NumFrames},
						"OOB levels is capped at available len":   {0, NumFrames + 1, "main.Increment", 8, 0, NumFrames, NumFrames},
						"OOB levels is capped at available len 1": {1, NumFrames + 1, "main.Increment", 11, 1, NumFrames - 1, NumFrames},
						"negative startFrame treated as 0":        {-10, 0, "main.Increment", 8, 0, NumFrames, NumFrames},
						"OOB startFrame returns empty trace":      {NumFrames, 0, "main.Increment", -1, -1, 0, NumFrames},
					}
					for name, tc := range tests {
						client.StackTraceRequest(1, tc.startFrame, tc.levels)
						stResp = client.ExpectStackTraceResponse(t)
						expectStackFramesNamed(name, t, stResp,
							tc.wantStartName, tc.wantStartLine, frameID(tc.wantStartFrame), tc.wantFramesReturned, tc.wantFramesAvailable)
					}
				},
				disconnect: false,
			}, {
				// Stop at line 18
				execute: func() {
					// Frame ids get reset at each breakpoint.
					client.StackTraceRequest(1, 0, 0)
					stResp = client.ExpectStackTraceResponse(t)
					expectStackFrames(t, stResp, "main.main", 18, startHandle, 3, 3)
				},
				disconnect: false,
			}})
	})
}

// TestScopesAndVariablesRequests executes to a breakpoint and tests different
// configurations of 'scopes' and 'variables' requests.
func TestScopesAndVariablesRequests(t *testing.T) {
	runTest(t, "testvariables", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client, "launch",
			// Launch
			func() {
				client.LaunchRequestWithArgs(map[string]interface{}{
					"mode": "exec", "program": fixture.Path, "showGlobalVariables": true,
				})
			},
			// Breakpoints are set within the program
			fixture.Source, []int{},
			[]onBreakpoint{{
				// Stop at first breakpoint
				execute: func() {
					client.StackTraceRequest(1, 0, 20)
					stack := client.ExpectStackTraceResponse(t)

					startLineno := 66
					if runtime.GOOS == "windows" && goversion.VersionAfterOrEqual(runtime.Version(), 1, 15) {
						// Go1.15 on windows inserts a NOP after the call to
						// runtime.Breakpoint and marks it same line as the
						// runtime.Breakpoint call, making this flaky, so skip the line check.
						startLineno = -1
					}

					expectStackFrames(t, stack, "main.foobar", startLineno, 1000, 4, 4)

					client.ScopesRequest(1000)
					scopes := client.ExpectScopesResponse(t)
					expectScope(t, scopes, 0, "Arguments", 1000)
					expectScope(t, scopes, 1, "Locals", 1001)
					expectScope(t, scopes, 2, "Globals (package main)", 1002)

					// Arguments

					client.VariablesRequest(1000)
					args := client.ExpectVariablesResponse(t)
					expectChildren(t, args, "Arguments", 2)
					expectVarExact(t, args, 0, "baz", "baz", `"bazburzum"`, noChildren)
					ref := expectVarExact(t, args, 1, "bar", "bar", `main.FooBar {Baz: 10, Bur: "lorem"}`, hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						bar := client.ExpectVariablesResponse(t)
						expectChildren(t, bar, "bar", 2)
						expectVarExact(t, bar, 0, "Baz", "bar.Baz", "10", noChildren)
						expectVarExact(t, bar, 1, "Bur", "bar.Bur", `"lorem"`, noChildren)
						validateEvaluateName(t, client, bar, 0)
						validateEvaluateName(t, client, bar, 1)
					}

					// Globals

					client.VariablesRequest(1002)
					globals := client.ExpectVariablesResponse(t)
					expectVarExact(t, globals, 0, "p1", "main.p1", "10", noChildren)

					// Locals

					client.VariablesRequest(1001)
					locals := client.ExpectVariablesResponse(t)
					expectChildren(t, locals, "Locals", 31)

					// reflect.Kind == Bool
					expectVarExact(t, locals, -1, "b1", "b1", "true", noChildren)
					expectVarExact(t, locals, -1, "b2", "b2", "false", noChildren)
					// reflect.Kind == Int
					expectVarExact(t, locals, -1, "a2", "a2", "6", noChildren)
					expectVarExact(t, locals, -1, "neg", "neg", "-1", noChildren)
					// reflect.Kind == Int8
					expectVarExact(t, locals, -1, "i8", "i8", "1", noChildren)
					// reflect.Kind == Int16 - see testvariables2
					// reflect.Kind == Int32 - see testvariables2
					// reflect.Kind == Int64 - see testvariables2
					// reflect.Kind == Uint
					// reflect.Kind == Uint8
					expectVarExact(t, locals, -1, "u8", "u8", "255", noChildren)
					// reflect.Kind == Uint16
					expectVarExact(t, locals, -1, "u16", "u16", "65535", noChildren)
					// reflect.Kind == Uint32
					expectVarExact(t, locals, -1, "u32", "u32", "4294967295", noChildren)
					// reflect.Kind == Uint64
					expectVarExact(t, locals, -1, "u64", "u64", "18446744073709551615", noChildren)
					// reflect.Kind == Uintptr
					expectVarExact(t, locals, -1, "up", "up", "5", noChildren)
					// reflect.Kind == Float32
					expectVarExact(t, locals, -1, "f32", "f32", "1.2", noChildren)
					// reflect.Kind == Float64
					expectVarExact(t, locals, -1, "a3", "a3", "7.23", noChildren)
					// reflect.Kind == Complex64
					ref = expectVarExact(t, locals, -1, "c64", "c64", "(1 + 2i)", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						c64 := client.ExpectVariablesResponse(t)
						expectChildren(t, c64, "c64", 2)
						expectVarExact(t, c64, 0, "real", "", "1", noChildren)
						expectVarExact(t, c64, 1, "imaginary", "", "2", noChildren)
					}
					// reflect.Kind == Complex128
					ref = expectVarExact(t, locals, -1, "c128", "c128", "(2 + 3i)", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						c128 := client.ExpectVariablesResponse(t)
						expectChildren(t, c128, "c128", 2)
						expectVarExact(t, c128, 0, "real", "", "2", noChildren)
						expectVarExact(t, c128, 1, "imaginary", "", "3", noChildren)
					}
					// reflect.Kind == Array
					ref = expectVarExact(t, locals, -1, "a4", "a4", "[2]int [1,2]", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						a4 := client.ExpectVariablesResponse(t)
						expectChildren(t, a4, "a4", 2)
						expectVarExact(t, a4, 0, "[0]", "a4[0]", "1", noChildren)
						expectVarExact(t, a4, 1, "[1]", "a4[1]", "2", noChildren)
					}
					ref = expectVarExact(t, locals, -1, "a11", "a11", `[3]main.FooBar [{Baz: 1, Bur: "a"},{Baz: 2, Bur: "b"},{Baz: 3, Bur: "c"}]`, hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						a11 := client.ExpectVariablesResponse(t)
						expectChildren(t, a11, "a11", 3)
						expectVarExact(t, a11, 0, "[0]", "a11[0]", `main.FooBar {Baz: 1, Bur: "a"}`, hasChildren)
						ref = expectVarExact(t, a11, 1, "[1]", "a11[1]", `main.FooBar {Baz: 2, Bur: "b"}`, hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							a11_1 := client.ExpectVariablesResponse(t)
							expectChildren(t, a11_1, "a11[1]", 2)
							expectVarExact(t, a11_1, 0, "Baz", "a11[1].Baz", "2", noChildren)
							expectVarExact(t, a11_1, 1, "Bur", "a11[1].Bur", `"b"`, noChildren)
							validateEvaluateName(t, client, a11_1, 0)
							validateEvaluateName(t, client, a11_1, 1)
						}
						expectVarExact(t, a11, 2, "[2]", "a11[2]", `main.FooBar {Baz: 3, Bur: "c"}`, hasChildren)
					}

					// reflect.Kind == Chan - see testvariables2
					// reflect.Kind == Func - see testvariables2
					// reflect.Kind == Interface - see testvariables2
					// reflect.Kind == Map - see testvariables2
					// reflect.Kind == Ptr
					ref = expectVarExact(t, locals, -1, "a7", "a7", `*main.FooBar {Baz: 5, Bur: "strum"}`, hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						a7 := client.ExpectVariablesResponse(t)
						expectChildren(t, a7, "a7", 1)
						ref = expectVarExact(t, a7, 0, "", "(*a7)", `main.FooBar {Baz: 5, Bur: "strum"}`, hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							a7val := client.ExpectVariablesResponse(t)
							expectChildren(t, a7val, "*a7", 2)
							expectVarExact(t, a7val, 0, "Baz", "(*a7).Baz", "5", noChildren)
							expectVarExact(t, a7val, 1, "Bur", "(*a7).Bur", `"strum"`, noChildren)
							validateEvaluateName(t, client, a7val, 0)
							validateEvaluateName(t, client, a7val, 1)
						}
					}
					// TODO(polina): how to test for "nil" (without type) and "void"?
					expectVarExact(t, locals, -1, "a9", "a9", "*main.FooBar nil", noChildren)
					// reflect.Kind == Slice
					ref = expectVarExact(t, locals, -1, "a5", "a5", "[]int len: 5, cap: 5, [1,2,3,4,5]", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						a5 := client.ExpectVariablesResponse(t)
						expectChildren(t, a5, "a5", 5)
						expectVarExact(t, a5, 0, "[0]", "a5[0]", "1", noChildren)
						expectVarExact(t, a5, 4, "[4]", "a5[4]", "5", noChildren)
						validateEvaluateName(t, client, a5, 0)
						validateEvaluateName(t, client, a5, 1)
					}
					ref = expectVarExact(t, locals, -1, "a12", "a12", `[]main.FooBar len: 2, cap: 2, [{Baz: 4, Bur: "d"},{Baz: 5, Bur: "e"}]`, hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						a12 := client.ExpectVariablesResponse(t)
						expectChildren(t, a12, "a12", 2)
						expectVarExact(t, a12, 0, "[0]", "a12[0]", `main.FooBar {Baz: 4, Bur: "d"}`, hasChildren)
						ref = expectVarExact(t, a12, 1, "[1]", "a12[1]", `main.FooBar {Baz: 5, Bur: "e"}`, hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							a12_1 := client.ExpectVariablesResponse(t)
							expectChildren(t, a12_1, "a12[1]", 2)
							expectVarExact(t, a12_1, 0, "Baz", "a12[1].Baz", "5", noChildren)
							expectVarExact(t, a12_1, 1, "Bur", "a12[1].Bur", `"e"`, noChildren)
							validateEvaluateName(t, client, a12_1, 0)
							validateEvaluateName(t, client, a12_1, 1)
						}
					}
					ref = expectVarExact(t, locals, -1, "a13", "a13", `[]*main.FooBar len: 3, cap: 3, [*{Baz: 6, Bur: "f"},*{Baz: 7, Bur: "g"},*{Baz: 8, Bur: "h"}]`, hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						a13 := client.ExpectVariablesResponse(t)
						expectChildren(t, a13, "a13", 3)
						expectVarExact(t, a13, 0, "[0]", "a13[0]", `*main.FooBar {Baz: 6, Bur: "f"}`, hasChildren)
						expectVarExact(t, a13, 1, "[1]", "a13[1]", `*main.FooBar {Baz: 7, Bur: "g"}`, hasChildren)
						ref = expectVarExact(t, a13, 2, "[2]", "a13[2]", `*main.FooBar {Baz: 8, Bur: "h"}`, hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							a13_2 := client.ExpectVariablesResponse(t)
							expectChildren(t, a13_2, "a13[2]", 1)
							ref = expectVarExact(t, a13_2, 0, "", "(*a13[2])", `main.FooBar {Baz: 8, Bur: "h"}`, hasChildren)
							validateEvaluateName(t, client, a13_2, 0)
							if ref > 0 {
								client.VariablesRequest(ref)
								val := client.ExpectVariablesResponse(t)
								expectChildren(t, val, "*a13[2]", 2)
								expectVarExact(t, val, 0, "Baz", "(*a13[2]).Baz", "8", noChildren)
								expectVarExact(t, val, 1, "Bur", "(*a13[2]).Bur", `"h"`, noChildren)
								validateEvaluateName(t, client, val, 0)
								validateEvaluateName(t, client, val, 1)
							}
						}
					}
					// reflect.Kind == String
					expectVarExact(t, locals, -1, "a1", "a1", `"foofoofoofoofoofoo"`, noChildren)
					expectVarExact(t, locals, -1, "a10", "a10", `"ofo"`, noChildren)
					// reflect.Kind == Struct
					ref = expectVarExact(t, locals, -1, "a6", "a6", `main.FooBar {Baz: 8, Bur: "word"}`, hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						a6 := client.ExpectVariablesResponse(t)
						expectChildren(t, a6, "a6", 2)
						expectVarExact(t, a6, 0, "Baz", "a6.Baz", "8", noChildren)
						expectVarExact(t, a6, 1, "Bur", "a6.Bur", `"word"`, noChildren)
					}
					ref = expectVarExact(t, locals, -1, "a8", "a8", `main.FooBar2 {Bur: 10, Baz: "feh"}`, hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						a8 := client.ExpectVariablesResponse(t)
						expectChildren(t, a8, "a8", 2)
						expectVarExact(t, a8, 0, "Bur", "a8.Bur", "10", noChildren)
						expectVarExact(t, a8, 1, "Baz", "a8.Baz", `"feh"`, noChildren)
					}
					// reflect.Kind == UnsafePointer - see testvariables2
				},
				disconnect: false,
			}, {
				// Stop at second breakpoint
				execute: func() {
					// Frame ids get reset at each breakpoint.
					client.StackTraceRequest(1, 0, 20)
					stack := client.ExpectStackTraceResponse(t)
					expectStackFrames(t, stack, "main.barfoo", 27, 1000, 5, 5)

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
					expectVarExact(t, locals, -1, "a1", "a1", `"bur"`, noChildren)

					client.VariablesRequest(1002) // Globals
					globals := client.ExpectVariablesResponse(t)
					expectVarExact(t, globals, 0, "p1", "main.p1", "10", noChildren)

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
		runDebugSessionWithBPs(t, client, "launch",
			// Launch
			func() {
				client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
			},
			// Breakpoints are set within the program
			fixture.Source, []int{},
			[]onBreakpoint{{
				execute: func() {
					client.StackTraceRequest(1, 0, 20)
					stack := client.ExpectStackTraceResponse(t)
					expectStackFrames(t, stack, "main.main", -1, 1000, 3, 3)

					client.ScopesRequest(1000)
					scopes := client.ExpectScopesResponse(t)
					expectScope(t, scopes, 0, "Arguments", 1000)
					expectScope(t, scopes, 1, "Locals", 1001)
				},
				disconnect: false,
			}, {
				execute: func() {
					client.StackTraceRequest(1, 0, 20)
					stack := client.ExpectStackTraceResponse(t)
					expectStackFrames(t, stack, "main.main", -1, 1000, 3, 3)

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
					expectVarExact(t, locals, -1, "ni8", "ni8", "-5", noChildren)
					// reflect.Kind == Int16
					expectVarExact(t, locals, -1, "ni16", "ni16", "-5", noChildren)
					// reflect.Kind == Int32
					expectVarExact(t, locals, -1, "ni32", "ni32", "-5", noChildren)
					// reflect.Kind == Int64
					expectVarExact(t, locals, -1, "ni64", "ni64", "-5", noChildren)
					// reflect.Kind == Uint
					// reflect.Kind == Uint8 - see testvariables
					// reflect.Kind == Uint16 - see testvariables
					// reflect.Kind == Uint32 - see testvariables
					// reflect.Kind == Uint64 - see testvariables
					// reflect.Kind == Uintptr - see testvariables
					// reflect.Kind == Float32 - see testvariables
					// reflect.Kind == Float64
					expectVarExact(t, locals, -1, "pinf", "pinf", "+Inf", noChildren)
					expectVarExact(t, locals, -1, "ninf", "ninf", "-Inf", noChildren)
					expectVarExact(t, locals, -1, "nan", "nan", "NaN", noChildren)
					// reflect.Kind == Complex64 - see testvariables
					// reflect.Kind == Complex128 - see testvariables
					// reflect.Kind == Array
					expectVarExact(t, locals, -1, "a0", "a0", "[0]int []", noChildren)
					// reflect.Kind == Chan
					ref := expectVarExact(t, locals, -1, "ch1", "ch1", "chan int 4/11", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						ch1 := client.ExpectVariablesResponse(t)
						expectChildren(t, ch1, "ch1", 11)
						expectVarExact(t, ch1, 0, "qcount", "ch1.qcount", "4", noChildren)
						expectVarRegex(t, ch1, 10, "lock", "ch1.lock", `runtime\.mutex {.*key: 0.*}`, hasChildren)
						validateEvaluateName(t, client, ch1, 0)
						validateEvaluateName(t, client, ch1, 10)
					}
					expectVarExact(t, locals, -1, "chnil", "chnil", "chan int nil", noChildren)
					// reflect.Kind == Func
					expectVarExact(t, locals, -1, "fn1", "fn1", "main.afunc", noChildren)
					expectVarExact(t, locals, -1, "fn2", "fn2", "nil", noChildren)
					// reflect.Kind == Interface
					expectVarExact(t, locals, -1, "ifacenil", "ifacenil", "interface {} nil", noChildren)
					ref = expectVarExact(t, locals, -1, "iface2", "iface2", "interface {}(string) \"test\"", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						iface2 := client.ExpectVariablesResponse(t)
						expectChildren(t, iface2, "iface2", 1)
						expectVarExact(t, iface2, 0, "data", "iface2.(data)", `"test"`, noChildren)
						validateEvaluateName(t, client, iface2, 0)
					}
					ref = expectVarExact(t, locals, -1, "iface4", "iface4", "interface {}([]go/constant.Value) [4]", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						iface4 := client.ExpectVariablesResponse(t)
						expectChildren(t, iface4, "iface4", 1)
						ref = expectVarExact(t, iface4, 0, "data", "iface4.(data)", "[]go/constant.Value len: 1, cap: 1, [4]", hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							iface4data := client.ExpectVariablesResponse(t)
							expectChildren(t, iface4data, "iface4.data", 1)
							ref = expectVarExact(t, iface4data, 0, "[0]", "iface4.(data)[0]", "go/constant.Value(go/constant.int64Val) 4", hasChildren)
							if ref > 0 {
								client.VariablesRequest(ref)
								iface4data0 := client.ExpectVariablesResponse(t)
								expectChildren(t, iface4data0, "iface4.data[0]", 1)
								expectVarExact(t, iface4data0, 0, "data", "iface4.(data)[0].(data)", "4", noChildren)
								validateEvaluateName(t, client, iface4data0, 0)
							}
						}
					}
					expectVarExact(t, locals, -1, "errnil", "errnil", "error nil", noChildren)
					ref = expectVarExact(t, locals, -1, "err1", "err1", "error(*main.astruct) *{A: 1, B: 2}", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						err1 := client.ExpectVariablesResponse(t)
						expectChildren(t, err1, "err1", 1)
						expectVarExact(t, err1, 0, "data", "err1.(data)", "*main.astruct {A: 1, B: 2}", hasChildren)
						validateEvaluateName(t, client, err1, 0)
					}
					ref = expectVarExact(t, locals, -1, "ptrinf", "ptrinf", "*interface {}(**interface {}) **...", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						ptrinf_val := client.ExpectVariablesResponse(t)
						expectChildren(t, ptrinf_val, "*ptrinf", 1)
						ref = expectVarExact(t, ptrinf_val, 0, "", "(*ptrinf)", "interface {}(**interface {}) **...", hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							ptrinf_val_data := client.ExpectVariablesResponse(t)
							expectChildren(t, ptrinf_val_data, "(*ptrinf).data", 1)
							expectVarExact(t, ptrinf_val_data, 0, "data", "(*ptrinf).(data)", "**interface {}(**interface {}) ...", hasChildren)
							validateEvaluateName(t, client, ptrinf_val_data, 0)
						}
					}
					// reflect.Kind == Map
					expectVarExact(t, locals, -1, "mnil", "mnil", "map[string]main.astruct nil", noChildren)
					// key - scalar, value - compound
					ref = expectVarExact(t, locals, -1, "m2", "m2", "map[int]*main.astruct [1: *{A: 10, B: 11}, ]", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						m2 := client.ExpectVariablesResponse(t)
						expectChildren(t, m2, "m2", 1) // each key-value represented by a single child
						ref = expectVarExact(t, m2, 0, "1", "m2[1]", "*main.astruct {A: 10, B: 11}", hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							m2kv1 := client.ExpectVariablesResponse(t)
							expectChildren(t, m2kv1, "m2[1]", 1)
							ref = expectVarExact(t, m2kv1, 0, "", "(*m2[1])", "main.astruct {A: 10, B: 11}", hasChildren)
							if ref > 0 {
								client.VariablesRequest(ref)
								m2kv1deref := client.ExpectVariablesResponse(t)
								expectChildren(t, m2kv1deref, "*m2[1]", 2)
								expectVarExact(t, m2kv1deref, 0, "A", "(*m2[1]).A", "10", noChildren)
								expectVarExact(t, m2kv1deref, 1, "B", "(*m2[1]).B", "11", noChildren)
								validateEvaluateName(t, client, m2kv1deref, 0)
								validateEvaluateName(t, client, m2kv1deref, 1)
							}
						}
					}
					// key - compound, value - scalar
					ref = expectVarExact(t, locals, -1, "m3", "m3", "map[main.astruct]int [{A: 1, B: 1}: 42, {A: 2, B: 2}: 43, ]", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						m3 := client.ExpectVariablesResponse(t)
						expectChildren(t, m3, "m3", 2) // each key-value represented by a single child
						ref = expectVarRegex(t, m3, 0, `main\.astruct {A: 1, B: 1}\(0x[0-9a-f]+\)`, `m3\[\(\*\(\*"main.astruct"\)\(0x[0-9a-f]+\)\)\]`, "42", hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							m3kv0 := client.ExpectVariablesResponse(t)
							expectChildren(t, m3kv0, "m3[0]", 2)
							expectVarRegex(t, m3kv0, 0, "A", `\(*\(*"main\.astruct"\)\(0x[0-9a-f]+\)\)\.A`, "1", noChildren)
							validateEvaluateName(t, client, m3kv0, 0)
						}
						ref = expectVarRegex(t, m3, 1, `main\.astruct {A: 2, B: 2}\(0x[0-9a-f]+\)`, `m3\[\(\*\(\*"main.astruct"\)\(0x[0-9a-f]+\)\)\]`, "43", hasChildren)
						if ref > 0 { // inspect another key from another key-value child
							client.VariablesRequest(ref)
							m3kv1 := client.ExpectVariablesResponse(t)
							expectChildren(t, m3kv1, "m3[1]", 2)
							expectVarRegex(t, m3kv1, 1, "B", `\(*\(*"main\.astruct"\)\(0x[0-9a-f]+\)\)\.B`, "2", noChildren)
							validateEvaluateName(t, client, m3kv1, 1)
						}
					}
					// key - compound, value - compound
					ref = expectVarExact(t, locals, -1, "m4", "m4", "map[main.astruct]main.astruct [{A: 1, B: 1}: {A: 11, B: 11}, {A: 2, B: 2}: {A: 22, B: 22}, ]", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						m4 := client.ExpectVariablesResponse(t)
						expectChildren(t, m4, "m4", 4) // each key and value represented by a child, so double the key-value count
						expectVarRegex(t, m4, 0, `\[key 0\]`, `\(\*\(\*"main\.astruct"\)\(0x[0-9a-f]+\)\)`, `main\.astruct {A: 1, B: 1}`, hasChildren)
						expectVarRegex(t, m4, 1, `\[val 0\]`, `m4\[\(\*\(\*"main\.astruct"\)\(0x[0-9a-f]+\)\)\]`, `main\.astruct {A: 11, B: 11}`, hasChildren)
						ref = expectVarRegex(t, m4, 2, `\[key 1\]`, `\(\*\(\*"main\.astruct"\)\(0x[0-9a-f]+\)\)`, `main\.astruct {A: 2, B: 2}`, hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							m4Key1 := client.ExpectVariablesResponse(t)
							expectChildren(t, m4Key1, "m4Key1", 2)
							expectVarRegex(t, m4Key1, 0, "A", `\(\*\(\*"main\.astruct"\)\(0x[0-9a-f]+\)\)\.A`, "2", noChildren)
							expectVarRegex(t, m4Key1, 1, "B", `\(\*\(\*"main\.astruct"\)\(0x[0-9a-f]+\)\)\.B`, "2", noChildren)
							validateEvaluateName(t, client, m4Key1, 0)
							validateEvaluateName(t, client, m4Key1, 1)
						}
						ref = expectVarRegex(t, m4, 3, `\[val 1\]`, `m4\[\(\*\(\*"main\.astruct"\)\(0x[0-9a-f]+\)\)\]`, `main\.astruct {A: 22, B: 22}`, hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							m4Val1 := client.ExpectVariablesResponse(t)
							expectChildren(t, m4Val1, "m4Val1", 2)
							expectVarRegex(t, m4Val1, 0, "A", `m4\[\(\*\(\*"main\.astruct"\)\(0x[0-9a-f]+\)\)\]\.A`, "22", noChildren)
							expectVarRegex(t, m4Val1, 1, "B", `m4\[\(\*\(\*"main\.astruct"\)\(0x[0-9a-f]+\)\)\]\.B`, "22", noChildren)
							validateEvaluateName(t, client, m4Val1, 0)
							validateEvaluateName(t, client, m4Val1, 1)
						}
					}
					expectVarExact(t, locals, -1, "emptymap", "emptymap", "map[string]string []", noChildren)
					// reflect.Kind == Ptr
					ref = expectVarExact(t, locals, -1, "pp1", "pp1", "**1", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						pp1val := client.ExpectVariablesResponse(t)
						expectChildren(t, pp1val, "*pp1", 1)
						ref = expectVarExact(t, pp1val, 0, "", "(*pp1)", "*1", hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							pp1valval := client.ExpectVariablesResponse(t)
							expectChildren(t, pp1valval, "*(*pp1)", 1)
							expectVarExact(t, pp1valval, 0, "", "(*(*pp1))", "1", noChildren)
							validateEvaluateName(t, client, pp1valval, 0)
						}
					}
					// reflect.Kind == Slice
					ref = expectVarExact(t, locals, -1, "zsslice", "zsslice", "[]struct {} len: 3, cap: 3, [{},{},{}]", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						zsslice := client.ExpectVariablesResponse(t)
						expectChildren(t, zsslice, "zsslice", 3)
						expectVarExact(t, zsslice, 2, "[2]", "zsslice[2]", "struct {} {}", noChildren)
						validateEvaluateName(t, client, zsslice, 2)
					}
					expectVarExact(t, locals, -1, "emptyslice", "emptyslice", "[]string len: 0, cap: 0, []", noChildren)
					expectVarExact(t, locals, -1, "nilslice", "nilslice", "[]int len: 0, cap: 0, nil", noChildren)
					// reflect.Kind == String
					expectVarExact(t, locals, -1, "longstr", "longstr", "\"very long string 0123456789a0123456789b0123456789c0123456789d012...+73 more\"", noChildren)
					// reflect.Kind == Struct
					expectVarExact(t, locals, -1, "zsvar", "zsvar", "struct {} {}", noChildren)
					// reflect.Kind == UnsafePointer
					// TODO(polina): how do I test for unsafe.Pointer(nil)?
					expectVarRegex(t, locals, -1, "upnil", "upnil", `unsafe\.Pointer\(0x0\)`, noChildren)
					expectVarRegex(t, locals, -1, "up1", "up1", `unsafe\.Pointer\(0x[0-9a-f]+\)`, noChildren)

					// Test unreadable variable
					ref = expectVarRegex(t, locals, -1, "unread", "unread", `\*\(unreadable .+\)`, hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						val := client.ExpectVariablesResponse(t)
						expectChildren(t, val, "*unread", 1)
						expectVarRegex(t, val, 0, "^$", `\(\*unread\)`, `\(unreadable .+\)`, noChildren)
						validateEvaluateName(t, client, val, 0)
					}
				},
				disconnect: true,
			}})
	})
}

// TestVariablesLoading exposes test cases where variables might be partiall or
// fully unloaded.
func TestVariablesLoading(t *testing.T) {
	runTest(t, "testvariables2", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client, "launch",
			// Launch
			func() {
				client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
			},
			// Breakpoints are set within the program
			fixture.Source, []int{},
			[]onBreakpoint{{
				execute:    func() {},
				disconnect: false,
			}, {
				execute: func() {
					// Change default config values to trigger certain unloaded corner cases
					DefaultLoadConfig.MaxStructFields = 5
					defer func() { DefaultLoadConfig.MaxStructFields = -1 }()

					client.StackTraceRequest(1, 0, 0)
					client.ExpectStackTraceResponse(t)

					client.ScopesRequest(1000)
					client.ExpectScopesResponse(t)

					client.VariablesRequest(1001) // Locals
					locals := client.ExpectVariablesResponse(t)

					// String partially missing based on LoadConfig.MaxStringLen
					expectVarExact(t, locals, -1, "longstr", "longstr", "\"very long string 0123456789a0123456789b0123456789c0123456789d012...+73 more\"", noChildren)

					// Array partially missing based on LoadConfig.MaxArrayValues
					ref := expectVarExact(t, locals, -1, "longarr", "longarr", "(loaded 64/100) [100]int [0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,...+36 more]", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						longarr := client.ExpectVariablesResponse(t)
						expectChildren(t, longarr, "longarr", 64)
					}

					// Slice partially missing based on LoadConfig.MaxArrayValues
					ref = expectVarExact(t, locals, -1, "longslice", "longslice", "(loaded 64/100) []int len: 100, cap: 100, [0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,...+36 more]", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						longarr := client.ExpectVariablesResponse(t)
						expectChildren(t, longarr, "longslice", 64)
					}

					// Map partially missing based on LoadConfig.MaxArrayValues
					ref = expectVarRegex(t, locals, -1, "m1", "m1", `\(loaded 64/66\) map\[string\]main\.astruct \[.+\.\.\.\+2 more\]`, hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						m1 := client.ExpectVariablesResponse(t)
						expectChildren(t, m1, "m1", 64)
					}

					// Struct partially missing based on LoadConfig.MaxStructFields
					ref = expectVarExact(t, locals, -1, "sd", "sd", "(loaded 5/6) main.D {u1: 0, u2: 0, u3: 0, u4: 0, u5: 0,...+1 more}", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						sd := client.ExpectVariablesResponse(t)
						expectChildren(t, sd, "sd", 5)
					}

					// Struct fully missing due to hitting LoadConfig.MaxVariableRecurse (also tests evaluateName)
					ref = expectVarRegex(t, locals, -1, "c1", "c1", `main\.cstruct {pb: \*main\.bstruct {a: \(\*main\.astruct\)\(0x[0-9a-f]+\)}, sa: []\*main\.astruct len: 3, cap: 3, [\*\(\*main\.astruct\)\(0x[0-9a-f]+\),\*\(\*main\.astruct\)\(0x[0-9a-f]+\),\*\(\*main.astruct\)\(0x[0-9a-f]+\)]}`, hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						c1 := client.ExpectVariablesResponse(t)
						expectChildren(t, c1, "c1", 2)
						ref = expectVarRegex(t, c1, 1, "sa", `c1\.sa`, `\[\]\*main\.astruct len: 3, cap: 3, \[\*\(\*main\.astruct\)\(0x[0-9a-f]+\),\*\(\*main\.astruct\)\(0x[0-9a-f]+\),\*\(\*main\.astruct\)\(0x[0-9a-f]+\)\]`, hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							c1sa := client.ExpectVariablesResponse(t)
							expectChildren(t, c1sa, "c1.sa", 3)
							ref = expectVarRegex(t, c1sa, 0, `\[0\]`, `c1\.sa\[0\]`, `\*\(\*main\.astruct\)\(0x[0-9a-f]+\)`, hasChildren)
							if ref > 0 {
								client.VariablesRequest(ref)
								c1sa0 := client.ExpectVariablesResponse(t)
								expectChildren(t, c1sa0, "c1.sa[0]", 1)
								// TODO(polina): there should be children here once we support auto loading
								expectVarRegex(t, c1sa0, 0, "", `\(\*c1\.sa\[0\]\)`, `\(loaded 0/2\) \(\*main\.astruct\)\(0x[0-9a-f]+\)`, noChildren)
							}
						}
					}

					// Struct fully missing due to hitting LoadConfig.MaxVariableRecurse (also tests evaluteName)
					ref = expectVarRegex(t, locals, -1, "aas", "aas", `\[\]main\.a len: 1, cap: 1, \[{aas: \[\]main\.a len: 1, cap: 1, \[\(\*main\.a\)\(0x[0-9a-f]+\)\]}\]`, hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						aas := client.ExpectVariablesResponse(t)
						expectChildren(t, aas, "aas", 1)
						ref = expectVarRegex(t, aas, 0, "[0]", `aas\[0\]`, `main\.a {aas: \[\]main.a len: 1, cap: 1, \[\(\*main\.a\)\(0x[0-9a-f]+\)\]}`, hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							aas0 := client.ExpectVariablesResponse(t)
							expectChildren(t, aas0, "aas[0]", 1)
							ref = expectVarRegex(t, aas0, 0, "aas", `aas\[0\]\.aas`, `\[\]main\.a len: 1, cap: 1, \[\(\*main\.a\)\(0x[0-9a-f]+\)\]`, hasChildren)
							if ref > 0 {
								client.VariablesRequest(ref)
								aas0aas := client.ExpectVariablesResponse(t)
								expectChildren(t, aas0aas, "aas[0].aas", 1)
								// TODO(polina): there should be a child here once we support auto loading - test for "aas[0].aas[0].aas"
								expectVarRegex(t, aas0aas, 0, "[0]", `aas\[0\]\.aas\[0\]`, `\(loaded 0/1\) \(\*main\.a\)\(0x[0-9a-f]+\)`, noChildren)
							}
						}
					}

					// Map fully missing due to hitting LoadConfig.MaxVariableRecurse (also tests evaluateName)
					ref = expectVarExact(t, locals, -1, "tm", "tm", "main.truncatedMap {v: []map[string]main.astruct len: 1, cap: 1, [[...]]}", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						tm := client.ExpectVariablesResponse(t)
						expectChildren(t, tm, "tm", 1)
						ref = expectVarExact(t, tm, 0, "v", "tm.v", "[]map[string]main.astruct len: 1, cap: 1, [[...]]", hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							tmV := client.ExpectVariablesResponse(t)
							expectChildren(t, tmV, "tm.v", 1)
							// TODO(polina): there should be children here once we support auto loading - test for "tm.v[0]["gutters"]"
							expectVarExact(t, tmV, 0, "[0]", "tm.v[0]", "(loaded 0/66) map[string]main.astruct [...]", noChildren)
						}
					}
				},
				disconnect: true,
			}})
	})
	runTest(t, "testvariables", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client, "launch",
			// Launch
			func() {
				client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
			},
			// Breakpoints are set within the program
			fixture.Source, []int{},
			[]onBreakpoint{{
				execute: func() {
					DefaultLoadConfig.FollowPointers = false
					defer func() { DefaultLoadConfig.FollowPointers = true }()

					client.StackTraceRequest(1, 0, 0)
					client.ExpectStackTraceResponse(t)

					var loadvars = func(frame int) {
						client.ScopesRequest(frame)
						scopes := client.ExpectScopesResponse(t)
						localsRef := 0
						for _, s := range scopes.Body.Scopes {
							if s.Name == "Locals" {
								localsRef = s.VariablesReference
							}
						}

						client.VariablesRequest(localsRef)
						locals := client.ExpectVariablesResponse(t)

						// Interface auto-loaded when hitting LoadConfig.MaxVariableRecurse=1

						ref := expectVarRegex(t, locals, -1, "ni", "ni", `\[\]interface {} len: 1, cap: 1, \[\[\]interface {} len: 1, cap: 1, \[\*\(\*interface {}\)\(0x[0-9a-f]+\)\]\]`, hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							ni := client.ExpectVariablesResponse(t)
							ref = expectVarRegex(t, ni, 0, `\[0\]`, `ni\[0\]`, `interface \{\}\(\[\]interface \{\}\) \[\*\(\*interface \{\}\)\(0x[0-9a-f]+\)\]`, hasChildren)
							if ref > 0 {
								client.VariablesRequest(ref)
								niI1 := client.ExpectVariablesResponse(t)
								ref = expectVarRegex(t, niI1, 0, "data", `ni\[0\]\.\(data\)`, `\[\]interface {} len: 1, cap: 1, \[\*\(\*interface {}\)\(0x[0-9a-f]+\)`, hasChildren)
								if ref > 0 {
									// Auto-loading happens here
									client.VariablesRequest(ref)
									niI1Data := client.ExpectVariablesResponse(t)
									ref = expectVarExact(t, niI1Data, 0, "[0]", "ni[0].(data)[0]", "interface {}(int) 123", hasChildren)
									if ref > 0 {
										client.VariablesRequest(ref)
										niI1DataI2 := client.ExpectVariablesResponse(t)
										expectVarExact(t, niI1DataI2, 0, "data", "ni[0].(data)[0].(data)", "123", noChildren)
									}
								}
							}
						}

						// Pointer value not loaded with LoadConfig.FollowPointers=false
						expectVarRegex(t, locals, -1, "a7", "a7", `\(not loaded\) \(\*main\.FooBar\)\(0x[0-9a-f]+\)`, noChildren)
					}
					loadvars(1000 /*first topmost frame*/)
					// step into another function
					client.StepInRequest(1)
					client.ExpectStepInResponse(t)
					client.ExpectStoppedEvent(t)
					handleStop(t, client, 1, "main.barfoo", 24)
					loadvars(1001 /*second frame here is same as topmost above*/)
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
		runDebugSessionWithBPs(t, client, "launch",
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
					expectStackFrames(t, stack, "main.main", 36, 1000, 3, 3)

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
					expectStackFrames(t, stack, "", 13, 1000, 4, 4)

					client.ScopesRequest(1000)
					scopes = client.ExpectScopesResponse(t)
					expectScope(t, scopes, 0, "Arguments", 1000)
					expectScope(t, scopes, 1, "Locals", 1001)
					expectScope(t, scopes, 2, "Globals (package github.com/go-delve/delve/_fixtures/internal/dir0/pkg)", 1002)

					client.VariablesRequest(1002)
					globals := client.ExpectVariablesResponse(t)
					expectChildren(t, globals, "Globals", 1)
					ref := expectVarExact(t, globals, 0, "SomeVar", "github.com/go-delve/delve/_fixtures/internal/dir0/pkg.SomeVar", "github.com/go-delve/delve/_fixtures/internal/dir0/pkg.SomeType {X: 0}", hasChildren)

					if ref > 0 {
						client.VariablesRequest(ref)
						somevar := client.ExpectVariablesResponse(t)
						expectChildren(t, somevar, "SomeVar", 1)
						// TODO(polina): unlike main.p, this prefix won't work
						expectVarExact(t, somevar, 0, "X", "github.com/go-delve/delve/_fixtures/internal/dir0/pkg.SomeVar.X", "0", noChildren)
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
		runDebugSessionWithBPs(t, client, "launch",
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
					expectStackFrames(t, stResp, "main.Increment", 8, 1000, 2 /*returned*/, 2 /*available*/)
				},
				disconnect: false,
			}})
	})
}

// TestSetBreakpoint executes to a breakpoint and tests different
// configurations of setBreakpoint requests.
func TestSetBreakpoint(t *testing.T) {
	runTest(t, "loopprog", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client, "launch",
			// Launch
			func() {
				client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
			},
			// Set breakpoints
			fixture.Source, []int{16}, // b main.main
			[]onBreakpoint{{
				execute: func() {
					handleStop(t, client, 1, "main.main", 16)

					type Breakpoint struct {
						line      int
						path      string
						verified  bool
						msgPrefix string
					}
					expectSetBreakpointsResponse := func(bps []Breakpoint) {
						t.Helper()
						got := client.ExpectSetBreakpointsResponse(t)
						if len(got.Body.Breakpoints) != len(bps) {
							t.Errorf("got %#v,\nwant len(Breakpoints)=%d", got, len(bps))
							return
						}
						for i, bp := range got.Body.Breakpoints {
							if bp.Line != bps[i].line || bp.Verified != bps[i].verified || bp.Source.Path != bps[i].path ||
								!strings.HasPrefix(bp.Message, bps[i].msgPrefix) {
								t.Errorf("got breakpoints[%d] = %#v, \nwant %#v", i, bp, bps[i])
							}
						}
					}

					// Set two breakpoints at the next two lines in main
					client.SetBreakpointsRequest(fixture.Source, []int{17, 18})
					expectSetBreakpointsResponse([]Breakpoint{{17, fixture.Source, true, ""}, {18, fixture.Source, true, ""}})

					// Clear 17, reset 18
					client.SetBreakpointsRequest(fixture.Source, []int{18})
					expectSetBreakpointsResponse([]Breakpoint{{18, fixture.Source, true, ""}})

					// Skip 17, continue to 18
					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)
					client.ExpectStoppedEvent(t)
					handleStop(t, client, 1, "main.main", 18)

					// Set another breakpoint inside the loop in loop(), twice to trigger error
					client.SetBreakpointsRequest(fixture.Source, []int{8, 8})
					expectSetBreakpointsResponse([]Breakpoint{{8, fixture.Source, true, ""}, {8, "", false, "Breakpoint exists"}})

					// Continue into the loop
					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)
					client.ExpectStoppedEvent(t)
					handleStop(t, client, 1, "main.loop", 8)
					client.VariablesRequest(1001) // Locals
					locals := client.ExpectVariablesResponse(t)
					expectVarExact(t, locals, 0, "i", "i", "0", noChildren) // i == 0

					// Edit the breakpoint to add a condition
					client.SetConditionalBreakpointsRequest(fixture.Source, []int{8}, map[int]string{8: "i == 3"})
					expectSetBreakpointsResponse([]Breakpoint{{8, fixture.Source, true, ""}})

					// Continue until condition is hit
					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)
					client.ExpectStoppedEvent(t)
					handleStop(t, client, 1, "main.loop", 8)
					client.VariablesRequest(1001) // Locals
					locals = client.ExpectVariablesResponse(t)
					expectVarExact(t, locals, 0, "i", "i", "3", noChildren) // i == 3

					// Edit the breakpoint to remove a condition
					client.SetConditionalBreakpointsRequest(fixture.Source, []int{8}, map[int]string{8: ""})
					expectSetBreakpointsResponse([]Breakpoint{{8, fixture.Source, true, ""}})

					// Continue for one more loop iteration
					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)
					client.ExpectStoppedEvent(t)
					handleStop(t, client, 1, "main.loop", 8)
					client.VariablesRequest(1001) // Locals
					locals = client.ExpectVariablesResponse(t)
					expectVarExact(t, locals, 0, "i", "i", "4", noChildren) // i == 4

					// Set at a line without a statement
					client.SetBreakpointsRequest(fixture.Source, []int{1000})
					expectSetBreakpointsResponse([]Breakpoint{{1000, "", false, "could not find statement"}}) // all cleared, none set
				},
				// The program has an infinite loop, so we must kill it by disconnecting.
				disconnect: true,
			}})
	})
}

// TestLaunchSubstitutePath sets a breakpoint using a path
// that does not exist and expects the substitutePath attribute
// in the launch configuration to take care of the mapping.
func TestLaunchSubstitutePath(t *testing.T) {
	runTest(t, "loopprog", func(client *daptest.Client, fixture protest.Fixture) {
		substitutePathTestHelper(t, fixture, client, "launch", map[string]interface{}{"mode": "exec", "program": fixture.Path})
	})
}

// TestAttachSubstitutePath sets a breakpoint using a path
// that does not exist and expects the substitutePath attribute
// in the launch configuration to take care of the mapping.
func TestAttachSubstitutePath(t *testing.T) {
	if runtime.GOOS == "freebsd" {
		t.SkipNow()
	}
	if runtime.GOOS == "windows" {
		t.Skip("test skipped on windows, see https://delve.beta.teamcity.com/project/Delve_windows for details")
	}
	runTest(t, "loopprog", func(client *daptest.Client, fixture protest.Fixture) {
		cmd := execFixture(t, fixture)

		substitutePathTestHelper(t, fixture, client, "attach", map[string]interface{}{"mode": "local", "processId": cmd.Process.Pid})
	})
}

func substitutePathTestHelper(t *testing.T, fixture protest.Fixture, client *daptest.Client, request string, launchAttachConfig map[string]interface{}) {
	t.Helper()
	nonexistentDir := filepath.Join(string(filepath.Separator), "path", "that", "does", "not", "exist")
	if runtime.GOOS == "windows" {
		nonexistentDir = "C:" + nonexistentDir
	}

	launchAttachConfig["stopOnEntry"] = false
	// All paths used in the 'substitutePath' attribute must be absolute directory paths.
	launchAttachConfig["substitutePath"] = []map[string]string{
		{"from": nonexistentDir, "to": filepath.Dir(fixture.Source)},
		// Since the path mappings are ordered, when converting from client path to
		// server path, this mapping will not apply, because nonexistentDir appears in
		// an earlier rule.
		{"from": nonexistentDir, "to": nonexistentDir},
		// Since the path mappings are ordered, when converting from server path to
		// client path, this mapping will not apply, because filepath.Dir(fixture.Source)
		// appears in an earlier rule.
		{"from": filepath.Dir(fixture.Source), "to": filepath.Dir(fixture.Source)},
	}

	runDebugSessionWithBPs(t, client, request,
		func() {
			switch request {
			case "attach":
				client.AttachRequest(launchAttachConfig)
			case "launch":
				client.LaunchRequestWithArgs(launchAttachConfig)
			default:
				t.Fatalf("invalid request: %s", request)
			}
		},
		// Set breakpoints
		filepath.Join(nonexistentDir, "loopprog.go"), []int{8},
		[]onBreakpoint{{

			execute: func() {
				handleStop(t, client, 1, "main.loop", 8)
			},
			disconnect: true,
		}})
}

// execFixture runs the binary fixture.Path and hooks up stdout and stderr
// to os.Stdout and os.Stderr.
func execFixture(t *testing.T, fixture protest.Fixture) *exec.Cmd {
	t.Helper()
	// TODO(polina): do I need to sanity check testBackend and runtime.GOOS?
	cmd := exec.Command(fixture.Path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return cmd
}

// TestWorkingDir executes to a breakpoint and tests that the specified
// working directory is the one used to run the program.
func TestWorkingDir(t *testing.T) {
	runTest(t, "workdir", func(client *daptest.Client, fixture protest.Fixture) {
		wd := os.TempDir()
		// For Darwin `os.TempDir()` returns `/tmp` which is symlink to `/private/tmp`.
		if runtime.GOOS == "darwin" {
			wd = "/private/tmp"
		}
		runDebugSessionWithBPs(t, client, "launch",
			// Launch
			func() {
				client.LaunchRequestWithArgs(map[string]interface{}{
					"mode":        "exec",
					"program":     fixture.Path,
					"stopOnEntry": false,
					"wd":          wd,
				})
			},
			// Set breakpoints
			fixture.Source, []int{10}, // b main.main
			[]onBreakpoint{{
				execute: func() {
					handleStop(t, client, 1, "main.main", 10)
					client.VariablesRequest(1001) // Locals
					locals := client.ExpectVariablesResponse(t)
					expectChildren(t, locals, "Locals", 2)
					expectVarExact(t, locals, 0, "pwd", "pwd", fmt.Sprintf("%q", wd), noChildren)
					expectVarExact(t, locals, 1, "err", "err", "error nil", noChildren)

				},
				disconnect: false,
			}})
	})
}

// expectEval is a helper for verifying the values within an EvaluateResponse.
//     value - the value of the evaluated expression
//     hasRef - true if the evaluated expression should have children and therefore a non-0 variable reference
//     ref - reference to retrieve children of this evaluated expression (0 if none)
func expectEval(t *testing.T, got *dap.EvaluateResponse, value string, hasRef bool) (ref int) {
	t.Helper()
	if got.Body.Result != value || (got.Body.VariablesReference > 0) != hasRef {
		t.Errorf("\ngot  %#v\nwant Result=%q hasRef=%t", got, value, hasRef)
	}
	return got.Body.VariablesReference
}

func expectEvalRegex(t *testing.T, got *dap.EvaluateResponse, valueRegex string, hasRef bool) (ref int) {
	t.Helper()
	matched, _ := regexp.MatchString(valueRegex, got.Body.Result)
	if !matched || (got.Body.VariablesReference > 0) != hasRef {
		t.Errorf("\ngot  %#v\nwant Result=%q hasRef=%t", got, valueRegex, hasRef)
	}
	return got.Body.VariablesReference
}

func TestEvaluateRequest(t *testing.T) {
	runTest(t, "testvariables", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client, "launch",
			// Launch
			func() {
				client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
			},
			fixture.Source, []int{}, // Breakpoint set in the program
			[]onBreakpoint{{ // Stop at first breakpoint
				execute: func() {
					handleStop(t, client, 1, "main.foobar", 66)

					// Variable lookup
					client.EvaluateRequest("a2", 1000, "this context will be ignored")
					got := client.ExpectEvaluateResponse(t)
					expectEval(t, got, "6", noChildren)

					client.EvaluateRequest("a5", 1000, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					ref := expectEval(t, got, "[]int len: 5, cap: 5, [1,2,3,4,5]", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						a5 := client.ExpectVariablesResponse(t)
						expectChildren(t, a5, "a5", 5)
						expectVarExact(t, a5, 0, "[0]", "(a5)[0]", "1", noChildren)
						expectVarExact(t, a5, 4, "[4]", "(a5)[4]", "5", noChildren)
						validateEvaluateName(t, client, a5, 0)
						validateEvaluateName(t, client, a5, 4)
					}

					// Variable lookup that's not fully loaded
					client.EvaluateRequest("ba", 1000, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					expectEval(t, got, "(loaded 64/200) []int len: 200, cap: 200, [0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,...+136 more]", hasChildren)

					// All (binary and unary) on basic types except <-, ++ and --
					client.EvaluateRequest("1+1", 1000, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					expectEval(t, got, "2", noChildren)

					// Comparison operators on any type
					client.EvaluateRequest("1<2", 1000, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					expectEval(t, got, "true", noChildren)

					// Type casts between numeric types
					client.EvaluateRequest("int(2.3)", 1000, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					expectEval(t, got, "2", noChildren)

					// Type casts of integer constants into any pointer type and vice versa
					client.EvaluateRequest("(*int)(2)", 1000, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					expectEval(t, got, "(not loaded) (*int)(0x2)", noChildren)

					// Type casts between string, []byte and []rune
					client.EvaluateRequest("[]byte(\"ABC€\")", 1000, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					expectEval(t, got, "[]uint8 len: 6, cap: 6, [65,66,67,226,130,172]", noChildren)

					// Struct member access (i.e. somevar.memberfield)
					client.EvaluateRequest("ms.Nest.Level", 1000, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					expectEval(t, got, "1", noChildren)

					// Slicing and indexing operators on arrays, slices and strings
					client.EvaluateRequest("a5[4]", 1000, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					expectEval(t, got, "5", noChildren)

					// Map access
					client.EvaluateRequest("mp[1]", 1000, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					ref = expectEval(t, got, "interface {}(int) 42", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						expr := client.ExpectVariablesResponse(t)
						expectChildren(t, expr, "mp[1]", 1)
						expectVarExact(t, expr, 0, "data", "(mp[1]).(data)", "42", noChildren)
						validateEvaluateName(t, client, expr, 0)
					}

					// Pointer dereference
					client.EvaluateRequest("*ms.Nest", 1000, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					ref = expectEvalRegex(t, got, `main\.Nest {Level: 1, Nest: \*main.Nest {Level: 2, Nest: \*\(\*main\.Nest\)\(0x[0-9a-f]+\)}}`, hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						expr := client.ExpectVariablesResponse(t)
						expectChildren(t, expr, "*ms.Nest", 2)
						expectVarExact(t, expr, 0, "Level", "(*ms.Nest).Level", "1", noChildren)
						validateEvaluateName(t, client, expr, 0)
					}

					// Calls to builtin functions: cap, len, complex, imag and real
					client.EvaluateRequest("len(a5)", 1000, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					expectEval(t, got, "5", noChildren)

					// Type assertion on interface variables (i.e. somevar.(concretetype))
					client.EvaluateRequest("mp[1].(int)", 1000, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					expectEval(t, got, "42", noChildren)
				},
				disconnect: false,
			}, { // Stop at second breakpoint
				execute: func() {
					handleStop(t, client, 1, "main.barfoo", 27)

					// Top-most frame
					client.EvaluateRequest("a1", 1000, "this context will be ignored")
					got := client.ExpectEvaluateResponse(t)
					expectEval(t, got, "\"bur\"", noChildren)
					// No frame defaults to top-most frame
					client.EvaluateRequest("a1", 0, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					expectEval(t, got, "\"bur\"", noChildren)
					// Next frame
					client.EvaluateRequest("a1", 1001, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					expectEval(t, got, "\"foofoofoofoofoofoo\"", noChildren)
					// Next frame
					client.EvaluateRequest("a1", 1002, "any context but watch")
					erres := client.ExpectVisibleErrorResponse(t)
					if erres.Body.Error.Format != "Unable to evaluate expression: could not find symbol value for a1" {
						t.Errorf("\ngot %#v\nwant Format=\"Unable to evaluate expression: could not find symbol value for a1\"", erres)
					}
					client.EvaluateRequest("a1", 1002, "watch")
					erres = client.ExpectErrorResponse(t)
					if erres.Body.Error.Format != "Unable to evaluate expression: could not find symbol value for a1" {
						t.Errorf("\ngot %#v\nwant Format=\"Unable to evaluate expression: could not find symbol value for a1\"", erres)
					}
				},
				disconnect: false,
			}})
	})
}

func TestEvaluateCallRequest(t *testing.T) {
	protest.MustSupportFunctionCalls(t, testBackend)
	runTest(t, "fncall", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client, "launch",
			// Launch
			func() {
				client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
			},
			fixture.Source, []int{88},
			[]onBreakpoint{{ // Stop in makeclos()
				execute: func() {
					handleStop(t, client, 1, "main.makeclos", 88)

					// Topmost frame: both types of expressions should work
					client.EvaluateRequest("callstacktrace", 1000, "this context will be ignored")
					client.ExpectEvaluateResponse(t)
					client.EvaluateRequest("call callstacktrace()", 1000, "this context will be ignored")
					client.ExpectEvaluateResponse(t)

					// Next frame: only non-call expressions will work
					client.EvaluateRequest("callstacktrace", 1001, "this context will be ignored")
					client.ExpectEvaluateResponse(t)
					client.EvaluateRequest("call callstacktrace()", 1001, "not watch")
					erres := client.ExpectVisibleErrorResponse(t)
					if erres.Body.Error.Format != "Unable to evaluate expression: call is only supported with topmost stack frame" {
						t.Errorf("\ngot %#v\nwant Format=\"Unable to evaluate expression: call is only supported with topmost stack frame\"", erres)
					}

					// A call can stop on a breakpoint
					client.EvaluateRequest("call callbreak()", 1000, "not watch")
					s := client.ExpectStoppedEvent(t)
					if s.Body.Reason != "hardcoded breakpoint" {
						t.Errorf("\ngot %#v\nwant Reason=\"hardcoded breakpoint\"", s)
					}
					erres = client.ExpectVisibleErrorResponse(t)
					if erres.Body.Error.Format != "Unable to evaluate expression: call stopped" {
						t.Errorf("\ngot %#v\nwant Format=\"Unable to evaluate expression: call stopped\"", erres)
					}

					// A call during a call causes an error
					client.EvaluateRequest("call callstacktrace()", 1000, "not watch")
					erres = client.ExpectVisibleErrorResponse(t)
					if erres.Body.Error.Format != "Unable to evaluate expression: cannot call function while another function call is already in progress" {
						t.Errorf("\ngot %#v\nwant Format=\"Unable to evaluate expression: cannot call function while another function call is already in progress\"", erres)
					}

					// Complete the call and get back to original breakpoint in makeclos()
					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)
					client.ExpectStoppedEvent(t)
					handleStop(t, client, 1, "main.makeclos", 88)

					// Inject a call for the same function that is stopped at breakpoint:
					// it might stop at the exact same breakpoint on the same goroutine,
					// but we should still detect that its an injected call that stopped
					// and not the return to the original point of injection after it
					// completed.
					client.EvaluateRequest("call makeclos(nil)", 1000, "not watch")
					stopped := client.ExpectStoppedEvent(t)
					erres = client.ExpectVisibleErrorResponse(t)
					if erres.Body.Error.Format != "Unable to evaluate expression: call stopped" {
						t.Errorf("\ngot %#v\nwant Format=\"Unable to evaluate expression: call stopped\"", erres)
					}
					handleStop(t, client, stopped.Body.ThreadId, "main.makeclos", 88)

					// Complete the call and get back to original breakpoint in makeclos()
					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)
					client.ExpectStoppedEvent(t)
					handleStop(t, client, 1, "main.makeclos", 88)
				},
				disconnect: false,
			}, { // Stop at runtime breakpoint
				execute: func() {
					handleStop(t, client, 1, "main.main", 197)

					// No return values
					client.EvaluateRequest("call call0(1, 2)", 1000, "this context will be ignored")
					got := client.ExpectEvaluateResponse(t)
					expectEval(t, got, "", noChildren)
					// One unnamed return value
					client.EvaluateRequest("call call1(one, two)", 1000, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					ref := expectEval(t, got, "3", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						rv := client.ExpectVariablesResponse(t)
						expectChildren(t, rv, "rv", 1)
						expectVarExact(t, rv, 0, "~r2", "", "3", noChildren)
					}
					// One named return value
					// Panic doesn't panic, but instead returns the error as a named return variable
					client.EvaluateRequest("call callpanic()", 1000, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					ref = expectEval(t, got, `interface {}(string) "callpanic panicked"`, hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						rv := client.ExpectVariablesResponse(t)
						expectChildren(t, rv, "rv", 1)
						ref = expectVarExact(t, rv, 0, "~panic", "", `interface {}(string) "callpanic panicked"`, hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							p := client.ExpectVariablesResponse(t)
							expectChildren(t, p, "~panic", 1)
							expectVarExact(t, p, 0, "data", "", "\"callpanic panicked\"", noChildren)
						}
					}
					// Multiple return values
					client.EvaluateRequest("call call2(one, two)", 1000, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					ref = expectEval(t, got, "1, 2", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						rvs := client.ExpectVariablesResponse(t)
						expectChildren(t, rvs, "rvs", 2)
						expectVarExact(t, rvs, 0, "~r2", "", "1", noChildren)
						expectVarExact(t, rvs, 1, "~r3", "", "2", noChildren)
					}
					// No frame defaults to top-most frame
					client.EvaluateRequest("call call1(one, two)", 0, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					expectEval(t, got, "3", hasChildren)
					// Extra spaces don't matter
					client.EvaluateRequest(" call  call1(one, one) ", 0, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					expectEval(t, got, "2", hasChildren)
					// Just 'call', even with extra space, is treated as {expression}
					client.EvaluateRequest("call ", 1000, "watch")
					got = client.ExpectEvaluateResponse(t)
					expectEval(t, got, "\"this is a variable named `call`\"", noChildren)
					// Call error
					client.EvaluateRequest("call call1(one)", 1000, "watch")
					erres := client.ExpectErrorResponse(t)
					if erres.Body.Error.Format != "Unable to evaluate expression: not enough arguments" {
						t.Errorf("\ngot %#v\nwant Format=\"Unable to evaluate expression: not enough arguments\"", erres)
					}
					// Call can exit
					client.EvaluateRequest("call callexit()", 1000, "this context will be ignored")
					client.ExpectTerminatedEvent(t)
				},
				disconnect: true,
			}})
	})
}

func TestNextAndStep(t *testing.T) {
	runTest(t, "testinline", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client, "launch",
			// Launch
			func() {
				client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
			},
			// Set breakpoints
			fixture.Source, []int{11},
			[]onBreakpoint{{ // Stop at line 11
				execute: func() {
					handleStop(t, client, 1, "main.initialize", 11)

					expectStop := func(fun string, line int) {
						t.Helper()
						se := client.ExpectStoppedEvent(t)
						if se.Body.Reason != "step" || se.Body.ThreadId != 1 || !se.Body.AllThreadsStopped {
							t.Errorf("got %#v, want Reason=\"step\", ThreadId=1, AllThreadsStopped=true", se)
						}
						handleStop(t, client, 1, fun, line)
					}

					client.StepOutRequest(1)
					client.ExpectStepOutResponse(t)
					expectStop("main.main", 18)

					client.NextRequest(1)
					client.ExpectNextResponse(t)
					expectStop("main.main", 19)

					client.StepInRequest(1)
					client.ExpectStepInResponse(t)
					expectStop("main.inlineThis", 5)

					client.NextRequest(-10000 /*this is ignored*/)
					client.ExpectNextResponse(t)
					expectStop("main.inlineThis", 6)
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
		runDebugSessionWithBPs(t, client, "launch",
			// Launch
			func() {
				client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
			},
			// Set breakpoints
			fixture.Source, []int{4},
			[]onBreakpoint{{ // Stop at line 4
				execute: func() {
					handleStop(t, client, 1, "main.main", 4)

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
		runDebugSessionWithBPs(t, client, "launch",
			// Launch
			func() {
				client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
			},
			// Set breakpoints
			fixture.Source, []int{5},
			[]onBreakpoint{{
				execute: func() {
					handleStop(t, client, 1, "main.main", 5)

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
	if !goversion.VersionAfterOrEqual(runtime.Version(), 1, 14) {
		// In Go 1.13, 'next' will step into the defer in the runtime
		// main function, instead of the next line in the main program.
		t.SkipNow()
	}

	runTest(t, "panic", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client, "launch",
			// Launch
			func() {
				client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
			},
			// Set breakpoints
			fixture.Source, []int{5},
			[]onBreakpoint{{
				execute: func() {
					handleStop(t, client, 1, "main.main", 5)

					client.NextRequest(1)
					client.ExpectNextResponse(t)

					se := client.ExpectStoppedEvent(t)

					if se.Body.ThreadId != 1 || se.Body.Reason != "panic" {
						t.Errorf("\ngot  %#v\nexpected ThreadId=1 Reason=\"panic\"", se)
					}
				},
				disconnect: true,
			}})
	})
}

func TestFatalThrowBreakpoint(t *testing.T) {
	runTest(t, "testdeadlock", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client, "launch",
			// Launch
			func() {
				client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
			},
			// Set breakpoints
			fixture.Source, []int{3},
			[]onBreakpoint{{
				execute: func() {
					handleStop(t, client, 1, "main.main", 3)

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
// Skips line check if line is -1.
func handleStop(t *testing.T, client *daptest.Client, thread int, name string, line int) {
	t.Helper()
	client.ThreadsRequest()
	client.ExpectThreadsResponse(t)

	client.StackTraceRequest(thread, 0, 20)
	st := client.ExpectStackTraceResponse(t)
	if len(st.Body.StackFrames) < 1 {
		t.Errorf("\ngot  %#v\nwant len(stackframes) => 1", st)
	} else {
		if line != -1 && st.Body.StackFrames[0].Line != line {
			t.Errorf("\ngot  %#v\nwant Line=%d", st, line)
		}
		if st.Body.StackFrames[0].Name != name {
			t.Errorf("\ngot  %#v\nwant Name=%q", st, name)
		}
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
// while specifying breakpoints and unique launch/attach criteria via parameters.
//    cmd            - "launch" or "attach"
//    cmdRequest     - a function that sends a launch or attach request,
//                     so the test author has full control of its arguments.
//                     Note that he rest of the test sequence assumes that
//                     stopOnEntry is false.
//     breakpoints   - list of lines, where breakpoints are to be set
//     onBreakpoints - list of test sequences to execute at each of the set breakpoints.
func runDebugSessionWithBPs(t *testing.T, client *daptest.Client, cmd string, cmdRequest func(), source string, breakpoints []int, onBPs []onBreakpoint) {
	client.InitializeRequest()
	client.ExpectInitializeResponse(t)

	cmdRequest()
	client.ExpectInitializedEvent(t)
	if cmd == "launch" {
		client.ExpectLaunchResponse(t)
	} else if cmd == "attach" {
		client.ExpectAttachResponse(t)
	} else {
		panic("expected launch or attach command")
	}

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
			client.DisconnectRequestWithKillOption(true)
			client.ExpectDisconnectResponse(t)
			return
		}
		client.ContinueRequest(1)
		client.ExpectContinueResponse(t)
		// "Continue" is triggered after the response is sent
	}

	if cmd == "launch" { // Let the program run to completion
		client.ExpectTerminatedEvent(t)
	}
	client.DisconnectRequestWithKillOption(true)
	client.ExpectDisconnectResponse(t)
}

// runDebugSession is a helper for executing the standard init and shutdown
// sequences for a program that does not stop on entry
// while specifying unique launch criteria via parameters.
func runDebugSession(t *testing.T, client *daptest.Client, cmd string, cmdRequest func(), source string) {
	runDebugSessionWithBPs(t, client, cmd, cmdRequest, source, nil, nil)
}

func TestLaunchDebugRequest(t *testing.T) {
	rescueStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		// We reuse the harness that builds, but ignore the built binary,
		// only relying on the source to be built in response to LaunchRequest.
		runDebugSession(t, client, "launch", func() {
			client.LaunchRequestWithArgs(map[string]interface{}{
				"mode": "debug", "program": fixture.Source, "output": "__mydir"})
		}, fixture.Source)
	})
	// Wait for the test to finish to capture all stderr
	time.Sleep(100 * time.Millisecond)

	w.Close()
	err, _ := ioutil.ReadAll(r)
	t.Log(string(err))
	os.Stderr = rescueStderr

	rmErrRe, _ := regexp.Compile(`could not remove .*\n`)
	rmErr := rmErrRe.FindString(string(err))
	if rmErr != "" {
		// On Windows, a file in use cannot be removed, resulting in "Access is denied".
		// When the process exits, Delve releases the binary by calling
		// BinaryInfo.Close(), but it appears that it is still in use (by Windows?)
		// shortly after. gobuild.Remove has a delay to address this, but
		// to avoid any test flakiness we guard against this failure here as well.
		if runtime.GOOS != "windows" || !strings.Contains(rmErr, "Access is denied") {
			t.Fatalf("Binary removal failure:\n%s\n", rmErr)
		}
	}
}

// TestLaunchRequestDefaults tests defaults for launch attribute that are explicit in other tests.
func TestLaunchRequestDefaults(t *testing.T) {
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSession(t, client, "launch", func() {
			client.LaunchRequestWithArgs(map[string]interface{}{
				"mode": "" /*"debug" by default*/, "program": fixture.Source, "output": "__mydir"})
		}, fixture.Source)
	})
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSession(t, client, "launch", func() {
			// Use the default output directory.
			client.LaunchRequestWithArgs(map[string]interface{}{
				/*"mode":"debug" by default*/ "program": fixture.Source, "output": "__mydir"})
		}, fixture.Source)
	})
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSession(t, client, "launch", func() {
			// Use the default output directory.
			client.LaunchRequestWithArgs(map[string]interface{}{
				"mode": "debug", "program": fixture.Source})
			// writes to default output dir __debug_bin
		}, fixture.Source)
	})
}

func TestLaunchRequestDefaultsNoDebug(t *testing.T) {
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		runNoDebugDebugSession(t, client, "launch", func() {
			client.LaunchRequestWithArgs(map[string]interface{}{
				"noDebug": true,
				"mode":    "", /*"debug" by default*/
				"program": fixture.Source,
				"output":  cleanExeName("__mydir")})
		}, fixture.Source)
	})
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		runNoDebugDebugSession(t, client, "launch", func() {
			// Use the default output directory.
			client.LaunchRequestWithArgs(map[string]interface{}{
				"noDebug": true,
				/*"mode":"debug" by default*/
				"program": fixture.Source,
				"output":  cleanExeName("__mydir")})
		}, fixture.Source)
	})
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		runNoDebugDebugSession(t, client, "launch", func() {
			// Use the default output directory.
			client.LaunchRequestWithArgs(map[string]interface{}{
				"noDebug": true,
				"mode":    "debug",
				"program": fixture.Source})
			// writes to default output dir __debug_bin
		}, fixture.Source)
	})
}

func runNoDebugDebugSession(t *testing.T, client *daptest.Client, cmd string, cmdRequest func(), source string) {
	client.InitializeRequest()
	client.ExpectInitializeResponse(t)

	cmdRequest()
	// ! client.InitializedEvent.
	// ! client.ExpectLaunchResponse
	client.ExpectTerminatedEvent(t)
}

func TestLaunchTestRequest(t *testing.T) {
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSession(t, client, "launch", func() {
			// We reuse the harness that builds, but ignore the built binary,
			// only relying on the source to be built in response to LaunchRequest.
			fixtures := protest.FindFixturesDir()
			testdir, _ := filepath.Abs(filepath.Join(fixtures, "buildtest"))
			client.LaunchRequestWithArgs(map[string]interface{}{
				"mode": "test", "program": testdir, "output": "__mytestdir"})
		}, fixture.Source)
	})
}

// Tests that 'args' from LaunchRequest are parsed and passed to the target
// program. The target program exits without an error on success, and
// panics on error, causing an unexpected StoppedEvent instead of
// Terminated Event.
func TestLaunchRequestWithArgs(t *testing.T) {
	runTest(t, "testargs", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSession(t, client, "launch", func() {
			client.LaunchRequestWithArgs(map[string]interface{}{
				"mode": "exec", "program": fixture.Path,
				"args": []string{"test", "pass flag"}})
		}, fixture.Source)
	})
}

// Tests that 'buildFlags' from LaunchRequest are parsed and passed to the
// compiler. The target program exits without an error on success, and
// panics on error, causing an unexpected StoppedEvent instead of
// TerminatedEvent.
func TestLaunchRequestWithBuildFlags(t *testing.T) {
	runTest(t, "buildflagtest", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSession(t, client, "launch", func() {
			// We reuse the harness that builds, but ignore the built binary,
			// only relying on the source to be built in response to LaunchRequest.
			client.LaunchRequestWithArgs(map[string]interface{}{
				"mode": "debug", "program": fixture.Source, "output": "__mydir",
				"buildFlags": "-ldflags '-X main.Hello=World'"})
		}, fixture.Source)
	})
}

func TestAttachRequest(t *testing.T) {
	if runtime.GOOS == "freebsd" {
		t.SkipNow()
	}
	if runtime.GOOS == "windows" {
		t.Skip("test skipped on windows, see https://delve.beta.teamcity.com/project/Delve_windows for details")
	}
	runTest(t, "loopprog", func(client *daptest.Client, fixture protest.Fixture) {
		// Start the program to attach to
		cmd := execFixture(t, fixture)

		runDebugSessionWithBPs(t, client, "attach",
			// Attach
			func() {
				client.AttachRequest(map[string]interface{}{
					/*"mode": "local" by default*/ "processId": cmd.Process.Pid, "stopOnEntry": false})
			},
			// Set breakpoints
			fixture.Source, []int{8},
			[]onBreakpoint{{
				// Stop at line 8
				execute: func() {
					handleStop(t, client, 1, "main.loop", 8)
					client.VariablesRequest(1001) // Locals
					locals := client.ExpectVariablesResponse(t)
					expectChildren(t, locals, "Locals", 1)
					expectVarRegex(t, locals, 0, "i", "i", "[0-9]+", noChildren)
				},
				disconnect: true,
			}})
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

		client.PauseRequest()
		expectNotYetImplemented("pause")
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

		// Bad "program"
		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "debug", "program": 12345})
		expectFailedToLaunchWithMessage(client.ExpectErrorResponse(t),
			"Failed to launch: The program attribute is missing in debug configuration.")

		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "debug", "program": nil})
		expectFailedToLaunchWithMessage(client.ExpectErrorResponse(t),
			"Failed to launch: The program attribute is missing in debug configuration.")

		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "debug"})
		expectFailedToLaunchWithMessage(client.ExpectErrorResponse(t),
			"Failed to launch: The program attribute is missing in debug configuration.")

		// Bad "mode"
		client.LaunchRequest("remote", fixture.Path, stopOnEntry)
		expectFailedToLaunchWithMessage(client.ExpectErrorResponse(t),
			"Failed to launch: Unsupported 'mode' value \"remote\" in debug configuration.")

		client.LaunchRequest("notamode", fixture.Path, stopOnEntry)
		expectFailedToLaunchWithMessage(client.ExpectErrorResponse(t),
			"Failed to launch: Unsupported 'mode' value \"notamode\" in debug configuration.")

		client.LaunchRequestWithArgs(map[string]interface{}{"mode": 12345, "program": fixture.Path})
		expectFailedToLaunchWithMessage(client.ExpectErrorResponse(t),
			"Failed to launch: Unsupported 'mode' value %!q(float64=12345) in debug configuration.")

		client.LaunchRequestWithArgs(map[string]interface{}{"mode": ""}) // empty mode defaults to "debug" (not an error)
		expectFailedToLaunchWithMessage(client.ExpectErrorResponse(t),
			"Failed to launch: The program attribute is missing in debug configuration.")

		client.LaunchRequestWithArgs(map[string]interface{}{}) // missing mode defaults to "debug" (not an error)
		expectFailedToLaunchWithMessage(client.ExpectErrorResponse(t),
			"Failed to launch: The program attribute is missing in debug configuration.")

		// Bad "args"
		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "exec", "program": fixture.Path, "args": nil})
		expectFailedToLaunchWithMessage(client.ExpectErrorResponse(t),
			"Failed to launch: 'args' attribute '<nil>' in debug configuration is not an array.")

		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "exec", "program": fixture.Path, "args": 12345})
		expectFailedToLaunchWithMessage(client.ExpectErrorResponse(t),
			"Failed to launch: 'args' attribute '12345' in debug configuration is not an array.")

		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "exec", "program": fixture.Path, "args": []int{1, 2}})
		expectFailedToLaunchWithMessage(client.ExpectErrorResponse(t),
			"Failed to launch: value '1' in 'args' attribute in debug configuration is not a string.")

		// Bad "buildFlags"
		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "debug", "program": fixture.Source, "buildFlags": 123})
		expectFailedToLaunchWithMessage(client.ExpectErrorResponse(t),
			"Failed to launch: 'buildFlags' attribute '123' in debug configuration is not a string.")

		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "debug", "program": fixture.Source, "substitutePath": 123})
		expectFailedToLaunchWithMessage(client.ExpectErrorResponse(t),
			"Failed to launch: 'substitutePath' attribute '123' in debug configuration is not a []{'from': string, 'to': string}")

		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "debug", "program": fixture.Source, "substitutePath": []interface{}{123}})
		expectFailedToLaunchWithMessage(client.ExpectErrorResponse(t),
			"Failed to launch: 'substitutePath' attribute '[123]' in debug configuration is not a []{'from': string, 'to': string}")

		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "debug", "program": fixture.Source, "substitutePath": []interface{}{map[string]interface{}{"to": "path2"}}})
		expectFailedToLaunchWithMessage(client.ExpectErrorResponse(t),
			"Failed to launch: 'substitutePath' attribute '[map[to:path2]]' in debug configuration is not a []{'from': string, 'to': string}")

		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "debug", "program": fixture.Source, "substitutePath": []interface{}{map[string]interface{}{"from": "path1", "to": 123}}})
		expectFailedToLaunchWithMessage(client.ExpectErrorResponse(t),
			"Failed to launch: 'substitutePath' attribute '[map[from:path1 to:123]]' in debug configuration is not a []{'from': string, 'to': string}")
		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "debug", "program": fixture.Source, "wd": 123})
		expectFailedToLaunchWithMessage(client.ExpectErrorResponse(t),
			"Failed to launch: 'wd' attribute '123' in debug configuration is not a string.")

		// Skip detailed message checks for potentially different OS-specific errors.
		client.LaunchRequest("exec", fixture.Path+"_does_not_exist", stopOnEntry)
		expectFailedToLaunch(client.ExpectErrorResponse(t)) // No such file or directory

		client.LaunchRequest("debug", fixture.Path+"_does_not_exist", stopOnEntry)
		expectFailedToLaunchWithMessage(client.ExpectErrorResponse(t), "Failed to launch: Build error: exit status 1")

		client.LaunchRequest("" /*debug by default*/, fixture.Path+"_does_not_exist", stopOnEntry)
		expectFailedToLaunchWithMessage(client.ExpectErrorResponse(t), "Failed to launch: Build error: exit status 1")

		client.LaunchRequest("exec", fixture.Source, stopOnEntry)
		expectFailedToLaunch(client.ExpectErrorResponse(t)) // Not an executable

		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "debug", "program": fixture.Source, "buildFlags": "bad flags"})
		expectFailedToLaunchWithMessage(client.ExpectErrorResponse(t), "Failed to launch: Build error: exit status 1")

		// We failed to launch the program. Make sure shutdown still works.
		client.DisconnectRequest()
		dresp := client.ExpectDisconnectResponse(t)
		if dresp.RequestSeq != seqCnt {
			t.Errorf("got %#v, want RequestSeq=%d", dresp, seqCnt)
		}
	})
}

func TestBadAttachRequest(t *testing.T) {
	runTest(t, "loopprog", func(client *daptest.Client, fixture protest.Fixture) {
		seqCnt := 1
		expectFailedToAttach := func(response *dap.ErrorResponse) {
			t.Helper()
			if response.RequestSeq != seqCnt {
				t.Errorf("RequestSeq got %d, want %d", seqCnt, response.RequestSeq)
			}
			if response.Command != "attach" {
				t.Errorf("Command got %q, want \"attach\"", response.Command)
			}
			if response.Message != "Failed to attach" {
				t.Errorf("Message got %q, want \"Failed to attach\"", response.Message)
			}
			if response.Body.Error.Id != 3001 {
				t.Errorf("Id got %d, want 3001", response.Body.Error.Id)
			}
			seqCnt++
		}

		expectFailedToAttachWithMessage := func(response *dap.ErrorResponse, errmsg string) {
			t.Helper()
			expectFailedToAttach(response)
			if response.Body.Error.Format != errmsg {
				t.Errorf("\ngot  %q\nwant %q", response.Body.Error.Format, errmsg)
			}
		}

		// Bad "mode"
		client.AttachRequest(map[string]interface{}{"mode": "remote"})
		expectFailedToAttachWithMessage(client.ExpectErrorResponse(t),
			"Failed to attach: Unsupported 'mode' value \"remote\" in debug configuration")

		client.AttachRequest(map[string]interface{}{"mode": "blah blah blah"})
		expectFailedToAttachWithMessage(client.ExpectErrorResponse(t),
			"Failed to attach: Unsupported 'mode' value \"blah blah blah\" in debug configuration")

		client.AttachRequest(map[string]interface{}{"mode": 123})
		expectFailedToAttachWithMessage(client.ExpectErrorResponse(t),
			"Failed to attach: Unsupported 'mode' value %!q(float64=123) in debug configuration")

		client.AttachRequest(map[string]interface{}{"mode": ""}) // empty mode defaults to "local" (not an error)
		expectFailedToAttachWithMessage(client.ExpectErrorResponse(t),
			"Failed to attach: The 'processId' attribute is missing in debug configuration")

		client.AttachRequest(map[string]interface{}{}) // no mode defaults to "local" (not an error)
		expectFailedToAttachWithMessage(client.ExpectErrorResponse(t),
			"Failed to attach: The 'processId' attribute is missing in debug configuration")

		// Bad "processId"
		client.AttachRequest(map[string]interface{}{"mode": "local"})
		expectFailedToAttachWithMessage(client.ExpectErrorResponse(t),
			"Failed to attach: The 'processId' attribute is missing in debug configuration")

		client.AttachRequest(map[string]interface{}{"mode": "local", "processId": nil})
		expectFailedToAttachWithMessage(client.ExpectErrorResponse(t),
			"Failed to attach: The 'processId' attribute is missing in debug configuration")

		client.AttachRequest(map[string]interface{}{"mode": "local", "processId": 0})
		expectFailedToAttachWithMessage(client.ExpectErrorResponse(t),
			"Failed to attach: The 'processId' attribute is missing in debug configuration")

		client.AttachRequest(map[string]interface{}{"mode": "local", "processId": "1"})
		expectFailedToAttachWithMessage(client.ExpectErrorResponse(t),
			"Failed to attach: The 'processId' attribute is missing in debug configuration")

		client.AttachRequest(map[string]interface{}{"mode": "local", "processId": 1})
		// The exact message varies on different systems, so skip that check
		expectFailedToAttach(client.ExpectErrorResponse(t)) // could not attach to pid 1

		// This will make debugger.(*Debugger) panic, which we will catch as an internal error.
		client.AttachRequest(map[string]interface{}{"mode": "local", "processId": -1})
		er := client.ExpectErrorResponse(t)
		if er.RequestSeq != seqCnt {
			t.Errorf("RequestSeq got %d, want %d", seqCnt, er.RequestSeq)
		}
		seqCnt++
		if er.Command != "" {
			t.Errorf("Command got %q, want \"attach\"", er.Command)
		}
		if er.Body.Error.Format != "Internal Error: runtime error: index out of range [0] with length 0" {
			t.Errorf("Message got %q, want \"Internal Error: runtime error: index out of range [0] with length 0\"", er.Message)
		}
		if er.Body.Error.Id != 8888 {
			t.Errorf("Id got %d, want 8888", er.Body.Error.Id)
		}

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
