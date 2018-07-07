package service_test

import (
	"flag"
	"fmt"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	protest "github.com/derekparker/delve/pkg/proc/test"
	"github.com/derekparker/delve/pkg/terminal"

	"github.com/derekparker/delve/pkg/goversion"
	"github.com/derekparker/delve/service"
	"github.com/derekparker/delve/service/api"
	"github.com/derekparker/delve/service/rpc2"
	"github.com/derekparker/delve/service/rpccommon"
)

var normalLoadConfig = api.LoadConfig{true, 1, 64, 64, -1}
var testBackend string

func TestMain(m *testing.M) {
	flag.StringVar(&testBackend, "backend", "", "selects backend")
	flag.Parse()
	if testBackend == "" {
		testBackend = os.Getenv("PROCTEST")
		if testBackend == "" {
			testBackend = "native"
		}
	}
	os.Exit(protest.RunTestsWithFixtures(m))
}

func withTestClient2(name string, t *testing.T, fn func(c service.Client)) {
	if testBackend == "rr" {
		protest.MustHaveRecordingAllowed(t)
	}
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("couldn't start listener: %s\n", err)
	}
	defer listener.Close()
	server := rpccommon.NewServer(&service.Config{
		Listener:    listener,
		ProcessArgs: []string{protest.BuildFixture(name, 0).Path},
		Backend:     testBackend,
	})
	if err := server.Run(); err != nil {
		t.Fatal(err)
	}
	client := rpc2.NewClient(listener.Addr().String())
	defer func() {
		dir, _ := client.TraceDirectory()
		client.Detach(true)
		if dir != "" {
			protest.SafeRemoveAll(dir)
		}
	}()

	fn(client)
}

func TestRunWithInvalidPath(t *testing.T) {
	if testBackend == "rr" {
		// This test won't work because rr returns an error, after recording, when
		// the recording failed but also when the recording succeeded but the
		// inferior returned an error. Therefore we have to ignore errors from rr.
		return
	}
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("couldn't start listener: %s\n", err)
	}
	defer listener.Close()
	server := rpccommon.NewServer(&service.Config{
		Listener:    listener,
		ProcessArgs: []string{"invalid_path"},
		APIVersion:  2,
		Backend:     testBackend,
	})
	if err := server.Run(); err == nil {
		t.Fatal("Expected Run to return error for invalid program path")
	}
}

func TestRestart_afterExit(t *testing.T) {
	withTestClient2("continuetestprog", t, func(c service.Client) {
		origPid := c.ProcessPid()
		state := <-c.Continue()
		if !state.Exited {
			t.Fatal("expected initial process to have exited")
		}
		if _, err := c.Restart(); err != nil {
			t.Fatal(err)
		}
		if c.ProcessPid() == origPid {
			t.Fatal("did not spawn new process, has same PID")
		}
		state = <-c.Continue()
		if !state.Exited {
			t.Fatalf("expected restarted process to have exited %v", state)
		}
	})
}

func TestRestart_breakpointPreservation(t *testing.T) {
	protest.AllowRecording(t)
	withTestClient2("continuetestprog", t, func(c service.Client) {
		_, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.main", Line: 1, Name: "firstbreakpoint", Tracepoint: true})
		assertNoError(err, t, "CreateBreakpoint()")
		stateCh := c.Continue()

		state := <-stateCh
		if state.CurrentThread.Breakpoint.Name != "firstbreakpoint" || !state.CurrentThread.Breakpoint.Tracepoint {
			t.Fatalf("Wrong breakpoint: %#v\n", state.CurrentThread.Breakpoint)
		}
		// Trace return breakpoint.
		state = <-stateCh
		if !state.CurrentThread.Breakpoint.Tracepoint {
			t.Fatalf("Wrong breakpoint: %#v\n", state.CurrentThread.Breakpoint)
		}
		state = <-stateCh
		if !state.Exited {
			t.Fatal("Did not exit after first tracepoint")
		}

		t.Log("Restart")
		c.Restart()
		stateCh = c.Continue()
		state = <-stateCh
		if state.CurrentThread.Breakpoint.Name != "firstbreakpoint" || !state.CurrentThread.Breakpoint.Tracepoint {
			t.Fatalf("Wrong breakpoint (after restart): %#v\n", state.CurrentThread.Breakpoint)
		}
		// Trace return breakpoint.
		state = <-stateCh
		if !state.CurrentThread.Breakpoint.Tracepoint {
			t.Fatalf("Wrong breakpoint: %#v\n", state.CurrentThread.Breakpoint)
		}
		state = <-stateCh
		if !state.Exited {
			t.Fatal("Did not exit after first tracepoint (after restart)")
		}
	})
}

func TestRestart_duringStop(t *testing.T) {
	withTestClient2("continuetestprog", t, func(c service.Client) {
		origPid := c.ProcessPid()
		_, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.main", Line: 1})
		if err != nil {
			t.Fatal(err)
		}
		state := <-c.Continue()
		if state.CurrentThread.Breakpoint == nil {
			t.Fatal("did not hit breakpoint")
		}
		if _, err := c.Restart(); err != nil {
			t.Fatal(err)
		}
		if c.ProcessPid() == origPid {
			t.Fatal("did not spawn new process, has same PID")
		}
		bps, err := c.ListBreakpoints()
		if err != nil {
			t.Fatal(err)
		}
		if len(bps) == 0 {
			t.Fatal("breakpoints not preserved")
		}
	})
}

func TestRestart_attachPid(t *testing.T) {
	// Assert it does not work and returns error.
	// We cannot restart a process we did not spawn.
	server := rpccommon.NewServer(&service.Config{
		Listener:   nil,
		AttachPid:  999,
		APIVersion: 2,
		Backend:    testBackend,
	})
	if err := server.Restart(); err == nil {
		t.Fatal("expected error on restart after attaching to pid but got none")
	}
}

func TestClientServer_exit(t *testing.T) {
	protest.AllowRecording(t)
	withTestClient2("continuetestprog", t, func(c service.Client) {
		state, err := c.GetState()
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if e, a := false, state.Exited; e != a {
			t.Fatalf("Expected exited %v, got %v", e, a)
		}
		state = <-c.Continue()
		if state.Err == nil {
			t.Fatalf("Error expected after continue")
		}
		if !state.Exited {
			t.Fatalf("Expected exit after continue: %v", state)
		}
		_, err = c.GetState()
		if err == nil {
			t.Fatal("Expected error on querying state from exited process")
		}
	})
}

func TestClientServer_step(t *testing.T) {
	protest.AllowRecording(t)
	withTestClient2("testprog", t, func(c service.Client) {
		_, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.helloworld", Line: -1})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		stateBefore := <-c.Continue()
		if stateBefore.Err != nil {
			t.Fatalf("Unexpected error: %v", stateBefore.Err)
		}

		stateAfter, err := c.Step()
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		if before, after := stateBefore.CurrentThread.PC, stateAfter.CurrentThread.PC; before >= after {
			t.Errorf("Expected %#v to be greater than %#v", before, after)
		}
	})
}

