package dap

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-delve/delve/pkg/goversion"
	"github.com/go-delve/delve/pkg/logflags"
	protest "github.com/go-delve/delve/pkg/proc/test"
	"github.com/go-delve/delve/service"
	"github.com/go-delve/delve/service/dap/daptest"
	"github.com/google/go-dap"
)

const stopOnEntry bool = true
const hasChildren bool = true
const noChildren bool = false

var testBackend string

func TestMain(m *testing.M) {
	logOutputVal := ""
	if _, isTeamCityTest := os.LookupEnv("TEAMCITY_VERSION"); isTeamCityTest {
		logOutputVal = "debugger,dap"
	}
	var logOutput string
	flag.StringVar(&logOutput, "log-output", logOutputVal, "configures log output")
	flag.Parse()
	logflags.Setup(logOutput != "", logOutput, "")
	protest.DefaultTestBackend(&testBackend)
	os.Exit(protest.RunTestsWithFixtures(m))
}

// name is for _fixtures/<name>.go
func runTest(t *testing.T, name string, test func(c *daptest.Client, f protest.Fixture)) {
	runTestBuildFlags(t, name, test, protest.AllNonOptimized)
}

// name is for _fixtures/<name>.go
func runTestBuildFlags(t *testing.T, name string, test func(c *daptest.Client, f protest.Fixture), buildFlags protest.BuildFlags) {
	fixture := protest.BuildFixture(name, buildFlags)

	// Start the DAP server.
	client := startDapServer(t)
	// client.Close will close the client connectinon, which will cause a connection error
	// on the server side and signal disconnect to unblock Stop() above.
	defer client.Close()

	test(client, fixture)
}

func startDapServer(t *testing.T) *daptest.Client {
	// Start the DAP server.
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	disconnectChan := make(chan struct{})
	server := NewServer(&service.Config{
		Listener:       listener,
		DisconnectChan: disconnectChan,
	})
	server.Run()
	// Give server time to start listening for clients
	time.Sleep(100 * time.Millisecond)

	// Run a goroutine that stops the server when disconnectChan is signaled.
	// This helps us test that certain events cause the server to stop as
	// expected.
	go func() {
		<-disconnectChan
		server.Stop()
	}()

	client := daptest.NewClient(listener.Addr().String())
	return client
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
//                                 :    << output event (Process exited)
//                                 :    << output event (Detaching)
//                                 : 13 << disconnect
// This test exhaustively tests Seq and RequestSeq on all messages from the
// server. Other tests do not necessarily need to repeat all these checks.
func TestLaunchStopOnEntry(t *testing.T) {
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		// 1 >> initialize, << initialize
		client.InitializeRequest()
		initResp := client.ExpectInitializeResponseAndCapabilities(t)
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
		stResp := client.ExpectInvisibleErrorResponse(t)
		if stResp.Seq != 0 || stResp.RequestSeq != 8 || stResp.Body.Error.Format != "Unable to produce stack trace: unknown goroutine 1" {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=8 Format=\"Unable to produce stack trace: unknown goroutine 1\"", stResp)
		}

		// 9 >> stackTrace, << error
		client.StackTraceRequest(1, 0, 20)
		stResp = client.ExpectInvisibleErrorResponse(t)
		if stResp.Seq != 0 || stResp.RequestSeq != 9 || stResp.Body.Error.Id != 2004 {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=9 Id=2004", stResp)
		}

		// 10 >> evaluate, << error
		client.EvaluateRequest("foo", 0 /*no frame specified*/, "repl")
		erResp := client.ExpectInvisibleErrorResponse(t)
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
		oep := client.ExpectOutputEventProcessExited(t, 0)
		if oep.Seq != 0 || oep.Body.Category != "console" {
			t.Errorf("\ngot %#v\nwant Seq=0 Category='console'", oep)
		}
		oed := client.ExpectOutputEventDetaching(t)
		if oed.Seq != 0 || oed.Body.Category != "console" {
			t.Errorf("\ngot %#v\nwant Seq=0 Category='console'", oed)
		}
		dResp := client.ExpectDisconnectResponse(t)
		if dResp.Seq != 0 || dResp.RequestSeq != 13 {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=13", dResp)
		}
		client.ExpectTerminatedEvent(t)
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
		initResp := client.ExpectInitializeResponseAndCapabilities(t)
		if initResp.Seq != 0 || initResp.RequestSeq != 1 {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=1", initResp)
		}

		// 2 >> attach, << initialized, << attach
		client.AttachRequest(
			map[string]interface{}{"mode": "local", "processId": cmd.Process.Pid, "stopOnEntry": true, "backend": "default"})
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
		erResp := client.ExpectInvisibleErrorResponse(t)
		if erResp.Seq != 0 || erResp.RequestSeq != 10 || erResp.Body.Error.Id != 2009 {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=10 Id=2009", erResp)
		}

		// 11 >> evaluate, << evaluate
		client.EvaluateRequest("1+1", 0 /*no frame specified*/, "repl")
		evResp := client.ExpectEvaluateResponse(t)
		if evResp.Seq != 0 || evResp.RequestSeq != 11 || evResp.Body.Result != "2" {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=10 Result=2", evResp)
		}

		// 12 >> continue, << continue
		client.ContinueRequest(1)
		cResp := client.ExpectContinueResponse(t)
		if cResp.Seq != 0 || cResp.RequestSeq != 12 {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=12", cResp)
		}

		// TODO(polina): once https://github.com/go-delve/delve/issues/2259 is
		// fixed, test with kill=false.

		// 13 >> disconnect, << disconnect
		client.DisconnectRequestWithKillOption(true)

		// Disconnect consists of Halt + Detach.
		// Halt interrupts command in progress, which triggers
		// a stopped event in parallel with the disconnect
		// sequence. It might arrive before or during the sequence
		// or never if the server exits before it is sent.
		msg := expectMessageFilterStopped(t, client)
		client.CheckOutputEvent(t, msg)
		msg = expectMessageFilterStopped(t, client)
		client.CheckDisconnectResponse(t, msg)
		client.ExpectTerminatedEvent(t)

		// If this call to KeepAlive isn't here there's a chance that stdout will
		// be garbage collected (since it is no longer alive long before this
		// point), when that happens, on unix-like OSes, the read end of the pipe
		// will be closed by the finalizer and the target process will die by
		// SIGPIPE, which the rest of this test does not expect.
		runtime.KeepAlive(stdout)
	})
}

// Like the test above, except the program is configured to continue on entry.
func TestContinueOnEntry(t *testing.T) {
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		// 1 >> initialize, << initialize
		client.InitializeRequest()
		client.ExpectInitializeResponseAndCapabilities(t)

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
		// "Continue" happens behind the scenes on another goroutine

		// 6 >> threads, << threads
		client.ThreadsRequest()
		// Since we are in async mode while running, we might receive messages in either order.
		for i := 0; i < 2; i++ {
			msg := client.ExpectMessage(t)
			switch m := msg.(type) {
			case *dap.ThreadsResponse:
				if m.Seq != 0 || m.RequestSeq != 6 {
					t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=6", m)
				}
				// Single current thread is sent when the program is running
				// because DAP spec expects at least one thread.
				// Also accept empty already-terminated response.
				if len(m.Body.Threads) != 0 && (len(m.Body.Threads) != 1 || m.Body.Threads[0].Id != -1 || m.Body.Threads[0].Name != "Current") {
					t.Errorf("\ngot  %#v\nwant Id=-1, Name=\"Current\" or empty", m.Body.Threads)
				}
			case *dap.TerminatedEvent:
			default:
				t.Fatalf("got %#v, want ThreadsResponse or TerminatedEvent", m)
			}
		}

		// It is possible for the program to terminate before the initial
		// threads request is processed. And in that case, the
		// response can be empty
		// 7 >> threads, << threads
		client.ThreadsRequest()
		tResp := client.ExpectThreadsResponse(t)
		if tResp.Seq != 0 || tResp.RequestSeq != 7 || len(tResp.Body.Threads) != 1 {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=7 len(Threads)=1", tResp)
		}
		if tResp.Body.Threads[0].Id != 1 || tResp.Body.Threads[0].Name != "Dummy" {
			t.Errorf("\ngot %#v\nwant Id=1, Name=\"Dummy\"", tResp)
		}

		// 8 >> disconnect, << disconnect
		client.DisconnectRequest()
		client.ExpectOutputEventProcessExited(t, 0)
		client.ExpectOutputEventDetaching(t)
		dResp := client.ExpectDisconnectResponse(t)
		if dResp.Seq != 0 || dResp.RequestSeq != 8 {
			t.Errorf("\ngot %#v\nwant Seq=0, RequestSeq=8", dResp)
		}
		client.ExpectTerminatedEvent(t)
	})
}

// TestPreSetBreakpoint corresponds to a debug session that is configured to
// continue on entry with a pre-set breakpoint.
func TestPreSetBreakpoint(t *testing.T) {
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		client.InitializeRequest()
		client.ExpectInitializeResponseAndCapabilities(t)

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
		// This triggers "continue" on a separate goroutine

		client.ThreadsRequest()
		// Since we are in async mode while running, we might receive messages in either order.
		for i := 0; i < 2; i++ {
			msg := client.ExpectMessage(t)
			switch m := msg.(type) {
			case *dap.ThreadsResponse:
				// If the thread request arrived while the program was running, we expect to get the dummy response
				// with a single goroutine "Current".
				// If the thread request arrived after the stop, we should get the goroutine stopped at main.Increment.
				if (len(m.Body.Threads) != 1 || m.Body.Threads[0].Id != -1 || m.Body.Threads[0].Name != "Current") &&
					(len(m.Body.Threads) < 1 || m.Body.Threads[0].Id != 1 || !strings.HasPrefix(m.Body.Threads[0].Name, "* [Go 1] main.Increment")) {
					t.Errorf("\ngot  %#v\nwant Id=-1, Name=\"Current\" or Id=1, Name=\"* [Go 1] main.Increment ...\"", m.Body.Threads)
				}
			case *dap.StoppedEvent:
				if m.Body.Reason != "breakpoint" || m.Body.ThreadId != 1 || !m.Body.AllThreadsStopped {
					t.Errorf("got %#v, want Body={Reason=\"breakpoint\", ThreadId=1, AllThreadsStopped=true}", m)
				}
			default:
				t.Fatalf("got %#v, want ThreadsResponse or StoppedEvent", m)
			}
		}

		// Threads-StackTrace-Scopes-Variables request waterfall is
		// triggered on stop event.
		client.ThreadsRequest()
		tResp := client.ExpectThreadsResponse(t)
		if len(tResp.Body.Threads) < 2 { // 1 main + runtime
			t.Errorf("\ngot  %#v\nwant len(Threads)>1", tResp.Body.Threads)
		}
		reMain, _ := regexp.Compile(`\* \[Go 1\] main.Increment \(Thread [0-9]+\)`)
		wantMain := dap.Thread{Id: 1, Name: "* [Go 1] main.Increment (Thread ...)"}
		wantRuntime := dap.Thread{Id: 2, Name: "[Go 2] runtime.gopark"}
		for _, got := range tResp.Body.Threads {
			if got.Id != 1 && !reMain.MatchString(got.Name) && !(strings.Contains(got.Name, "runtime.") || strings.Contains(got.Name, "runtime/")) {
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
			checkFrame := func(got dap.StackFrame, id int, name string, sourceName string, line int) {
				t.Helper()
				if got.Id != id || got.Name != name {
					t.Errorf("\ngot  %#v\nwant Id=%d Name=%s", got, id, name)
				}
				if (sourceName != "" && got.Source.Name != sourceName) || (line > 0 && got.Line != line) {
					t.Errorf("\ngot  %#v\nwant Source.Name=%s Line=%d", got, sourceName, line)
				}
			}
			checkFrame(stResp.Body.StackFrames[0], 1000, "main.Increment", "increment.go", 8)
			checkFrame(stResp.Body.StackFrames[1], 1001, "main.Increment", "increment.go", 11)
			checkFrame(stResp.Body.StackFrames[2], 1002, "main.Increment", "increment.go", 11)
			checkFrame(stResp.Body.StackFrames[3], 1003, "main.main", "increment.go", 17)
			checkFrame(stResp.Body.StackFrames[4], 1004, "runtime.main", "proc.go", -1)
			checkFrame(stResp.Body.StackFrames[5], 1005, "runtime.goexit", "", -1)
		}

		client.ScopesRequest(1000)
		scopes := client.ExpectScopesResponse(t)
		if len(scopes.Body.Scopes) > 2 {
			t.Errorf("\ngot  %#v\nwant len(Scopes)=2 (Arguments & Locals)", scopes)
		}
		checkScope(t, scopes, 0, "Arguments", 1000)
		checkScope(t, scopes, 1, "Locals", 1001)

		client.VariablesRequest(1000) // Arguments
		args := client.ExpectVariablesResponse(t)
		checkChildren(t, args, "Arguments", 2)
		checkVarExact(t, args, 0, "y", "y", "0", "uint", noChildren)
		checkVarExact(t, args, 1, "~r1", "", "0", "uint", noChildren)

		client.VariablesRequest(1001) // Locals
		locals := client.ExpectVariablesResponse(t)
		checkChildren(t, locals, "Locals", 0)

		client.ContinueRequest(1)
		ctResp := client.ExpectContinueResponse(t)
		if !ctResp.Body.AllThreadsContinued {
			t.Errorf("\ngot  %#v\nwant AllThreadsContinued=true", ctResp.Body)
		}
		// "Continue" is triggered after the response is sent

		client.ExpectTerminatedEvent(t)

		// Pause request after termination should result in an error.
		// But in certain cases this request actually succeeds.
		client.PauseRequest(1)
		switch r := client.ExpectMessage(t).(type) {
		case *dap.ErrorResponse:
			if r.Message != "Unable to halt execution" {
				t.Errorf("\ngot  %#v\nwant Message='Unable to halt execution'", r)
			}
		case *dap.PauseResponse:
		default:
			t.Fatalf("Unexpected response type: expect error or pause, got %#v", r)
		}

		client.DisconnectRequest()
		client.ExpectOutputEventProcessExited(t, 0)
		client.ExpectOutputEventDetaching(t)
		client.ExpectDisconnectResponse(t)
		client.ExpectTerminatedEvent(t)
	})
}

// checkStackFrames is a helper for verifying the values within StackTraceResponse.
//     wantStartName - name of the first returned frame (ignored if "")
//     wantStartLine - file line of the first returned frame (ignored if <0).
//     wantStartID - id of the first frame returned (ignored if wantFrames is 0).
//     wantFrames - number of frames returned (length of StackTraceResponse.Body.StackFrames array).
//     wantTotalFrames - total number of stack frames available (StackTraceResponse.Body.TotalFrames).
func checkStackFrames(t *testing.T, got *dap.StackTraceResponse,
	wantStartName string, wantStartLine, wantStartID, wantFrames, wantTotalFrames int) {
	t.Helper()
	checkStackFramesNamed("", t, got, wantStartName, wantStartLine, wantStartID, wantFrames, wantTotalFrames)
}

