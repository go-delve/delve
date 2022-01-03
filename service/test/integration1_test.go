package service_test

import (
	"fmt"
	"math/rand"
	"net"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	protest "github.com/go-delve/delve/pkg/proc/test"
	"github.com/go-delve/delve/service/debugger"

	"github.com/go-delve/delve/pkg/goversion"
	"github.com/go-delve/delve/service"
	"github.com/go-delve/delve/service/api"
	"github.com/go-delve/delve/service/rpc1"
	"github.com/go-delve/delve/service/rpccommon"
)

func withTestClient1(name string, t *testing.T, fn func(c *rpc1.RPCClient)) {
	withTestClient1Extended(name, t, func(c *rpc1.RPCClient, fixture protest.Fixture) {
		fn(c)
	})
}

func withTestClient1Extended(name string, t *testing.T, fn func(c *rpc1.RPCClient, fixture protest.Fixture)) {
	if testBackend == "rr" {
		protest.MustHaveRecordingAllowed(t)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("couldn't start listener: %s\n", err)
	}
	defer listener.Close()
	var buildFlags protest.BuildFlags
	if buildMode == "pie" {
		buildFlags = protest.BuildModePIE
	}
	fixture := protest.BuildFixture(name, buildFlags)
	server := rpccommon.NewServer(&service.Config{
		Listener:    listener,
		ProcessArgs: []string{fixture.Path},
		Debugger: debugger.Config{
			Backend: testBackend,
		},
	})
	if err := server.Run(); err != nil {
		t.Fatal(err)
	}
	client := rpc1.NewClient(listener.Addr().String())
	defer func() {
		client.Detach(true)
	}()

	fn(client, fixture)
}

func Test1RunWithInvalidPath(t *testing.T) {
	if testBackend == "rr" {
		// This test won't work because rr returns an error, after recording, when
		// the recording failed but also when the recording succeeded but the
		// inferior returned an error. Therefore we have to ignore errors from rr.
		return
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("couldn't start listener: %s\n", err)
	}
	defer listener.Close()
	server := rpccommon.NewServer(&service.Config{
		Listener:    listener,
		ProcessArgs: []string{"invalid_path"},
		Debugger: debugger.Config{
			Backend: testBackend,
		},
	})
	if err := server.Run(); err == nil {
		t.Fatal("Expected Run to return error for invalid program path")
	}
}