func TestClientServer_stepout(t *testing.T) {
	protest.AllowRecording(t)
	withTestClient2("testnextprog", t, func(c service.Client) {
		_, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.helloworld", Line: -1})
		assertNoError(err, t, "CreateBreakpoint()")
		stateBefore := <-c.Continue()
		assertNoError(stateBefore.Err, t, "Continue()")
		if stateBefore.CurrentThread.Line != 13 {
			t.Fatalf("wrong line number %s:%d, expected %d", stateBefore.CurrentThread.File, stateBefore.CurrentThread.Line, 13)
		}
		stateAfter, err := c.StepOut()
		assertNoError(err, t, "StepOut()")
		if stateAfter.CurrentThread.Line != 35 {
			t.Fatalf("wrong line number %s:%d, expected %d", stateAfter.CurrentThread.File, stateAfter.CurrentThread.Line, 13)
		}
	})
}

func testnext2(testcases []nextTest, initialLocation string, t *testing.T) {
	protest.AllowRecording(t)
	withTestClient2("testnextprog", t, func(c service.Client) {
		bp, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: initialLocation, Line: -1})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		state := <-c.Continue()
		if state.Err != nil {
			t.Fatalf("Unexpected error: %v", state.Err)
		}

		_, err = c.ClearBreakpoint(bp.ID)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		for _, tc := range testcases {
			if state.CurrentThread.Line != tc.begin {
				t.Fatalf("Program not stopped at correct spot expected %d was %d", tc.begin, state.CurrentThread.Line)
			}

			t.Logf("Next for scenario %#v", tc)
			state, err = c.Next()
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if state.CurrentThread.Line != tc.end {
				t.Fatalf("Program did not continue to correct next location expected %d was %d", tc.end, state.CurrentThread.Line)
			}
		}
	})
}

func TestNextGeneral(t *testing.T) {
	var testcases []nextTest

	ver, _ := goversion.Parse(runtime.Version())

	if ver.Major < 0 || ver.AfterOrEqual(goversion.GoVersion{1, 7, -1, 0, 0, ""}) {
		testcases = []nextTest{
			{17, 19},
			{19, 20},
			{20, 23},
			{23, 24},
			{24, 26},
			{26, 31},
			{31, 23},
			{23, 24},
			{24, 26},
			{26, 31},
			{31, 23},
			{23, 24},
			{24, 26},
			{26, 27},
			{27, 28},
			{28, 34},
		}
	} else {
		testcases = []nextTest{
			{17, 19},
			{19, 20},
			{20, 23},
			{23, 24},
			{24, 26},
			{26, 31},
			{31, 23},
			{23, 24},
			{24, 26},
			{26, 31},
			{31, 23},
			{23, 24},
			{24, 26},
			{26, 27},
			{27, 34},
		}
	}

	testnext2(testcases, "main.testnext", t)
}

func TestNextFunctionReturn(t *testing.T) {
	testcases := []nextTest{
		{13, 14},
		{14, 15},
		{15, 35},
	}
	testnext2(testcases, "main.helloworld", t)
}

func TestClientServer_breakpointInMainThread(t *testing.T) {
	protest.AllowRecording(t)
	withTestClient2("testprog", t, func(c service.Client) {
		bp, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.helloworld", Line: 1})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		state := <-c.Continue()
		if err != nil {
			t.Fatalf("Unexpected error: %v, state: %#v", err, state)
		}

		pc := state.CurrentThread.PC

		if pc-1 != bp.Addr && pc != bp.Addr {
			f, l := state.CurrentThread.File, state.CurrentThread.Line
			t.Fatalf("Break not respected:\nPC:%#v %s:%d\nFN:%#v \n", pc, f, l, bp.Addr)
		}
	})
}

func TestClientServer_breakpointInSeparateGoroutine(t *testing.T) {
	protest.AllowRecording(t)
	withTestClient2("testthreads", t, func(c service.Client) {
		_, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.anotherthread", Line: 1})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		state := <-c.Continue()
		if state.Err != nil {
			t.Fatalf("Unexpected error: %v, state: %#v", state.Err, state)
		}

		f, l := state.CurrentThread.File, state.CurrentThread.Line
		if f != "testthreads.go" && l != 9 {
			t.Fatal("Program did not hit breakpoint")
		}
	})
}

func TestClientServer_breakAtNonexistentPoint(t *testing.T) {
	withTestClient2("testprog", t, func(c service.Client) {
		_, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "nowhere", Line: 1})
		if err == nil {
			t.Fatal("Should not be able to break at non existent function")
		}
	})
}

func TestClientServer_clearBreakpoint(t *testing.T) {
	withTestClient2("testprog", t, func(c service.Client) {
		bp, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.sleepytime", Line: 1})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		if e, a := 1, countBreakpoints(t, c); e != a {
			t.Fatalf("Expected breakpoint count %d, got %d", e, a)
		}

		deleted, err := c.ClearBreakpoint(bp.ID)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		if deleted.ID != bp.ID {
			t.Fatalf("Expected deleted breakpoint ID %v, got %v", bp.ID, deleted.ID)
		}

		if e, a := 0, countBreakpoints(t, c); e != a {
			t.Fatalf("Expected breakpoint count %d, got %d", e, a)
		}
	})
}

func TestClientServer_switchThread(t *testing.T) {
	protest.AllowRecording(t)
	withTestClient2("testnextprog", t, func(c service.Client) {
		// With invalid thread id
		_, err := c.SwitchThread(-1)
		if err == nil {
			t.Fatal("Expected error for invalid thread id")
		}

		_, err = c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.main", Line: 1})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		state := <-c.Continue()
		if state.Err != nil {
			t.Fatalf("Unexpected error: %v, state: %#v", state.Err, state)
		}

		var nt int
		ct := state.CurrentThread.ID
		threads, err := c.ListThreads()
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		for _, th := range threads {
			if th.ID != ct {
				nt = th.ID
				break
			}
		}
		if nt == 0 {
			t.Fatal("could not find thread to switch to")
		}
		// With valid thread id
		state, err = c.SwitchThread(nt)
		if err != nil {
			t.Fatal(err)
		}
		if state.CurrentThread.ID != nt {
			t.Fatal("Did not switch threads")
		}
	})
}

func TestClientServer_infoLocals(t *testing.T) {
	protest.AllowRecording(t)
	withTestClient2("testnextprog", t, func(c service.Client) {
		fp := testProgPath(t, "testnextprog")
		_, err := c.CreateBreakpoint(&api.Breakpoint{File: fp, Line: 23})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		state := <-c.Continue()
		if state.Err != nil {
			t.Fatalf("Unexpected error: %v, state: %#v", state.Err, state)
		}
		locals, err := c.ListLocalVariables(api.EvalScope{-1, 0}, normalLoadConfig)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if len(locals) != 3 {
			t.Fatalf("Expected 3 locals, got %d %#v", len(locals), locals)
		}
	})
}