func checkStackFramesNamed(testName string, t *testing.T, got *dap.StackTraceResponse,
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

// checkScope is a helper for verifying the values within a ScopesResponse.
//     i - index of the scope within ScopesRespose.Body.Scopes array
//     name - name of the scope
//     varRef - reference to retrieve variables of this scope
func checkScope(t *testing.T, got *dap.ScopesResponse, i int, name string, varRef int) {
	t.Helper()
	if len(got.Body.Scopes) <= i {
		t.Errorf("\ngot  %d\nwant len(Scopes)>%d", len(got.Body.Scopes), i)
	}
	goti := got.Body.Scopes[i]
	if goti.Name != name || goti.VariablesReference != varRef || goti.Expensive {
		t.Errorf("\ngot  %#v\nwant Name=%q VariablesReference=%d Expensive=false", goti, name, varRef)
	}
}

// checkChildren is a helper for verifying the number of variables within a VariablesResponse.
//      parentName - pseudoname of the enclosing variable or scope (used for error message only)
//      numChildren - number of variables/fields/elements of this variable
func checkChildren(t *testing.T, got *dap.VariablesResponse, parentName string, numChildren int) {
	t.Helper()
	if got.Body.Variables == nil {
		t.Errorf("\ngot  %s children=%#v want []", parentName, got.Body.Variables)
	}
	if len(got.Body.Variables) != numChildren {
		t.Errorf("\ngot  len(%s)=%d (children=%#v)\nwant len=%d", parentName, len(got.Body.Variables), got.Body.Variables, numChildren)
	}
}

// checkVar is a helper for verifying the values within a VariablesResponse.
//     i - index of the variable within VariablesRespose.Body.Variables array (-1 will search all vars for a match)
//     name - name of the variable
//     evalName - fully qualified variable name or alternative expression to load this variable
//     value - the value of the variable
//     useExactMatch - true if name, evalName and value are to be compared to exactly, false if to be used as regex
//     hasRef - true if the variable should have children and therefore a non-0 variable reference
//     ref - reference to retrieve children of this variable (0 if none)
func checkVar(t *testing.T, got *dap.VariablesResponse, i int, name, evalName, value, typ string, useExactMatch, hasRef bool, indexed, named int) (ref int) {
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
		t.Errorf("\ngot  %#v\nwant Variables[i].Name=%q (not found)", got, name)
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
	matchedType := false
	if useExactMatch {
		matchedType = (goti.Type == typ)
	} else {
		matchedType, _ = regexp.MatchString(typ, goti.Type)
	}
	if !matchedType {
		t.Errorf("\ngot  %s=%q\nwant %q", name, goti.Type, typ)
	}
	if indexed >= 0 && goti.IndexedVariables != indexed {
		t.Errorf("\ngot  %s=%d indexed\nwant %d indexed", name, goti.IndexedVariables, indexed)
	}
	if named >= 0 && goti.NamedVariables != named {
		t.Errorf("\ngot  %s=%d named\nwant %d named", name, goti.NamedVariables, named)
	}
	return goti.VariablesReference
}

// checkVarExact is a helper like checkVar that matches value exactly.
func checkVarExact(t *testing.T, got *dap.VariablesResponse, i int, name, evalName, value, typ string, hasRef bool) (ref int) {
	t.Helper()
	return checkVarExactIndexed(t, got, i, name, evalName, value, typ, hasRef, -1, -1)
}

// checkVarExact is a helper like checkVar that matches value exactly.
func checkVarExactIndexed(t *testing.T, got *dap.VariablesResponse, i int, name, evalName, value, typ string, hasRef bool, indexed, named int) (ref int) {
	t.Helper()
	return checkVar(t, got, i, name, evalName, value, typ, true, hasRef, indexed, named)
}

// checkVarRegex is a helper like checkVar that treats value, evalName or name as a regex.
func checkVarRegex(t *testing.T, got *dap.VariablesResponse, i int, name, evalName, value, typ string, hasRef bool) (ref int) {
	t.Helper()
	return checkVarRegexIndexed(t, got, i, name, evalName, value, typ, hasRef, -1, -1)
}

// checkVarRegex is a helper like checkVar that treats value, evalName or name as a regex.
func checkVarRegexIndexed(t *testing.T, got *dap.VariablesResponse, i int, name, evalName, value, typ string, hasRef bool, indexed, named int) (ref int) {
	t.Helper()
	return checkVar(t, got, i, name, evalName, value, typ, false, hasRef, indexed, named)
}

func expectMessageFilterStopped(t *testing.T, client *daptest.Client) dap.Message {
	msg := client.ExpectMessage(t)
	if _, isStopped := msg.(*dap.StoppedEvent); isStopped {
		msg = client.ExpectMessage(t)
	}
	return msg
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
						checkStackFramesNamed(name, t, stResp,
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
					checkStackFrames(t, stResp, "main.main", 18, startHandle, 3, 3)
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
					"mode": "exec", "program": fixture.Path, "showGlobalVariables": true, "backend": "default",
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

					checkStackFrames(t, stack, "main.foobar", startLineno, 1000, 4, 4)

					client.ScopesRequest(1000)
					scopes := client.ExpectScopesResponse(t)
					checkScope(t, scopes, 0, "Arguments", 1000)
					checkScope(t, scopes, 1, "Locals", 1001)
					checkScope(t, scopes, 2, "Globals (package main)", 1002)

					// Arguments

					client.VariablesRequest(1000)
					args := client.ExpectVariablesResponse(t)
					checkChildren(t, args, "Arguments", 2)
					checkVarExact(t, args, 0, "baz", "baz", `"bazburzum"`, "string", noChildren)
					ref := checkVarExact(t, args, 1, "bar", "bar", `main.FooBar {Baz: 10, Bur: "lorem"}`, "main.FooBar", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						bar := client.ExpectVariablesResponse(t)
						checkChildren(t, bar, "bar", 2)
						checkVarExact(t, bar, 0, "Baz", "bar.Baz", "10", "int", noChildren)
						checkVarExact(t, bar, 1, "Bur", "bar.Bur", `"lorem"`, "string", noChildren)
						validateEvaluateName(t, client, bar, 0)
						validateEvaluateName(t, client, bar, 1)
					}

					// Globals

					client.VariablesRequest(1002)
					globals := client.ExpectVariablesResponse(t)
					checkVarExact(t, globals, 0, "p1", "main.p1", "10", "int", noChildren)

					// Locals

					client.VariablesRequest(1001)
					locals := client.ExpectVariablesResponse(t)
					checkChildren(t, locals, "Locals", 31)

					// reflect.Kind == Bool
					checkVarExact(t, locals, -1, "b1", "b1", "true", "bool", noChildren)
					checkVarExact(t, locals, -1, "b2", "b2", "false", "bool", noChildren)
					// reflect.Kind == Int
					checkVarExact(t, locals, -1, "a2", "a2", "6", "int", noChildren)
					checkVarExact(t, locals, -1, "neg", "neg", "-1", "int", noChildren)
					// reflect.Kind == Int8
					checkVarExact(t, locals, -1, "i8", "i8", "1", "int8", noChildren)
					// reflect.Kind == Int16 - see testvariables2
					// reflect.Kind == Int32 - see testvariables2
					// reflect.Kind == Int64 - see testvariables2
					// reflect.Kind == Uint
					// reflect.Kind == Uint8
					checkVarExact(t, locals, -1, "u8", "u8", "255", "uint8", noChildren)
					// reflect.Kind == Uint16
					checkVarExact(t, locals, -1, "u16", "u16", "65535", "uint16", noChildren)
					// reflect.Kind == Uint32
					checkVarExact(t, locals, -1, "u32", "u32", "4294967295", "uint32", noChildren)
					// reflect.Kind == Uint64
					checkVarExact(t, locals, -1, "u64", "u64", "18446744073709551615", "uint64", noChildren)
					// reflect.Kind == Uintptr
					checkVarExact(t, locals, -1, "up", "up", "5", "uintptr", noChildren)
					// reflect.Kind == Float32
					checkVarExact(t, locals, -1, "f32", "f32", "1.2", "float32", noChildren)
					// reflect.Kind == Float64
					checkVarExact(t, locals, -1, "a3", "a3", "7.23", "float64", noChildren)
					// reflect.Kind == Complex64
					ref = checkVarExact(t, locals, -1, "c64", "c64", "(1 + 2i)", "complex64", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						c64 := client.ExpectVariablesResponse(t)
						checkChildren(t, c64, "c64", 2)
						checkVarExact(t, c64, 0, "real", "", "1", "float32", noChildren)
						checkVarExact(t, c64, 1, "imaginary", "", "2", "float32", noChildren)
					}
					// reflect.Kind == Complex128
					ref = checkVarExact(t, locals, -1, "c128", "c128", "(2 + 3i)", "complex128", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						c128 := client.ExpectVariablesResponse(t)
						checkChildren(t, c128, "c128", 2)
						checkVarExact(t, c128, 0, "real", "", "2", "float64", noChildren)
						checkVarExact(t, c128, 1, "imaginary", "", "3", "float64", noChildren)
					}
					// reflect.Kind == Array
					ref = checkVarExact(t, locals, -1, "a4", "a4", "[2]int [1,2]", "[2]int", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						a4 := client.ExpectVariablesResponse(t)
						checkChildren(t, a4, "a4", 2)
						checkVarExact(t, a4, 0, "[0]", "a4[0]", "1", "int", noChildren)
						checkVarExact(t, a4, 1, "[1]", "a4[1]", "2", "int", noChildren)
					}
					ref = checkVarExact(t, locals, -1, "a11", "a11", `[3]main.FooBar [{Baz: 1, Bur: "a"},{Baz: 2, Bur: "b"},{Baz: 3, Bur: "c"}]`, "[3]main.FooBar", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						a11 := client.ExpectVariablesResponse(t)
						checkChildren(t, a11, "a11", 3)
						checkVarExact(t, a11, 0, "[0]", "a11[0]", `main.FooBar {Baz: 1, Bur: "a"}`, "main.FooBar", hasChildren)
						ref = checkVarExact(t, a11, 1, "[1]", "a11[1]", `main.FooBar {Baz: 2, Bur: "b"}`, "main.FooBar", hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							a11_1 := client.ExpectVariablesResponse(t)
							checkChildren(t, a11_1, "a11[1]", 2)
							checkVarExact(t, a11_1, 0, "Baz", "a11[1].Baz", "2", "int", noChildren)
							checkVarExact(t, a11_1, 1, "Bur", "a11[1].Bur", `"b"`, "string", noChildren)
							validateEvaluateName(t, client, a11_1, 0)
							validateEvaluateName(t, client, a11_1, 1)
						}
						checkVarExact(t, a11, 2, "[2]", "a11[2]", `main.FooBar {Baz: 3, Bur: "c"}`, "main.FooBar", hasChildren)
					}

					// reflect.Kind == Chan - see testvariables2
					// reflect.Kind == Func - see testvariables2
					// reflect.Kind == Interface - see testvariables2
					// reflect.Kind == Map - see testvariables2
					// reflect.Kind == Ptr
					ref = checkVarExact(t, locals, -1, "a7", "a7", `*main.FooBar {Baz: 5, Bur: "strum"}`, "*main.FooBar", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						a7 := client.ExpectVariablesResponse(t)
						checkChildren(t, a7, "a7", 1)
						ref = checkVarExact(t, a7, 0, "", "(*a7)", `main.FooBar {Baz: 5, Bur: "strum"}`, "main.FooBar", hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							a7val := client.ExpectVariablesResponse(t)
							checkChildren(t, a7val, "*a7", 2)
							checkVarExact(t, a7val, 0, "Baz", "(*a7).Baz", "5", "int", noChildren)
							checkVarExact(t, a7val, 1, "Bur", "(*a7).Bur", `"strum"`, "string", noChildren)
							validateEvaluateName(t, client, a7val, 0)
							validateEvaluateName(t, client, a7val, 1)
						}
					}
					// TODO(polina): how to test for "nil" (without type) and "void"?
					checkVarExact(t, locals, -1, "a9", "a9", "*main.FooBar nil", "*main.FooBar", noChildren)
					// reflect.Kind == Slice
					ref = checkVarExact(t, locals, -1, "a5", "a5", "[]int len: 5, cap: 5, [1,2,3,4,5]", "[]int", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						a5 := client.ExpectVariablesResponse(t)
						checkChildren(t, a5, "a5", 5)
						checkVarExact(t, a5, 0, "[0]", "a5[0]", "1", "int", noChildren)
						checkVarExact(t, a5, 4, "[4]", "a5[4]", "5", "int", noChildren)
						validateEvaluateName(t, client, a5, 0)
						validateEvaluateName(t, client, a5, 1)
					}
					ref = checkVarExact(t, locals, -1, "a12", "a12", `[]main.FooBar len: 2, cap: 2, [{Baz: 4, Bur: "d"},{Baz: 5, Bur: "e"}]`, "[]main.FooBar", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						a12 := client.ExpectVariablesResponse(t)
						checkChildren(t, a12, "a12", 2)
						checkVarExact(t, a12, 0, "[0]", "a12[0]", `main.FooBar {Baz: 4, Bur: "d"}`, "main.FooBar", hasChildren)
						ref = checkVarExact(t, a12, 1, "[1]", "a12[1]", `main.FooBar {Baz: 5, Bur: "e"}`, "main.FooBar", hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							a12_1 := client.ExpectVariablesResponse(t)
							checkChildren(t, a12_1, "a12[1]", 2)
							checkVarExact(t, a12_1, 0, "Baz", "a12[1].Baz", "5", "int", noChildren)
							checkVarExact(t, a12_1, 1, "Bur", "a12[1].Bur", `"e"`, "string", noChildren)
							validateEvaluateName(t, client, a12_1, 0)
							validateEvaluateName(t, client, a12_1, 1)
						}
					}
					ref = checkVarExact(t, locals, -1, "a13", "a13", `[]*main.FooBar len: 3, cap: 3, [*{Baz: 6, Bur: "f"},*{Baz: 7, Bur: "g"},*{Baz: 8, Bur: "h"}]`, "[]*main.FooBar", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						a13 := client.ExpectVariablesResponse(t)
						checkChildren(t, a13, "a13", 3)
						checkVarExact(t, a13, 0, "[0]", "a13[0]", `*main.FooBar {Baz: 6, Bur: "f"}`, "*main.FooBar", hasChildren)
						checkVarExact(t, a13, 1, "[1]", "a13[1]", `*main.FooBar {Baz: 7, Bur: "g"}`, "*main.FooBar", hasChildren)
						ref = checkVarExact(t, a13, 2, "[2]", "a13[2]", `*main.FooBar {Baz: 8, Bur: "h"}`, "*main.FooBar", hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							a13_2 := client.ExpectVariablesResponse(t)
							checkChildren(t, a13_2, "a13[2]", 1)
							ref = checkVarExact(t, a13_2, 0, "", "(*a13[2])", `main.FooBar {Baz: 8, Bur: "h"}`, "main.FooBar", hasChildren)
							validateEvaluateName(t, client, a13_2, 0)
							if ref > 0 {
								client.VariablesRequest(ref)
								val := client.ExpectVariablesResponse(t)
								checkChildren(t, val, "*a13[2]", 2)
								checkVarExact(t, val, 0, "Baz", "(*a13[2]).Baz", "8", "int", noChildren)
								checkVarExact(t, val, 1, "Bur", "(*a13[2]).Bur", `"h"`, "string", noChildren)
								validateEvaluateName(t, client, val, 0)
								validateEvaluateName(t, client, val, 1)
							}
						}
					}
					// reflect.Kind == String
					checkVarExact(t, locals, -1, "a1", "a1", `"foofoofoofoofoofoo"`, "string", noChildren)
					checkVarExact(t, locals, -1, "a10", "a10", `"ofo"`, "string", noChildren)
					// reflect.Kind == Struct
					ref = checkVarExact(t, locals, -1, "a6", "a6", `main.FooBar {Baz: 8, Bur: "word"}`, "main.FooBar", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						a6 := client.ExpectVariablesResponse(t)
						checkChildren(t, a6, "a6", 2)
						checkVarExact(t, a6, 0, "Baz", "a6.Baz", "8", "int", noChildren)
						checkVarExact(t, a6, 1, "Bur", "a6.Bur", `"word"`, "string", noChildren)
					}
					ref = checkVarExact(t, locals, -1, "a8", "a8", `main.FooBar2 {Bur: 10, Baz: "feh"}`, "main.FooBar2", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						a8 := client.ExpectVariablesResponse(t)
						checkChildren(t, a8, "a8", 2)
						checkVarExact(t, a8, 0, "Bur", "a8.Bur", "10", "int", noChildren)
						checkVarExact(t, a8, 1, "Baz", "a8.Baz", `"feh"`, "string", noChildren)
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
					checkStackFrames(t, stack, "main.barfoo", 27, 1000, 5, 5)

					client.ScopesRequest(1000)
					scopes := client.ExpectScopesResponse(t)
					checkScope(t, scopes, 0, "Arguments", 1000)
					checkScope(t, scopes, 1, "Locals", 1001)
					checkScope(t, scopes, 2, "Globals (package main)", 1002)

					client.ScopesRequest(1111)
					erres := client.ExpectInvisibleErrorResponse(t)
					if erres.Body.Error.Format != "Unable to list locals: unknown frame id 1111" {
						t.Errorf("\ngot %#v\nwant Format=\"Unable to list locals: unknown frame id 1111\"", erres)
					}

					client.VariablesRequest(1000) // Arguments
					args := client.ExpectVariablesResponse(t)
					checkChildren(t, args, "Arguments", 0)

					client.VariablesRequest(1001) // Locals
					locals := client.ExpectVariablesResponse(t)
					checkChildren(t, locals, "Locals", 1)
					checkVarExact(t, locals, -1, "a1", "a1", `"bur"`, "string", noChildren)

					client.VariablesRequest(1002) // Globals
					globals := client.ExpectVariablesResponse(t)
					checkVarExact(t, globals, 0, "p1", "main.p1", "10", "int", noChildren)

					client.VariablesRequest(7777)
					erres = client.ExpectInvisibleErrorResponse(t)
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
					checkStackFrames(t, stack, "main.main", -1, 1000, 3, 3)

					client.ScopesRequest(1000)
					scopes := client.ExpectScopesResponse(t)
					checkScope(t, scopes, 0, "Arguments", 1000)
					checkScope(t, scopes, 1, "Locals", 1001)
				},
				disconnect: false,
			}, {
				execute: func() {
					client.StackTraceRequest(1, 0, 20)
					stack := client.ExpectStackTraceResponse(t)
					checkStackFrames(t, stack, "main.main", -1, 1000, 3, 3)

					client.ScopesRequest(1000)
					scopes := client.ExpectScopesResponse(t)
					if len(scopes.Body.Scopes) > 2 {
						t.Errorf("\ngot  %#v\nwant len(scopes)=2 (Argumes & Locals)", scopes)
					}
					checkScope(t, scopes, 0, "Arguments", 1000)
					checkScope(t, scopes, 1, "Locals", 1001)

					// Arguments

					client.VariablesRequest(1000)
					args := client.ExpectVariablesResponse(t)
					checkChildren(t, args, "Arguments", 0)

					// Locals

					client.VariablesRequest(1001)
					locals := client.ExpectVariablesResponse(t)

					// reflect.Kind == Bool - see testvariables
					// reflect.Kind == Int - see testvariables
					// reflect.Kind == Int8
					checkVarExact(t, locals, -1, "ni8", "ni8", "-5", "int8", noChildren)
					// reflect.Kind == Int16
					checkVarExact(t, locals, -1, "ni16", "ni16", "-5", "int16", noChildren)
					// reflect.Kind == Int32
					checkVarExact(t, locals, -1, "ni32", "ni32", "-5", "int32", noChildren)
					// reflect.Kind == Int64
					checkVarExact(t, locals, -1, "ni64", "ni64", "-5", "int64", noChildren)
					// reflect.Kind == Uint
					// reflect.Kind == Uint8 - see testvariables
					// reflect.Kind == Uint16 - see testvariables
					// reflect.Kind == Uint32 - see testvariables
					// reflect.Kind == Uint64 - see testvariables
					// reflect.Kind == Uintptr - see testvariables
					// reflect.Kind == Float32 - see testvariables
					// reflect.Kind == Float64
					checkVarExact(t, locals, -1, "pinf", "pinf", "+Inf", "float64", noChildren)
					checkVarExact(t, locals, -1, "ninf", "ninf", "-Inf", "float64", noChildren)
					checkVarExact(t, locals, -1, "nan", "nan", "NaN", "float64", noChildren)
					// reflect.Kind == Complex64 - see testvariables
					// reflect.Kind == Complex128 - see testvariables
					// reflect.Kind == Array
					checkVarExact(t, locals, -1, "a0", "a0", "[0]int []", "[0]int", noChildren)
					// reflect.Kind == Chan
					ref := checkVarExact(t, locals, -1, "ch1", "ch1", "chan int 4/11", "chan int", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						ch1 := client.ExpectVariablesResponse(t)
						checkChildren(t, ch1, "ch1", 11)
						checkVarExact(t, ch1, 0, "qcount", "ch1.qcount", "4", "uint", noChildren)
						checkVarRegex(t, ch1, 10, "lock", "ch1.lock", `runtime\.mutex {.*key: 0.*}`, `runtime\.mutex`, hasChildren)
						validateEvaluateName(t, client, ch1, 0)
						validateEvaluateName(t, client, ch1, 10)
					}
					checkVarExact(t, locals, -1, "chnil", "chnil", "chan int nil", "chan int", noChildren)
					// reflect.Kind == Func
					checkVarExact(t, locals, -1, "fn1", "fn1", "main.afunc", "main.functype", noChildren)
					checkVarExact(t, locals, -1, "fn2", "fn2", "nil", "main.functype", noChildren)
					// reflect.Kind == Interface
					checkVarExact(t, locals, -1, "ifacenil", "ifacenil", "interface {} nil", "interface {}", noChildren)
					ref = checkVarExact(t, locals, -1, "iface2", "iface2", "interface {}(string) \"test\"", "interface {}", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						iface2 := client.ExpectVariablesResponse(t)
						checkChildren(t, iface2, "iface2", 1)
						checkVarExact(t, iface2, 0, "data", "iface2.(data)", `"test"`, "string", noChildren)
						validateEvaluateName(t, client, iface2, 0)
					}
					ref = checkVarExact(t, locals, -1, "iface4", "iface4", "interface {}([]go/constant.Value) [4]", "interface {}", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						iface4 := client.ExpectVariablesResponse(t)
						checkChildren(t, iface4, "iface4", 1)
						ref = checkVarExact(t, iface4, 0, "data", "iface4.(data)", "[]go/constant.Value len: 1, cap: 1, [4]", "[]go/constant.Value", hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							iface4data := client.ExpectVariablesResponse(t)
							checkChildren(t, iface4data, "iface4.data", 1)
							ref = checkVarExact(t, iface4data, 0, "[0]", "iface4.(data)[0]", "go/constant.Value(go/constant.int64Val) 4", "go/constant.Value", hasChildren)
							if ref > 0 {
								client.VariablesRequest(ref)
								iface4data0 := client.ExpectVariablesResponse(t)
								checkChildren(t, iface4data0, "iface4.data[0]", 1)
								checkVarExact(t, iface4data0, 0, "data", "iface4.(data)[0].(data)", "4", "go/constant.int64Val", noChildren)
								validateEvaluateName(t, client, iface4data0, 0)
							}
						}
					}
					checkVarExact(t, locals, -1, "errnil", "errnil", "error nil", "error", noChildren)
					ref = checkVarExact(t, locals, -1, "err1", "err1", "error(*main.astruct) *{A: 1, B: 2}", "error", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						err1 := client.ExpectVariablesResponse(t)
						checkChildren(t, err1, "err1", 1)
						checkVarExact(t, err1, 0, "data", "err1.(data)", "*main.astruct {A: 1, B: 2}", "*main.astruct", hasChildren)
						validateEvaluateName(t, client, err1, 0)
					}
					ref = checkVarExact(t, locals, -1, "ptrinf", "ptrinf", "*interface {}(**interface {}) **...", "*interface {}", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						ptrinf_val := client.ExpectVariablesResponse(t)
						checkChildren(t, ptrinf_val, "*ptrinf", 1)
						ref = checkVarExact(t, ptrinf_val, 0, "", "(*ptrinf)", "interface {}(**interface {}) **...", "interface {}", hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							ptrinf_val_data := client.ExpectVariablesResponse(t)
							checkChildren(t, ptrinf_val_data, "(*ptrinf).data", 1)
							checkVarExact(t, ptrinf_val_data, 0, "data", "(*ptrinf).(data)", "**interface {}(**interface {}) ...", "**interface {}", hasChildren)
							validateEvaluateName(t, client, ptrinf_val_data, 0)
						}
					}
					// reflect.Kind == Map
					checkVarExact(t, locals, -1, "mnil", "mnil", "map[string]main.astruct nil", "map[string]main.astruct", noChildren)
					// key - scalar, value - compound
					ref = checkVarExact(t, locals, -1, "m2", "m2", "map[int]*main.astruct [1: *{A: 10, B: 11}, ]", "map[int]*main.astruct", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						m2 := client.ExpectVariablesResponse(t)
						checkChildren(t, m2, "m2", 2) // each key-value represented by a single child
						checkVarExact(t, m2, 0, "len()", "len(m2)", "1", "int", noChildren)
						ref = checkVarExact(t, m2, 1, "1", "m2[1]", "*main.astruct {A: 10, B: 11}", "int: *main.astruct", hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							m2kv1 := client.ExpectVariablesResponse(t)
							checkChildren(t, m2kv1, "m2[1]", 1)
							ref = checkVarExact(t, m2kv1, 0, "", "(*m2[1])", "main.astruct {A: 10, B: 11}", "main.astruct", hasChildren)
							if ref > 0 {
								client.VariablesRequest(ref)
								m2kv1deref := client.ExpectVariablesResponse(t)
								checkChildren(t, m2kv1deref, "*m2[1]", 2)
								checkVarExact(t, m2kv1deref, 0, "A", "(*m2[1]).A", "10", "int", noChildren)
								checkVarExact(t, m2kv1deref, 1, "B", "(*m2[1]).B", "11", "int", noChildren)
								validateEvaluateName(t, client, m2kv1deref, 0)
								validateEvaluateName(t, client, m2kv1deref, 1)
							}
						}
					}
					// key - compound, value - scalar
					ref = checkVarExact(t, locals, -1, "m3", "m3", "map[main.astruct]int [{A: 1, B: 1}: 42, {A: 2, B: 2}: 43, ]", "map[main.astruct]int", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						m3 := client.ExpectVariablesResponse(t)
						checkChildren(t, m3, "m3", 3) // each key-value represented by a single child
						checkVarExact(t, m3, 0, "len()", "len(m3)", "2", "int", noChildren)
						ref = checkVarRegex(t, m3, 1, `main\.astruct {A: 1, B: 1}`, `m3\[\(\*\(\*"main.astruct"\)\(0x[0-9a-f]+\)\)\]`, "42", "int", hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							m3kv0 := client.ExpectVariablesResponse(t)
							checkChildren(t, m3kv0, "m3[0]", 2)
							checkVarRegex(t, m3kv0, 0, "A", `\(*\(*"main\.astruct"\)\(0x[0-9a-f]+\)\)\.A`, "1", "int", noChildren)
							validateEvaluateName(t, client, m3kv0, 0)
						}
						ref = checkVarRegex(t, m3, 2, `main\.astruct {A: 2, B: 2}`, `m3\[\(\*\(\*"main.astruct"\)\(0x[0-9a-f]+\)\)\]`, "43", "", hasChildren)
						if ref > 0 { // inspect another key from another key-value child
							client.VariablesRequest(ref)
							m3kv1 := client.ExpectVariablesResponse(t)
							checkChildren(t, m3kv1, "m3[1]", 2)
							checkVarRegex(t, m3kv1, 1, "B", `\(*\(*"main\.astruct"\)\(0x[0-9a-f]+\)\)\.B`, "2", "int", noChildren)
							validateEvaluateName(t, client, m3kv1, 1)
						}
					}
					// key - compound, value - compound
					ref = checkVarExact(t, locals, -1, "m4", "m4", "map[main.astruct]main.astruct [{A: 1, B: 1}: {A: 11, B: 11}, {A: 2, B: 2}: {A: 22, B: 22}, ]", "map[main.astruct]main.astruct", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						m4 := client.ExpectVariablesResponse(t)
						checkChildren(t, m4, "m4", 5) // each key and value represented by a child, so double the key-value count
						checkVarExact(t, m4, 0, "len()", "len(m4)", "2", "int", noChildren)
						checkVarRegex(t, m4, 1, `\[key 0\]`, `\(\*\(\*"main\.astruct"\)\(0x[0-9a-f]+\)\)`, `main\.astruct {A: 1, B: 1}`, `main\.astruct`, hasChildren)
						checkVarRegex(t, m4, 2, `\[val 0\]`, `m4\[\(\*\(\*"main\.astruct"\)\(0x[0-9a-f]+\)\)\]`, `main\.astruct {A: 11, B: 11}`, `main\.astruct`, hasChildren)
						ref = checkVarRegex(t, m4, 3, `\[key 1\]`, `\(\*\(\*"main\.astruct"\)\(0x[0-9a-f]+\)\)`, `main\.astruct {A: 2, B: 2}`, `main\.astruct`, hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							m4Key1 := client.ExpectVariablesResponse(t)
							checkChildren(t, m4Key1, "m4Key1", 2)
							checkVarRegex(t, m4Key1, 0, "A", `\(\*\(\*"main\.astruct"\)\(0x[0-9a-f]+\)\)\.A`, "2", "int", noChildren)
							checkVarRegex(t, m4Key1, 1, "B", `\(\*\(\*"main\.astruct"\)\(0x[0-9a-f]+\)\)\.B`, "2", "int", noChildren)
							validateEvaluateName(t, client, m4Key1, 0)
							validateEvaluateName(t, client, m4Key1, 1)
						}
						ref = checkVarRegex(t, m4, 4, `\[val 1\]`, `m4\[\(\*\(\*"main\.astruct"\)\(0x[0-9a-f]+\)\)\]`, `main\.astruct {A: 22, B: 22}`, "main.astruct", hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							m4Val1 := client.ExpectVariablesResponse(t)
							checkChildren(t, m4Val1, "m4Val1", 2)
							checkVarRegex(t, m4Val1, 0, "A", `m4\[\(\*\(\*"main\.astruct"\)\(0x[0-9a-f]+\)\)\]\.A`, "22", "int", noChildren)
							checkVarRegex(t, m4Val1, 1, "B", `m4\[\(\*\(\*"main\.astruct"\)\(0x[0-9a-f]+\)\)\]\.B`, "22", "int", noChildren)
							validateEvaluateName(t, client, m4Val1, 0)
							validateEvaluateName(t, client, m4Val1, 1)
						}
					}
					checkVarExact(t, locals, -1, "emptymap", "emptymap", "map[string]string []", "map[string]string", noChildren)
					// reflect.Kind == Ptr
					ref = checkVarExact(t, locals, -1, "pp1", "pp1", "**1", "**int", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						pp1val := client.ExpectVariablesResponse(t)
						checkChildren(t, pp1val, "*pp1", 1)
						ref = checkVarExact(t, pp1val, 0, "", "(*pp1)", "*1", "*int", hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							pp1valval := client.ExpectVariablesResponse(t)
							checkChildren(t, pp1valval, "*(*pp1)", 1)
							checkVarExact(t, pp1valval, 0, "", "(*(*pp1))", "1", "int", noChildren)
							validateEvaluateName(t, client, pp1valval, 0)
						}
					}
					// reflect.Kind == Slice
					ref = checkVarExact(t, locals, -1, "zsslice", "zsslice", "[]struct {} len: 3, cap: 3, [{},{},{}]", "[]struct {}", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						zsslice := client.ExpectVariablesResponse(t)
						checkChildren(t, zsslice, "zsslice", 3)
						checkVarExact(t, zsslice, 2, "[2]", "zsslice[2]", "struct {} {}", "struct {}", noChildren)
						validateEvaluateName(t, client, zsslice, 2)
					}
					checkVarExact(t, locals, -1, "emptyslice", "emptyslice", "[]string len: 0, cap: 0, []", "[]string", noChildren)
					checkVarExact(t, locals, -1, "nilslice", "nilslice", "[]int len: 0, cap: 0, nil", "[]int", noChildren)
					// reflect.Kind == String
					checkVarExact(t, locals, -1, "longstr", "longstr", longstr, "string", noChildren)
					// reflect.Kind == Struct
					checkVarExact(t, locals, -1, "zsvar", "zsvar", "struct {} {}", "struct {}", noChildren)
					// reflect.Kind == UnsafePointer
					// TODO(polina): how do I test for unsafe.Pointer(nil)?
					checkVarRegex(t, locals, -1, "upnil", "upnil", `unsafe\.Pointer\(0x0\)`, "int", noChildren)
					checkVarRegex(t, locals, -1, "up1", "up1", `unsafe\.Pointer\(0x[0-9a-f]+\)`, "int", noChildren)

					// Test unreadable variable
					ref = checkVarRegex(t, locals, -1, "unread", "unread", `\*\(unreadable .+\)`, "int", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						val := client.ExpectVariablesResponse(t)
						checkChildren(t, val, "*unread", 1)
						checkVarRegex(t, val, 0, "^$", `\(\*unread\)`, `\(unreadable .+\)`, "int", noChildren)
						validateEvaluateName(t, client, val, 0)
					}
				},
				disconnect: true,
			}})
	})
}