func Test1Restart_afterExit(t *testing.T) {
	withTestClient1("continuetestprog", t, func(c *rpc1.RPCClient) {
		origPid := c.ProcessPid()
		state := <-c.Continue()
		if !state.Exited {
			t.Fatal("expected initial process to have exited")
		}
		if err := c.Restart(); err != nil {
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

func Test1Restart_breakpointPreservation(t *testing.T) {
	withTestClient1("continuetestprog", t, func(c *rpc1.RPCClient) {
		_, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.main", Line: 1, Name: "firstbreakpoint", Tracepoint: true})
		assertNoError(err, t, "CreateBreakpoint()")
		stateCh := c.Continue()

		state := <-stateCh
		if state.CurrentThread.Breakpoint.Name != "firstbreakpoint" || !state.CurrentThread.Breakpoint.Tracepoint {
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
		state = <-stateCh
		if !state.Exited {
			t.Fatal("Did not exit after first tracepoint (after restart)")
		}
	})
}

func Test1Restart_duringStop(t *testing.T) {
	withTestClient1("continuetestprog", t, func(c *rpc1.RPCClient) {
		origPid := c.ProcessPid()
		_, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.main", Line: 1})
		if err != nil {
			t.Fatal(err)
		}
		state := <-c.Continue()
		if state.CurrentThread.Breakpoint == nil {
			t.Fatal("did not hit breakpoint")
		}
		if err := c.Restart(); err != nil {
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

func Test1ClientServer_exit(t *testing.T) {
	withTestClient1("continuetestprog", t, func(c *rpc1.RPCClient) {
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

func Test1ClientServer_step(t *testing.T) {
	withTestClient1("testprog", t, func(c *rpc1.RPCClient) {
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
			t.Fatalf("Expected %#v to be greater than %#v", after, before)
		}
	})
}

func testnext(testcases []nextTest, initialLocation string, t *testing.T) {
	withTestClient1("testnextprog", t, func(c *rpc1.RPCClient) {
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

func Test1NextGeneral(t *testing.T) {
	var testcases []nextTest

	ver, _ := goversion.Parse(runtime.Version())

	if ver.Major < 0 || ver.AfterOrEqual(goversion.GoVersion{Major: 1, Minor: 7, Rev: -1}) {
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

	testnext(testcases, "main.testnext", t)
}

func Test1NextFunctionReturn(t *testing.T) {
	testcases := []nextTest{
		{13, 14},
		{14, 15},
		{15, 35},
	}
	testnext(testcases, "main.helloworld", t)
}

func Test1ClientServer_breakpointInMainThread(t *testing.T) {
	withTestClient1("testprog", t, func(c *rpc1.RPCClient) {
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

func Test1ClientServer_breakpointInSeparateGoroutine(t *testing.T) {
	withTestClient1("testthreads", t, func(c *rpc1.RPCClient) {
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

func Test1ClientServer_breakAtNonexistentPoint(t *testing.T) {
	withTestClient1("testprog", t, func(c *rpc1.RPCClient) {
		_, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "nowhere", Line: 1})
		if err == nil {
			t.Fatal("Should not be able to break at non existent function")
		}
	})
}

func Test1ClientServer_clearBreakpoint(t *testing.T) {
	withTestClient1("testprog", t, func(c *rpc1.RPCClient) {
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

func Test1ClientServer_switchThread(t *testing.T) {
	withTestClient1("testnextprog", t, func(c *rpc1.RPCClient) {
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

func Test1ClientServer_infoLocals(t *testing.T) {
	withTestClient1("testnextprog", t, func(c *rpc1.RPCClient) {
		fp := testProgPath(t, "testnextprog")
		_, err := c.CreateBreakpoint(&api.Breakpoint{File: fp, Line: 24})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		state := <-c.Continue()
		if state.Err != nil {
			t.Fatalf("Unexpected error: %v, state: %#v", state.Err, state)
		}
		locals, err := c.ListLocalVariables(api.EvalScope{GoroutineID: -1})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if len(locals) != 3 {
			t.Fatalf("Expected 3 locals, got %d %#v", len(locals), locals)
		}
	})
}

func Test1ClientServer_infoArgs(t *testing.T) {
	withTestClient1("testnextprog", t, func(c *rpc1.RPCClient) {
		fp := testProgPath(t, "testnextprog")
		_, err := c.CreateBreakpoint(&api.Breakpoint{File: fp, Line: 47})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		state := <-c.Continue()
		if state.Err != nil {
			t.Fatalf("Unexpected error: %v, state: %#v", state.Err, state)
		}
		regs, err := c.ListRegisters()
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if regs == "" {
			t.Fatal("Expected string showing registers values, got empty string")
		}
		locals, err := c.ListFunctionArgs(api.EvalScope{GoroutineID: -1})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if len(locals) != 2 {
			t.Fatalf("Expected 2 function args, got %d %#v", len(locals), locals)
		}
	})
}

func Test1ClientServer_traceContinue(t *testing.T) {
	withTestClient1("integrationprog", t, func(c *rpc1.RPCClient) {
		fp := testProgPath(t, "integrationprog")
		_, err := c.CreateBreakpoint(&api.Breakpoint{File: fp, Line: 15, Tracepoint: true, Goroutine: true, Stacktrace: 5, Variables: []string{"i"}})
		if err != nil {
			t.Fatalf("Unexpected error: %v\n", err)
		}
		count := 0
		contChan := c.Continue()
		for state := range contChan {
			if state.CurrentThread != nil && state.CurrentThread.Breakpoint != nil {
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

func Test1ClientServer_traceContinue2(t *testing.T) {
	withTestClient1("integrationprog", t, func(c *rpc1.RPCClient) {
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

func Test1ClientServer_FindLocations(t *testing.T) {
	withTestClient1("locationsprog", t, func(c *rpc1.RPCClient) {
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
	})

	withTestClient1("testnextdefer", t, func(c *rpc1.RPCClient) {
		firstMainLine := findLocationHelper(t, c, "testnextdefer.go:5", false, 1, 0)[0]
		findLocationHelper(t, c, "main.main", false, 1, firstMainLine)
	})

	withTestClient1("stacktraceprog", t, func(c *rpc1.RPCClient) {
		stacktracemeAddr := findLocationHelper(t, c, "stacktraceprog.go:4", false, 1, 0)[0]
		findLocationHelper(t, c, "main.stacktraceme", false, 1, stacktracemeAddr)
	})

	withTestClient1Extended("locationsUpperCase", t, func(c *rpc1.RPCClient, fixture protest.Fixture) {
		// Upper case
		findLocationHelper(t, c, "locationsUpperCase.go:6", false, 1, 0)

		// Fully qualified path
		findLocationHelper(t, c, fixture.Source+":6", false, 1, 0)
		bp, err := c.CreateBreakpoint(&api.Breakpoint{File: fixture.Source, Line: 6})
		if err != nil {
			t.Fatalf("Could not set breakpoint in %s: %v\n", fixture.Source, err)
		}
		c.ClearBreakpoint(bp.ID)

		//  Allow `/` or `\` on Windows
		if runtime.GOOS == "windows" {
			findLocationHelper(t, c, filepath.FromSlash(fixture.Source)+":6", false, 1, 0)
			bp, err = c.CreateBreakpoint(&api.Breakpoint{File: filepath.FromSlash(fixture.Source), Line: 6})
			if err != nil {
				t.Fatalf("Could not set breakpoint in %s: %v\n", filepath.FromSlash(fixture.Source), err)
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
		findLocationHelper(t, c, strings.ToLower(fixture.Source)+":6", shouldWrongCaseBeError, numExpectedMatches, 0)
		bp, err = c.CreateBreakpoint(&api.Breakpoint{File: strings.ToLower(fixture.Source), Line: 6})
		if (err == nil) == shouldWrongCaseBeError {
			t.Fatalf("Could not set breakpoint in %s: %v\n", strings.ToLower(fixture.Source), err)
		}
		c.ClearBreakpoint(bp.ID)
	})
}

func Test1ClientServer_FindLocationsAddr(t *testing.T) {
	withTestClient1("locationsprog2", t, func(c *rpc1.RPCClient) {
		<-c.Continue()

		afunction := findLocationHelper(t, c, "main.afunction", false, 1, 0)[0]
		anonfunc := findLocationHelper(t, c, "main.main.func1", false, 1, 0)[0]

		findLocationHelper(t, c, "*fn1", false, 1, afunction)
		findLocationHelper(t, c, "*fn3", false, 1, anonfunc)
	})
}

func Test1ClientServer_EvalVariable(t *testing.T) {
	withTestClient1("testvariables", t, func(c *rpc1.RPCClient) {
		state := <-c.Continue()

		if state.Err != nil {
			t.Fatalf("Continue(): %v\n", state.Err)
		}

		var1, err := c.EvalVariable(api.EvalScope{GoroutineID: -1}, "a1")
		assertNoError(err, t, "EvalVariable")

		t.Logf("var1: %s", var1.SinglelineString())

		if var1.Value != "foofoofoofoofoofoo" {
			t.Fatalf("Wrong variable value: %s", var1.Value)
		}
	})
}

func Test1ClientServer_SetVariable(t *testing.T) {
	withTestClient1("testvariables", t, func(c *rpc1.RPCClient) {
		state := <-c.Continue()

		if state.Err != nil {
			t.Fatalf("Continue(): %v\n", state.Err)
		}

		assertNoError(c.SetVariable(api.EvalScope{GoroutineID: -1}, "a2", "8"), t, "SetVariable()")

		a2, err := c.EvalVariable(api.EvalScope{GoroutineID: -1}, "a2")
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

func Test1ClientServer_FullStacktrace(t *testing.T) {
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		t.Skip("cgo doesn't work on darwin/arm64")
	}

	lenient := false
	if runtime.GOOS == "windows" {
		lenient = true
	}

	withTestClient1("goroutinestackprog", t, func(c *rpc1.RPCClient) {
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
			frames, err := c.Stacktrace(g.ID, 40, true)
			assertNoError(err, t, fmt.Sprintf("Stacktrace(%d)", g.ID))
			t.Logf("goroutine %d", g.ID)
			for i, frame := range frames {
				t.Logf("\tframe %d off=%#x bpoff=%#x pc=%#x %s:%d %s", i, frame.FrameOffset, frame.FramePointerOffset, frame.PC, frame.File, frame.Line, frame.Function.Name())
				if frame.Function == nil {
					continue
				}
				if frame.Function.Name() != "main.agoroutine" {
					continue
				}
				for _, arg := range frame.Arguments {
					if arg.Name != "i" {
						continue
					}
					t.Logf("\tvariable i is %+v\n", arg)
					argn, err := strconv.Atoi(arg.Value)
					if err == nil {
						found[argn] = true
					}
				}
			}
		}

		for i := range found {
			if !found[i] {
				if lenient {
					lenient = false
				} else {
					t.Fatalf("Goroutine %d not found", i)
				}
			}
		}

		t.Logf("continue")

		state = <-c.Continue()
		if state.Err != nil {
			t.Fatalf("Continue(): %v\n", state.Err)
		}

		frames, err := c.Stacktrace(-1, 10, true)
		assertNoError(err, t, "Stacktrace")

		cur := 3
		for i, frame := range frames {
			t.Logf("\tframe %d off=%#x bpoff=%#x pc=%#x %s:%d %s", i, frame.FrameOffset, frame.FramePointerOffset, frame.PC, frame.File, frame.Line, frame.Function.Name())
			if i == 0 {
				continue
			}
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

func Test1Issue355(t *testing.T) {
	// After the target process has terminated should return an error but not crash
	withTestClient1("continuetestprog", t, func(c *rpc1.RPCClient) {
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

		s, err := c.Next()
		assertErrorOrExited(s, err, t, "Next()")
		s, err = c.Step()
		assertErrorOrExited(s, err, t, "Step()")
		s, err = c.StepInstruction()
		assertErrorOrExited(s, err, t, "StepInstruction()")
		s, err = c.SwitchThread(tid)
		assertErrorOrExited(s, err, t, "SwitchThread()")
		s, err = c.SwitchGoroutine(gid)
		assertErrorOrExited(s, err, t, "SwitchGoroutine()")
		s, err = c.Halt()
		assertErrorOrExited(s, err, t, "Halt()")
		_, err = c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.main", Line: -1})
		assertError(err, t, "CreateBreakpoint()")
		_, err = c.ClearBreakpoint(bp.ID)
		assertError(err, t, "ClearBreakpoint()")
		_, err = c.ListThreads()
		assertError(err, t, "ListThreads()")
		_, err = c.GetThread(tid)
		assertError(err, t, "GetThread()")
		assertError(c.SetVariable(api.EvalScope{GoroutineID: gid}, "a", "10"), t, "SetVariable()")
		_, err = c.ListLocalVariables(api.EvalScope{GoroutineID: gid})
		assertError(err, t, "ListLocalVariables()")
		_, err = c.ListFunctionArgs(api.EvalScope{GoroutineID: gid})
		assertError(err, t, "ListFunctionArgs()")
		_, err = c.ListRegisters()
		assertError(err, t, "ListRegisters()")
		_, err = c.ListGoroutines()
		assertError(err, t, "ListGoroutines()")
		_, err = c.Stacktrace(gid, 10, false)
		assertError(err, t, "Stacktrace()")
		_, err = c.FindLocation(api.EvalScope{GoroutineID: gid}, "+1")
		assertError(err, t, "FindLocation()")
		_, err = c.DisassemblePC(api.EvalScope{GoroutineID: -1}, 0x40100, api.IntelFlavour)
		assertError(err, t, "DisassemblePC()")
	})
}

func Test1Disasm(t *testing.T) {
	// Tests that disassembling by PC, range, and current PC all yeld similar results
	// Tests that disassembly by current PC will return a disassembly containing the instruction at PC
	// Tests that stepping on a calculated CALL instruction will yield a disassembly that contains the
	// effective destination of the CALL instruction
	withTestClient1("locationsprog2", t, func(c *rpc1.RPCClient) {
		ch := c.Continue()
		state := <-ch
		assertNoError(state.Err, t, "Continue()")

		locs, err := c.FindLocation(api.EvalScope{GoroutineID: -1}, "main.main")
		assertNoError(err, t, "FindLocation()")
		if len(locs) != 1 {
			t.Fatalf("wrong number of locations for main.main: %d", len(locs))
		}
		d1, err := c.DisassemblePC(api.EvalScope{GoroutineID: -1}, locs[0].PC, api.IntelFlavour)
		assertNoError(err, t, "DisassemblePC()")
		if len(d1) < 2 {
			t.Fatalf("wrong size of disassembly: %d", len(d1))
		}

		pcstart := d1[0].Loc.PC
		pcend := d1[len(d1)-1].Loc.PC + uint64(len(d1[len(d1)-1].Bytes))
		d2, err := c.DisassembleRange(api.EvalScope{GoroutineID: -1}, pcstart, pcend, api.IntelFlavour)
		assertNoError(err, t, "DisassembleRange()")

		if len(d1) != len(d2) {
			t.Logf("d1: %v", d1)
			t.Logf("d2: %v", d2)
			t.Fatal("mismatched length between disassemble pc and disassemble range")
		}

		d3, err := c.DisassemblePC(api.EvalScope{GoroutineID: -1}, state.CurrentThread.PC, api.IntelFlavour)
		assertNoError(err, t, "DisassemblePC() - second call")

		if len(d1) != len(d3) {
			t.Logf("d1: %v", d1)
			t.Logf("d3: %v", d3)
			t.Fatal("mismatched length between the two calls of disassemble pc")
		}

		// look for static call to afunction() on line 29
		found := false
		for i := range d3 {
			if d3[i].Loc.Line == 29 && (strings.HasPrefix(d3[i].Text, "call") || strings.HasPrefix(d3[i].Text, "CALL")) && d3[i].DestLoc != nil && d3[i].DestLoc.Function != nil && d3[i].DestLoc.Function.Name() == "main.afunction" {
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

		if runtime.GOARCH == "386" && buildMode == "pie" {
			// Skip the rest of the test because on intel 386 with PIE build mode
			// the compiler will insert calls to __x86.get_pc_thunk which do not have DIEs and we can't resolve.
			return
		}

		startinstr := getCurinstr(d3)
		count := 0
		for {
			if count > 20 {
				t.Fatal("too many step instructions executed without finding a call instruction")
			}
			state, err := c.StepInstruction()
			assertNoError(err, t, fmt.Sprintf("StepInstruction() %d", count))

			d3, err = c.DisassemblePC(api.EvalScope{GoroutineID: -1}, state.CurrentThread.PC, api.IntelFlavour)
			assertNoError(err, t, fmt.Sprintf("StepInstruction() %d", count))

			curinstr := getCurinstr(d3)

			if curinstr == nil {
				t.Fatalf("Could not find current instruction %d", count)
			}

			if curinstr.Loc.Line != startinstr.Loc.Line {
				t.Fatal("Calling StepInstruction() repeatedly did not find the call instruction")
			}

			if strings.HasPrefix(curinstr.Text, "call") || strings.HasPrefix(curinstr.Text, "CALL") {
				t.Logf("call: %v", curinstr)
				if curinstr.DestLoc == nil || curinstr.DestLoc.Function == nil {
					t.Fatalf("Call instruction does not have destination: %v", curinstr)
				}
				if curinstr.DestLoc.Function.Name() != "main.afunction" {
					t.Fatalf("Call instruction destination not main.afunction: %v", curinstr)
				}
				break
			}

			count++
		}
	})
}

func Test1NegativeStackDepthBug(t *testing.T) {
	// After the target process has terminated should return an error but not crash
	withTestClient1("continuetestprog", t, func(c *rpc1.RPCClient) {
		_, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.sayhi", Line: -1})
		assertNoError(err, t, "CreateBreakpoint()")
		ch := c.Continue()
		state := <-ch
		assertNoError(state.Err, t, "Continue()")
		_, err = c.Stacktrace(-1, -2, true)
		assertError(err, t, "Stacktrace()")
	})
}

func Test1ClientServer_CondBreakpoint(t *testing.T) {
	if runtime.GOOS == "freebsd" {
		t.Skip("test is not valid on FreeBSD")
	}
	withTestClient1("parallel_next", t, func(c *rpc1.RPCClient) {
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

		nvar, err := c.EvalVariable(api.EvalScope{GoroutineID: -1}, "n")
		assertNoError(err, t, "EvalVariable()")

		if nvar.SinglelineString() != "7" {
			t.Fatalf("Stopped on wrong goroutine %s\n", nvar.Value)
		}
	})
}

func Test1Issue419(t *testing.T) {
	// Calling service/rpc.(*Client).Halt could cause a crash because both Halt and Continue simultaneously
	// try to read 'runtime.g' and debug/dwarf.Data.Type is not thread safe
	withTestClient1("issue419", t, func(c *rpc1.RPCClient) {
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

func Test1TypesCommand(t *testing.T) {
	withTestClient1("testvariables2", t, func(c *rpc1.RPCClient) {
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

func Test1Issue406(t *testing.T) {
	withTestClient1("issue406", t, func(c *rpc1.RPCClient) {
		locs, err := c.FindLocation(api.EvalScope{GoroutineID: -1}, "issue406.go:146")
		assertNoError(err, t, "FindLocation()")
		_, err = c.CreateBreakpoint(&api.Breakpoint{Addr: locs[0].PC})
		assertNoError(err, t, "CreateBreakpoint()")
		ch := c.Continue()
		state := <-ch
		assertNoError(state.Err, t, "Continue()")
		v, err := c.EvalVariable(api.EvalScope{GoroutineID: -1}, "cfgtree")
		assertNoError(err, t, "EvalVariable()")
		vs := v.MultilineString("", "")
		t.Logf("cfgtree formats to: %s\n", vs)
	})
}