func TestClientServer_infoArgs(t *testing.T) {
	protest.AllowRecording(t)
	withTestClient2("testnextprog", t, func(c service.Client) {
		fp := testProgPath(t, "testnextprog")
		_, err := c.CreateBreakpoint(&api.Breakpoint{File: fp, Line: 47})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		state := <-c.Continue()
		if state.Err != nil {
			t.Fatalf("Unexpected error: %v, state: %#v", state.Err, state)
		}
		regs, err := c.ListRegisters(0, false)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if len(regs) == 0 {
			t.Fatal("Expected string showing registers values, got empty string")
		}
		locals, err := c.ListFunctionArgs(api.EvalScope{-1, 0}, normalLoadConfig)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if len(locals) != 2 {
			t.Fatalf("Expected 2 function args, got %d %#v", len(locals), locals)
		}
	})
}

func TestClientServer_traceCmd(t *testing.T) {
	ver, _ := goversion.Parse(runtime.Version())
	if !ver.AfterOrEqual(goversion.GoVersion{1, 10, -1, 0, 0, ""}) {
		t.Skip("Go version too old")
	}
	protest.AllowRecording(t)
	withTestClient2("integrationprog", t, func(c service.Client) {
		c.SetReturnValuesLoadConfig(&normalLoadConfig)
		fp := testProgPath(t, "integrationprog")
		_, err := c.CreateBreakpoint(&api.Breakpoint{Name: "testtracepoint", File: fp, Line: 9, Tracepoint: true, LoadArgs: &terminal.ShortLoadConfig})
		if err != nil {
			t.Fatalf("Unexpected error: %v\n", err)
		}
		count := 0
		contChan := c.Continue()
		for state := range contChan {
			if state.CurrentThread != nil && state.CurrentThread.Breakpoint != nil {
				curbp := state.CurrentThread.Breakpoint
				bpi := state.CurrentThread.BreakpointInfo
				if curbp.Name == "testtracepoint" {
					if len(bpi.Arguments) != 1 {
						t.Fatalf("wrong number of arguments returned, expected 1 got %d", len(bpi.Arguments))
					}
					if bpi.Arguments[0].Value != strconv.Itoa(count) {
						t.Fatal("wrong argument returned", bpi.Arguments[0].Value, count)
					}
				} else { // Trace return tracepoint
					if len(state.CurrentThread.ReturnValues) != 1 {
						t.Fatalf("wrong number of return values returned, expected 1 got %d", len(state.CurrentThread.ReturnValues))
					}
					if !strings.Contains(state.CurrentThread.ReturnValues[0].Value, "hi") {
						t.Fatal("wrong return value returned", state.CurrentThread.ReturnValues)
					}
					count++
				}
			}
		}

		if count != 3 {
			t.Fatal("wrong number of breakpoints hit")
		}
	})
}

func TestClientServer_traceContinue(t *testing.T) {
	protest.AllowRecording(t)
	withTestClient2("integrationprog", t, func(c service.Client) {
		fp := testProgPath(t, "integrationprog")
		_, err := c.CreateBreakpoint(&api.Breakpoint{Name: "testtracepoint", File: fp, Line: 15, Tracepoint: true, Goroutine: true, Stacktrace: 5, Variables: []string{"i"}})
		if err != nil {
			t.Fatalf("Unexpected error: %v\n", err)
		}
		count := 0
		contChan := c.Continue()
		for state := range contChan {
			if state.CurrentThread != nil && state.CurrentThread.Breakpoint != nil && state.CurrentThread.Breakpoint.Name == "testtracepoint" {
				count++

				t.Logf("%v", state)

				bpi := state.CurrentThread.BreakpointInfo

				if bpi.Goroutine == nil {
					t.Fatalf("No goroutine information")
				}

				if len(bpi.Stacktrace) <= 0 {
					t.Fatalf("No stacktrace\n")
				}

				if len(bpi.Variables) != 1 {
					t.Fatalf("Wrong number of variables returned: %d", len(bpi.Variables))
				}

				if bpi.Variables[0].Name != "i" {
					t.Fatalf("Wrong variable returned %s", bpi.Variables[0].Name)
				}

				t.Logf("Variable i is %v", bpi.Variables[0])

				n, err := strconv.Atoi(bpi.Variables[0].Value)

				if err != nil || n != count-1 {
					t.Fatalf("Wrong variable value %q (%v %d)", bpi.Variables[0].Value, err, count)
				}
			}
			if state.Exited {
				continue
			}
			t.Logf("%v", state)
			if state.Err != nil {
				t.Fatalf("Unexpected error during continue: %v\n", state.Err)
			}

		}

		if count != 3 {
			t.Fatalf("Wrong number of continues hit: %d\n", count)
		}
	})
}

func TestClientServer_traceContinue2(t *testing.T) {
	protest.AllowRecording(t)
	withTestClient2("integrationprog", t, func(c service.Client) {
		bp1, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.main", Line: 1, Tracepoint: true})
		if err != nil {
			t.Fatalf("Unexpected error: %v\n", err)
		}
		bp2, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.sayhi", Line: 1, Tracepoint: true})
		if err != nil {
			t.Fatalf("Unexpected error: %v\n", err)
		}
		countMain := 0
		countSayhi := 0
		contChan := c.Continue()
		for state := range contChan {
			if state.CurrentThread != nil && state.CurrentThread.Breakpoint != nil {
				switch state.CurrentThread.Breakpoint.ID {
				case bp1.ID:
					countMain++
				case bp2.ID:
					countSayhi++
				}

				t.Logf("%v", state)
			}
			if state.Exited {
				continue
			}
			if state.Err != nil {
				t.Fatalf("Unexpected error during continue: %v\n", state.Err)
			}

		}

		if countMain != 1 {
			t.Fatalf("Wrong number of continues (main.main) hit: %d\n", countMain)
		}

		if countSayhi != 3 {
			t.Fatalf("Wrong number of continues (main.sayhi) hit: %d\n", countSayhi)
		}
	})
}