// TestScopesRequestsOptimized executes to a breakpoint and tests different
// that the names of the "Locals" and "Arguments" scopes are correctly annotated with
// a warning about debugging an optimized function.
func TestScopesRequestsOptimized(t *testing.T) {
	runTestBuildFlags(t, "testvariables", func(client *daptest.Client, fixture protest.Fixture) {
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

					checkStackFrames(t, stack, "main.foobar", startLineno, 1000, 4, 4)

					client.ScopesRequest(1000)
					scopes := client.ExpectScopesResponse(t)
					checkScope(t, scopes, 0, "Arguments (warning: optimized function)", 1000)
					checkScope(t, scopes, 1, "Locals (warning: optimized function)", 1001)
					checkScope(t, scopes, 2, "Globals (package main)", 1002)
				},
				disconnect: false,
			}, {
				// Stop at second breakpoint
				execute: func() {
					// Frame ids get reset at each breakpoint.
					client.StackTraceRequest(1, 0, 20)
					stack := client.ExpectStackTraceResponse(t)
					checkStackFrames(t, stack, "main.barfoo", 27, 1000, 5, 5)

					client.ScopesRequest(1000)
					scopes := client.ExpectScopesResponse(t)
					checkScope(t, scopes, 0, "Arguments (warning: optimized function)", 1000)
					checkScope(t, scopes, 1, "Locals (warning: optimized function)", 1001)
					checkScope(t, scopes, 2, "Globals (package main)", 1002)
				},
				disconnect: false,
			}})
	},
		protest.EnableOptimization)
}

// TestVariablesLoading exposes test cases where variables might be partially or
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
					saveDefaultConfig := DefaultLoadConfig
					DefaultLoadConfig.MaxStructFields = 5
					DefaultLoadConfig.MaxStringLen = 64
					defer func() {
						DefaultLoadConfig = saveDefaultConfig
					}()

					client.StackTraceRequest(1, 0, 0)
					client.ExpectStackTraceResponse(t)

					client.ScopesRequest(1000)
					client.ExpectScopesResponse(t)

					client.VariablesRequest(1001) // Locals
					locals := client.ExpectVariablesResponse(t)

					// String partially missing based on LoadConfig.MaxStringLen
					// See also TestVariableLoadingOfLongStrings
					checkVarExact(t, locals, -1, "longstr", "longstr", longstrLoaded64, "string", noChildren)

					checkArrayChildren := func(t *testing.T, longarr *dap.VariablesResponse, parentName string, start int) {
						t.Helper()
						for i, child := range longarr.Body.Variables {
							idx := start + i
							if child.Name != fmt.Sprintf("[%d]", idx) || child.EvaluateName != fmt.Sprintf("%s[%d]", parentName, idx) {
								t.Errorf("Expected %s[%d] to have Name=\"[%d]\" EvaluateName=\"%s[%d]\", got %#v", parentName, idx, idx, parentName, idx, child)
							}
						}
					}

					// Array not fully loaded based on LoadConfig.MaxArrayValues.
					// Expect to be able to load array by paging.
					ref := checkVarExactIndexed(t, locals, -1, "longarr", "longarr", "[100]int [0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,...+36 more]", "[100]int", hasChildren, 100, 0)
					if ref > 0 {
						client.VariablesRequest(ref)
						longarr := client.ExpectVariablesResponse(t)
						checkChildren(t, longarr, "longarr", 64)
						checkArrayChildren(t, longarr, "longarr", 0)

						client.IndexedVariablesRequest(ref, 0, 100)
						longarr = client.ExpectVariablesResponse(t)
						checkChildren(t, longarr, "longarr", 100)
						checkArrayChildren(t, longarr, "longarr", 0)

						client.IndexedVariablesRequest(ref, 50, 50)
						longarr = client.ExpectVariablesResponse(t)
						checkChildren(t, longarr, "longarr", 50)
						checkArrayChildren(t, longarr, "longarr", 50)
					}

					// Slice not fully loaded based on LoadConfig.MaxArrayValues.
					// Expect to be able to load slice by paging.
					ref = checkVarExactIndexed(t, locals, -1, "longslice", "longslice", "[]int len: 100, cap: 100, [0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,...+36 more]", "[]int", hasChildren, 100, 0)
					if ref > 0 {
						client.VariablesRequest(ref)
						longarr := client.ExpectVariablesResponse(t)
						checkChildren(t, longarr, "longslice", 64)
						checkArrayChildren(t, longarr, "longslice", 0)

						client.IndexedVariablesRequest(ref, 0, 100)
						longarr = client.ExpectVariablesResponse(t)
						checkChildren(t, longarr, "longslice", 100)
						checkArrayChildren(t, longarr, "longslice", 0)

						client.IndexedVariablesRequest(ref, 50, 50)
						longarr = client.ExpectVariablesResponse(t)
						checkChildren(t, longarr, "longslice", 50)
						checkArrayChildren(t, longarr, "longslice", 50)
					}

					// Map not fully loaded based on LoadConfig.MaxArrayValues
					// Expect to be able to load map by paging.
					ref = checkVarRegexIndexed(t, locals, -1, "m1", "m1", `map\[string\]main\.astruct \[.+\.\.\.`, `map\[string\]main\.astruct`, hasChildren, 66, 1)
					if ref > 0 {
						client.VariablesRequest(ref)
						m1 := client.ExpectVariablesResponse(t)
						checkChildren(t, m1, "m1", 65)

						client.IndexedVariablesRequest(ref, 0, 66)
						m1 = client.ExpectVariablesResponse(t)
						checkChildren(t, m1, "m1", 66)

						client.IndexedVariablesRequest(ref, 0, 33)
						m1part1 := client.ExpectVariablesResponse(t)
						checkChildren(t, m1part1, "m1", 33)

						client.IndexedVariablesRequest(ref, 33, 33)
						m1part2 := client.ExpectVariablesResponse(t)
						checkChildren(t, m1part2, "m1", 33)

						if len(m1part1.Body.Variables)+len(m1part2.Body.Variables) == len(m1.Body.Variables) {
							for i, got := range m1part1.Body.Variables {
								want := m1.Body.Variables[i]
								if got.Name != want.Name || got.Value != want.Value {
									t.Errorf("got %#v, want Name=%q Value=%q", got, want.Name, want.Value)
								}
							}
							for i, got := range m1part2.Body.Variables {
								want := m1.Body.Variables[i+len(m1part1.Body.Variables)]
								if got.Name != want.Name || got.Value != want.Value {
									t.Errorf("got %#v, want Name=%q Value=%q", got, want.Name, want.Value)
								}
							}
						}
						client.NamedVariablesRequest(ref)
						named := client.ExpectVariablesResponse(t)
						checkChildren(t, named, "m1", 1)
						checkVarExact(t, named, 0, "len()", "len(m1)", "66", "int", noChildren)
					}

					// Struct partially missing based on LoadConfig.MaxStructFields
					ref = checkVarExact(t, locals, -1, "sd", "sd", "(loaded 5/6) main.D {u1: 0, u2: 0, u3: 0, u4: 0, u5: 0,...+1 more}", "main.D", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						sd := client.ExpectVariablesResponse(t)
						checkChildren(t, sd, "sd", 5)
					}

					// Fully missing struct auto-loaded when reaching LoadConfig.MaxVariableRecurse (also tests evaluateName corner case)
					ref = checkVarRegex(t, locals, -1, "c1", "c1", `main\.cstruct {pb: \*main\.bstruct {a: \(\*main\.astruct\)\(0x[0-9a-f]+\)}, sa: []\*main\.astruct len: 3, cap: 3, [\*\(\*main\.astruct\)\(0x[0-9a-f]+\),\*\(\*main\.astruct\)\(0x[0-9a-f]+\),\*\(\*main.astruct\)\(0x[0-9a-f]+\)]}`, `main\.cstruct`, hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						c1 := client.ExpectVariablesResponse(t)
						checkChildren(t, c1, "c1", 2)
						ref = checkVarRegex(t, c1, 1, "sa", `c1\.sa`, `\[\]\*main\.astruct len: 3, cap: 3, \[\*\(\*main\.astruct\)\(0x[0-9a-f]+\),\*\(\*main\.astruct\)\(0x[0-9a-f]+\),\*\(\*main\.astruct\)\(0x[0-9a-f]+\)\]`, `\[\]\*main\.astruct`, hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							c1sa := client.ExpectVariablesResponse(t)
							checkChildren(t, c1sa, "c1.sa", 3)
							ref = checkVarRegex(t, c1sa, 0, `\[0\]`, `c1\.sa\[0\]`, `\*\(\*main\.astruct\)\(0x[0-9a-f]+\)`, `\*main\.astruct`, hasChildren)
							if ref > 0 {
								// Auto-loading of fully missing struc children happens here
								client.VariablesRequest(ref)
								c1sa0 := client.ExpectVariablesResponse(t)
								checkChildren(t, c1sa0, "c1.sa[0]", 1)
								// TODO(polina): there should be children here once we support auto loading
								checkVarExact(t, c1sa0, 0, "", "(*c1.sa[0])", "main.astruct {A: 1, B: 2}", "main.astruct", hasChildren)
							}
						}
					}

					// Fully missing struct auto-loaded when hitting LoadConfig.MaxVariableRecurse (also tests evaluteName corner case)
					ref = checkVarRegex(t, locals, -1, "aas", "aas", `\[\]main\.a len: 1, cap: 1, \[{aas: \[\]main\.a len: 1, cap: 1, \[\(\*main\.a\)\(0x[0-9a-f]+\)\]}\]`, `\[\]main\.a`, hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						aas := client.ExpectVariablesResponse(t)
						checkChildren(t, aas, "aas", 1)
						ref = checkVarRegex(t, aas, 0, "[0]", `aas\[0\]`, `main\.a {aas: \[\]main.a len: 1, cap: 1, \[\(\*main\.a\)\(0x[0-9a-f]+\)\]}`, `main\.a`, hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							aas0 := client.ExpectVariablesResponse(t)
							checkChildren(t, aas0, "aas[0]", 1)
							ref = checkVarRegex(t, aas0, 0, "aas", `aas\[0\]\.aas`, `\[\]main\.a len: 1, cap: 1, \[\(\*main\.a\)\(0x[0-9a-f]+\)\]`, `\[\]main\.a`, hasChildren)
							if ref > 0 {
								// Auto-loading of fully missing struct children happens here
								client.VariablesRequest(ref)
								aas0aas := client.ExpectVariablesResponse(t)
								checkChildren(t, aas0aas, "aas[0].aas", 1)
								// TODO(polina): there should be a child here once we support auto loading - test for "aas[0].aas[0].aas"
								ref = checkVarRegex(t, aas0aas, 0, "[0]", `aas\[0\]\.aas\[0\]`, `main\.a {aas: \[\]main\.a len: 1, cap: 1, \[\(\*main\.a\)\(0x[0-9a-f]+\)\]}`, "main.a", hasChildren)
								if ref > 0 {
									client.VariablesRequest(ref)
									aas0aas0 := client.ExpectVariablesResponse(t)
									checkChildren(t, aas0aas, "aas[0].aas[0]", 1)
									checkVarRegex(t, aas0aas0, 0, "aas", `aas\[0\]\.aas\[0\]\.aas`, `\[\]main\.a len: 1, cap: 1, \[\(\*main\.a\)\(0x[0-9a-f]+\)\]`, `\[\]main\.a`, hasChildren)
								}
							}
						}
					}

					// Fully missing map auto-loaded when hitting LoadConfig.MaxVariableRecurse (also tests evaluateName corner case)
					ref = checkVarExact(t, locals, -1, "tm", "tm", "main.truncatedMap {v: []map[string]main.astruct len: 1, cap: 1, [[...]]}", "main.truncatedMap", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						tm := client.ExpectVariablesResponse(t)
						checkChildren(t, tm, "tm", 1)
						ref = checkVarExact(t, tm, 0, "v", "tm.v", "[]map[string]main.astruct len: 1, cap: 1, [[...]]", "[]map[string]main.astruct", hasChildren)
						if ref > 0 {
							// Auto-loading of fully missing map chidlren happens here, but they get trancated at MaxArrayValuess
							client.VariablesRequest(ref)
							tmV := client.ExpectVariablesResponse(t)
							checkChildren(t, tmV, "tm.v", 1)
							ref = checkVarRegex(t, tmV, 0, `\[0\]`, `tm\.v\[0\]`, `map\[string\]main\.astruct \[.+\.\.\.`, `map\[string\]main\.astruct`, hasChildren)
							if ref > 0 {
								client.VariablesRequest(ref)
								tmV0 := client.ExpectVariablesResponse(t)
								checkChildren(t, tmV0, "tm.v[0]", 65)
							}
						}
					}

					// Auto-loading works with call return variables as well
					protest.MustSupportFunctionCalls(t, testBackend)
					client.EvaluateRequest("call rettm()", 1000, "repl")
					got := client.ExpectEvaluateResponse(t)
					ref = checkEval(t, got, "main.truncatedMap {v: []map[string]main.astruct len: 1, cap: 1, [[...]]}", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						rv := client.ExpectVariablesResponse(t)
						checkChildren(t, rv, "rv", 1)
						ref = checkVarExact(t, rv, 0, "~r0", "", "main.truncatedMap {v: []map[string]main.astruct len: 1, cap: 1, [[...]]}", "main.truncatedMap", hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							tm := client.ExpectVariablesResponse(t)
							checkChildren(t, tm, "tm", 1)
							ref = checkVarExact(t, tm, 0, "v", "", "[]map[string]main.astruct len: 1, cap: 1, [[...]]", "[]map[string]main.astruct", hasChildren)
							if ref > 0 {
								// Auto-loading of fully missing map chidlren happens here, but they get trancated at MaxArrayValuess
								client.VariablesRequest(ref)
								tmV := client.ExpectVariablesResponse(t)
								checkChildren(t, tmV, "tm.v", 1)
								// TODO(polina): this evaluate name is not usable - it should be empty
								ref = checkVarRegex(t, tmV, 0, `\[0\]`, `\[0\]`, `map\[string\]main\.astruct \[.+\.\.\.`, `map\[string\]main\.astruct`, hasChildren)
								if ref > 0 {
									client.VariablesRequest(ref)
									tmV0 := client.ExpectVariablesResponse(t)
									checkChildren(t, tmV0, "tm.v[0]", 65)
								}
							}
						}
					}

					// TODO(polina): need fully missing array/slice test case

					// Zero slices, structs and maps are not treated as fully missing
					// See zsvar, zsslice,, emptyslice, emptymap, a0
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

						ref := checkVarRegex(t, locals, -1, "ni", "ni", `\[\]interface {} len: 1, cap: 1, \[\[\]interface {} len: 1, cap: 1, \[\*\(\*interface {}\)\(0x[0-9a-f]+\)\]\]`, `\[\]interface {}`, hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							ni := client.ExpectVariablesResponse(t)
							ref = checkVarRegex(t, ni, 0, `\[0\]`, `ni\[0\]`, `interface \{\}\(\[\]interface \{\}\) \[\*\(\*interface \{\}\)\(0x[0-9a-f]+\)\]`, "interface {}", hasChildren)
							if ref > 0 {
								client.VariablesRequest(ref)
								niI1 := client.ExpectVariablesResponse(t)
								ref = checkVarRegex(t, niI1, 0, "data", `ni\[0\]\.\(data\)`, `\[\]interface {} len: 1, cap: 1, \[\*\(\*interface {}\)\(0x[0-9a-f]+\)`, `\[\]interface {}`, hasChildren)
								if ref > 0 {
									// Auto-loading happens here
									client.VariablesRequest(ref)
									niI1Data := client.ExpectVariablesResponse(t)
									ref = checkVarExact(t, niI1Data, 0, "[0]", "ni[0].(data)[0]", "interface {}(int) 123", "interface {}", hasChildren)
									if ref > 0 {
										client.VariablesRequest(ref)
										niI1DataI2 := client.ExpectVariablesResponse(t)
										checkVarExact(t, niI1DataI2, 0, "data", "ni[0].(data)[0].(data)", "123", "int", noChildren)
									}
								}
							}
						}

						// Pointer values loaded even with LoadConfig.FollowPointers=false
						checkVarExact(t, locals, -1, "a7", "a7", "*main.FooBar {Baz: 5, Bur: \"strum\"}", "*main.FooBar", hasChildren)

						// Auto-loading works on results of evaluate expressions as well
						client.EvaluateRequest("a7", frame, "repl")
						checkEval(t, client.ExpectEvaluateResponse(t), "*main.FooBar {Baz: 5, Bur: \"strum\"}", hasChildren)

						client.EvaluateRequest("&a7", frame, "repl")
						pa7 := client.ExpectEvaluateResponse(t)
						ref = checkEvalRegex(t, pa7, `\*\(\*main\.FooBar\)\(0x[0-9a-f]+\)`, hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							a7 := client.ExpectVariablesResponse(t)
							checkVarExact(t, a7, 0, "a7", "(*(&a7))", "*main.FooBar {Baz: 5, Bur: \"strum\"}", "*main.FooBar", hasChildren)
						}
					}

					// Frame-independent loading expressions allow us to auto-load
					// variables in any frame, not just topmost.
					loadvars(1000 /*first topmost frame*/)
					// step into another function
					client.StepInRequest(1)
					client.ExpectStepInResponse(t)
					client.ExpectStoppedEvent(t)
					checkStop(t, client, 1, "main.barfoo", 24)
					loadvars(1001 /*second frame here is same as topmost above*/)
				},
				disconnect: true,
			}})
	})
}

// TestVariablesMetadata exposes test cases where variables contain metadata that
// can be accessed by requesting named variables.
func TestVariablesMetadata(t *testing.T) {
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
					checkStop(t, client, 1, "main.main", 368)

					client.VariablesRequest(1001) // Locals
					locals := client.ExpectVariablesResponse(t)

					checkNamedChildren := func(ref int, name, typeStr string, vals []string, evaluate bool) {
						// byteslice, request named variables
						client.NamedVariablesRequest(ref)
						named := client.ExpectVariablesResponse(t)
						checkChildren(t, named, name, 1)
						checkVarExact(t, named, 0, "string()", "", "\"tèst\"", "string", false)

						client.VariablesRequest(ref)
						all := client.ExpectVariablesResponse(t)
						checkChildren(t, all, name, len(vals)+1)
						checkVarExact(t, all, 0, "string()", "", "\"tèst\"", "string", false)
						for i, v := range vals {
							idx := fmt.Sprintf("[%d]", i)
							evalName := fmt.Sprintf("%s[%d]", name, i)
							if evaluate {
								evalName = fmt.Sprintf("(%s)[%d]", name, i)
							}
							checkVarExact(t, all, i+1, idx, evalName, v, typeStr, false)
						}
					}

					// byteslice
					ref := checkVarExactIndexed(t, locals, -1, "byteslice", "byteslice", "[]uint8 len: 5, cap: 5, [116,195,168,115,116]", "[]uint8", true, 5, 1)
					checkNamedChildren(ref, "byteslice", "uint8", []string{"116", "195", "168", "115", "116"}, false)

					client.EvaluateRequest("byteslice", 0, "")
					got := client.ExpectEvaluateResponse(t)
					ref = checkEvalIndexed(t, got, "[]uint8 len: 5, cap: 5, [116,195,168,115,116]", hasChildren, 5, 1)
					checkNamedChildren(ref, "byteslice", "uint8", []string{"116", "195", "168", "115", "116"}, true)

					// runeslice
					ref = checkVarExactIndexed(t, locals, -1, "runeslice", "runeslice", "[]int32 len: 4, cap: 4, [116,232,115,116]", "[]int32", true, 4, 1)
					checkNamedChildren(ref, "runeslice", "int32", []string{"116", "232", "115", "116"}, false)

					client.EvaluateRequest("runeslice", 0, "repl")
					got = client.ExpectEvaluateResponse(t)
					ref = checkEvalIndexed(t, got, "[]int32 len: 4, cap: 4, [116,232,115,116]", hasChildren, 4, 1)
					checkNamedChildren(ref, "runeslice", "int32", []string{"116", "232", "115", "116"}, true)

					// bytearray
					ref = checkVarExactIndexed(t, locals, -1, "bytearray", "bytearray", "[5]uint8 [116,195,168,115,116]", "[5]uint8", true, 5, 1)
					checkNamedChildren(ref, "bytearray", "uint8", []string{"116", "195", "168", "115", "116"}, false)

					client.EvaluateRequest("bytearray", 0, "hover")
					got = client.ExpectEvaluateResponse(t)
					ref = checkEvalIndexed(t, got, "[5]uint8 [116,195,168,115,116]", hasChildren, 5, 1)
					checkNamedChildren(ref, "bytearray", "uint8", []string{"116", "195", "168", "115", "116"}, true)

					// runearray
					ref = checkVarExactIndexed(t, locals, -1, "runearray", "runearray", "[4]int32 [116,232,115,116]", "[4]int32", true, 4, 1)
					checkNamedChildren(ref, "runearray", "int32", []string{"116", "232", "115", "116"}, false)

					client.EvaluateRequest("runearray", 0, "watch")
					got = client.ExpectEvaluateResponse(t)
					ref = checkEvalIndexed(t, got, "[4]int32 [116,232,115,116]", hasChildren, 4, 1)
					checkNamedChildren(ref, "runearray", "int32", []string{"116", "232", "115", "116"}, true)
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
					checkStackFrames(t, stack, "main.main", 36, 1000, 3, 3)

					client.ScopesRequest(1000)
					scopes := client.ExpectScopesResponse(t)
					checkScope(t, scopes, 0, "Arguments", 1000)
					checkScope(t, scopes, 1, "Locals", 1001)
					checkScope(t, scopes, 2, "Globals (package main)", 1002)

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
					checkStackFrames(t, stack, "", 13, 1000, 4, 4)

					client.ScopesRequest(1000)
					scopes = client.ExpectScopesResponse(t)
					checkScope(t, scopes, 0, "Arguments", 1000)
					checkScope(t, scopes, 1, "Locals", 1001)
					checkScope(t, scopes, 2, "Globals (package github.com/go-delve/delve/_fixtures/internal/dir0/pkg)", 1002)

					client.VariablesRequest(1002)
					globals := client.ExpectVariablesResponse(t)
					checkChildren(t, globals, "Globals", 1)
					ref := checkVarExact(t, globals, 0, "SomeVar", "github.com/go-delve/delve/_fixtures/internal/dir0/pkg.SomeVar", "github.com/go-delve/delve/_fixtures/internal/dir0/pkg.SomeType {X: 0}", "github.com/go-delve/delve/_fixtures/internal/dir0/pkg.SomeType", hasChildren)

					if ref > 0 {
						client.VariablesRequest(ref)
						somevar := client.ExpectVariablesResponse(t)
						checkChildren(t, somevar, "SomeVar", 1)
						// TODO(polina): unlike main.p, this prefix won't work
						checkVarExact(t, somevar, 0, "X", "github.com/go-delve/delve/_fixtures/internal/dir0/pkg.SomeVar.X", "0", "float64", noChildren)
					}
				},
				disconnect: false,
			}})
	})
}

// TestShadowedVariables executes to a breakpoint and checks the shadowed
// variable is named correctly.
func TestShadowedVariables(t *testing.T) {
	runTest(t, "testshadow", func(client *daptest.Client, fixture protest.Fixture) {
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
				// Stop at line 13
				execute: func() {
					client.StackTraceRequest(1, 0, 20)
					stack := client.ExpectStackTraceResponse(t)
					checkStackFrames(t, stack, "main.main", 13, 1000, 3, 3)

					client.ScopesRequest(1000)
					scopes := client.ExpectScopesResponse(t)
					checkScope(t, scopes, 0, "Arguments", 1000)
					checkScope(t, scopes, 1, "Locals", 1001)
					checkScope(t, scopes, 2, "Globals (package main)", 1002)

					client.VariablesRequest(1001)
					locals := client.ExpectVariablesResponse(t)

					checkVarExact(t, locals, 0, "(a)", "a", "0", "int", !hasChildren)
					checkVarExact(t, locals, 1, "a", "a", "1", "int", !hasChildren)

					// Check that the non-shadowed of "a" is returned from evaluate request.
					validateEvaluateName(t, client, locals, 1)
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
					checkStackFrames(t, stResp, "main.Increment", 8, 1000, 2 /*returned*/, 2 /*available*/)
				},
				disconnect: false,
			}})
	})
}

type Breakpoint struct {
	line      int
	path      string
	verified  bool
	msgPrefix string
}

func expectSetBreakpointsResponse(t *testing.T, client *daptest.Client, bps []Breakpoint) {
	t.Helper()
	checkSetBreakpointsResponse(t, client, bps, client.ExpectSetBreakpointsResponse(t))
}

func checkSetBreakpointsResponse(t *testing.T, client *daptest.Client, bps []Breakpoint, got *dap.SetBreakpointsResponse) {
	t.Helper()
	checkBreakpoints(t, client, bps, got.Body.Breakpoints)
}

func checkBreakpoints(t *testing.T, client *daptest.Client, bps []Breakpoint, breakpoints []dap.Breakpoint) {
	t.Helper()
	if len(breakpoints) != len(bps) {
		t.Errorf("got %#v,\nwant len(Breakpoints)=%d", breakpoints, len(bps))
		return
	}
	for i, bp := range breakpoints {
		if bps[i].line < 0 && !bps[i].verified {
			if bp.Verified != bps[i].verified || !stringContainsCaseInsensitive(bp.Message, bps[i].msgPrefix) {
				t.Errorf("got breakpoints[%d] = %#v, \nwant %#v", i, bp, bps[i])
			}
			continue
		}
		if bp.Line != bps[i].line || bp.Verified != bps[i].verified || bp.Source.Path != bps[i].path ||
			!strings.HasPrefix(bp.Message, bps[i].msgPrefix) {
			t.Errorf("got breakpoints[%d] = %#v, \nwant %#v", i, bp, bps[i])
		}
	}
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
					checkStop(t, client, 1, "main.main", 16)

					// Set two breakpoints at the next two lines in main
					client.SetBreakpointsRequest(fixture.Source, []int{17, 18})
					expectSetBreakpointsResponse(t, client, []Breakpoint{{17, fixture.Source, true, ""}, {18, fixture.Source, true, ""}})

					// Clear 17, reset 18
					client.SetBreakpointsRequest(fixture.Source, []int{18})
					expectSetBreakpointsResponse(t, client, []Breakpoint{{18, fixture.Source, true, ""}})

					// Skip 17, continue to 18
					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)
					client.ExpectStoppedEvent(t)
					checkStop(t, client, 1, "main.main", 18)

					// Set another breakpoint inside the loop in loop(), twice to trigger error
					client.SetBreakpointsRequest(fixture.Source, []int{8, 8})
					expectSetBreakpointsResponse(t, client, []Breakpoint{{8, fixture.Source, true, ""}, {-1, "", false, "breakpoint exists"}})

					// Continue into the loop
					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)
					client.ExpectStoppedEvent(t)
					checkStop(t, client, 1, "main.loop", 8)
					client.VariablesRequest(1001) // Locals
					locals := client.ExpectVariablesResponse(t)
					checkVarExact(t, locals, 0, "i", "i", "0", "int", noChildren) // i == 0

					// Edit the breakpoint to add a condition
					client.SetConditionalBreakpointsRequest(fixture.Source, []int{8}, map[int]string{8: "i == 3"})
					expectSetBreakpointsResponse(t, client, []Breakpoint{{8, fixture.Source, true, ""}})

					// Continue until condition is hit
					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)
					client.ExpectStoppedEvent(t)
					checkStop(t, client, 1, "main.loop", 8)
					client.VariablesRequest(1001) // Locals
					locals = client.ExpectVariablesResponse(t)
					checkVarExact(t, locals, 0, "i", "i", "3", "int", noChildren) // i == 3

					// Edit the breakpoint to remove a condition
					client.SetConditionalBreakpointsRequest(fixture.Source, []int{8}, map[int]string{8: ""})
					expectSetBreakpointsResponse(t, client, []Breakpoint{{8, fixture.Source, true, ""}})

					// Continue for one more loop iteration
					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)
					client.ExpectStoppedEvent(t)
					checkStop(t, client, 1, "main.loop", 8)
					client.VariablesRequest(1001) // Locals
					locals = client.ExpectVariablesResponse(t)
					checkVarExact(t, locals, 0, "i", "i", "4", "int", noChildren) // i == 4

					// Set at a line without a statement
					client.SetBreakpointsRequest(fixture.Source, []int{1000})
					expectSetBreakpointsResponse(t, client, []Breakpoint{{-1, "", false, "could not find statement"}}) // all cleared, none set
				},
				// The program has an infinite loop, so we must kill it by disconnecting.
				disconnect: true,
			}})
	})
}

func checkHitBreakpointIds(t *testing.T, se *dap.StoppedEvent, reason string, id int) {
	if se.Body.ThreadId != 1 || se.Body.Reason != reason || len(se.Body.HitBreakpointIds) != 1 || se.Body.HitBreakpointIds[0] != id {
		t.Errorf("got %#v, want Reason=%q, ThreadId=1, HitBreakpointIds=[]int{%d}", se, reason, id)
	}
}

// TestHitBreakpointIds executes to a breakpoint and tests that
// the breakpoint ids in the stopped event are correct.
func TestHitBreakpointIds(t *testing.T) {
	runTest(t, "locationsprog", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client, "launch",
			// Launch
			func() {
				client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
			},
			// Set breakpoints
			fixture.Source, []int{30}, // b main.main
			[]onBreakpoint{{
				execute: func() {
					checkStop(t, client, 1, "main.main", 30)

					// Set two source breakpoints and two function breakpoints.
					client.SetBreakpointsRequest(fixture.Source, []int{23, 33})
					sourceBps := client.ExpectSetBreakpointsResponse(t).Body.Breakpoints
					checkBreakpoints(t, client, []Breakpoint{{line: 23, path: fixture.Source, verified: true}, {line: 33, path: fixture.Source, verified: true}}, sourceBps)

					client.SetFunctionBreakpointsRequest([]dap.FunctionBreakpoint{
						{Name: "anotherFunction"},
						{Name: "anotherFunction:1"},
					})
					functionBps := client.ExpectSetFunctionBreakpointsResponse(t).Body.Breakpoints
					checkBreakpoints(t, client, []Breakpoint{{line: 26, path: fixture.Source, verified: true}, {line: 27, path: fixture.Source, verified: true}}, functionBps)

					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)
					se := client.ExpectStoppedEvent(t)
					checkHitBreakpointIds(t, se, "breakpoint", sourceBps[1].Id)
					checkStop(t, client, 1, "main.main", 33)

					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)
					se = client.ExpectStoppedEvent(t)
					checkHitBreakpointIds(t, se, "breakpoint", sourceBps[0].Id)
					checkStop(t, client, 1, "main.(*SomeType).SomeFunction", 23)

					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)
					se = client.ExpectStoppedEvent(t)
					checkHitBreakpointIds(t, se, "function breakpoint", functionBps[0].Id)
					checkStop(t, client, 1, "main.anotherFunction", 26)

					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)
					se = client.ExpectStoppedEvent(t)
					checkHitBreakpointIds(t, se, "function breakpoint", functionBps[1].Id)
					checkStop(t, client, 1, "main.anotherFunction", 27)
				},
				disconnect: true,
			}})
	})
}

func stringContainsCaseInsensitive(got, want string) bool {
	return strings.Contains(strings.ToLower(got), strings.ToLower(want))
}

// TestSetFunctionBreakpoints is inspired by service/test.TestClientServer_FindLocations.
func TestSetFunctionBreakpoints(t *testing.T) {
	runTest(t, "locationsprog", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client, "launch",
			// Launch
			func() {
				client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
			},
			// Set breakpoints
			fixture.Source, []int{30}, // b main.main
			[]onBreakpoint{{
				execute: func() {
					checkStop(t, client, 1, "main.main", 30)

					type Breakpoint struct {
						line       int
						sourceName string
						verified   bool
						errMsg     string
					}
					expectSetFunctionBreakpointsResponse := func(bps []Breakpoint) {
						t.Helper()
						got := client.ExpectSetFunctionBreakpointsResponse(t)
						if len(got.Body.Breakpoints) != len(bps) {
							t.Errorf("got %#v,\nwant len(Breakpoints)=%d", got, len(bps))
							return
						}
						for i, bp := range got.Body.Breakpoints {
							if bps[i].line < 0 && !bps[i].verified {
								if bp.Verified != bps[i].verified || !stringContainsCaseInsensitive(bp.Message, bps[i].errMsg) {
									t.Errorf("got breakpoints[%d] = %#v, \nwant %#v", i, bp, bps[i])
								}
								continue
							}
							// Some function breakpoints may be in packages that have been imported and we do not control, so
							// we do not always want to check breakpoint lines.
							if (bps[i].line >= 0 && bp.Line != bps[i].line) || bp.Verified != bps[i].verified || bp.Source.Name != bps[i].sourceName {
								t.Errorf("got breakpoints[%d] = %#v, \nwant %#v", i, bp, bps[i])
							}
						}
					}

					client.SetFunctionBreakpointsRequest([]dap.FunctionBreakpoint{
						{Name: "anotherFunction"},
					})
					expectSetFunctionBreakpointsResponse([]Breakpoint{{26, filepath.Base(fixture.Source), true, ""}})

					client.SetFunctionBreakpointsRequest([]dap.FunctionBreakpoint{
						{Name: "main.anotherFunction"},
					})
					expectSetFunctionBreakpointsResponse([]Breakpoint{{26, filepath.Base(fixture.Source), true, ""}})

					client.SetFunctionBreakpointsRequest([]dap.FunctionBreakpoint{
						{Name: "SomeType.String"},
					})
					expectSetFunctionBreakpointsResponse([]Breakpoint{{14, filepath.Base(fixture.Source), true, ""}})

					client.SetFunctionBreakpointsRequest([]dap.FunctionBreakpoint{
						{Name: "(*SomeType).String"},
					})
					expectSetFunctionBreakpointsResponse([]Breakpoint{{14, filepath.Base(fixture.Source), true, ""}})

					client.SetFunctionBreakpointsRequest([]dap.FunctionBreakpoint{
						{Name: "main.SomeType.String"},
					})
					expectSetFunctionBreakpointsResponse([]Breakpoint{{14, filepath.Base(fixture.Source), true, ""}})

					client.SetFunctionBreakpointsRequest([]dap.FunctionBreakpoint{
						{Name: "main.(*SomeType).String"},
					})
					expectSetFunctionBreakpointsResponse([]Breakpoint{{14, filepath.Base(fixture.Source), true, ""}})

					// Test line offsets
					client.SetFunctionBreakpointsRequest([]dap.FunctionBreakpoint{
						{Name: "main.anotherFunction:1"},
					})
					expectSetFunctionBreakpointsResponse([]Breakpoint{{27, filepath.Base(fixture.Source), true, ""}})

					// Test function names in imported package.
					// Issue #275
					client.SetFunctionBreakpointsRequest([]dap.FunctionBreakpoint{
						{Name: "io/ioutil.ReadFile"},
					})
					expectSetFunctionBreakpointsResponse([]Breakpoint{{-1, "ioutil.go", true, ""}})

					// Issue #296
					client.SetFunctionBreakpointsRequest([]dap.FunctionBreakpoint{
						{Name: "/io/ioutil.ReadFile"},
					})
					expectSetFunctionBreakpointsResponse([]Breakpoint{{-1, "ioutil.go", true, ""}})
					client.SetFunctionBreakpointsRequest([]dap.FunctionBreakpoint{
						{Name: "ioutil.ReadFile"},
					})
					expectSetFunctionBreakpointsResponse([]Breakpoint{{-1, "ioutil.go", true, ""}})

					// Function Breakpoint name also accepts breakpoints that are specified as file:line.
					// TODO(suzmue): We could return an error, but it probably is not necessary since breakpoints,
					// and function breakpoints come in with different requests.
					client.SetFunctionBreakpointsRequest([]dap.FunctionBreakpoint{
						{Name: fmt.Sprintf("%s:14", fixture.Source)},
					})
					expectSetFunctionBreakpointsResponse([]Breakpoint{{14, filepath.Base(fixture.Source), true, ""}})
					client.SetFunctionBreakpointsRequest([]dap.FunctionBreakpoint{
						{Name: fmt.Sprintf("%s:14", filepath.Base(fixture.Source))},
					})
					expectSetFunctionBreakpointsResponse([]Breakpoint{{14, filepath.Base(fixture.Source), true, ""}})

					// Expect error for ambiguous function name.
					client.SetFunctionBreakpointsRequest([]dap.FunctionBreakpoint{
						{Name: "String"},
					})
					expectSetFunctionBreakpointsResponse([]Breakpoint{{-1, "", false, "Location \"String\" ambiguous"}})

					client.SetFunctionBreakpointsRequest([]dap.FunctionBreakpoint{
						{Name: "main.String"},
					})
					expectSetFunctionBreakpointsResponse([]Breakpoint{{-1, "", false, "Location \"main.String\" ambiguous"}})

					// Expect error for function that does not exist.
					client.SetFunctionBreakpointsRequest([]dap.FunctionBreakpoint{
						{Name: "fakeFunction"},
					})
					expectSetFunctionBreakpointsResponse([]Breakpoint{{-1, "", false, "location \"fakeFunction\" not found"}})

					// Expect error for negative line number.
					client.SetFunctionBreakpointsRequest([]dap.FunctionBreakpoint{
						{Name: "main.anotherFunction:-1"},
					})
					expectSetFunctionBreakpointsResponse([]Breakpoint{{-1, "", false, "line offset negative or not a number"}})

					// Expect error when function name is regex.
					client.SetFunctionBreakpointsRequest([]dap.FunctionBreakpoint{
						{Name: `/^.*String.*$/`},
					})
					expectSetFunctionBreakpointsResponse([]Breakpoint{{-1, "", false, "breakpoint name \"/^.*String.*$/\" could not be parsed as a function"}})

					// Expect error when function name is an offset.
					client.SetFunctionBreakpointsRequest([]dap.FunctionBreakpoint{
						{Name: "+1"},
					})
					expectSetFunctionBreakpointsResponse([]Breakpoint{{-1, filepath.Base(fixture.Source), false, "breakpoint name \"+1\" could not be parsed as a function"}})

					// Expect error when function name is a line number.
					client.SetFunctionBreakpointsRequest([]dap.FunctionBreakpoint{
						{Name: "14"},
					})
					expectSetFunctionBreakpointsResponse([]Breakpoint{{-1, filepath.Base(fixture.Source), false, "breakpoint name \"14\" could not be parsed as a function"}})

					// Expect error when function name is an address.
					client.SetFunctionBreakpointsRequest([]dap.FunctionBreakpoint{
						{Name: "*b"},
					})
					expectSetFunctionBreakpointsResponse([]Breakpoint{{-1, filepath.Base(fixture.Source), false, "breakpoint name \"*b\" could not be parsed as a function"}})

					// Expect error when function name is a relative path.
					client.SetFunctionBreakpointsRequest([]dap.FunctionBreakpoint{
						{Name: fmt.Sprintf(".%s%s:14", string(filepath.Separator), filepath.Base(fixture.Source))},
					})
					// This relative path could also be caught by the parser, so we should not match the error message.
					expectSetFunctionBreakpointsResponse([]Breakpoint{{-1, filepath.Base(fixture.Source), false, ""}})
					client.SetFunctionBreakpointsRequest([]dap.FunctionBreakpoint{
						{Name: fmt.Sprintf("..%s%s:14", string(filepath.Separator), filepath.Base(fixture.Source))},
					})
					// This relative path could also be caught by the parser, so we should not match the error message.
					expectSetFunctionBreakpointsResponse([]Breakpoint{{-1, filepath.Base(fixture.Source), false, ""}})

					// Test multiple function breakpoints.
					client.SetFunctionBreakpointsRequest([]dap.FunctionBreakpoint{
						{Name: "SomeType.String"}, {Name: "anotherFunction"},
					})
					expectSetFunctionBreakpointsResponse([]Breakpoint{{14, filepath.Base(fixture.Source), true, ""}, {26, filepath.Base(fixture.Source), true, ""}})

					// Test multiple breakpoints to the same location.
					client.SetFunctionBreakpointsRequest([]dap.FunctionBreakpoint{
						{Name: "SomeType.String"}, {Name: "(*SomeType).String"},
					})
					expectSetFunctionBreakpointsResponse([]Breakpoint{{14, filepath.Base(fixture.Source), true, ""}, {-1, "", false, "breakpoint exists"}})

					// Set two breakpoints at SomeType.String and SomeType.SomeFunction.
					client.SetFunctionBreakpointsRequest([]dap.FunctionBreakpoint{
						{Name: "SomeType.String"}, {Name: "SomeType.SomeFunction"},
					})
					expectSetFunctionBreakpointsResponse([]Breakpoint{{14, filepath.Base(fixture.Source), true, ""}, {22, filepath.Base(fixture.Source), true, ""}})

					// Clear SomeType.String, reset SomeType.SomeFunction (SomeType.String is called before SomeType.SomeFunction).
					client.SetFunctionBreakpointsRequest([]dap.FunctionBreakpoint{
						{Name: "SomeType.SomeFunction"},
					})
					expectSetFunctionBreakpointsResponse([]Breakpoint{{22, filepath.Base(fixture.Source), true, ""}})

					// Expect the next breakpoint to be at SomeType.SomeFunction.
					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)

					if se := client.ExpectStoppedEvent(t); se.Body.Reason != "function breakpoint" || se.Body.ThreadId != 1 {
						t.Errorf("got %#v, want Reason=\"function breakpoint\", ThreadId=1", se)
					}
					checkStop(t, client, 1, "main.(*SomeType).SomeFunction", 22)

					// Set a breakpoint at the next line in the program.
					client.SetBreakpointsRequest(fixture.Source, []int{23})
					got := client.ExpectSetBreakpointsResponse(t)
					if len(got.Body.Breakpoints) != 1 {
						t.Errorf("got %#v,\nwant len(Breakpoints)=%d", got, 1)
						return
					}
					bp := got.Body.Breakpoints[0]
					if bp.Line != 23 || bp.Verified != true || bp.Source.Path != fixture.Source {
						t.Errorf("got breakpoints[0] = %#v, \nwant Line=23 Verified=true Source.Path=%q", bp, fixture.Source)
					}

					// Set a function breakpoint, this should not clear the breakpoint that was set in the previous setBreakpoints request.
					client.SetFunctionBreakpointsRequest([]dap.FunctionBreakpoint{
						{Name: "anotherFunction"},
					})
					expectSetFunctionBreakpointsResponse([]Breakpoint{{26, filepath.Base(fixture.Source), true, ""}})

					// Expect the next breakpoint to be at line 23.
					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)

					if se := client.ExpectStoppedEvent(t); se.Body.Reason != "breakpoint" || se.Body.ThreadId != 1 {
						t.Errorf("got %#v, want Reason=\"breakpoint\", ThreadId=1", se)
					}
					checkStop(t, client, 1, "main.(*SomeType).SomeFunction", 23)

					// Set a breakpoint, this should not clear the breakpoint that was set in the previous setFunctionBreakpoints request.
					client.SetBreakpointsRequest(fixture.Source, []int{37})
					got = client.ExpectSetBreakpointsResponse(t)
					if len(got.Body.Breakpoints) != 1 {
						t.Errorf("got %#v,\nwant len(Breakpoints)=%d", got, 1)
						return
					}
					bp = got.Body.Breakpoints[0]
					if bp.Line != 37 || bp.Verified != true || bp.Source.Path != fixture.Source {
						t.Errorf("got breakpoints[0] = %#v, \nwant Line=23 Verified=true Source.Path=%q", bp, fixture.Source)
					}

					// Expect the next breakpoint to be at line anotherFunction.
					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)

					if se := client.ExpectStoppedEvent(t); se.Body.Reason != "function breakpoint" || se.Body.ThreadId != 1 {
						t.Errorf("got %#v, want Reason=\"function breakpoint\", ThreadId=1", se)
					}
					checkStop(t, client, 1, "main.anotherFunction", 26)

				},
				disconnect: true,
			}})
	})
}