func TestClientServer_FindLocations(t *testing.T) {
	withTestClient2("locationsprog", t, func(c service.Client) {
		someFunctionCallAddr := findLocationHelper(t, c, "locationsprog.go:26", false, 1, 0)[0]
		someFunctionLine1 := findLocationHelper(t, c, "locationsprog.go:27", false, 1, 0)[0]
		findLocationHelper(t, c, "anotherFunction:1", false, 1, someFunctionLine1)
		findLocationHelper(t, c, "main.anotherFunction:1", false, 1, someFunctionLine1)
		findLocationHelper(t, c, "anotherFunction", false, 1, someFunctionCallAddr)
		findLocationHelper(t, c, "main.anotherFunction", false, 1, someFunctionCallAddr)
		findLocationHelper(t, c, fmt.Sprintf("*0x%x", someFunctionCallAddr), false, 1, someFunctionCallAddr)
		findLocationHelper(t, c, "sprog.go:26", true, 0, 0)

		findLocationHelper(t, c, "String", true, 0, 0)
		findLocationHelper(t, c, "main.String", true, 0, 0)

		someTypeStringFuncAddr := findLocationHelper(t, c, "locationsprog.go:14", false, 1, 0)[0]
		otherTypeStringFuncAddr := findLocationHelper(t, c, "locationsprog.go:18", false, 1, 0)[0]
		findLocationHelper(t, c, "SomeType.String", false, 1, someTypeStringFuncAddr)
		findLocationHelper(t, c, "(*SomeType).String", false, 1, someTypeStringFuncAddr)
		findLocationHelper(t, c, "main.SomeType.String", false, 1, someTypeStringFuncAddr)
		findLocationHelper(t, c, "main.(*SomeType).String", false, 1, someTypeStringFuncAddr)

		// Issue #275
		readfile := findLocationHelper(t, c, "io/ioutil.ReadFile", false, 1, 0)[0]

		// Issue #296
		findLocationHelper(t, c, "/io/ioutil.ReadFile", false, 1, readfile)
		findLocationHelper(t, c, "ioutil.ReadFile", false, 1, readfile)

		stringAddrs := findLocationHelper(t, c, "/^main.*Type.*String$/", false, 2, 0)

		if otherTypeStringFuncAddr != stringAddrs[0] && otherTypeStringFuncAddr != stringAddrs[1] {
			t.Fatalf("Wrong locations returned for \"/.*Type.*String/\", got: %v expected: %v and %v\n", stringAddrs, someTypeStringFuncAddr, otherTypeStringFuncAddr)
		}

		_, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.main", Line: 4, Tracepoint: false})
		if err != nil {
			t.Fatalf("CreateBreakpoint(): %v\n", err)
		}

		<-c.Continue()

		locationsprog35Addr := findLocationHelper(t, c, "locationsprog.go:35", false, 1, 0)[0]
		findLocationHelper(t, c, fmt.Sprintf("%s:35", testProgPath(t, "locationsprog")), false, 1, locationsprog35Addr)
		findLocationHelper(t, c, "+1", false, 1, locationsprog35Addr)
		findLocationHelper(t, c, "35", false, 1, locationsprog35Addr)
		findLocationHelper(t, c, "-1", false, 1, findLocationHelper(t, c, "locationsprog.go:33", false, 1, 0)[0])

		findLocationHelper(t, c, `*amap["k"]`, false, 1, findLocationHelper(t, c, `amap["k"]`, false, 1, 0)[0])
	})

	withTestClient2("testnextdefer", t, func(c service.Client) {
		firstMainLine := findLocationHelper(t, c, "testnextdefer.go:5", false, 1, 0)[0]
		findLocationHelper(t, c, "main.main", false, 1, firstMainLine)
	})

	withTestClient2("stacktraceprog", t, func(c service.Client) {
		stacktracemeAddr := findLocationHelper(t, c, "stacktraceprog.go:4", false, 1, 0)[0]
		findLocationHelper(t, c, "main.stacktraceme", false, 1, stacktracemeAddr)
	})

	withTestClient2("locationsUpperCase", t, func(c service.Client) {
		// Upper case
		findLocationHelper(t, c, "locationsUpperCase.go:6", false, 1, 0)

		// Fully qualified path
		path := protest.Fixtures[protest.FixtureKey{"locationsUpperCase", 0}].Source
		findLocationHelper(t, c, path+":6", false, 1, 0)
		bp, err := c.CreateBreakpoint(&api.Breakpoint{File: path, Line: 6})
		if err != nil {
			t.Fatalf("Could not set breakpoint in %s: %v\n", path, err)
		}
		c.ClearBreakpoint(bp.ID)

		//  Allow `/` or `\` on Windows
		if runtime.GOOS == "windows" {
			findLocationHelper(t, c, filepath.FromSlash(path)+":6", false, 1, 0)
			bp, err = c.CreateBreakpoint(&api.Breakpoint{File: filepath.FromSlash(path), Line: 6})
			if err != nil {
				t.Fatalf("Could not set breakpoint in %s: %v\n", filepath.FromSlash(path), err)
			}
			c.ClearBreakpoint(bp.ID)
		}

		// Case-insensitive on Windows, case-sensitive otherwise
		shouldWrongCaseBeError := true
		numExpectedMatches := 0
		if runtime.GOOS == "windows" {
			shouldWrongCaseBeError = false
			numExpectedMatches = 1
		}
		findLocationHelper(t, c, strings.ToLower(path)+":6", shouldWrongCaseBeError, numExpectedMatches, 0)
		bp, err = c.CreateBreakpoint(&api.Breakpoint{File: strings.ToLower(path), Line: 6})
		if (err == nil) == shouldWrongCaseBeError {
			t.Fatalf("Could not set breakpoint in %s: %v\n", strings.ToLower(path), err)
		}
		c.ClearBreakpoint(bp.ID)
	})
}

func TestClientServer_FindLocationsAddr(t *testing.T) {
	withTestClient2("locationsprog2", t, func(c service.Client) {
		<-c.Continue()

		afunction := findLocationHelper(t, c, "main.afunction", false, 1, 0)[0]
		anonfunc := findLocationHelper(t, c, "main.main.func1", false, 1, 0)[0]

		findLocationHelper(t, c, "*fn1", false, 1, afunction)
		findLocationHelper(t, c, "*fn3", false, 1, anonfunc)
	})
}

func TestClientServer_FindLocationsExactMatch(t *testing.T) {
	// if an expression matches multiple functions but one of them is an exact
	// match it should be used anyway.
	// In this example "math/rand.Intn" would normally match "math/rand.Intn"
	// and "math/rand.(*Rand).Intn" but since the first match is exact it
	// should be prioritized.
	withTestClient2("locationsprog3", t, func(c service.Client) {
		<-c.Continue()
		findLocationHelper(t, c, "math/rand.Intn", false, 1, 0)
	})
}

func TestClientServer_EvalVariable(t *testing.T) {
	withTestClient2("testvariables", t, func(c service.Client) {
		state := <-c.Continue()

		if state.Err != nil {
			t.Fatalf("Continue(): %v\n", state.Err)
		}

		var1, err := c.EvalVariable(api.EvalScope{-1, 0}, "a1", normalLoadConfig)
		assertNoError(err, t, "EvalVariable")

		t.Logf("var1: %s", var1.SinglelineString())

		if var1.Value != "foofoofoofoofoofoo" {
			t.Fatalf("Wrong variable value: %s", var1.Value)
		}
	})
}

func TestClientServer_SetVariable(t *testing.T) {
	withTestClient2("testvariables", t, func(c service.Client) {
		state := <-c.Continue()

		if state.Err != nil {
			t.Fatalf("Continue(): %v\n", state.Err)
		}

		assertNoError(c.SetVariable(api.EvalScope{-1, 0}, "a2", "8"), t, "SetVariable()")

		a2, err := c.EvalVariable(api.EvalScope{-1, 0}, "a2", normalLoadConfig)
		if err != nil {
			t.Fatalf("Could not evaluate variable: %v", err)
		}

		t.Logf("a2: %v", a2)

		n, err := strconv.Atoi(a2.Value)

		if err != nil && n != 8 {
			t.Fatalf("Wrong variable value: %v", a2)
		}
	})
}