func expectSetBreakpointsResponseAndStoppedEvent(t *testing.T, client *daptest.Client) (se *dap.StoppedEvent, br *dap.SetBreakpointsResponse) {
	for i := 0; i < 2; i++ {
		switch m := client.ExpectMessage(t).(type) {
		case *dap.StoppedEvent:
			se = m
		case *dap.SetBreakpointsResponse:
			br = m
		default:
			t.Fatalf("Unexpected message type: expect StoppedEvent or SetBreakpointsResponse, got %#v", m)
		}
	}
	if se == nil || br == nil {
		t.Fatal("Expected StoppedEvent and SetBreakpointsResponse")
	}
	return se, br
}

func expectSetFunctionBreakpointsResponseAndStoppedEvent(t *testing.T, client *daptest.Client) (se *dap.StoppedEvent, br *dap.SetFunctionBreakpointsResponse) {
	for i := 0; i < 2; i++ {
		switch m := client.ExpectMessage(t).(type) {
		case *dap.StoppedEvent:
			se = m
		case *dap.SetFunctionBreakpointsResponse:
			br = m
		default:
			t.Fatalf("Unexpected message type: expect StoppedEvent or SetFunctionBreakpointsResponse, got %#v", m)
		}
	}
	if se == nil || br == nil {
		t.Fatal("Expected StoppedEvent and SetFunctionBreakpointsResponse")
	}
	return se, br
}

func TestSetBreakpointWhileRunning(t *testing.T) {
	runTest(t, "integrationprog", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client, "launch",
			// Launch
			func() {
				client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
			},
			// Set breakpoints
			fixture.Source, []int{16},
			[]onBreakpoint{{
				execute: func() {
					// The program loops 3 times over lines 14-15-8-9-10-16
					checkStop(t, client, 1, "main.main", 16) // Line that sleeps for 1 second

					// We can set breakpoints while nexting
					client.NextRequest(1)
					client.ExpectNextResponse(t)
					client.SetBreakpointsRequest(fixture.Source, []int{15}) // [16,] => [15,]
					se, br := expectSetBreakpointsResponseAndStoppedEvent(t, client)
					if se.Body.Reason != "pause" || !se.Body.AllThreadsStopped || se.Body.ThreadId != 0 && se.Body.ThreadId != 1 {
						t.Errorf("\ngot  %#v\nwant Reason='pause' AllThreadsStopped=true ThreadId=0/1", se)
					}
					checkSetBreakpointsResponse(t, client, []Breakpoint{{15, fixture.Source, true, ""}}, br)
					// Halt cancelled next, so if we continue we will not stop at line 14.
					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)
					se = client.ExpectStoppedEvent(t)
					if se.Body.Reason != "breakpoint" || !se.Body.AllThreadsStopped || se.Body.ThreadId != 1 {
						t.Errorf("\ngot  %#v\nwant Reason='breakpoint' AllThreadsStopped=true ThreadId=1", se)
					}
					checkStop(t, client, 1, "main.main", 15)

					// We can set breakpoints while continuing
					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)
					client.SetBreakpointsRequest(fixture.Source, []int{9}) // [15,] => [9,]
					se, br = expectSetBreakpointsResponseAndStoppedEvent(t, client)
					if se.Body.Reason != "pause" || !se.Body.AllThreadsStopped || se.Body.ThreadId != 0 && se.Body.ThreadId != 1 {
						t.Errorf("\ngot  %#v\nwant Reason='pause' AllThreadsStopped=true ThreadId=0/1", se)
					}
					checkSetBreakpointsResponse(t, client, []Breakpoint{{9, fixture.Source, true, ""}}, br)
					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)
					se = client.ExpectStoppedEvent(t)
					if se.Body.Reason != "breakpoint" || !se.Body.AllThreadsStopped || se.Body.ThreadId != 1 {
						t.Errorf("\ngot  %#v\nwant Reason='breakpoint' AllThreadsStopped=true ThreadId=1", se)
					}
					checkStop(t, client, 1, "main.sayhi", 9)

				},
				disconnect: true,
			}})
	})
}

func TestSetFunctionBreakpointWhileRunning(t *testing.T) {
	runTest(t, "integrationprog", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client, "launch",
			// Launch
			func() {
				client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
			},
			// Set breakpoints
			fixture.Source, []int{16},
			[]onBreakpoint{{
				execute: func() {
					// The program loops 3 times over lines 14-15-8-9-10-16
					checkStop(t, client, 1, "main.main", 16) // Line that sleeps for 1 second

					// We can set breakpoints while nexting
					client.NextRequest(1)
					client.ExpectNextResponse(t)
					client.SetFunctionBreakpointsRequest([]dap.FunctionBreakpoint{{Name: "main.sayhi"}}) // [16,] => [16, 8]
					se, br := expectSetFunctionBreakpointsResponseAndStoppedEvent(t, client)
					if se.Body.Reason != "pause" || !se.Body.AllThreadsStopped || se.Body.ThreadId != 0 && se.Body.ThreadId != 1 {
						t.Errorf("\ngot  %#v\nwant Reason='pause' AllThreadsStopped=true ThreadId=0/1", se)
					}
					checkBreakpoints(t, client, []Breakpoint{{8, fixture.Source, true, ""}}, br.Body.Breakpoints)

					client.SetBreakpointsRequest(fixture.Source, []int{}) // [16,8] => [8]
					expectSetBreakpointsResponse(t, client, []Breakpoint{})

					// Halt cancelled next, so if we continue we will not stop at line 14.
					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)
					se = client.ExpectStoppedEvent(t)
					if se.Body.Reason != "function breakpoint" || !se.Body.AllThreadsStopped || se.Body.ThreadId != 1 {
						t.Errorf("\ngot  %#v\nwant Reason='breakpoint' AllThreadsStopped=true ThreadId=1", se)
					}
					checkStop(t, client, 1, "main.sayhi", 8)

					// We can set breakpoints while continuing
					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)
					client.SetFunctionBreakpointsRequest([]dap.FunctionBreakpoint{}) // [8,] => []
					se, br = expectSetFunctionBreakpointsResponseAndStoppedEvent(t, client)
					if se.Body.Reason != "pause" || !se.Body.AllThreadsStopped || se.Body.ThreadId != 0 && se.Body.ThreadId != 1 {
						t.Errorf("\ngot  %#v\nwant Reason='pause' AllThreadsStopped=true ThreadId=0/1", se)
					}
					checkBreakpoints(t, client, []Breakpoint{}, br.Body.Breakpoints)
					if se.Body.Reason != "pause" || !se.Body.AllThreadsStopped || se.Body.ThreadId != 0 && se.Body.ThreadId != 1 {
						t.Errorf("\ngot  %#v\nwant Reason='pause' AllThreadsStopped=true ThreadId=0/1", se)
					}
					checkBreakpoints(t, client, []Breakpoint{}, br.Body.Breakpoints)

					client.SetBreakpointsRequest(fixture.Source, []int{16}) // [] => [16]
					expectSetBreakpointsResponse(t, client, []Breakpoint{{16, fixture.Source, true, ""}})

					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)
					se = client.ExpectStoppedEvent(t)
					if se.Body.Reason != "breakpoint" || !se.Body.AllThreadsStopped || se.Body.ThreadId != 1 {
						t.Errorf("\ngot  %#v\nwant Reason='breakpoint' AllThreadsStopped=true ThreadId=1", se)
					}
					checkStop(t, client, 1, "main.main", 16)

				},
				disconnect: true,
			}})
	})
}

func TestHitConditionBreakpoints(t *testing.T) {
	runTest(t, "break", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client, "launch",
			// Launch
			func() {
				client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
			},
			// Set breakpoints
			fixture.Source, []int{4},
			[]onBreakpoint{{
				execute: func() {
					client.SetHitConditionalBreakpointsRequest(fixture.Source, []int{7}, map[int]string{7: "4"})
					expectSetBreakpointsResponse(t, client, []Breakpoint{{7, fixture.Source, true, ""}})

					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)
					client.ExpectStoppedEvent(t)
					checkStop(t, client, 1, "main.main", 7)

					// Check that we are stopped at the correct value of i.
					client.VariablesRequest(1001)
					locals := client.ExpectVariablesResponse(t)
					checkVarExact(t, locals, 0, "i", "i", "4", "int", noChildren)

					// Change the hit condition.
					client.SetHitConditionalBreakpointsRequest(fixture.Source, []int{7}, map[int]string{7: "% 2"})
					expectSetBreakpointsResponse(t, client, []Breakpoint{{7, fixture.Source, true, ""}})

					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)
					client.ExpectStoppedEvent(t)
					checkStop(t, client, 1, "main.main", 7)

					// Check that we are stopped at the correct value of i.
					client.VariablesRequest(1001)
					locals = client.ExpectVariablesResponse(t)
					checkVarExact(t, locals, 0, "i", "i", "6", "int", noChildren)

					// Expect an error if an assignment is passed.
					client.SetHitConditionalBreakpointsRequest(fixture.Source, []int{7}, map[int]string{7: "= 2"})
					expectSetBreakpointsResponse(t, client, []Breakpoint{{-1, "", false, ""}})

					// Change the hit condition.
					client.SetHitConditionalBreakpointsRequest(fixture.Source, []int{7}, map[int]string{7: "< 8"})
					expectSetBreakpointsResponse(t, client, []Breakpoint{{7, fixture.Source, true, ""}})
					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)
					client.ExpectStoppedEvent(t)
					checkStop(t, client, 1, "main.main", 7)

					// Check that we are stopped at the correct value of i.
					client.VariablesRequest(1001)
					locals = client.ExpectVariablesResponse(t)
					checkVarExact(t, locals, 0, "i", "i", "7", "int", noChildren)

					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)

					client.ExpectTerminatedEvent(t)
				},
				disconnect: false,
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
	// The rules in 'substitutePath' will be applied as follows:
	// - mapping paths from client to server:
	//		The first rule["from"] to match a prefix of 'path' will be applied:
	//			strings.Replace(path, rule["from"], rule["to"], 1)
	// - mapping paths from server to client:
	//		The first rule["to"] to match a prefix of 'path' will be applied:
	//			strings.Replace(path, rule["to"], rule["from"], 1)
	launchAttachConfig["substitutePath"] = []map[string]string{
		{"from": nonexistentDir, "to": filepath.Dir(fixture.Source)},
		// Since the path mappings are ordered, when converting from client path to
		// server path, this mapping will not apply, because nonexistentDir appears in
		// an earlier rule.
		{"from": nonexistentDir, "to": "this_is_a_bad_path"},
		// Since the path mappings are ordered, when converting from server path to
		// client path, this mapping will not apply, because filepath.Dir(fixture.Source)
		// appears in an earlier rule.
		{"from": "this_is_a_bad_path", "to": filepath.Dir(fixture.Source)},
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
				checkStop(t, client, 1, "main.loop", 8)
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
					"cwd":         wd,
				})
			},
			// Set breakpoints
			fixture.Source, []int{10}, // b main.main
			[]onBreakpoint{{
				execute: func() {
					checkStop(t, client, 1, "main.main", 10)
					client.VariablesRequest(1001) // Locals
					locals := client.ExpectVariablesResponse(t)
					checkChildren(t, locals, "Locals", 2)
					for i := range locals.Body.Variables {
						switch locals.Body.Variables[i].Name {
						case "pwd":
							checkVarExact(t, locals, i, "pwd", "pwd", fmt.Sprintf("%q", wd), "string", noChildren)
						case "err":
							checkVarExact(t, locals, i, "err", "err", "error nil", "error", noChildren)
						}
					}
				},
				disconnect: false,
			}})
	})
}

// checkEval is a helper for verifying the values within an EvaluateResponse.
//     value - the value of the evaluated expression
//     hasRef - true if the evaluated expression should have children and therefore a non-0 variable reference
//     ref - reference to retrieve children of this evaluated expression (0 if none)
func checkEval(t *testing.T, got *dap.EvaluateResponse, value string, hasRef bool) (ref int) {
	t.Helper()
	if got.Body.Result != value || (got.Body.VariablesReference > 0) != hasRef {
		t.Errorf("\ngot  %#v\nwant Result=%q hasRef=%t", got, value, hasRef)
	}
	return got.Body.VariablesReference
}

// checkEvalIndexed is a helper for verifying the values within an EvaluateResponse.
//     value - the value of the evaluated expression
//     hasRef - true if the evaluated expression should have children and therefore a non-0 variable reference
//     ref - reference to retrieve children of this evaluated expression (0 if none)
func checkEvalIndexed(t *testing.T, got *dap.EvaluateResponse, value string, hasRef bool, indexed, named int) (ref int) {
	t.Helper()
	if got.Body.Result != value || (got.Body.VariablesReference > 0) != hasRef || got.Body.IndexedVariables != indexed || got.Body.NamedVariables != named {
		t.Errorf("\ngot  %#v\nwant Result=%q hasRef=%t IndexedVariables=%d NamedVariables=%d", got, value, hasRef, indexed, named)
	}
	return got.Body.VariablesReference
}

func checkEvalRegex(t *testing.T, got *dap.EvaluateResponse, valueRegex string, hasRef bool) (ref int) {
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
					checkStop(t, client, 1, "main.foobar", 66)

					// Variable lookup
					client.EvaluateRequest("a2", 1000, "this context will be ignored")
					got := client.ExpectEvaluateResponse(t)
					checkEval(t, got, "6", noChildren)

					client.EvaluateRequest("a5", 1000, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					ref := checkEval(t, got, "[]int len: 5, cap: 5, [1,2,3,4,5]", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						a5 := client.ExpectVariablesResponse(t)
						checkChildren(t, a5, "a5", 5)
						checkVarExact(t, a5, 0, "[0]", "(a5)[0]", "1", "int", noChildren)
						checkVarExact(t, a5, 4, "[4]", "(a5)[4]", "5", "int", noChildren)
						validateEvaluateName(t, client, a5, 0)
						validateEvaluateName(t, client, a5, 4)
					}

					// Variable lookup that's not fully loaded
					client.EvaluateRequest("ba", 1000, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					checkEvalIndexed(t, got, "[]int len: 200, cap: 200, [0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,...+136 more]", hasChildren, 200, 0)

					// All (binary and unary) on basic types except <-, ++ and --
					client.EvaluateRequest("1+1", 1000, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					checkEval(t, got, "2", noChildren)

					// Comparison operators on any type
					client.EvaluateRequest("1<2", 1000, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					checkEval(t, got, "true", noChildren)

					// Type casts between numeric types
					client.EvaluateRequest("int(2.3)", 1000, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					checkEval(t, got, "2", noChildren)

					// Type casts of integer constants into any pointer type and vice versa
					client.EvaluateRequest("(*int)(2)", 1000, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					ref = checkEvalRegex(t, got, `\*\(unreadable .+\)`, hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						val := client.ExpectVariablesResponse(t)
						checkChildren(t, val, "*(*int)(2)", 1)
						checkVarRegex(t, val, 0, "^$", `\(\*\(\(\*int\)\(2\)\)\)`, `\(unreadable .+\)`, "int", noChildren)
						validateEvaluateName(t, client, val, 0)
					}

					// Type casts between string, []byte and []rune
					client.EvaluateRequest("[]byte(\"ABC€\")", 1000, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					checkEvalIndexed(t, got, "[]uint8 len: 6, cap: 6, [65,66,67,226,130,172]", noChildren, 6, 0)

					// Struct member access (i.e. somevar.memberfield)
					client.EvaluateRequest("ms.Nest.Level", 1000, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					checkEval(t, got, "1", noChildren)

					// Slicing and indexing operators on arrays, slices and strings
					client.EvaluateRequest("a5[4]", 1000, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					checkEval(t, got, "5", noChildren)

					// Map access
					client.EvaluateRequest("mp[1]", 1000, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					ref = checkEval(t, got, "interface {}(int) 42", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						expr := client.ExpectVariablesResponse(t)
						checkChildren(t, expr, "mp[1]", 1)
						checkVarExact(t, expr, 0, "data", "(mp[1]).(data)", "42", "int", noChildren)
						validateEvaluateName(t, client, expr, 0)
					}

					// Pointer dereference
					client.EvaluateRequest("*ms.Nest", 1000, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					ref = checkEvalRegex(t, got, `main\.Nest {Level: 1, Nest: \*main.Nest {Level: 2, Nest: \*\(\*main\.Nest\)\(0x[0-9a-f]+\)}}`, hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						expr := client.ExpectVariablesResponse(t)
						checkChildren(t, expr, "*ms.Nest", 2)
						checkVarExact(t, expr, 0, "Level", "(*ms.Nest).Level", "1", "int", noChildren)
						validateEvaluateName(t, client, expr, 0)
					}

					// Calls to builtin functions: cap, len, complex, imag and real
					client.EvaluateRequest("len(a5)", 1000, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					checkEval(t, got, "5", noChildren)

					// Type assertion on interface variables (i.e. somevar.(concretetype))
					client.EvaluateRequest("mp[1].(int)", 1000, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					checkEval(t, got, "42", noChildren)
				},
				disconnect: false,
			}, { // Stop at second breakpoint
				execute: func() {
					checkStop(t, client, 1, "main.barfoo", 27)

					// Top-most frame
					client.EvaluateRequest("a1", 1000, "this context will be ignored")
					got := client.ExpectEvaluateResponse(t)
					checkEval(t, got, "\"bur\"", noChildren)
					// No frame defaults to top-most frame
					client.EvaluateRequest("a1", 0, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					checkEval(t, got, "\"bur\"", noChildren)
					// Next frame
					client.EvaluateRequest("a1", 1001, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					checkEval(t, got, "\"foofoofoofoofoofoo\"", noChildren)
					// Next frame
					client.EvaluateRequest("a1", 1002, "any context but watch")
					erres := client.ExpectVisibleErrorResponse(t)
					if erres.Body.Error.Format != "Unable to evaluate expression: could not find symbol value for a1" {
						t.Errorf("\ngot %#v\nwant Format=\"Unable to evaluate expression: could not find symbol value for a1\"", erres)
					}
					client.EvaluateRequest("a1", 1002, "watch")
					erres = client.ExpectInvisibleErrorResponse(t)
					if erres.Body.Error.Format != "Unable to evaluate expression: could not find symbol value for a1" {
						t.Errorf("\ngot %#v\nwant Format=\"Unable to evaluate expression: could not find symbol value for a1\"", erres)
					}
					client.EvaluateRequest("a1", 1002, "repl")
					erres = client.ExpectInvisibleErrorResponse(t)
					if erres.Body.Error.Format != "Unable to evaluate expression: could not find symbol value for a1" {
						t.Errorf("\ngot %#v\nwant Format=\"Unable to evaluate expression: could not find symbol value for a1\"", erres)
					}
					client.EvaluateRequest("a1", 1002, "hover")
					erres = client.ExpectInvisibleErrorResponse(t)
					if erres.Body.Error.Format != "Unable to evaluate expression: could not find symbol value for a1" {
						t.Errorf("\ngot %#v\nwant Format=\"Unable to evaluate expression: could not find symbol value for a1\"", erres)
					}
					client.EvaluateRequest("a1", 1002, "clipboard")
					erres = client.ExpectVisibleErrorResponse(t)
					if erres.Body.Error.Format != "Unable to evaluate expression: could not find symbol value for a1" {
						t.Errorf("\ngot %#v\nwant Format=\"Unable to evaluate expression: could not find symbol value for a1\"", erres)
					}
				},
				disconnect: false,
			}})
	})
}

// From testvariables2 fixture
const (
	// As defined in the code
	longstr = `"very long string 0123456789a0123456789b0123456789c0123456789d0123456789e0123456789f0123456789g012345678h90123456789i0123456789j0123456789"`
	// Loaded with MaxStringLen=64
	longstrLoaded64   = `"very long string 0123456789a0123456789b0123456789c0123456789d012...+73 more"`
	longstrLoaded64re = `\"very long string 0123456789a0123456789b0123456789c0123456789d012\.\.\.\+73 more\"`
)

// TestVariableValueTruncation tests that in certain cases
// we truncate the loaded variable values to make display more user-friendly.
func TestVariableValueTruncation(t *testing.T) {
	runTest(t, "testvariables2", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client, "launch",
			// Launch
			func() {
				client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
			},
			// Breakpoint set within the program
			fixture.Source, []int{},
			[]onBreakpoint{{
				execute: func() {
					checkStop(t, client, 1, "main.main", -1)

					client.VariablesRequest(1001) // Locals
					locals := client.ExpectVariablesResponse(t)

					// Compound variable values may be truncated
					m1Full := `map\[string\]main\.astruct \[(\"[A-Za-z]+\": {A: [0-9]+, B: [0-9]+}, )+,\.\.\.\+2 more\]`
					m1Part := `map\[string\]main\.astruct \[(\"[A-Za-z]+\": {A: [0-9]+, B: [0-9]+}, )+.+\.\.\.`

					// In variable responses
					checkVarRegex(t, locals, -1, "m1", "m1", m1Part, `map\[string\]main\.astruct`, hasChildren)

					// In evaluate responses (select contexts only)
					tests := []struct {
						context string
						want    string
					}{
						{"", m1Part},
						{"watch", m1Part},
						{"repl", m1Part},
						{"hover", m1Part},
						{"variables", m1Full}, // used for copy
						{"clipboard", m1Full}, // used for copy
						{"somethingelse", m1Part},
					}
					for _, tc := range tests {
						t.Run(tc.context, func(t *testing.T) {
							client.EvaluateRequest("m1", 0, tc.context)
							checkEvalRegex(t, client.ExpectEvaluateResponse(t), tc.want, hasChildren)
						})
					}

					// Compound map keys may be truncated even further
					// As the keys are always inside of a map container,
					// this applies to variables requests only, not evalute requests.

					// key - compound, value - scalar (inlined key:value display) => truncate key if too long
					ref := checkVarExact(t, locals, -1, "m5", "m5", "map[main.C]int [{s: "+longstr+"}: 1, ]", "map[main.C]int", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						// Key format: <truncated>... @<address>
						checkVarRegex(t, client.ExpectVariablesResponse(t), 1, `main\.C {s: "very long string 0123456789.+\.\.\. @ 0x[0-9a-f]+`, `m5\[\(\*\(\*"main\.C"\)\(0x[0-9a-f]+\)\)\]`, "1", `int`, hasChildren)
					}
					// key - scalar, value - scalar (inlined key:value display) => key not truncated
					ref = checkVarExact(t, locals, -1, "m6", "m6", "map[string]int ["+longstr+": 123, ]", "map[string]int", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						checkVarExact(t, client.ExpectVariablesResponse(t), 1, longstr, `m6[`+longstr+`]`, "123", "string: int", noChildren)
					}
					// key - compound, value - compound (array-like display) => key not truncated
					ref = checkVarExact(t, locals, -1, "m7", "m7", "map[main.C]main.C [{s: "+longstr+"}: {s: \"hello\"}, ]", "map[main.C]main.C", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						m7 := client.ExpectVariablesResponse(t)
						checkVarRegex(t, m7, 1, "[key 0]", `\(\*\(\*\"main\.C\"\)\(0x[0-9a-f]+\)\)`, `main\.C {s: `+longstr+`}`, `main\.C`, hasChildren)
					}
				},
				disconnect: true,
			}})
	})
}

// TestVariableLoadingOfLongStrings tests that different string loading limits
// apply that depending on the context.
func TestVariableLoadingOfLongStrings(t *testing.T) {
	protest.MustSupportFunctionCalls(t, testBackend)
	runTest(t, "longstrings", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client, "launch",
			// Launch
			func() {
				client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
			},
			// Breakpoint set within the program
			fixture.Source, []int{},
			[]onBreakpoint{{
				execute: func() {
					checkStop(t, client, 1, "main.main", -1)

					client.VariablesRequest(1001) // Locals
					locals := client.ExpectVariablesResponse(t)

					// Limits vary for evaluate requests with different contexts
					tests := []struct {
						context string
						limit   int
					}{
						{"", DefaultLoadConfig.MaxStringLen},
						{"watch", DefaultLoadConfig.MaxStringLen},
						{"repl", maxSingleStringLen},
						{"hover", maxSingleStringLen},
						{"variables", maxSingleStringLen},
						{"clipboard", maxSingleStringLen},
						{"somethingelse", DefaultLoadConfig.MaxStringLen},
					}
					for _, tc := range tests {
						t.Run(tc.context, func(t *testing.T) {
							// Long string by itself (limits vary)
							client.EvaluateRequest("s4097", 0, tc.context)
							want := fmt.Sprintf(`"x+\.\.\.\+%d more"`, 4097-tc.limit)
							checkEvalRegex(t, client.ExpectEvaluateResponse(t), want, noChildren)

							// Evaluated container variables return values with minimally loaded
							// strings, which are further truncated for displaying, so we
							// can't test for loading limit except in contexts where an untruncated
							// value is returned.
							client.EvaluateRequest("&s4097", 0, tc.context)
							switch tc.context {
							case "variables", "clipboard":
								want = fmt.Sprintf(`\*"x+\.\.\.\+%d more`, 4097-DefaultLoadConfig.MaxStringLen)
							default:
								want = fmt.Sprintf(`\*"x{%d}\.\.\.`, maxVarValueLen-2)
							}
							checkEvalRegex(t, client.ExpectEvaluateResponse(t), want, hasChildren)
						})
					}

					// Long strings returned from calls are subject to a different limit,
					// same limit regardless of context
					for _, context := range []string{"", "watch", "repl", "variables", "hover", "clipboard", "somethingelse"} {
						t.Run(context, func(t *testing.T) {
							client.EvaluateRequest(`call buildString(4097)`, 1000, context)
							want := fmt.Sprintf(`"x+\.\.\.\+%d more"`, 4097-maxStringLenInCallRetVars)
							got := client.ExpectEvaluateResponse(t)
							checkEvalRegex(t, got, want, hasChildren)
						})
					}

					// Variables requests use the most conservative loading limit
					checkVarRegex(t, locals, -1, "s513", "s513", `"x{512}\.\.\.\+1 more"`, "string", noChildren)
					// Container variables are subject to additional stricter value truncation that drops +more part
					checkVarRegex(t, locals, -1, "nested", "nested", `map\[int\]string \[513: \"x+\.\.\.`, "string", hasChildren)
				},
				disconnect: true,
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
					checkStop(t, client, 1, "main.makeclos", 88)

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
					checkStop(t, client, 1, "main.makeclos", 88)

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
					checkStop(t, client, stopped.Body.ThreadId, "main.makeclos", 88)

					// Complete the call and get back to original breakpoint in makeclos()
					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)
					client.ExpectStoppedEvent(t)
					checkStop(t, client, 1, "main.makeclos", 88)
				},
				disconnect: false,
			}, { // Stop at runtime breakpoint
				execute: func() {
					checkStop(t, client, 1, "main.main", -1)

					// No return values
					client.EvaluateRequest("call call0(1, 2)", 1000, "this context will be ignored")
					got := client.ExpectEvaluateResponse(t)
					checkEval(t, got, "", noChildren)
					// One unnamed return value
					client.EvaluateRequest("call call1(one, two)", 1000, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					ref := checkEval(t, got, "3", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						rv := client.ExpectVariablesResponse(t)
						checkChildren(t, rv, "rv", 1)
						checkVarExact(t, rv, 0, "~r2", "", "3", "int", noChildren)
					}
					// One named return value
					// Panic doesn't panic, but instead returns the error as a named return variable
					client.EvaluateRequest("call callpanic()", 1000, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					ref = checkEval(t, got, `interface {}(string) "callpanic panicked"`, hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						rv := client.ExpectVariablesResponse(t)
						checkChildren(t, rv, "rv", 1)
						ref = checkVarExact(t, rv, 0, "~panic", "", `interface {}(string) "callpanic panicked"`, "interface {}", hasChildren)
						if ref > 0 {
							client.VariablesRequest(ref)
							p := client.ExpectVariablesResponse(t)
							checkChildren(t, p, "~panic", 1)
							checkVarExact(t, p, 0, "data", "", "\"callpanic panicked\"", "string", noChildren)
						}
					}
					// Multiple return values
					client.EvaluateRequest("call call2(one, two)", 1000, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					ref = checkEval(t, got, "1, 2", hasChildren)
					if ref > 0 {
						client.VariablesRequest(ref)
						rvs := client.ExpectVariablesResponse(t)
						checkChildren(t, rvs, "rvs", 2)
						checkVarExact(t, rvs, 0, "~r2", "", "1", "int", noChildren)
						checkVarExact(t, rvs, 1, "~r3", "", "2", "int", noChildren)
					}
					// No frame defaults to top-most frame
					client.EvaluateRequest("call call1(one, two)", 0, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					checkEval(t, got, "3", hasChildren)
					// Extra spaces don't matter
					client.EvaluateRequest(" call  call1(one, one) ", 0, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					checkEval(t, got, "2", hasChildren)
					// Just 'call', even with extra space, is treated as {expression}
					client.EvaluateRequest("call ", 1000, "watch")
					got = client.ExpectEvaluateResponse(t)
					checkEval(t, got, "\"this is a variable named `call`\"", noChildren)

					// Call error
					client.EvaluateRequest("call call1(one)", 1000, "watch")
					erres := client.ExpectInvisibleErrorResponse(t)
					if erres.Body.Error.Format != "Unable to evaluate expression: not enough arguments" {
						t.Errorf("\ngot %#v\nwant Format=\"Unable to evaluate expression: not enough arguments\"", erres)
					}

					// Assignment - expect no error, but no return value.
					client.EvaluateRequest("call one = two", 1000, "this context will be ignored")
					got = client.ExpectEvaluateResponse(t)
					checkEval(t, got, "", noChildren)
					// Check one=two was applied.
					client.EvaluateRequest("one", 1000, "repl")
					got = client.ExpectEvaluateResponse(t)
					checkEval(t, got, "2", noChildren)

					// Call can exit.
					client.EvaluateRequest("call callexit()", 1000, "this context will be ignored")
					client.ExpectTerminatedEvent(t)
					if res := client.ExpectVisibleErrorResponse(t); !strings.Contains(res.Body.Error.Format, "terminated") {
						t.Errorf("\ngot %#v\nwant Format=.*terminated.*", res)
					}
				},
				terminated: true,
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
					checkStop(t, client, 1, "main.initialize", 11)

					expectStop := func(fun string, line int) {
						t.Helper()
						se := client.ExpectStoppedEvent(t)
						if se.Body.Reason != "step" || se.Body.ThreadId != 1 || !se.Body.AllThreadsStopped {
							t.Errorf("got %#v, want Reason=\"step\", ThreadId=1, AllThreadsStopped=true", se)
						}
						checkStop(t, client, 1, fun, line)
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

					client.NextRequest(-1000)
					client.ExpectNextResponse(t)
					if se := client.ExpectStoppedEvent(t); se.Body.Reason != "error" || se.Body.Text != "unknown goroutine -1000" {
						t.Errorf("got %#v, want Reason=\"error\", Text=\"unknown goroutine -1000\"", se)
					}
					checkStop(t, client, 1, "main.inlineThis", 5)
				},
				disconnect: false,
			}})
	})
}

func TestNextParked(t *testing.T) {
	if runtime.GOOS == "freebsd" {
		t.SkipNow()
	}
	runTest(t, "parallel_next", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client, "launch",
			// Launch
			func() {
				client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
			},
			// Set breakpoints
			fixture.Source, []int{15},
			[]onBreakpoint{{ // Stop at line 15
				execute: func() {
					goroutineId := testStepParkedHelper(t, client, fixture)

					client.NextRequest(goroutineId)
					client.ExpectNextResponse(t)

					se := client.ExpectStoppedEvent(t)
					if se.Body.ThreadId != goroutineId {
						t.Fatalf("Next did not continue on the selected goroutine, expected %d got %d", goroutineId, se.Body.ThreadId)
					}
				},
				disconnect: false,
			}})
	})
}

func TestStepInParked(t *testing.T) {
	if runtime.GOOS == "freebsd" {
		t.SkipNow()
	}
	runTest(t, "parallel_next", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client, "launch",
			// Launch
			func() {
				client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
			},
			// Set breakpoints
			fixture.Source, []int{15},
			[]onBreakpoint{{ // Stop at line 15
				execute: func() {
					goroutineId := testStepParkedHelper(t, client, fixture)

					client.StepInRequest(goroutineId)
					client.ExpectStepInResponse(t)

					se := client.ExpectStoppedEvent(t)
					if se.Body.ThreadId != goroutineId {
						t.Fatalf("StepIn did not continue on the selected goroutine, expected %d got %d", goroutineId, se.Body.ThreadId)
					}
				},
				disconnect: false,
			}})
	})
}

func testStepParkedHelper(t *testing.T, client *daptest.Client, fixture protest.Fixture) int {
	t.Helper()
	// Set a breakpoint at main.sayHi
	client.SetBreakpointsRequest(fixture.Source, []int{8})
	client.ExpectSetBreakpointsResponse(t)

	var goroutineId = -1
	for goroutineId < 0 {
		client.ContinueRequest(1)
		client.ExpectContinueResponse(t)

		se := client.ExpectStoppedEvent(t)

		client.ThreadsRequest()
		threads := client.ExpectThreadsResponse(t)

		// Search for a parked goroutine that we know for sure will have to be
		// resumed before the program can exit. This is a parked goroutine that:
		// 1. is executing main.sayhi
		// 2. hasn't called wg.Done yet
		// 3. is not the currently selected goroutine
		for _, g := range threads.Body.Threads {
			// We do not need to check the thread that the program
			// is currently stopped on.
			if g.Id == se.Body.ThreadId {
				continue
			}
			client.StackTraceRequest(g.Id, 0, 5)
			frames := client.ExpectStackTraceResponse(t)
			for _, frame := range frames.Body.StackFrames {
				// line 11 is the line where wg.Done is called
				if frame.Name == "main.sayhi" && frame.Line < 11 {
					goroutineId = g.Id
					break
				}
			}
			if goroutineId >= 0 {
				break
			}
		}
	}

	// Clear all breakpoints.
	client.SetBreakpointsRequest(fixture.Source, []int{})
	client.ExpectSetBreakpointsResponse(t)

	return goroutineId
}

// TestStepOutPreservesGoroutine is inspired by proc_test.TestStepOutPreservesGoroutine
// and checks that StepOut preserves the currently selected goroutine.
func TestStepOutPreservesGoroutine(t *testing.T) {
	// Checks that StepOut preserves the currently selected goroutine.
	if runtime.GOOS == "freebsd" {
		t.SkipNow()
	}
	rand.Seed(time.Now().Unix())
	runTest(t, "issue2113", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client, "launch",
			// Launch
			func() {
				client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
			},
			// Set breakpoints
			fixture.Source, []int{25},
			[]onBreakpoint{{ // Stop at line 25
				execute: func() {
					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)

					// The program contains runtime.Breakpoint()
					se := client.ExpectStoppedEvent(t)

					client.ThreadsRequest()
					gs := client.ExpectThreadsResponse(t)

					candg := []int{}
					bestg := []int{}
					for _, g := range gs.Body.Threads {
						// We do not need to check the thread that the program
						// is currently stopped on.
						if g.Id == se.Body.ThreadId {
							continue
						}

						client.StackTraceRequest(g.Id, 0, 20)
						frames := client.ExpectStackTraceResponse(t)
						for _, frame := range frames.Body.StackFrames {
							if frame.Name == "main.coroutine" {
								candg = append(candg, g.Id)
								if strings.HasPrefix(frames.Body.StackFrames[0].Name, "runtime.") {
									bestg = append(bestg, g.Id)
								}
								break
							}
						}
					}
					var goroutineId int
					if len(bestg) > 0 {
						goroutineId = bestg[rand.Intn(len(bestg))]
						t.Logf("selected goroutine %d (best)\n", goroutineId)
					} else if len(candg) > 0 {
						goroutineId = candg[rand.Intn(len(candg))]
						t.Logf("selected goroutine %d\n", goroutineId)

					}

					if goroutineId != 0 {
						client.StepOutRequest(goroutineId)
						client.ExpectStepOutResponse(t)
					} else {
						client.ContinueRequest(-1)
						client.ExpectContinueResponse(t)
					}

					switch e := client.ExpectMessage(t).(type) {
					case *dap.StoppedEvent:
						if e.Body.ThreadId != goroutineId {
							t.Fatalf("StepOut did not continue on the selected goroutine, expected %d got %d", goroutineId, e.Body.ThreadId)
						}
					case *dap.TerminatedEvent:
						t.Logf("program terminated")
					default:
						t.Fatalf("Unexpected event type: expect stopped or terminated event, got %#v", e)
					}
				},
				disconnect: false,
			}})
	})
}
func checkStopOnNextWhileNextingError(t *testing.T, client *daptest.Client, threadID int) {
	t.Helper()
	se := client.ExpectStoppedEvent(t)
	if se.Body.ThreadId != threadID || se.Body.Reason != "exception" || se.Body.Description != "invalid command" || se.Body.Text != BetterNextWhileNextingError {
		t.Errorf("\ngot  %#v\nwant ThreadId=%d Reason=\"exception\" Description=\"invalid command\" Text=\"%s\"", se, threadID, BetterNextWhileNextingError)
	}
	client.ExceptionInfoRequest(1)
	eInfo := client.ExpectExceptionInfoResponse(t)
	if eInfo.Body.ExceptionId != "invalid command" || eInfo.Body.Description != BetterNextWhileNextingError {
		t.Errorf("\ngot  %#v\nwant ExceptionId=\"invalid command\" Text=\"%s\"", eInfo, BetterNextWhileNextingError)
	}
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
					checkStop(t, client, 1, "main.main", 4)

					expectStoppedOnError := func(errorPrefix string) {
						t.Helper()
						se := client.ExpectStoppedEvent(t)
						if se.Body.ThreadId != 1 || se.Body.Reason != "exception" || se.Body.Description != "runtime error" || !strings.HasPrefix(se.Body.Text, errorPrefix) {
							t.Errorf("\ngot  %#v\nwant ThreadId=1 Reason=\"exception\" Description=\"runtime error\" Text=\"%s\"", se, errorPrefix)
						}
						client.ExceptionInfoRequest(1)
						eInfo := client.ExpectExceptionInfoResponse(t)
						if eInfo.Body.ExceptionId != "runtime error" || !strings.HasPrefix(eInfo.Body.Description, errorPrefix) {
							t.Errorf("\ngot  %#v\nwant ExceptionId=\"runtime error\" Text=\"%s\"", eInfo, errorPrefix)
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
					checkStopOnNextWhileNextingError(t, client, 1)

					client.StepInRequest(1)
					client.ExpectStepInResponse(t)
					checkStopOnNextWhileNextingError(t, client, 1)

					client.StepOutRequest(1)
					client.ExpectStepOutResponse(t)
					checkStopOnNextWhileNextingError(t, client, 1)
				},
				disconnect: true,
			}})
	})
}