func TestClientServer_FullStacktrace(t *testing.T) {
	protest.AllowRecording(t)
	withTestClient2("goroutinestackprog", t, func(c service.Client) {
		_, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.stacktraceme", Line: -1})
		assertNoError(err, t, "CreateBreakpoint()")
		state := <-c.Continue()
		if state.Err != nil {
			t.Fatalf("Continue(): %v\n", state.Err)
		}

		gs, err := c.ListGoroutines()
		assertNoError(err, t, "GoroutinesInfo()")
		found := make([]bool, 10)
		for _, g := range gs {
			frames, err := c.Stacktrace(g.ID, 10, &normalLoadConfig)
			assertNoError(err, t, fmt.Sprintf("Stacktrace(%d)", g.ID))
			for i, frame := range frames {
				if frame.Function == nil {
					continue
				}
				if frame.Function.Name != "main.agoroutine" {
					continue
				}
				t.Logf("frame %d: %v", i, frame)
				for _, arg := range frame.Arguments {
					if arg.Name != "i" {
						continue
					}
					t.Logf("frame %v, variable i is %v\n", frame, arg)
					argn, err := strconv.Atoi(arg.Value)
					if err == nil {
						found[argn] = true
					}
				}
			}
		}

		for i := range found {
			if !found[i] {
				t.Fatalf("Goroutine %d not found", i)
			}
		}

		state = <-c.Continue()
		if state.Err != nil {
			t.Fatalf("Continue(): %v\n", state.Err)
		}

		frames, err := c.Stacktrace(-1, 10, &normalLoadConfig)
		assertNoError(err, t, "Stacktrace")

		cur := 3
		for i, frame := range frames {
			if i == 0 {
				continue
			}
			t.Logf("frame %d: %v", i, frame)
			v := frame.Var("n")
			if v == nil {
				t.Fatalf("Could not find value of variable n in frame %d", i)
			}
			vn, err := strconv.Atoi(v.Value)
			if err != nil || vn != cur {
				t.Fatalf("Expected value %d got %d (error: %v)", cur, vn, err)
			}
			cur--
			if cur < 0 {
				break
			}
		}
	})
}

func TestIssue355(t *testing.T) {
	// After the target process has terminated should return an error but not crash
	protest.AllowRecording(t)
	withTestClient2("continuetestprog", t, func(c service.Client) {
		bp, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.sayhi", Line: -1})
		assertNoError(err, t, "CreateBreakpoint()")
		ch := c.Continue()
		state := <-ch
		tid := state.CurrentThread.ID
		gid := state.SelectedGoroutine.ID
		assertNoError(state.Err, t, "First Continue()")
		ch = c.Continue()
		state = <-ch
		if !state.Exited {
			t.Fatalf("Target did not terminate after second continue")
		}

		ch = c.Continue()
		state = <-ch
		assertError(state.Err, t, "Continue()")

		_, err = c.Next()
		assertError(err, t, "Next()")
		_, err = c.Step()
		assertError(err, t, "Step()")
		_, err = c.StepInstruction()
		assertError(err, t, "StepInstruction()")
		_, err = c.SwitchThread(tid)
		assertError(err, t, "SwitchThread()")
		_, err = c.SwitchGoroutine(gid)
		assertError(err, t, "SwitchGoroutine()")
		_, err = c.Halt()
		assertError(err, t, "Halt()")
		_, err = c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.main", Line: -1})
		if testBackend != "rr" {
			assertError(err, t, "CreateBreakpoint()")
		}
		_, err = c.ClearBreakpoint(bp.ID)
		if testBackend != "rr" {
			assertError(err, t, "ClearBreakpoint()")
		}
		_, err = c.ListThreads()
		assertError(err, t, "ListThreads()")
		_, err = c.GetThread(tid)
		assertError(err, t, "GetThread()")
		assertError(c.SetVariable(api.EvalScope{gid, 0}, "a", "10"), t, "SetVariable()")
		_, err = c.ListLocalVariables(api.EvalScope{gid, 0}, normalLoadConfig)
		assertError(err, t, "ListLocalVariables()")
		_, err = c.ListFunctionArgs(api.EvalScope{gid, 0}, normalLoadConfig)
		assertError(err, t, "ListFunctionArgs()")
		_, err = c.ListRegisters(0, false)
		assertError(err, t, "ListRegisters()")
		_, err = c.ListGoroutines()
		assertError(err, t, "ListGoroutines()")
		_, err = c.Stacktrace(gid, 10, &normalLoadConfig)
		assertError(err, t, "Stacktrace()")
		_, err = c.FindLocation(api.EvalScope{gid, 0}, "+1")
		assertError(err, t, "FindLocation()")
		_, err = c.DisassemblePC(api.EvalScope{-1, 0}, 0x40100, api.IntelFlavour)
		assertError(err, t, "DisassemblePC()")
	})
}

func TestDisasm(t *testing.T) {
	// Tests that disassembling by PC, range, and current PC all yeld similar results
	// Tests that disassembly by current PC will return a disassembly containing the instruction at PC
	// Tests that stepping on a calculated CALL instruction will yield a disassembly that contains the
	// effective destination of the CALL instruction
	withTestClient2("locationsprog2", t, func(c service.Client) {
		ch := c.Continue()
		state := <-ch
		assertNoError(state.Err, t, "Continue()")

		locs, err := c.FindLocation(api.EvalScope{-1, 0}, "main.main")
		assertNoError(err, t, "FindLocation()")
		if len(locs) != 1 {
			t.Fatalf("wrong number of locations for main.main: %d", len(locs))
		}
		d1, err := c.DisassemblePC(api.EvalScope{-1, 0}, locs[0].PC, api.IntelFlavour)
		assertNoError(err, t, "DisassemblePC()")
		if len(d1) < 2 {
			t.Fatalf("wrong size of disassembly: %d", len(d1))
		}

		pcstart := d1[0].Loc.PC
		pcend := d1[len(d1)-1].Loc.PC + uint64(len(d1[len(d1)-1].Bytes))
		d2, err := c.DisassembleRange(api.EvalScope{-1, 0}, pcstart, pcend, api.IntelFlavour)
		assertNoError(err, t, "DisassembleRange()")

		if len(d1) != len(d2) {
			t.Logf("d1: %v", d1)
			t.Logf("d2: %v", d2)
			t.Fatal("mismatched length between disassemble pc and disassemble range")
		}

		d3, err := c.DisassemblePC(api.EvalScope{-1, 0}, state.CurrentThread.PC, api.IntelFlavour)
		assertNoError(err, t, "DisassemblePC() - second call")

		if len(d1) != len(d3) {
			t.Logf("d1: %v", d1)
			t.Logf("d3: %v", d3)
			t.Fatal("mismatched length between the two calls of disassemble pc")
		}

		// look for static call to afunction() on line 29
		found := false
		for i := range d3 {
			if d3[i].Loc.Line == 29 && strings.HasPrefix(d3[i].Text, "call") && d3[i].DestLoc != nil && d3[i].DestLoc.Function != nil && d3[i].DestLoc.Function.Name == "main.afunction" {
				found = true
				break
			}
		}
		if !found {
			t.Fatal("Could not find call to main.afunction on line 29")
		}

		haspc := false
		for i := range d3 {
			if d3[i].AtPC {
				haspc = true
				break
			}
		}

		if !haspc {
			t.Logf("d3: %v", d3)
			t.Fatal("PC instruction not found")
		}

		startinstr := getCurinstr(d3)

		count := 0
		for {
			if count > 20 {
				t.Fatal("too many step instructions executed without finding a call instruction")
			}
			state, err := c.StepInstruction()
			assertNoError(err, t, fmt.Sprintf("StepInstruction() %d", count))

			d3, err = c.DisassemblePC(api.EvalScope{-1, 0}, state.CurrentThread.PC, api.IntelFlavour)
			assertNoError(err, t, fmt.Sprintf("StepInstruction() %d", count))

			curinstr := getCurinstr(d3)

			if curinstr == nil {
				t.Fatalf("Could not find current instruction %d", count)
			}

			if curinstr.Loc.Line != startinstr.Loc.Line {
				t.Fatal("Calling StepInstruction() repeatedly did not find the call instruction")
			}

			if strings.HasPrefix(curinstr.Text, "call") {
				t.Logf("call: %v", curinstr)
				if curinstr.DestLoc == nil || curinstr.DestLoc.Function == nil {
					t.Fatalf("Call instruction does not have destination: %v", curinstr)
				}
				if curinstr.DestLoc.Function.Name != "main.afunction" {
					t.Fatalf("Call instruction destination not main.afunction: %v", curinstr)
				}
				break
			}

			count++
		}
	})
}

func TestNegativeStackDepthBug(t *testing.T) {
	// After the target process has terminated should return an error but not crash
	protest.AllowRecording(t)
	withTestClient2("continuetestprog", t, func(c service.Client) {
		_, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.sayhi", Line: -1})
		assertNoError(err, t, "CreateBreakpoint()")
		ch := c.Continue()
		state := <-ch
		assertNoError(state.Err, t, "Continue()")
		_, err = c.Stacktrace(-1, -2, &normalLoadConfig)
		assertError(err, t, "Stacktrace()")
	})
}

func TestClientServer_CondBreakpoint(t *testing.T) {
	protest.AllowRecording(t)
	withTestClient2("parallel_next", t, func(c service.Client) {
		bp, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.sayhi", Line: 1})
		assertNoError(err, t, "CreateBreakpoint()")
		bp.Cond = "n == 7"
		assertNoError(c.AmendBreakpoint(bp), t, "AmendBreakpoint() 1")
		bp, err = c.GetBreakpoint(bp.ID)
		assertNoError(err, t, "GetBreakpoint() 1")
		bp.Variables = append(bp.Variables, "n")
		assertNoError(c.AmendBreakpoint(bp), t, "AmendBreakpoint() 2")
		bp, err = c.GetBreakpoint(bp.ID)
		assertNoError(err, t, "GetBreakpoint() 2")
		if bp.Cond == "" {
			t.Fatalf("No condition set on breakpoint %#v", bp)
		}
		if len(bp.Variables) != 1 {
			t.Fatalf("Wrong number of expressions to evaluate on breakpoint %#v", bp)
		}
		state := <-c.Continue()
		assertNoError(state.Err, t, "Continue()")

		nvar, err := c.EvalVariable(api.EvalScope{-1, 0}, "n", normalLoadConfig)
		assertNoError(err, t, "EvalVariable()")

		if nvar.SinglelineString() != "7" {
			t.Fatalf("Stopped on wrong goroutine %s\n", nvar.Value)
		}
	})
}

func TestSkipPrologue(t *testing.T) {
	withTestClient2("locationsprog2", t, func(c service.Client) {
		<-c.Continue()

		afunction := findLocationHelper(t, c, "main.afunction", false, 1, 0)[0]
		findLocationHelper(t, c, "*fn1", false, 1, afunction)
		findLocationHelper(t, c, "locationsprog2.go:8", false, 1, afunction)

		afunction0 := findLocationHelper(t, c, "main.afunction:0", false, 1, 0)[0]

		if afunction == afunction0 {
			t.Fatal("Skip prologue failed")
		}
	})
}

func TestSkipPrologue2(t *testing.T) {
	withTestClient2("callme", t, func(c service.Client) {
		callme := findLocationHelper(t, c, "main.callme", false, 1, 0)[0]
		callmeZ := findLocationHelper(t, c, "main.callme:0", false, 1, 0)[0]
		findLocationHelper(t, c, "callme.go:5", false, 1, callme)
		if callme == callmeZ {
			t.Fatal("Skip prologue failed")
		}

		callme2 := findLocationHelper(t, c, "main.callme2", false, 1, 0)[0]
		callme2Z := findLocationHelper(t, c, "main.callme2:0", false, 1, 0)[0]
		findLocationHelper(t, c, "callme.go:12", false, 1, callme2)
		if callme2 == callme2Z {
			t.Fatal("Skip prologue failed")
		}

		callme3 := findLocationHelper(t, c, "main.callme3", false, 1, 0)[0]
		callme3Z := findLocationHelper(t, c, "main.callme3:0", false, 1, 0)[0]
		ver, _ := goversion.Parse(runtime.Version())
		if ver.Major < 0 || ver.AfterOrEqual(goversion.GoVer18Beta) {
			findLocationHelper(t, c, "callme.go:19", false, 1, callme3)
		} else {
			// callme3 does not have local variables therefore the first line of the
			// function is immediately after the prologue
			// This is only true before 1.8 where frame pointer chaining introduced a
			// bit of prologue even for functions without local variables
			findLocationHelper(t, c, "callme.go:19", false, 1, callme3Z)
		}
		if callme3 == callme3Z {
			t.Fatal("Skip prologue failed")
		}
	})
}

func TestIssue419(t *testing.T) {
	// Calling service/rpc.(*Client).Halt could cause a crash because both Halt and Continue simultaneously
	// try to read 'runtime.g' and debug/dwarf.Data.Type is not thread safe
	withTestClient2("issue419", t, func(c service.Client) {
		go func() {
			rand.Seed(time.Now().Unix())
			d := time.Duration(rand.Intn(4) + 1)
			time.Sleep(d * time.Second)
			_, err := c.Halt()
			assertNoError(err, t, "RequestManualStop()")
		}()
		statech := c.Continue()
		state := <-statech
		assertNoError(state.Err, t, "Continue()")
	})
}

func TestTypesCommand(t *testing.T) {
	protest.AllowRecording(t)
	withTestClient2("testvariables2", t, func(c service.Client) {
		state := <-c.Continue()
		assertNoError(state.Err, t, "Continue()")
		types, err := c.ListTypes("")
		assertNoError(err, t, "ListTypes()")

		found := false
		for i := range types {
			if types[i] == "main.astruct" {
				found = true
				break
			}
		}
		if !found {
			t.Fatal("Type astruct not found in ListTypes output")
		}

		types, err = c.ListTypes("^main.astruct$")
		assertNoError(err, t, "ListTypes(\"main.astruct\")")
		if len(types) != 1 {
			t.Fatalf("ListTypes(\"^main.astruct$\") did not filter properly, expected 1 got %d: %v", len(types), types)
		}
	})
}