// TestNextWhileNexting is inspired by command_test.TestIssue387 and tests
// that when 'next' is interrupted by a 'breakpoint', calling 'next'
// again will produce an error with a helpful message, and 'continue'
// will resume the program.
func TestNextWhileNexting(t *testing.T) {
	if runtime.GOOS == "freebsd" {
		t.Skip("test is not valid on FreeBSD")
	}
	// a breakpoint triggering during a 'next' operation will interrupt 'next''
	// Unlike the test for the terminal package, we cannot be certain
	// of the number of breakpoints we expect to hit, since multiple
	// breakpoints being hit at the same time is not supported in dap stopped
	// events.
	runTest(t, "issue387", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client, "launch",
			// Launch
			func() {
				client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
			},
			// Set breakpoints
			fixture.Source, []int{15},
			[]onBreakpoint{{ // Stop at line 15
				execute: func() {
					checkStop(t, client, 1, "main.main", 15)

					client.SetBreakpointsRequest(fixture.Source, []int{8})
					client.ExpectSetBreakpointsResponse(t)

					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)

					bpSe := client.ExpectStoppedEvent(t)
					threadID := bpSe.Body.ThreadId
					checkStop(t, client, threadID, "main.dostuff", 8)

					for pos := 9; pos < 11; pos++ {
						client.NextRequest(threadID)
						client.ExpectNextResponse(t)

						stepInProgress := true
						for stepInProgress {
							m := client.ExpectStoppedEvent(t)
							switch m.Body.Reason {
							case "step":
								if !m.Body.AllThreadsStopped {
									t.Errorf("got %#v, want Reason=\"step\", AllThreadsStopped=true", m)
								}
								checkStop(t, client, m.Body.ThreadId, "main.dostuff", pos)
								stepInProgress = false
							case "breakpoint":
								if !m.Body.AllThreadsStopped {
									t.Errorf("got %#v, want Reason=\"breakpoint\", AllThreadsStopped=true", m)
								}
								checkStop(t, client, m.Body.ThreadId, "main.dostuff", 8)

								if stepInProgress {
									// We encountered a breakpoint on a different thread. We should have to resume execution
									// using continue.
									client.NextRequest(m.Body.ThreadId)
									client.ExpectNextResponse(t)
									checkStopOnNextWhileNextingError(t, client, m.Body.ThreadId)
									// Continue since we have not finished the step request.
									client.ContinueRequest(threadID)
									client.ExpectContinueResponse(t)
								} else {
									// Switch to stepping on this thread instead.
									pos = 8
									threadID = m.Body.ThreadId
								}
							default:
								t.Fatalf("got %#v, want StoppedEvent on step or breakpoint", m)
							}
						}
					}
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
					checkStop(t, client, 1, "main.main", 5)

					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)

					text := "\"BOOM!\""
					se := client.ExpectStoppedEvent(t)
					if se.Body.ThreadId != 1 || se.Body.Reason != "exception" || se.Body.Description != "panic" || se.Body.Text != text {
						t.Errorf("\ngot  %#v\nwant ThreadId=1 Reason=\"exception\" Description=\"panic\" Text=%q", se, text)
					}

					client.ExceptionInfoRequest(1)
					eInfo := client.ExpectExceptionInfoResponse(t)
					if eInfo.Body.ExceptionId != "panic" || eInfo.Body.Description != text {
						t.Errorf("\ngot  %#v\nwant ExceptionId=\"panic\" Description=%q", eInfo, text)
					}

					client.StackTraceRequest(se.Body.ThreadId, 0, 20)
					st := client.ExpectStackTraceResponse(t)
					for i, frame := range st.Body.StackFrames {
						if strings.HasPrefix(frame.Name, "runtime.") {
							if frame.Source.PresentationHint != "deemphasize" {
								t.Errorf("\ngot Body.StackFrames[%d]=%#v\nwant Source.PresentationHint=\"deemphasize\"", i, frame)
							}
						} else if frame.Source.PresentationHint != "" {
							t.Errorf("\ngot Body.StackFrames[%d]=%#v\nwant Source.PresentationHint=\"\"", i, frame)
						}

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
					checkStop(t, client, 1, "main.main", 5)

					client.NextRequest(1)
					client.ExpectNextResponse(t)

					text := "\"BOOM!\""
					se := client.ExpectStoppedEvent(t)
					if se.Body.ThreadId != 1 || se.Body.Reason != "exception" || se.Body.Description != "panic" || se.Body.Text != text {
						t.Errorf("\ngot  %#v\nwant ThreadId=1 Reason=\"exception\" Description=\"panic\" Text=%q", se, text)
					}

					client.ExceptionInfoRequest(1)
					eInfo := client.ExpectExceptionInfoResponse(t)
					if eInfo.Body.ExceptionId != "panic" || eInfo.Body.Description != text {
						t.Errorf("\ngot  %#v\nwant ExceptionId=\"panic\" Description=%q", eInfo, text)
					}
				},
				disconnect: true,
			}})
	})
}

func TestFatalThrowBreakpoint(t *testing.T) {
	// This is not currently flaky for Go 1.17 see https://github.com/golang/go/issues/46425.
	if goversion.VersionAfterOrEqual(runtime.Version(), 1, 17) {
		t.Skip()
	}
	runTest(t, "fatalerror", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client, "launch",
			// Launch
			func() {
				client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
			},
			// Set breakpoints
			fixture.Source, []int{3},
			[]onBreakpoint{{
				execute: func() {
					checkStop(t, client, 1, "main.main", 3)

					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)

					var text string
					// This does not work for Go 1.16.
					ver, _ := goversion.Parse(runtime.Version())
					if ver.Major != 1 || ver.Minor != 16 {
						text = "\"go of nil func value\""
					}

					se := client.ExpectStoppedEvent(t)
					if se.Body.ThreadId != 1 || se.Body.Reason != "exception" || se.Body.Description != "fatal error" || se.Body.Text != text {
						t.Errorf("\ngot  %#v\nwant ThreadId=1 Reason=\"exception\" Description=\"fatal error\" Text=%q", se, text)
					}

					// This does not work for Go 1.16.
					errorPrefix := text
					if errorPrefix == "" {
						errorPrefix = "Throw reason unavailable, see https://github.com/golang/go/issues/46425"
					}
					client.ExceptionInfoRequest(1)
					eInfo := client.ExpectExceptionInfoResponse(t)
					if eInfo.Body.ExceptionId != "fatal error" || !strings.HasPrefix(eInfo.Body.Description, errorPrefix) {
						t.Errorf("\ngot  %#v\nwant ExceptionId=\"runtime error\" Text=%s", eInfo, errorPrefix)
					}

				},
				disconnect: true,
			}})
	})
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
					checkStop(t, client, 1, "main.main", 3)

					client.ContinueRequest(1)
					client.ExpectContinueResponse(t)

					// TODO(suzmue): Enable this test for 1.17 when https://github.com/golang/go/issues/46425 is fixed.
					var text string
					if !goversion.VersionAfterOrEqual(runtime.Version(), 1, 16) {
						text = "\"all goroutines are asleep - deadlock!\""
					}
					se := client.ExpectStoppedEvent(t)
					if se.Body.Reason != "exception" || se.Body.Description != "fatal error" || se.Body.Text != text {
						t.Errorf("\ngot  %#v\nwant Reason=\"exception\" Description=\"fatal error\" Text=%q", se, text)
					}

					// TODO(suzmue): Get the exception info for the thread and check the description
					// includes "all goroutines are asleep - deadlock!".
					// Stopped events with no selected goroutines need to be supported to test deadlock.
				},
				disconnect: true,
			}})
	})
}

func verifyStopLocation(t *testing.T, client *daptest.Client, thread int, name string, line int) {
	t.Helper()

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
}

// checkStop covers the standard sequence of requests issued by
// a client at a breakpoint or another non-terminal stop event.
// The details have been tested by other tests,
// so this is just a sanity check.
// Skips line check if line is -1.
func checkStop(t *testing.T, client *daptest.Client, thread int, name string, line int) {
	t.Helper()
	client.ThreadsRequest()
	client.ExpectThreadsResponse(t)

	verifyStopLocation(t, client, thread, name, line)

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
// If terminated is true, we expect requests at this breakpoint
// to result in termination.
type onBreakpoint struct {
	execute    func()
	disconnect bool
	terminated bool
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
	client.ExpectInitializeResponseAndCapabilities(t)

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
			if onBP.terminated {
				client.ExpectOutputEventProcessExited(t, 0)
				client.ExpectOutputEventDetaching(t)
			} else {
				client.ExpectOutputEventDetachingKill(t)
			}
			client.ExpectDisconnectResponse(t)
			client.ExpectTerminatedEvent(t)
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
	if cmd == "launch" {
		client.ExpectOutputEventProcessExited(t, 0)
		client.ExpectOutputEventDetaching(t)
	} else if cmd == "attach" {
		client.ExpectOutputEventDetachingKill(t)
	}
	client.ExpectDisconnectResponse(t)
	client.ExpectTerminatedEvent(t)
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

	tmpBin := "__tmpBin"
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		// We reuse the harness that builds, but ignore the built binary,
		// only relying on the source to be built in response to LaunchRequest.
		runDebugSession(t, client, "launch", func() {
			wd, _ := os.Getwd()
			client.LaunchRequestWithArgs(map[string]interface{}{
				"mode": "debug", "program": fixture.Source, "output": filepath.Join(wd, tmpBin)})
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
		if runtime.GOOS != "windows" || !stringContainsCaseInsensitive(rmErr, "Access is denied") {
			t.Fatalf("Binary removal failure:\n%s\n", rmErr)
		}
	} else {
		// We did not get a removal error, but did we even try to remove before exiting?
		// Confirm that the binary did get removed.
		if _, err := os.Stat(tmpBin); err == nil || os.IsExist(err) {
			t.Fatal("Failed to remove temp binary", tmpBin)
		}
	}
}

// TestLaunchRequestDefaults tests defaults for launch attribute that are explicit in other tests.
func TestLaunchRequestDefaults(t *testing.T) {
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSession(t, client, "launch", func() {
			client.LaunchRequestWithArgs(map[string]interface{}{
				"mode": "" /*"debug" by default*/, "program": fixture.Source, "output": "__mybin"})
		}, fixture.Source)
	})
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSession(t, client, "launch", func() {
			// Use the default output directory.
			client.LaunchRequestWithArgs(map[string]interface{}{
				/*"mode":"debug" by default*/ "program": fixture.Source, "output": "__mybin"})
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

	// if noDebug is not a bool, behave as if it is the default value (false).
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSession(t, client, "launch", func() {
			client.LaunchRequestWithArgs(map[string]interface{}{
				"mode": "debug", "program": fixture.Source, "noDebug": "true"})
		}, fixture.Source)
	})
}

func TestLaunchRequestNoDebug_GoodStatus(t *testing.T) {
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		runNoDebugDebugSession(t, client, func() {
			client.LaunchRequestWithArgs(map[string]interface{}{
				"noDebug": true,
				"mode":    "debug",
				"program": fixture.Source,
				"output":  "__mybin"})
		}, fixture.Source, []int{8}, 0)
	})
}

func TestLaunchRequestNoDebug_BadStatus(t *testing.T) {
	runTest(t, "issue1101", func(client *daptest.Client, fixture protest.Fixture) {
		runNoDebugDebugSession(t, client, func() {
			client.LaunchRequestWithArgs(map[string]interface{}{
				"noDebug": true,
				"mode":    "debug",
				"program": fixture.Source,
				"output":  "__mybin"})
		}, fixture.Source, []int{8}, 2)
	})
}

// runNoDebugDebugSession tests the session started with noDebug=true runs uninterrupted
// even when breakpoint is set.
func runNoDebugDebugSession(t *testing.T, client *daptest.Client, cmdRequest func(), source string, breakpoints []int, status int) {
	client.InitializeRequest()
	client.ExpectInitializeResponseAndCapabilities(t)

	cmdRequest()
	// no initialized event.
	// noDebug mode applies only to "launch" requests.
	client.ExpectLaunchResponse(t)

	client.ExpectOutputEventProcessExited(t, status)
	client.ExpectTerminatedEvent(t)
	client.DisconnectRequestWithKillOption(true)
	client.ExpectDisconnectResponse(t)
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
				"mode": "debug", "program": fixture.Source, "output": "__mybin",
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
					checkStop(t, client, 1, "main.loop", 8)
					client.VariablesRequest(1001) // Locals
					locals := client.ExpectVariablesResponse(t)
					checkChildren(t, locals, "Locals", 1)
					checkVarRegex(t, locals, 0, "i", "i", "[0-9]+", "int", noChildren)
				},
				disconnect: true,
			}})
	})
}

func TestPauseAndContinue(t *testing.T) {
	runTest(t, "loopprog", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client, "launch",
			// Launch
			func() {
				client.LaunchRequest("exec", fixture.Path, !stopOnEntry)
			},
			// Set breakpoints
			fixture.Source, []int{6},
			[]onBreakpoint{{
				execute: func() {
					verifyStopLocation(t, client, 1, "main.loop", 6)

					// Continue resumes all goroutines, so thread id is ignored
					client.ContinueRequest(12345)
					client.ExpectContinueResponse(t)

					time.Sleep(time.Second)

					// Halt pauses all goroutines, so thread id is ignored
					client.PauseRequest(56789)
					// Since we are in async mode while running, we might receive next two messages in either order.
					for i := 0; i < 2; i++ {
						msg := client.ExpectMessage(t)
						switch m := msg.(type) {
						case *dap.StoppedEvent:
							if m.Body.Reason != "pause" || m.Body.ThreadId != 0 && m.Body.ThreadId != 1 {
								t.Errorf("\ngot %#v\nwant ThreadId=0/1 Reason='pause'", m)
							}
						case *dap.PauseResponse:
						default:
							t.Fatalf("got %#v, want StoppedEvent or PauseResponse", m)
						}
					}

					// Pause will be a no-op at a pause: there will be no additional stopped events
					client.PauseRequest(1)
					client.ExpectPauseResponse(t)
				},
				// The program has an infinite loop, so we must kill it by disconnecting.
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

type helperForSetVariable struct {
	t *testing.T
	c *daptest.Client
}

func (h *helperForSetVariable) expectSetVariableAndStop(ref int, name, value string) {
	h.t.Helper()
	h.expectSetVariable0(ref, name, value, true)
}
func (h *helperForSetVariable) expectSetVariable(ref int, name, value string) {
	h.t.Helper()
	h.expectSetVariable0(ref, name, value, false)
}

func (h *helperForSetVariable) failSetVariable(ref int, name, value, wantErrInfo string) {
	h.t.Helper()
	h.failSetVariable0(ref, name, value, wantErrInfo, false)
}

func (h *helperForSetVariable) failSetVariableAndStop(ref int, name, value, wantErrInfo string) {
	h.t.Helper()
	h.failSetVariable0(ref, name, value, wantErrInfo, true)
}

func (h *helperForSetVariable) evaluate(expr, want string, hasRef bool) {
	h.t.Helper()
	h.c.EvaluateRequest(expr, 1000, "whatever")
	got := h.c.ExpectEvaluateResponse(h.t)
	checkEval(h.t, got, want, hasRef)
}

func (h *helperForSetVariable) evaluateRegex(expr, want string, hasRef bool) {
	h.t.Helper()
	h.c.EvaluateRequest(expr, 1000, "whatever")
	got := h.c.ExpectEvaluateResponse(h.t)
	checkEvalRegex(h.t, got, want, hasRef)
}

func (h *helperForSetVariable) expectSetVariable0(ref int, name, value string, wantStop bool) {
	h.t.Helper()

	h.c.SetVariableRequest(ref, name, value)
	if wantStop {
		h.c.ExpectStoppedEvent(h.t)
	}
	if got, want := h.c.ExpectSetVariableResponse(h.t), value; got.Success != true || got.Body.Value != want {
		h.t.Errorf("SetVariableRequest(%v, %v)=%#v, want {Success=true, Body.Value=%q", name, value, got, want)
	}
}

func (h *helperForSetVariable) failSetVariable0(ref int, name, value, wantErrInfo string, wantStop bool) {
	h.t.Helper()

	h.c.SetVariableRequest(ref, name, value)
	if wantStop {
		h.c.ExpectStoppedEvent(h.t)
	}
	resp := h.c.ExpectErrorResponse(h.t)
	if got := resp.Body.Error.Format; !stringContainsCaseInsensitive(got, wantErrInfo) {
		h.t.Errorf("got %#v, want error string containing %v", got, wantErrInfo)
	}
}

func (h *helperForSetVariable) variables(ref int) *dap.VariablesResponse {
	h.t.Helper()
	h.c.VariablesRequest(ref)
	return h.c.ExpectVariablesResponse(h.t)
}

// TestSetVariable tests SetVariable features that do not need function call support.
func TestSetVariable(t *testing.T) {
	runTest(t, "testvariables", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client, "launch",
			func() {
				client.LaunchRequestWithArgs(map[string]interface{}{
					"mode": "exec", "program": fixture.Path, "showGlobalVariables": true,
				})
			},
			fixture.Source, []int{}, // breakpoints are set within the program.
			[]onBreakpoint{{
				execute: func() {
					tester := &helperForSetVariable{t, client}

					startLineno := 66 // after runtime.Breakpoint
					if runtime.GOOS == "windows" && goversion.VersionAfterOrEqual(runtime.Version(), 1, 15) {
						// Go1.15 on windows inserts a NOP after the call to
						// runtime.Breakpoint and marks it same line as the
						// runtime.Breakpoint call, making this flaky, so skip the line check.
						startLineno = -1
					}

					checkStop(t, client, 1, "main.foobar", startLineno)

					// Args of foobar(baz string, bar FooBar)
					args := tester.variables(1000)

					checkVarExact(t, args, 1, "bar", "bar", `main.FooBar {Baz: 10, Bur: "lorem"}`, "main.FooBar", hasChildren)
					tester.failSetVariable(1000, "bar", `main.FooBar {Baz: 42, Bur: "ipsum"}`, "*ast.CompositeLit not implemented")

					// Nested field.
					barRef := checkVarExact(t, args, 1, "bar", "bar", `main.FooBar {Baz: 10, Bur: "lorem"}`, "main.FooBar", hasChildren)
					tester.expectSetVariable(barRef, "Baz", "42")
					tester.evaluate("bar", `main.FooBar {Baz: 42, Bur: "lorem"}`, hasChildren)

					tester.failSetVariable(barRef, "Baz", `"string"`, "can not convert")

					// Local variables
					locals := tester.variables(1001)

					// int
					checkVarExact(t, locals, -1, "a2", "a2", "6", "int", noChildren)
					tester.expectSetVariable(1001, "a2", "42")
					tester.evaluate("a2", "42", noChildren)

					tester.failSetVariable(1001, "a2", "false", "can not convert")

					// float
					checkVarExact(t, locals, -1, "a3", "a3", "7.23", "float64", noChildren)
					tester.expectSetVariable(1001, "a3", "-0.1")
					tester.evaluate("a3", "-0.1", noChildren)

					// array of int
					a4Ref := checkVarExact(t, locals, -1, "a4", "a4", "[2]int [1,2]", "[2]int", hasChildren)
					tester.expectSetVariable(a4Ref, "[1]", "-7")
					tester.evaluate("a4", "[2]int [1,-7]", hasChildren)

					tester.failSetVariable(1001, "a4", "[2]int{3, 4}", "not implemented")

					// slice of int
					a5Ref := checkVarExact(t, locals, -1, "a5", "a5", "[]int len: 5, cap: 5, [1,2,3,4,5]", "[]int", hasChildren)
					tester.expectSetVariable(a5Ref, "[3]", "100")
					tester.evaluate("a5", "[]int len: 5, cap: 5, [1,2,3,100,5]", hasChildren)

					// composite literal and its nested fields.
					a7Ref := checkVarExact(t, locals, -1, "a7", "a7", `*main.FooBar {Baz: 5, Bur: "strum"}`, "*main.FooBar", hasChildren)
					a7Val := tester.variables(a7Ref)
					a7ValRef := checkVarExact(t, a7Val, -1, "", "(*a7)", `main.FooBar {Baz: 5, Bur: "strum"}`, "main.FooBar", hasChildren)
					tester.expectSetVariable(a7ValRef, "Baz", "7")
					tester.evaluate("(*a7)", `main.FooBar {Baz: 7, Bur: "strum"}`, hasChildren)

					// pointer
					checkVarExact(t, locals, -1, "a9", "a9", `*main.FooBar nil`, "*main.FooBar", noChildren)
					tester.expectSetVariable(1001, "a9", "&a6")
					tester.evaluate("a9", `*main.FooBar {Baz: 8, Bur: "word"}`, hasChildren)

					// slice of pointers
					a13Ref := checkVarExact(t, locals, -1, "a13", "a13", `[]*main.FooBar len: 3, cap: 3, [*{Baz: 6, Bur: "f"},*{Baz: 7, Bur: "g"},*{Baz: 8, Bur: "h"}]`, "[]*main.FooBar", hasChildren)
					a13 := tester.variables(a13Ref)
					a13c0Ref := checkVarExact(t, a13, -1, "[0]", "a13[0]", `*main.FooBar {Baz: 6, Bur: "f"}`, "*main.FooBar", hasChildren)
					a13c0 := tester.variables(a13c0Ref)
					a13c0valRef := checkVarExact(t, a13c0, -1, "", "(*a13[0])", `main.FooBar {Baz: 6, Bur: "f"}`, "main.FooBar", hasChildren)
					tester.expectSetVariable(a13c0valRef, "Baz", "777")
					tester.evaluate("a13[0]", `*main.FooBar {Baz: 777, Bur: "f"}`, hasChildren)

					// complex
					tester.evaluate("c64", `(1 + 2i)`, hasChildren)
					tester.expectSetVariable(1001, "c64", "(2 + 3i)")
					tester.evaluate("c64", `(2 + 3i)`, hasChildren)
					// note: complex's real, imaginary part can't be directly mutable.

					//
					// Global variables
					//    p1 = 10
					client.VariablesRequest(1002)
					globals := client.ExpectVariablesResponse(t)

					checkVarExact(t, globals, -1, "p1", "main.p1", "10", "int", noChildren)
					tester.expectSetVariable(1002, "p1", "-10")
					tester.evaluate("p1", "-10", noChildren)
					tester.failSetVariable(1002, "p1", "0.1", "can not convert")
				},
				disconnect: true,
			}})
	})

	runTest(t, "testvariables2", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client, "launch",
			func() {
				client.LaunchRequestWithArgs(map[string]interface{}{
					"mode": "exec", "program": fixture.Path, "showGlobalVariables": true,
				})
			},
			fixture.Source, []int{}, // breakpoints are set within the program.
			[]onBreakpoint{{
				execute: func() {
					tester := &helperForSetVariable{t, client}

					startLineno := 360 // after runtime.Breakpoint
					if runtime.GOOS == "windows" && goversion.VersionAfterOrEqual(runtime.Version(), 1, 15) {
						startLineno = -1
					}

					checkStop(t, client, 1, "main.main", startLineno)
					locals := tester.variables(1001)

					// channel
					tester.evaluate("chnil", "chan int nil", noChildren)
					tester.expectSetVariable(1001, "chnil", "ch1")
					tester.evaluate("chnil", "chan int 4/11", hasChildren)

					// func
					tester.evaluate("fn2", "nil", noChildren)
					tester.expectSetVariable(1001, "fn2", "fn1")
					tester.evaluate("fn2", "main.afunc", noChildren)

					// interface
					tester.evaluate("ifacenil", "interface {} nil", noChildren)
					tester.expectSetVariable(1001, "ifacenil", "iface1")
					tester.evaluate("ifacenil", "interface {}(*main.astruct) *{A: 1, B: 2}", hasChildren)

					// interface.(data)
					iface1Ref := checkVarExact(t, locals, -1, "iface1", "iface1", "interface {}(*main.astruct) *{A: 1, B: 2}", "interface {}", hasChildren)
					iface1 := tester.variables(iface1Ref)
					iface1DataRef := checkVarExact(t, iface1, -1, "data", "iface1.(data)", "*main.astruct {A: 1, B: 2}", "*main.astruct", hasChildren)
					iface1Data := tester.variables(iface1DataRef)
					iface1DataValueRef := checkVarExact(t, iface1Data, -1, "", "(*iface1.(data))", "main.astruct {A: 1, B: 2}", "main.astruct", hasChildren)
					tester.expectSetVariable(iface1DataValueRef, "A", "2021")
					tester.evaluate("iface1", "interface {}(*main.astruct) *{A: 2021, B: 2}", hasChildren)

					// map: string -> struct
					tester.evaluate(`m1["Malone"]`, "main.astruct {A: 2, B: 3}", hasChildren)
					m1Ref := checkVarRegex(t, locals, -1, "m1", "m1", `.*map\[string\]main\.astruct.*`, `map\[string\]main\.astruct`, hasChildren)
					m1 := tester.variables(m1Ref)
					elem1 := m1.Body.Variables[1]
					tester.expectSetVariable(elem1.VariablesReference, "A", "-9999")
					tester.expectSetVariable(elem1.VariablesReference, "B", "10000")
					tester.evaluate(elem1.EvaluateName, "main.astruct {A: -9999, B: 10000}", hasChildren)

					// map: struct -> int
					m3Ref := checkVarExact(t, locals, -1, "m3", "m3", "map[main.astruct]int [{A: 1, B: 1}: 42, {A: 2, B: 2}: 43, ]", "map[main.astruct]int", hasChildren)
					tester.expectSetVariable(m3Ref, "main.astruct {A: 1, B: 1}", "8888")
					// note: updating keys is possible, but let's not promise anything.
					tester.evaluateRegex("m3", `.*\[\{A: 1, B: 1\}: 8888,.*`, hasChildren)

					// map: struct -> struct
					m4Ref := checkVarRegex(t, locals, -1, "m4", "m4", `map\[main\.astruct]main\.astruct.*\[\{A: 1, B: 1\}: \{A: 11, B: 11\}.*`, `map\[main\.astruct\]main\.astruct`, hasChildren)
					m4 := tester.variables(m4Ref)
					m4Val1Ref := checkVarRegex(t, m4, -1, "[val 0]", `.*0x[0-9a-f]+.*`, `main.astruct.*`, `main\.astruct`, hasChildren)
					tester.expectSetVariable(m4Val1Ref, "A", "-9999")
					tester.evaluateRegex("m4", `.*A: -9999,.*`, hasChildren)

					// unsigned pointer
					checkVarRegex(t, locals, -1, "up1", "up1", `unsafe\.Pointer\(0x[0-9a-f]+\)`, "unsafe.Pointer", noChildren)
					tester.expectSetVariable(1001, "up1", "unsafe.Pointer(0x0)")
					tester.evaluate("up1", "unsafe.Pointer(0x0)", noChildren)

					// val := A{val: 1}
					valRef := checkVarExact(t, locals, -1, "val", "val", `main.A {val: 1}`, "main.A", hasChildren)
					tester.expectSetVariable(valRef, "val", "3")
					tester.evaluate("val", `main.A {val: 3}`, hasChildren)
				},
				disconnect: true,
			}})
	})
}

// TestSetVariableWithCall tests SetVariable features that do not depend on function calls support.
func TestSetVariableWithCall(t *testing.T) {
	protest.MustSupportFunctionCalls(t, testBackend)

	runTest(t, "testvariables", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client, "launch",
			func() {
				client.LaunchRequestWithArgs(map[string]interface{}{
					"mode": "exec", "program": fixture.Path, "showGlobalVariables": true,
				})
			},
			fixture.Source, []int{66, 67},
			[]onBreakpoint{{
				execute: func() {
					tester := &helperForSetVariable{t, client}

					startLineno := 66
					if runtime.GOOS == "windows" && goversion.VersionAfterOrEqual(runtime.Version(), 1, 15) {
						// Go1.15 on windows inserts a NOP after the call to
						// runtime.Breakpoint and marks it same line as the
						// runtime.Breakpoint call, making this flaky, so skip the line check.
						startLineno = -1
					}

					checkStop(t, client, 1, "main.foobar", startLineno)

					// Args of foobar(baz string, bar FooBar)
					args := tester.variables(1000)

					checkVarExact(t, args, 0, "baz", "baz", `"bazburzum"`, "string", noChildren)
					tester.expectSetVariable(1000, "baz", `"BazBurZum"`)
					tester.evaluate("baz", `"BazBurZum"`, noChildren)

					args = tester.variables(1000)
					barRef := checkVarExact(t, args, 1, "bar", "bar", `main.FooBar {Baz: 10, Bur: "lorem"}`, "main.FooBar", hasChildren)
					tester.expectSetVariable(barRef, "Bur", `"ipsum"`)
					tester.evaluate("bar", `main.FooBar {Baz: 10, Bur: "ipsum"}`, hasChildren)

					// Local variables
					locals := tester.variables(1001)

					checkVarExact(t, locals, -1, "a1", "a1", `"foofoofoofoofoofoo"`, "string", noChildren)
					tester.expectSetVariable(1001, "a1", `"barbarbar"`)
					tester.evaluate("a1", `"barbarbar"`, noChildren)

					a6Ref := checkVarExact(t, locals, -1, "a6", "a6", `main.FooBar {Baz: 8, Bur: "word"}`, "main.FooBar", hasChildren)
					tester.failSetVariable(a6Ref, "Bur", "false", "can not convert")

					tester.expectSetVariable(a6Ref, "Bur", `"sentence"`)
					tester.evaluate("a6", `main.FooBar {Baz: 8, Bur: "sentence"}`, hasChildren)
				},
			}, {
				// Stop at second breakpoint and set a1.
				execute: func() {
					tester := &helperForSetVariable{t, client}

					checkStop(t, client, 1, "main.barfoo", -1)
					// Test: set string 'a1' in main.barfoo.
					// This shouldn't affect 'a1' in main.foobar - we will check that in the next breakpoint.
					locals := tester.variables(1001)
					checkVarExact(t, locals, -1, "a1", "a1", `"bur"`, "string", noChildren)
					tester.expectSetVariable(1001, "a1", `"fur"`)
					tester.evaluate("a1", `"fur"`, noChildren)
					// We will check a1 in main.foobar isn't affected from the next breakpoint.

					client.StackTraceRequest(1, 1, 20)
					res := client.ExpectStackTraceResponse(t)
					if len(res.Body.StackFrames) < 1 {
						t.Fatalf("stack trace response = %#v, wanted at least one stack frame", res)
					}
					outerFrame := res.Body.StackFrames[0].Id
					client.EvaluateRequest("a1", outerFrame, "whatever_context")
					evalRes := client.ExpectEvaluateResponse(t)
					checkEval(t, evalRes, `"barbarbar"`, noChildren)
				},
				disconnect: true,
			}})
	})

	runTest(t, "fncall", func(client *daptest.Client, fixture protest.Fixture) {
		runDebugSessionWithBPs(t, client, "launch",
			func() {
				client.LaunchRequestWithArgs(map[string]interface{}{
					"mode": "exec", "program": fixture.Path, "showGlobalVariables": true,
				})
			},
			fixture.Source, []int{}, // breakpoints are set within the program.
			[]onBreakpoint{{
				// Stop at second breakpoint and set a1.
				execute: func() {
					tester := &helperForSetVariable{t, client}

					checkStop(t, client, 1, "main.main", -1)

					_ = tester.variables(1001)

					// successful variable set using a function call.
					tester.expectSetVariable(1001, "str", `callstacktrace()`)
					tester.evaluateRegex("str", `.*in main.callstacktrace at.*`, noChildren)

					tester.failSetVariableAndStop(1001, "str", `callpanic()`, `callpanic panicked`)
					checkStop(t, client, 1, "main.main", -1)

					// breakpoint during a function call.
					tester.failSetVariableAndStop(1001, "str", `callbreak()`, "call stopped")

					// TODO(hyangah): continue after this causes runtime error while resuming
					// unfinished injected call.
					//   runtime error: can not convert %!s(<nil>) constant to string
					// This can be reproducible with dlv cli. (`call str = callbreak(); continue`)
				},
				disconnect: true,
			}})
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
		checkFailedToLaunch := func(response *dap.ErrorResponse) {
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

		checkFailedToLaunchWithMessage := func(response *dap.ErrorResponse, errmsg string) {
			t.Helper()
			checkFailedToLaunch(response)
			if response.Body.Error.Format != errmsg {
				t.Errorf("\ngot  %q\nwant %q", response.Body.Error.Format, errmsg)
			}
		}

		// Test for the DAP-specific detailed error message.
		client.LaunchRequest("exec", "", stopOnEntry)
		checkFailedToLaunchWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to launch: The program attribute is missing in debug configuration.")

		// Bad "program"
		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "debug", "program": 12345})
		checkFailedToLaunchWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to launch: The program attribute is missing in debug configuration.")

		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "debug", "program": nil})
		checkFailedToLaunchWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to launch: The program attribute is missing in debug configuration.")

		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "debug"})
		checkFailedToLaunchWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to launch: The program attribute is missing in debug configuration.")

		// Bad "mode"
		client.LaunchRequest("remote", fixture.Path, stopOnEntry)
		checkFailedToLaunchWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to launch: Unsupported 'mode' value \"remote\" in debug configuration.")

		client.LaunchRequest("notamode", fixture.Path, stopOnEntry)
		checkFailedToLaunchWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to launch: Unsupported 'mode' value \"notamode\" in debug configuration.")

		client.LaunchRequestWithArgs(map[string]interface{}{"mode": 12345, "program": fixture.Path})
		checkFailedToLaunchWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to launch: Unsupported 'mode' value %!q(float64=12345) in debug configuration.")

		client.LaunchRequestWithArgs(map[string]interface{}{"mode": ""}) // empty mode defaults to "debug" (not an error)
		checkFailedToLaunchWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to launch: The program attribute is missing in debug configuration.")

		client.LaunchRequestWithArgs(map[string]interface{}{}) // missing mode defaults to "debug" (not an error)
		checkFailedToLaunchWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to launch: The program attribute is missing in debug configuration.")

		// Bad "args"
		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "exec", "program": fixture.Path, "args": nil})
		checkFailedToLaunchWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to launch: 'args' attribute '<nil>' in debug configuration is not an array.")

		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "exec", "program": fixture.Path, "args": 12345})
		checkFailedToLaunchWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to launch: 'args' attribute '12345' in debug configuration is not an array.")

		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "exec", "program": fixture.Path, "args": []int{1, 2}})
		checkFailedToLaunchWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to launch: value '1' in 'args' attribute in debug configuration is not a string.")

		// Bad "buildFlags"
		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "debug", "program": fixture.Source, "buildFlags": 123})
		checkFailedToLaunchWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to launch: 'buildFlags' attribute '123' in debug configuration is not a string.")

		// Bad "backend"
		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "debug", "program": fixture.Source, "backend": 123})
		checkFailedToLaunchWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to launch: 'backend' attribute '123' in debug configuration is not a string.")
		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "debug", "program": fixture.Source, "backend": "foo"})
		checkFailedToLaunchWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to launch: could not launch process: unknown backend \"foo\"")
		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "debug", "program": fixture.Source, "backend": ""})
		checkFailedToLaunchWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to launch: could not launch process: unknown backend \"\"")

		// Bad "substitutePath"
		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "debug", "program": fixture.Source, "substitutePath": 123})
		checkFailedToLaunchWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to launch: 'substitutePath' attribute '123' in debug configuration is not a []{'from': string, 'to': string}")

		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "debug", "program": fixture.Source, "substitutePath": []interface{}{123}})
		checkFailedToLaunchWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to launch: 'substitutePath' attribute '[123]' in debug configuration is not a []{'from': string, 'to': string}")

		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "debug", "program": fixture.Source, "substitutePath": []interface{}{map[string]interface{}{"to": "path2"}}})
		checkFailedToLaunchWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to launch: 'substitutePath' attribute '[map[to:path2]]' in debug configuration is not a []{'from': string, 'to': string}")

		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "debug", "program": fixture.Source, "substitutePath": []interface{}{map[string]interface{}{"from": "path1", "to": 123}}})
		checkFailedToLaunchWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to launch: 'substitutePath' attribute '[map[from:path1 to:123]]' in debug configuration is not a []{'from': string, 'to': string}")

		// Bad "cwd"
		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "debug", "program": fixture.Source, "cwd": 123})
		checkFailedToLaunchWithMessage(client.ExpectErrorResponse(t),
			"Failed to launch: 'cwd' attribute '123' in debug configuration is not a string.")

		// Skip detailed message checks for potentially different OS-specific errors.
		client.LaunchRequest("exec", fixture.Path+"_does_not_exist", stopOnEntry)
		checkFailedToLaunch(client.ExpectInvisibleErrorResponse(t)) // No such file or directory

		client.LaunchRequest("debug", fixture.Path+"_does_not_exist", stopOnEntry)
		oe := client.ExpectOutputEvent(t)
		if !strings.HasPrefix(oe.Body.Output, "Build Error: ") || oe.Body.Category != "stderr" {
			t.Errorf("got %#v, want Category=\"stderr\" Output=\"Build Error: ...\"", oe)
		}
		checkFailedToLaunch(client.ExpectInvisibleErrorResponse(t))

		client.LaunchRequest("" /*debug by default*/, fixture.Path+"_does_not_exist", stopOnEntry)
		oe = client.ExpectOutputEvent(t)
		if !strings.HasPrefix(oe.Body.Output, "Build Error: ") || oe.Body.Category != "stderr" {
			t.Errorf("got %#v, want Category=\"stderr\" Output=\"Build Error: ...\"", oe)
		}
		checkFailedToLaunch(client.ExpectInvisibleErrorResponse(t))

		client.LaunchRequest("exec", fixture.Source, stopOnEntry)
		checkFailedToLaunch(client.ExpectInvisibleErrorResponse(t)) // Not an executable

		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "debug", "program": fixture.Source, "buildFlags": "-bad -flags"})
		oe = client.ExpectOutputEvent(t)
		if !strings.HasPrefix(oe.Body.Output, "Build Error: ") || oe.Body.Category != "stderr" {
			t.Errorf("got %#v, want Category=\"stderr\" Output=\"Build Error: ...\"", oe)
		}
		checkFailedToLaunchWithMessage(client.ExpectInvisibleErrorResponse(t), "Failed to launch: Build error: Check the debug console for details.")
		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "debug", "program": fixture.Source, "noDebug": true, "buildFlags": "-bad -flags"})
		oe = client.ExpectOutputEvent(t)
		if !strings.HasPrefix(oe.Body.Output, "Build Error: ") || oe.Body.Category != "stderr" {
			t.Errorf("got %#v, want Category=\"stderr\" Output=\"Build Error: ...\"", oe)
		}
		checkFailedToLaunchWithMessage(client.ExpectInvisibleErrorResponse(t), "Failed to launch: Build error: Check the debug console for details.")

		// Bad "cwd"
		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "debug", "program": fixture.Source, "noDebug": false, "cwd": "dir/invalid"})
		checkFailedToLaunch(client.ExpectErrorResponse(t)) // invalid directory, the error message is system-dependent.
		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "debug", "program": fixture.Source, "noDebug": true, "cwd": "dir/invalid"})
		checkFailedToLaunch(client.ExpectErrorResponse(t)) // invalid directory, the error message is system-dependent.

		// Bad parameters on "replay" and "core" modes
		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "replay", "traceDirPath": ""})
		checkFailedToLaunchWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to launch: The 'traceDirPath' attribute is missing in debug configuration.")
		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "core", "coreFilePath": ""})
		checkFailedToLaunchWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to launch: The program attribute is missing in debug configuration.")
		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "replay", "program": fixture.Source, "traceDirPath": ""})
		checkFailedToLaunchWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to launch: The 'traceDirPath' attribute is missing in debug configuration.")
		client.LaunchRequestWithArgs(map[string]interface{}{"mode": "core", "program": fixture.Source, "coreFilePath": ""})
		checkFailedToLaunchWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to launch: The 'coreFilePath' attribute is missing in debug configuration.")

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
		checkFailedToAttach := func(response *dap.ErrorResponse) {
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

		checkFailedToAttachWithMessage := func(response *dap.ErrorResponse, errmsg string) {
			t.Helper()
			checkFailedToAttach(response)
			if response.Body.Error.Format != errmsg {
				t.Errorf("\ngot  %q\nwant %q", response.Body.Error.Format, errmsg)
			}
		}

		// Bad "mode"
		client.AttachRequest(map[string]interface{}{"mode": "remote"})
		checkFailedToAttachWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to attach: Unsupported 'mode' value \"remote\" in debug configuration")

		client.AttachRequest(map[string]interface{}{"mode": "blah blah blah"})
		checkFailedToAttachWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to attach: Unsupported 'mode' value \"blah blah blah\" in debug configuration")

		client.AttachRequest(map[string]interface{}{"mode": 123})
		checkFailedToAttachWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to attach: Unsupported 'mode' value %!q(float64=123) in debug configuration")

		client.AttachRequest(map[string]interface{}{"mode": ""}) // empty mode defaults to "local" (not an error)
		checkFailedToAttachWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to attach: The 'processId' attribute is missing in debug configuration")

		client.AttachRequest(map[string]interface{}{}) // no mode defaults to "local" (not an error)
		checkFailedToAttachWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to attach: The 'processId' attribute is missing in debug configuration")

		// Bad "processId"
		client.AttachRequest(map[string]interface{}{"mode": "local"})
		checkFailedToAttachWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to attach: The 'processId' attribute is missing in debug configuration")

		client.AttachRequest(map[string]interface{}{"mode": "local", "processId": nil})
		checkFailedToAttachWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to attach: The 'processId' attribute is missing in debug configuration")

		client.AttachRequest(map[string]interface{}{"mode": "local", "processId": 0})
		checkFailedToAttachWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to attach: The 'processId' attribute is missing in debug configuration")

		client.AttachRequest(map[string]interface{}{"mode": "local", "processId": "1"})
		checkFailedToAttachWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to attach: The 'processId' attribute is missing in debug configuration")

		client.AttachRequest(map[string]interface{}{"mode": "local", "processId": 1})
		// The exact message varies on different systems, so skip that check
		checkFailedToAttach(client.ExpectInvisibleErrorResponse(t)) // could not attach to pid 1

		// This will make debugger.(*Debugger) panic, which we will catch as an internal error.
		client.AttachRequest(map[string]interface{}{"mode": "local", "processId": -1})
		er := client.ExpectInvisibleErrorResponse(t)
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

		// Bad "backend"
		client.AttachRequest(map[string]interface{}{"mode": "local", "processId": 1, "backend": 123})
		checkFailedToAttachWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to attach: 'backend' attribute '123' in debug configuration is not a string.")
		client.AttachRequest(map[string]interface{}{"mode": "local", "processId": 1, "backend": "foo"})
		checkFailedToAttachWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to attach: could not attach to pid 1: unknown backend \"foo\"")
		client.AttachRequest(map[string]interface{}{"mode": "local", "processId": 1, "backend": ""})
		checkFailedToAttachWithMessage(client.ExpectInvisibleErrorResponse(t),
			"Failed to attach: could not attach to pid 1: unknown backend \"\"")

		// We failed to attach to the program. Make sure shutdown still works.
		client.DisconnectRequest()
		dresp := client.ExpectDisconnectResponse(t)
		if dresp.RequestSeq != seqCnt {
			t.Errorf("got %#v, want RequestSeq=%d", dresp, seqCnt)
		}
	})
}

func TestBadInitializeRequest(t *testing.T) {
	runInitializeTest := func(args dap.InitializeRequestArguments, err string) {
		t.Helper()
		// Only one initialize request is allowed, so use a new server
		// for each test.
		client := startDapServer(t)
		// client.Close will close the client connectinon, which will cause a connection error
		// on the server side and signal disconnect to unblock Stop() above.
		defer client.Close()

		client.InitializeRequestWithArgs(args)
		response := client.ExpectErrorResponse(t)
		if response.Command != "initialize" {
			t.Errorf("Command got %q, want \"launch\"", response.Command)
		}
		if response.Message != "Failed to initialize" {
			t.Errorf("Message got %q, want \"Failed to launch\"", response.Message)
		}
		if response.Body.Error.Id != 3002 {
			t.Errorf("Id got %d, want 3002", response.Body.Error.Id)
		}
		if response.Body.Error.Format != err {
			t.Errorf("\ngot  %q\nwant %q", response.Body.Error.Format, err)
		}
	}

	// Bad path format.
	runInitializeTest(dap.InitializeRequestArguments{
		AdapterID:       "go",
		PathFormat:      "url", // unsupported 'pathFormat'
		LinesStartAt1:   true,
		ColumnsStartAt1: true,
		Locale:          "en-us",
	},
		"Failed to initialize: Unsupported 'pathFormat' value 'url'.",
	)

	// LinesStartAt1 must be true.
	runInitializeTest(dap.InitializeRequestArguments{
		AdapterID:       "go",
		PathFormat:      "path",
		LinesStartAt1:   false, // only 1-based line numbers are supported
		ColumnsStartAt1: true,
		Locale:          "en-us",
	},
		"Failed to initialize: Only 1-based line numbers are supported.",
	)

	// ColumnsStartAt1 must be true.
	runInitializeTest(dap.InitializeRequestArguments{
		AdapterID:       "go",
		PathFormat:      "path",
		LinesStartAt1:   true,
		ColumnsStartAt1: false, // only 1-based column numbers are supported
		Locale:          "en-us",
	},
		"Failed to initialize: Only 1-based column numbers are supported.",
	)
}

func TestBadlyFormattedMessageToServer(t *testing.T) {
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		// Send a badly formatted message to the server, and expect it to close the
		// connection.
		client.BadRequest()
		time.Sleep(100 * time.Millisecond)

		_, err := client.ReadMessage()

		if err != io.EOF {
			t.Errorf("got err=%v, want io.EOF", err)
		}
	})
	runTest(t, "increment", func(client *daptest.Client, fixture protest.Fixture) {
		// Send an unknown request message to the server, and expect it to send
		// an error response.
		client.UnknownRequest()
		err := client.ExpectErrorResponse(t)
		if err.Body.Error.Format != "Internal Error: Request command 'unknown' is not supported (seq: 1)" || err.RequestSeq != 1 {
			t.Errorf("got %v, want  RequestSeq=1 Error=\"Internal Error: Request command 'unknown' is not supported (seq: 1)\"", err)
		}

		// Make sure that the unknown request did not kill the server.
		client.InitializeRequest()
		client.ExpectInitializeResponse(t)
	})
}