func TestIssue406(t *testing.T) {
	protest.AllowRecording(t)
	withTestClient2("issue406", t, func(c service.Client) {
		locs, err := c.FindLocation(api.EvalScope{-1, 0}, "issue406.go:146")
		assertNoError(err, t, "FindLocation()")
		_, err = c.CreateBreakpoint(&api.Breakpoint{Addr: locs[0].PC})
		assertNoError(err, t, "CreateBreakpoint()")
		ch := c.Continue()
		state := <-ch
		assertNoError(state.Err, t, "Continue()")
		v, err := c.EvalVariable(api.EvalScope{-1, 0}, "cfgtree", normalLoadConfig)
		assertNoError(err, t, "EvalVariable()")
		vs := v.MultilineString("")
		t.Logf("cfgtree formats to: %s\n", vs)
	})
}

func TestEvalExprName(t *testing.T) {
	withTestClient2("testvariables2", t, func(c service.Client) {
		state := <-c.Continue()
		assertNoError(state.Err, t, "Continue()")

		var1, err := c.EvalVariable(api.EvalScope{-1, 0}, "i1+1", normalLoadConfig)
		assertNoError(err, t, "EvalVariable")

		const name = "i1+1"

		t.Logf("i1+1 → %#v", var1)

		if var1.Name != name {
			t.Fatalf("Wrong variable name %q, expected %q", var1.Name, name)
		}
	})
}

func TestClientServer_Issue528(t *testing.T) {
	// FindLocation with Receiver.MethodName syntax does not work
	// on remote package names due to a bug in debug/gosym that
	// Was fixed in go 1.7 // Commit that fixes the issue in go:
	// f744717d1924340b8f5e5a385e99078693ad9097

	ver, _ := goversion.Parse(runtime.Version())
	if ver.Major > 0 && !ver.AfterOrEqual(goversion.GoVersion{1, 7, -1, 0, 0, ""}) {
		t.Log("Test skipped")
		return
	}

	withTestClient2("issue528", t, func(c service.Client) {
		findLocationHelper(t, c, "State.Close", false, 1, 0)
	})
}

func TestClientServer_FpRegisters(t *testing.T) {
	regtests := []struct{ name, value string }{
		{"ST(0)", "0x3fffe666660000000000"},
		{"ST(1)", "0x3fffd9999a0000000000"},
		{"ST(2)", "0x3fffcccccd0000000000"},
		{"ST(3)", "0x3fffc000000000000000"},
		{"ST(4)", "0x3fffb333333333333000"},
		{"ST(5)", "0x3fffa666666666666800"},
		{"ST(6)", "0x3fff9999999999999800"},
		{"ST(7)", "0x3fff8cccccccccccd000"},
		{"XMM0", "0x3ff33333333333333ff199999999999a	v2_int={ 3ff199999999999a 3ff3333333333333 }	v4_int={ 9999999a 3ff19999 33333333 3ff33333 }	v8_int={ 999a 9999 9999 3ff1 3333 3333 3333 3ff3 }	v16_int={ 9a 99 99 99 99 99 f1 3f 33 33 33 33 33 33 f3 3f }"},
		{"XMM1", "0x3ff66666666666663ff4cccccccccccd"},
		{"XMM2", "0x3fe666663fd9999a3fcccccd3fc00000"},
		{"XMM3", "0x3ff199999999999a3ff3333333333333"},
		{"XMM4", "0x3ff4cccccccccccd3ff6666666666666"},
		{"XMM5", "0x3fcccccd3fc000003fe666663fd9999a"},
		{"XMM6", "0x4004cccccccccccc4003333333333334"},
		{"XMM7", "0x40026666666666664002666666666666"},
		{"XMM8", "0x4059999a404ccccd4059999a404ccccd"},
	}
	protest.AllowRecording(t)
	withTestClient2("fputest/", t, func(c service.Client) {
		<-c.Continue()
		regs, err := c.ListRegisters(0, true)
		assertNoError(err, t, "ListRegisters()")

		t.Logf("%s", regs.String())

		for _, regtest := range regtests {
			found := false
			for _, reg := range regs {
				if reg.Name == regtest.name {
					found = true
					if !strings.HasPrefix(reg.Value, regtest.value) {
						t.Fatalf("register %s expected %q got %q", reg.Name, regtest.value, reg.Value)
					}
				}
			}
			if !found {
				t.Fatalf("register %s not found: %v", regtest.name, regs)
			}
		}
	})
}

func TestClientServer_RestartBreakpointPosition(t *testing.T) {
	protest.AllowRecording(t)
	withTestClient2("locationsprog2", t, func(c service.Client) {
		bpBefore, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.afunction", Line: -1, Tracepoint: true, Name: "this"})
		addrBefore := bpBefore.Addr
		t.Logf("%x\n", bpBefore.Addr)
		assertNoError(err, t, "CreateBreakpoint")
		stateCh := c.Continue()
		for range stateCh {
		}
		_, err = c.Halt()
		assertNoError(err, t, "Halt")
		_, err = c.Restart()
		assertNoError(err, t, "Restart")
		bps, err := c.ListBreakpoints()
		assertNoError(err, t, "ListBreakpoints")
		for _, bp := range bps {
			if bp.Name == bpBefore.Name {
				if bp.Addr != addrBefore {
					t.Fatalf("Address changed after restart: %x %x", bp.Addr, addrBefore)
				}
				t.Logf("%x %x\n", bp.Addr, addrBefore)
			}
		}
	})
}

func TestClientServer_SelectedGoroutineLoc(t *testing.T) {
	// CurrentLocation of SelectedGoroutine should reflect what's happening on
	// the thread running the goroutine, not the position the goroutine was in
	// the last time it was parked.
	protest.AllowRecording(t)
	withTestClient2("testprog", t, func(c service.Client) {
		_, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.main", Line: -11})
		assertNoError(err, t, "CreateBreakpoint")

		s := <-c.Continue()
		assertNoError(s.Err, t, "Continue")

		gloc := s.SelectedGoroutine.CurrentLoc

		if gloc.PC != s.CurrentThread.PC {
			t.Errorf("mismatched PC %#x %#x", gloc.PC, s.CurrentThread.PC)
		}

		if gloc.File != s.CurrentThread.File || gloc.Line != s.CurrentThread.Line {
			t.Errorf("mismatched file:lineno: %s:%d %s:%d", gloc.File, gloc.Line, s.CurrentThread.File, s.CurrentThread.Line)
		}
	})
}

func TestClientServer_ReverseContinue(t *testing.T) {
	protest.AllowRecording(t)
	if testBackend != "rr" {
		t.Skip("backend is not rr")
	}
	withTestClient2("continuetestprog", t, func(c service.Client) {
		_, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.main", Line: -1})
		assertNoError(err, t, "CreateBreakpoint(main.main)")
		_, err = c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.sayhi", Line: -1})
		assertNoError(err, t, "CreateBreakpoint(main.sayhi)")

		state := <-c.Continue()
		assertNoError(state.Err, t, "first continue")
		mainPC := state.CurrentThread.PC
		t.Logf("after first continue %#x", mainPC)

		state = <-c.Continue()
		assertNoError(state.Err, t, "second continue")
		sayhiPC := state.CurrentThread.PC
		t.Logf("after second continue %#x", sayhiPC)

		if mainPC == sayhiPC {
			t.Fatalf("expected different PC after second PC (%#x)", mainPC)
		}

		state = <-c.Rewind()
		assertNoError(state.Err, t, "rewind")

		if mainPC != state.CurrentThread.PC {
			t.Fatalf("Expected rewind to go back to the first breakpoint: %#x", state.CurrentThread.PC)
		}
	})
}

func TestClientServer_collectBreakpointInfoOnNext(t *testing.T) {
	protest.AllowRecording(t)
	withTestClient2("testnextprog", t, func(c service.Client) {
		_, err := c.CreateBreakpoint(&api.Breakpoint{
			Addr:       findLocationHelper(t, c, "testnextprog.go:23", false, 1, 0)[0],
			Variables:  []string{"j"},
			LoadLocals: &normalLoadConfig})
		assertNoError(err, t, "CreateBreakpoint()")
		_, err = c.CreateBreakpoint(&api.Breakpoint{
			Addr:       findLocationHelper(t, c, "testnextprog.go:24", false, 1, 0)[0],
			Variables:  []string{"j"},
			LoadLocals: &normalLoadConfig})
		assertNoError(err, t, "CreateBreakpoint()")

		stateBefore := <-c.Continue()
		assertNoError(stateBefore.Err, t, "Continue()")
		if stateBefore.CurrentThread.Line != 23 {
			t.Fatalf("wrong line number %s:%d, expected %d", stateBefore.CurrentThread.File, stateBefore.CurrentThread.Line, 23)
		}
		if bi := stateBefore.CurrentThread.BreakpointInfo; bi == nil || len(bi.Variables) != 1 {
			t.Fatalf("bad breakpoint info %v", bi)
		}

		stateAfter, err := c.Next()
		assertNoError(err, t, "Next()")
		if stateAfter.CurrentThread.Line != 24 {
			t.Fatalf("wrong line number %s:%d, expected %d", stateAfter.CurrentThread.File, stateAfter.CurrentThread.Line, 24)
		}
		if bi := stateAfter.CurrentThread.BreakpointInfo; bi == nil || len(bi.Variables) != 1 {
			t.Fatalf("bad breakpoint info %v", bi)
		}
	})
}

func TestClientServer_collectBreakpointInfoError(t *testing.T) {
	protest.AllowRecording(t)
	withTestClient2("testnextprog", t, func(c service.Client) {
		_, err := c.CreateBreakpoint(&api.Breakpoint{
			Addr:       findLocationHelper(t, c, "testnextprog.go:23", false, 1, 0)[0],
			Variables:  []string{"nonexistentvariable", "j"},
			LoadLocals: &normalLoadConfig})
		assertNoError(err, t, "CreateBreakpoint()")
		state := <-c.Continue()
		assertNoError(state.Err, t, "Continue()")
	})
}

func TestClientServerConsistentExit(t *testing.T) {
	// This test is useful because it ensures that Next and Continue operations both
	// exit with the same exit status and details when the target application terminates.
	// Other program execution API calls should also behave in the same way.
	// An error should be present in state.Err.
	withTestClient2("pr1055", t, func(c service.Client) {
		fp := testProgPath(t, "pr1055")
		_, err := c.CreateBreakpoint(&api.Breakpoint{File: fp, Line: 12})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		state := <-c.Continue()
		if state.Err != nil {
			t.Fatalf("Unexpected error: %v, state: %#v", state.Err, state)
		}
		state, err = c.Next()
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !state.Exited {
			t.Fatal("Process state is not exited")
		}
		if state.ExitStatus != 2 {
			t.Fatalf("Process exit status is not 2, got: %v", state.ExitStatus)
		}
	})
}

func TestClientServer_StepOutReturn(t *testing.T) {
	ver, _ := goversion.Parse(runtime.Version())
	if ver.Major >= 0 && !ver.AfterOrEqual(goversion.GoVersion{1, 10, -1, 0, 0, ""}) {
		t.Skip("return variables aren't marked on 1.9 or earlier")
	}
	withTestClient2("stepoutret", t, func(c service.Client) {
		c.SetReturnValuesLoadConfig(&normalLoadConfig)
		_, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.stepout", Line: -1})
		assertNoError(err, t, "CreateBreakpoint()")
		stateBefore := <-c.Continue()
		assertNoError(stateBefore.Err, t, "Continue()")
		stateAfter, err := c.StepOut()
		assertNoError(err, t, "StepOut")
		ret := stateAfter.CurrentThread.ReturnValues

		if len(ret) != 2 {
			t.Fatalf("wrong number of return values %v", ret)
		}

		if ret[0].Name != "str" {
			t.Fatalf("(str) bad return value name %s", ret[0].Name)
		}
		if ret[0].Kind != reflect.String {
			t.Fatalf("(str) bad return value kind %v", ret[0].Kind)
		}
		if ret[0].Value != "return 47" {
			t.Fatalf("(str) bad return value %q", ret[0].Value)
		}

		if ret[1].Name != "num" {
			t.Fatalf("(num) bad return value name %s", ret[1].Name)
		}
		if ret[1].Kind != reflect.Int {
			t.Fatalf("(num) bad return value kind %v", ret[1].Kind)
		}
		if ret[1].Value != "48" {
			t.Fatalf("(num) bad return value %s", ret[1].Value)
		}
	})
}

func TestAcceptMulticlient(t *testing.T) {
	if testBackend == "rr" {
		t.Skip("recording not allowed for TestAcceptMulticlient")
	}
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("couldn't start listener: %s\n", err)
	}
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		defer listener.Close()
		disconnectChan := make(chan struct{})
		server := rpccommon.NewServer(&service.Config{
			Listener:       listener,
			ProcessArgs:    []string{protest.BuildFixture("testvariables2", 0).Path},
			Backend:        testBackend,
			AcceptMulti:    true,
			DisconnectChan: disconnectChan,
		})
		if err := server.Run(); err != nil {
			t.Fatal(err)
		}
		<-disconnectChan
		server.Stop()
	}()
	client1 := rpc2.NewClient(listener.Addr().String())
	client1.Disconnect(false)

	client2 := rpc2.NewClient(listener.Addr().String())
	state := <-client2.Continue()
	if state.CurrentThread.Function.Name != "main.main" {
		t.Fatalf("bad state after continue: %v\n", state)
	}
	client2.Detach(true)
	<-serverDone
}
