package service_test

import (
	"flag"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	protest "github.com/go-delve/delve/pkg/proc/test"
	"github.com/go-delve/delve/service/debugger"

	"github.com/go-delve/delve/pkg/goversion"
	"github.com/go-delve/delve/pkg/logflags"
	"github.com/go-delve/delve/service"
	"github.com/go-delve/delve/service/api"
	"github.com/go-delve/delve/service/rpc2"
	"github.com/go-delve/delve/service/rpccommon"
)

var normalLoadConfig = api.LoadConfig{
	FollowPointers:     true,
	MaxVariableRecurse: 1,
	MaxStringLen:       64,
	MaxArrayValues:     64,
	MaxStructFields:    -1,
}

var testBackend, buildMode string

func TestMain(m *testing.M) {
	flag.StringVar(&testBackend, "backend", "", "selects backend")
	flag.StringVar(&buildMode, "test-buildmode", "", "selects build mode")
	var logOutput string
	flag.StringVar(&logOutput, "log-output", "", "configures log output")
	flag.Parse()
	protest.DefaultTestBackend(&testBackend)
	if buildMode != "" && buildMode != "pie" {
		fmt.Fprintf(os.Stderr, "unknown build mode %q", buildMode)
		os.Exit(1)
	}
	logflags.Setup(logOutput != "", logOutput, "")
	os.Exit(protest.RunTestsWithFixtures(m))
}

func withTestClient2(name string, t *testing.T, fn func(c service.Client)) {
	withTestClient2Extended(name, t, 0, [3]string{}, func(c service.Client, fixture protest.Fixture) {
		fn(c)
	})
}

func startServer(name string, buildFlags protest.BuildFlags, t *testing.T, redirects [3]string) (clientConn net.Conn, fixture protest.Fixture) {
	if testBackend == "rr" {
		protest.MustHaveRecordingAllowed(t)
	}
	listener, clientConn := service.ListenerPipe()
	defer listener.Close()
	if buildMode == "pie" {
		buildFlags |= protest.BuildModePIE
	}
	fixture = protest.BuildFixture(name, buildFlags)
	for i := range redirects {
		if redirects[i] != "" {
			redirects[i] = filepath.Join(fixture.BuildDir, redirects[i])
		}
	}
	server := rpccommon.NewServer(&service.Config{
		Listener:    listener,
		ProcessArgs: []string{fixture.Path},
		Debugger: debugger.Config{
			Backend:        testBackend,
			CheckGoVersion: true,
			Packages:       []string{fixture.Source},
			BuildFlags:     "", // build flags can be an empty string here because the only test that uses it, does not set special flags.
			ExecuteKind:    debugger.ExecutingGeneratedFile,
			Redirects:      redirects,
		},
	})
	if err := server.Run(); err != nil {
		t.Fatal(err)
	}
	return clientConn, fixture
}

func withTestClient2Extended(name string, t *testing.T, buildFlags protest.BuildFlags, redirects [3]string, fn func(c service.Client, fixture protest.Fixture)) {
	clientConn, fixture := startServer(name, buildFlags, t, redirects)
	client := rpc2.NewClientFromConn(clientConn)
	defer func() {
		client.Detach(true)
	}()

	fn(client, fixture)
}

func TestRunWithInvalidPath(t *testing.T) {
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
		APIVersion:  2,
		Debugger: debugger.Config{
			Backend:     testBackend,
			ExecuteKind: debugger.ExecutingGeneratedFile,
		},
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
		if _, err := c.Restart(false); err != nil {
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
		state = <-stateCh
		if !state.Exited {
			t.Fatal("Did not exit after first tracepoint")
		}

		t.Log("Restart")
		c.Restart(false)
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
		if _, err := c.Restart(false); err != nil {
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

// This source is a slightly modified version of
// _fixtures/testenv.go. The only difference is that
// the name of the environment variable we are trying to
// read is named differently, so we can assert the code
// was actually changed in the test.
const modifiedSource = `package main

import (
	"fmt"
	"os"
	"runtime"
)

func main() {
	x := os.Getenv("SOMEMODIFIEDVAR")
	runtime.Breakpoint()
	fmt.Printf("SOMEMODIFIEDVAR=%s\n", x)
}
`

func TestRestart_rebuild(t *testing.T) {
	// In the original fixture file the env var tested for is SOMEVAR.
	os.Setenv("SOMEVAR", "bah")

	withTestClient2Extended("testenv", t, 0, [3]string{}, func(c service.Client, f protest.Fixture) {
		<-c.Continue()

		var1, err := c.EvalVariable(api.EvalScope{GoroutineID: -1}, "x", normalLoadConfig)
		assertNoError(err, t, "EvalVariable")

		if var1.Value != "bah" {
			t.Fatalf("expected 'bah' got %q", var1.Value)
		}

		fi, err := os.Stat(f.Source)
		assertNoError(err, t, "Stat fixture.Source")

		originalSource, err := ioutil.ReadFile(f.Source)
		assertNoError(err, t, "Reading original source")

		// Ensure we write the original source code back after the test exits.
		defer ioutil.WriteFile(f.Source, originalSource, fi.Mode())

		// Write modified source code to the fixture file.
		err = ioutil.WriteFile(f.Source, []byte(modifiedSource), fi.Mode())
		assertNoError(err, t, "Writing modified source")

		// First set our new env var and ensure later that the
		// modified source code picks it up.
		os.Setenv("SOMEMODIFIEDVAR", "foobar")

		// Restart the program, rebuilding from source.
		_, err = c.Restart(true)
		assertNoError(err, t, "Restart(true)")

		<-c.Continue()

		var1, err = c.EvalVariable(api.EvalScope{GoroutineID: -1}, "x", normalLoadConfig)
		assertNoError(err, t, "EvalVariable")

		if var1.Value != "foobar" {
			t.Fatalf("expected 'foobar' got %q", var1.Value)
		}
	})
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
			t.Fatalf("Expected %#v to be greater than %#v", after, before)
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
		if state.Err != nil {
			t.Fatalf("Unexpected error: %v, state: %#v", state.Err, state)
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

func TestClientServer_toggleBreakpoint(t *testing.T) {
	withTestClient2("testtoggle", t, func(c service.Client) {
		toggle := func(bp *api.Breakpoint) {
			dbp, err := c.ToggleBreakpoint(bp.ID)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if dbp.ID != bp.ID {
				t.Fatalf("The IDs don't match")
			}
		}

		// This one is toggled twice
		bp1, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.lineOne", Tracepoint: true})
		if err != nil {
			t.Fatalf("Unexpected error: %v\n", err)
		}

		toggle(bp1)
		toggle(bp1)

		// This one is toggled once
		bp2, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.lineTwo", Tracepoint: true})
		if err != nil {
			t.Fatalf("Unexpected error: %v\n", err)
		}

		toggle(bp2)

		// This one is never toggled
		bp3, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.lineThree", Tracepoint: true})
		if err != nil {
			t.Fatalf("Unexpected error: %v\n", err)
		}

		if e, a := 3, countBreakpoints(t, c); e != a {
			t.Fatalf("Expected breakpoint count %d, got %d", e, a)
		}

		enableCount := 0
		disabledCount := 0

		contChan := c.Continue()
		for state := range contChan {
			if state.CurrentThread != nil && state.CurrentThread.Breakpoint != nil {
				switch state.CurrentThread.Breakpoint.ID {
				case bp1.ID, bp3.ID:
					enableCount++
				case bp2.ID:
					disabledCount++
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

		if enableCount != 2 {
			t.Fatalf("Wrong number of enabled hits: %d\n", enableCount)
		}

		if disabledCount != 0 {
			t.Fatalf("A disabled breakpoint was hit: %d\n", disabledCount)
		}
	})
}

func TestClientServer_toggleAmendedBreakpoint(t *testing.T) {
	withTestClient2("testtoggle", t, func(c service.Client) {
		toggle := func(bp *api.Breakpoint) {
			dbp, err := c.ToggleBreakpoint(bp.ID)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if dbp.ID != bp.ID {
				t.Fatalf("The IDs don't match")
			}
		}

		// This one is toggled twice
		bp, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.lineOne", Tracepoint: true})
		if err != nil {
			t.Fatalf("Unexpected error: %v\n", err)
		}
		bp.Cond = "n == 7"
		assertNoError(c.AmendBreakpoint(bp), t, "AmendBreakpoint() 1")

		// Toggle off.
		toggle(bp)
		// Toggle on.
		toggle(bp)

		amended, err := c.GetBreakpoint(bp.ID)
		if err != nil {
			t.Fatal(err)
		}
		if amended.Cond == "" {
			t.Fatal("breakpoint amendedments not preserved after toggle")
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
		_, err := c.CreateBreakpoint(&api.Breakpoint{File: fp, Line: 24})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		state := <-c.Continue()
		if state.Err != nil {
			t.Fatalf("Unexpected error: %v, state: %#v", state.Err, state)
		}
		locals, err := c.ListLocalVariables(api.EvalScope{GoroutineID: -1}, normalLoadConfig)
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
		regs, err := c.ListThreadRegisters(0, false)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if len(regs) == 0 {
			t.Fatal("Expected string showing registers values, got empty string")
		}

		regs, err = c.ListScopeRegisters(api.EvalScope{GoroutineID: -1, Frame: 0}, false)
		assertNoError(err, t, "ListScopeRegisters(-1, 0)")
		if len(regs) == 0 {
			t.Fatal("Expected string showing registers values, got empty string")
		}
		t.Logf("GoroutineID: -1, Frame: 0\n%s", regs.String())

		regs, err = c.ListScopeRegisters(api.EvalScope{GoroutineID: -1, Frame: 1}, false)
		assertNoError(err, t, "ListScopeRegisters(-1, 1)")
		if len(regs) == 0 {
			t.Fatal("Expected string showing registers values, got empty string")
		}
		t.Logf("GoroutineID: -1, Frame: 1\n%s", regs.String())

		locals, err := c.ListFunctionArgs(api.EvalScope{GoroutineID: -1}, normalLoadConfig)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if len(locals) != 2 {
			t.Fatalf("Expected 2 function args, got %d %#v", len(locals), locals)
		}
	})
}

func TestClientServer_traceContinue(t *testing.T) {
	protest.AllowRecording(t)
	withTestClient2("integrationprog", t, func(c service.Client) {
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

		locsNoSubst, _ := c.FindLocation(api.EvalScope{GoroutineID: -1}, "_fixtures/locationsprog.go:35", false, nil)
		sep := "/"
		if strings.Contains(locsNoSubst[0].File, "\\") {
			sep = "\\"
		}
		substRules := [][2]string{[2]string{strings.Replace(locsNoSubst[0].File, "locationsprog.go", "", 1), strings.Replace(locsNoSubst[0].File, "_fixtures"+sep+"locationsprog.go", "nonexistent", 1)}}
		t.Logf("substitute rules: %q -> %q", substRules[0][0], substRules[0][1])
		locsSubst, err := c.FindLocation(api.EvalScope{GoroutineID: -1}, "nonexistent/locationsprog.go:35", false, substRules)
		if err != nil {
			t.Fatalf("FindLocation(locationsprog.go:35) with substitute rules: %v", err)
		}
		t.Logf("FindLocation(\"/nonexistent/path/locationsprog.go:35\") -> %#v", locsSubst)
		if locsNoSubst[0].PC != locsSubst[0].PC {
			t.Fatalf("FindLocation with substitute rules mismatch %#v %#v", locsNoSubst[0], locsSubst[0])
		}

	})

	withTestClient2("testnextdefer", t, func(c service.Client) {
		firstMainLine := findLocationHelper(t, c, "testnextdefer.go:5", false, 1, 0)[0]
		findLocationHelper(t, c, "main.main", false, 1, firstMainLine)
	})

	withTestClient2("stacktraceprog", t, func(c service.Client) {
		stacktracemeAddr := findLocationHelper(t, c, "stacktraceprog.go:4", false, 1, 0)[0]
		findLocationHelper(t, c, "main.stacktraceme", false, 1, stacktracemeAddr)
	})

	withTestClient2Extended("locationsUpperCase", t, 0, [3]string{}, func(c service.Client, fixture protest.Fixture) {
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

	if goversion.VersionAfterOrEqual(runtime.Version(), 1, 13) {
		withTestClient2("pkgrenames", t, func(c service.Client) {
			someFuncLoc := findLocationHelper(t, c, "github.com/go-delve/delve/_fixtures/internal/dir%2eio.SomeFunction:0", false, 1, 0)[0]
			findLocationHelper(t, c, "dirio.SomeFunction:0", false, 1, someFuncLoc)
		})
	}
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

		var1, err := c.EvalVariable(api.EvalScope{GoroutineID: -1}, "a1", normalLoadConfig)
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

		assertNoError(c.SetVariable(api.EvalScope{GoroutineID: -1}, "a2", "8"), t, "SetVariable()")

		a2, err := c.EvalVariable(api.EvalScope{GoroutineID: -1}, "a2", normalLoadConfig)
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
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		t.Skip("cgo doesn't work on darwin/arm64")
	}
	withTestClient2("goroutinestackprog", t, func(c service.Client) {
		_, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.stacktraceme", Line: -1})
		assertNoError(err, t, "CreateBreakpoint()")
		state := <-c.Continue()
		if state.Err != nil {
			t.Fatalf("Continue(): %v\n", state.Err)
		}

		gs, _, err := c.ListGoroutines(0, 0)
		assertNoError(err, t, "GoroutinesInfo()")
		found := make([]bool, 10)
		for _, g := range gs {
			frames, err := c.Stacktrace(g.ID, 40, 0, &normalLoadConfig)
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
				t.Fatalf("Goroutine %d not found", i)
			}
		}

		t.Logf("continue")

		state = <-c.Continue()
		if state.Err != nil {
			t.Fatalf("Continue(): %v\n", state.Err)
		}

		frames, err := c.Stacktrace(-1, 10, 0, &normalLoadConfig)
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

func assertErrorOrExited(s *api.DebuggerState, err error, t *testing.T, reason string) {
	if err != nil {
		return
	}
	if s != nil && s.Exited {
		return
	}
	t.Fatalf("%s (no error and no exited status)", reason)
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
		assertError(c.SetVariable(api.EvalScope{GoroutineID: gid}, "a", "10"), t, "SetVariable()")
		_, err = c.ListLocalVariables(api.EvalScope{GoroutineID: gid}, normalLoadConfig)
		assertError(err, t, "ListLocalVariables()")
		_, err = c.ListFunctionArgs(api.EvalScope{GoroutineID: gid}, normalLoadConfig)
		assertError(err, t, "ListFunctionArgs()")
		_, err = c.ListThreadRegisters(0, false)
		assertError(err, t, "ListThreadRegisters()")
		_, err = c.ListScopeRegisters(api.EvalScope{GoroutineID: gid}, false)
		assertError(err, t, "ListScopeRegisters()")
		_, _, err = c.ListGoroutines(0, 0)
		assertError(err, t, "ListGoroutines()")
		_, err = c.Stacktrace(gid, 10, 0, &normalLoadConfig)
		assertError(err, t, "Stacktrace()")
		_, err = c.FindLocation(api.EvalScope{GoroutineID: gid}, "+1", false, nil)
		assertError(err, t, "FindLocation()")
		_, err = c.DisassemblePC(api.EvalScope{GoroutineID: -1}, 0x40100, api.IntelFlavour)
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

		locs, err := c.FindLocation(api.EvalScope{GoroutineID: -1}, "main.main", false, nil)
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

		// start address should be less than end address
		_, err = c.DisassembleRange(api.EvalScope{GoroutineID: -1}, pcend, pcstart, api.IntelFlavour)
		assertError(err, t, "DisassembleRange()")

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

func TestNegativeStackDepthBug(t *testing.T) {
	// After the target process has terminated should return an error but not crash
	protest.AllowRecording(t)
	withTestClient2("continuetestprog", t, func(c service.Client) {
		_, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.sayhi", Line: -1})
		assertNoError(err, t, "CreateBreakpoint()")
		ch := c.Continue()
		state := <-ch
		assertNoError(state.Err, t, "Continue()")
		_, err = c.Stacktrace(-1, -2, 0, &normalLoadConfig)
		assertError(err, t, "Stacktrace()")
	})
}

func TestClientServer_CondBreakpoint(t *testing.T) {
	if runtime.GOOS == "freebsd" {
		t.Skip("test is not valid on FreeBSD")
	}
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

		nvar, err := c.EvalVariable(api.EvalScope{GoroutineID: -1}, "n", normalLoadConfig)
		assertNoError(err, t, "EvalVariable()")

		if nvar.SinglelineString() != "7" {
			t.Fatalf("Stopped on wrong goroutine %s\n", nvar.Value)
		}
	})
}

func clientEvalVariable(t *testing.T, c service.Client, expr string) *api.Variable {
	v, err := c.EvalVariable(api.EvalScope{GoroutineID: -1}, expr, normalLoadConfig)
	assertNoError(err, t, fmt.Sprintf("EvalVariable(%s)", expr))
	return v
}

func TestSkipPrologue(t *testing.T) {
	withTestClient2("locationsprog2", t, func(c service.Client) {
		<-c.Continue()

		afunction := findLocationHelper(t, c, "main.afunction", false, 1, 0)[0]
		findLocationHelper(t, c, "*fn1", false, 1, afunction)
		findLocationHelper(t, c, "locationsprog2.go:8", false, 1, afunction)

		afunction0 := uint64(clientEvalVariable(t, c, "main.afunction").Addr)

		if afunction == afunction0 {
			t.Fatal("Skip prologue failed")
		}
	})
}

func TestSkipPrologue2(t *testing.T) {
	withTestClient2("callme", t, func(c service.Client) {
		callme := findLocationHelper(t, c, "main.callme", false, 1, 0)[0]
		callmeZ := uint64(clientEvalVariable(t, c, "main.callme").Addr)
		findLocationHelper(t, c, "callme.go:5", false, 1, callme)
		if callme == callmeZ {
			t.Fatal("Skip prologue failed")
		}

		callme2 := findLocationHelper(t, c, "main.callme2", false, 1, 0)[0]
		callme2Z := uint64(clientEvalVariable(t, c, "main.callme2").Addr)
		findLocationHelper(t, c, "callme.go:12", false, 1, callme2)
		if callme2 == callme2Z {
			t.Fatal("Skip prologue failed")
		}

		callme3 := findLocationHelper(t, c, "main.callme3", false, 1, 0)[0]
		callme3Z := uint64(clientEvalVariable(t, c, "main.callme3").Addr)
		ver, _ := goversion.Parse(runtime.Version())

		if (ver.Major < 0 || ver.AfterOrEqual(goversion.GoVer18Beta)) && runtime.GOARCH != "386" {
			findLocationHelper(t, c, "callme.go:19", false, 1, callme3)
		} else {
			// callme3 does not have local variables therefore the first line of the
			// function is immediately after the prologue
			// This is only true before go1.8 or on Intel386 where frame pointer chaining
			// introduced a bit of prologue even for functions without local variables
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
	finish := make(chan struct{})
	withTestClient2("issue419", t, func(c service.Client) {
		go func() {
			defer close(finish)
			rand.Seed(time.Now().Unix())
			d := time.Duration(rand.Intn(4) + 1)
			time.Sleep(d * time.Second)
			t.Logf("halt")
			_, err := c.Halt()
			assertNoError(err, t, "RequestManualStop()")
		}()
		statech := c.Continue()
		state := <-statech
		assertNoError(state.Err, t, "Continue()")
		t.Logf("done")
		<-finish
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
		locs, err := c.FindLocation(api.EvalScope{GoroutineID: -1}, "issue406.go:146", false, nil)
		assertNoError(err, t, "FindLocation()")
		_, err = c.CreateBreakpoint(&api.Breakpoint{Addr: locs[0].PC})
		assertNoError(err, t, "CreateBreakpoint()")
		ch := c.Continue()
		state := <-ch
		assertNoError(state.Err, t, "Continue()")
		v, err := c.EvalVariable(api.EvalScope{GoroutineID: -1}, "cfgtree", normalLoadConfig)
		assertNoError(err, t, "EvalVariable()")
		vs := v.MultilineString("", "")
		t.Logf("cfgtree formats to: %s\n", vs)
	})
}

func TestEvalExprName(t *testing.T) {
	withTestClient2("testvariables2", t, func(c service.Client) {
		state := <-c.Continue()
		assertNoError(state.Err, t, "Continue()")

		var1, err := c.EvalVariable(api.EvalScope{GoroutineID: -1}, "i1+1", normalLoadConfig)
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
	if ver.Major > 0 && !ver.AfterOrEqual(goversion.GoVersion{Major: 1, Minor: 7, Rev: -1}) {
		t.Log("Test skipped")
		return
	}

	withTestClient2("issue528", t, func(c service.Client) {
		findLocationHelper(t, c, "State.Close", false, 1, 0)
	})
}

func TestClientServer_FpRegisters(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("test is valid only on AMD64")
	}
	regtests := []struct{ name, value string }{
		// x87
		{"ST(0)", "0x3fffe666660000000000"},
		{"ST(1)", "0x3fffd9999a0000000000"},
		{"ST(2)", "0x3fffcccccd0000000000"},
		{"ST(3)", "0x3fffc000000000000000"},
		{"ST(4)", "0x3fffb333333333333000"},
		{"ST(5)", "0x3fffa666666666666800"},
		{"ST(6)", "0x3fff9999999999999800"},
		{"ST(7)", "0x3fff8cccccccccccd000"},

		// SSE
		{"XMM0", "0x3ff33333333333333ff199999999999a	v2_int={ 3ff199999999999a 3ff3333333333333 }	v4_int={ 9999999a 3ff19999 33333333 3ff33333 }	v8_int={ 999a 9999 9999 3ff1 3333 3333 3333 3ff3 }	v16_int={ 9a 99 99 99 99 99 f1 3f 33 33 33 33 33 33 f3 3f }"},
		{"XMM1", "0x3ff66666666666663ff4cccccccccccd"},
		{"XMM2", "0x3fe666663fd9999a3fcccccd3fc00000"},
		{"XMM3", "0x3ff199999999999a3ff3333333333333"},
		{"XMM4", "0x3ff4cccccccccccd3ff6666666666666"},
		{"XMM5", "0x3fcccccd3fc000003fe666663fd9999a"},
		{"XMM6", "0x4004cccccccccccc4003333333333334"},
		{"XMM7", "0x40026666666666664002666666666666"},
		{"XMM8", "0x4059999a404ccccd4059999a404ccccd"},

		// AVX 2
		{"XMM11", "0x3ff66666666666663ff4cccccccccccd"},
		{"XMM11", "…[YMM11h] 0x3ff66666666666663ff4cccccccccccd"},

		// AVX 512
		{"XMM12", "0x3ff66666666666663ff4cccccccccccd"},
		{"XMM12", "…[YMM12h] 0x3ff66666666666663ff4cccccccccccd"},
		{"XMM12", "…[ZMM12hl] 0x3ff66666666666663ff4cccccccccccd"},
		{"XMM12", "…[ZMM12hh] 0x3ff66666666666663ff4cccccccccccd"},
	}
	protest.AllowRecording(t)
	withTestClient2Extended("fputest/", t, 0, [3]string{}, func(c service.Client, fixture protest.Fixture) {
		_, err := c.CreateBreakpoint(&api.Breakpoint{File: filepath.Join(fixture.BuildDir, "fputest.go"), Line: 25})
		assertNoError(err, t, "CreateBreakpoint")

		state := <-c.Continue()
		t.Logf("state after continue: %#v", state)

		scope := api.EvalScope{GoroutineID: -1}

		boolvar := func(name string) bool {
			v, err := c.EvalVariable(scope, name, normalLoadConfig)
			if err != nil {
				t.Fatalf("could not read %s variable", name)
			}
			t.Logf("%s variable: %#v", name, v)
			return v.Value != "false"
		}

		avx2 := boolvar("avx2")
		avx512 := boolvar("avx512")

		if runtime.GOOS == "windows" {
			// not supported
			avx2 = false
			avx512 = false
		}

		state = <-c.Continue()
		t.Logf("state after continue: %#v", state)

		regs, err := c.ListThreadRegisters(0, true)
		assertNoError(err, t, "ListThreadRegisters()")

		for _, regtest := range regtests {
			if regtest.name == "XMM11" && !avx2 {
				continue
			}
			if regtest.name == "XMM12" && (!avx512 || testBackend == "rr") {
				continue
			}
			found := false
			for _, reg := range regs {
				if reg.Name == regtest.name {
					found = true
					if strings.HasPrefix(regtest.value, "…") {
						if !strings.Contains(reg.Value, regtest.value[len("…"):]) {
							t.Fatalf("register %s expected to contain %q got %q", reg.Name, regtest.value, reg.Value)
						}
					} else {
						if !strings.HasPrefix(reg.Value, regtest.value) {
							t.Fatalf("register %s expected %q got %q", reg.Name, regtest.value, reg.Value)
						}
					}
				}
			}
			if !found {
				t.Fatalf("register %s not found: %v", regtest.name, regs)
			}
		}

		// Test register expressions

		for _, tc := range []struct{ expr, tgt string }{
			{"XMM1[:32]", `"cdccccccccccf43f666666666666f63f"`},
			{"_XMM1[:32]", `"cdccccccccccf43f666666666666f63f"`},
			{"__XMM1[:32]", `"cdccccccccccf43f666666666666f63f"`},
			{"XMM1.int8[0]", `-51`},
			{"XMM1.uint16[0]", `52429`},
			{"XMM1.float32[0]", `-107374184`},
			{"XMM1.float64[0]", `1.3`},
			{"RAX.uint8[0]", "42"},
		} {
			v, err := c.EvalVariable(scope, tc.expr, normalLoadConfig)
			if err != nil {
				t.Fatalf("could not evalue expression %s: %v", tc.expr, err)
			}
			out := v.SinglelineString()

			if out != tc.tgt {
				t.Fatalf("for %q expected %q got %q\n", tc.expr, tc.tgt, out)
			}
		}
	})
}

func TestClientServer_RestartBreakpointPosition(t *testing.T) {
	protest.AllowRecording(t)
	if buildMode == "pie" || (runtime.GOOS == "darwin" && runtime.GOARCH == "arm64") {
		t.Skip("not meaningful in PIE mode")
	}
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
		_, err = c.Restart(false)
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

		// Ensure future commands also return the correct exit status.
		// Previously there was a bug where the command which prompted the
		// process to exit (continue, next, etc...) would return the corrent
		// exit status but subsequent commands would return an incorrect exit
		// status of 0. To test this we simply repeat the 'next' command and
		// ensure we get the correct response again.
		state, err = c.Next()
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !state.Exited {
			t.Fatal("Second process state is not exited")
		}
		if state.ExitStatus != 2 {
			t.Fatalf("Second process exit status is not 2, got: %v", state.ExitStatus)
		}
	})
}

func TestClientServer_StepOutReturn(t *testing.T) {
	ver, _ := goversion.Parse(runtime.Version())
	if ver.Major >= 0 && !ver.AfterOrEqual(goversion.GoVersion{Major: 1, Minor: 10, Rev: -1}) {
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

		stridx := 0
		numidx := 1

		if !goversion.VersionAfterOrEqual(runtime.Version(), 1, 12) {
			// in 1.11 and earlier the order of return values in DWARF is
			// unspecified, in 1.11 and later it follows the order of definition
			// specified by the user
			for i := range ret {
				if ret[i].Name == "str" {
					stridx = i
					numidx = 1 - i
					break
				}
			}
		}

		if ret[stridx].Name != "str" {
			t.Fatalf("(str) bad return value name %s", ret[stridx].Name)
		}
		if ret[stridx].Kind != reflect.String {
			t.Fatalf("(str) bad return value kind %v", ret[stridx].Kind)
		}
		if ret[stridx].Value != "return 47" {
			t.Fatalf("(str) bad return value %q", ret[stridx].Value)
		}

		if ret[numidx].Name != "num" {
			t.Fatalf("(num) bad return value name %s", ret[numidx].Name)
		}
		if ret[numidx].Kind != reflect.Int {
			t.Fatalf("(num) bad return value kind %v", ret[numidx].Kind)
		}
		if ret[numidx].Value != "48" {
			t.Fatalf("(num) bad return value %s", ret[numidx].Value)
		}
	})
}

func TestAcceptMulticlient(t *testing.T) {
	if testBackend == "rr" {
		t.Skip("recording not allowed for TestAcceptMulticlient")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
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
			AcceptMulti:    true,
			DisconnectChan: disconnectChan,
			Debugger: debugger.Config{
				Backend:     testBackend,
				ExecuteKind: debugger.ExecutingGeneratedTest,
			},
		})
		if err := server.Run(); err != nil {
			panic(err)
		}
		<-disconnectChan
		server.Stop()
	}()
	client1 := rpc2.NewClient(listener.Addr().String())
	client1.Disconnect(false)

	client2 := rpc2.NewClient(listener.Addr().String())
	state := <-client2.Continue()
	if state.CurrentThread.Function.Name() != "main.main" {
		t.Fatalf("bad state after continue: %v\n", state)
	}
	client2.Detach(true)
	<-serverDone
}

func mustHaveDebugCalls(t *testing.T, c service.Client) {
	locs, err := c.FindLocation(api.EvalScope{GoroutineID: -1}, "runtime.debugCallV1", false, nil)
	if len(locs) == 0 || err != nil {
		t.Skip("function calls not supported on this version of go")
	}
}

func TestClientServerFunctionCall(t *testing.T) {
	protest.MustSupportFunctionCalls(t, testBackend)
	withTestClient2("fncall", t, func(c service.Client) {
		mustHaveDebugCalls(t, c)
		c.SetReturnValuesLoadConfig(&normalLoadConfig)
		state := <-c.Continue()
		assertNoError(state.Err, t, "Continue()")
		beforeCallFn := state.CurrentThread.Function.Name()
		state, err := c.Call(-1, "call1(one, two)", false)
		assertNoError(err, t, "Call()")
		t.Logf("returned to %q", state.CurrentThread.Function.Name())
		if state.CurrentThread.Function.Name() != beforeCallFn {
			t.Fatalf("did not return to the calling function %q %q", beforeCallFn, state.CurrentThread.Function.Name())
		}
		if state.CurrentThread.ReturnValues == nil {
			t.Fatal("no return values on return from call")
		}
		t.Logf("Return values %v", state.CurrentThread.ReturnValues)
		if len(state.CurrentThread.ReturnValues) != 1 {
			t.Fatal("not enough return values")
		}
		if state.CurrentThread.ReturnValues[0].Value != "3" {
			t.Fatalf("wrong return value %s", state.CurrentThread.ReturnValues[0].Value)
		}
		state = <-c.Continue()
		if !state.Exited {
			t.Fatalf("expected process to exit after call %v", state.CurrentThread)
		}
	})
}

func TestClientServerFunctionCallBadPos(t *testing.T) {
	protest.MustSupportFunctionCalls(t, testBackend)
	if goversion.VersionAfterOrEqual(runtime.Version(), 1, 12) {
		t.Skip("this is a safe point for Go 1.12")
	}
	withTestClient2("fncall", t, func(c service.Client) {
		mustHaveDebugCalls(t, c)
		loc, err := c.FindLocation(api.EvalScope{GoroutineID: -1}, "fmt/print.go:649", false, nil)
		assertNoError(err, t, "could not find location")

		_, err = c.CreateBreakpoint(&api.Breakpoint{File: loc[0].File, Line: loc[0].Line})
		assertNoError(err, t, "CreateBreakpoin")

		state := <-c.Continue()
		assertNoError(state.Err, t, "Continue()")

		state = <-c.Continue()
		assertNoError(state.Err, t, "Continue()")

		c.SetReturnValuesLoadConfig(&normalLoadConfig)
		state, err = c.Call(-1, "main.call1(main.zero, main.zero)", false)
		if err == nil || err.Error() != "call not at safe point" {
			t.Fatalf("wrong error or no error: %v", err)
		}
	})
}

func TestClientServerFunctionCallPanic(t *testing.T) {
	protest.MustSupportFunctionCalls(t, testBackend)
	withTestClient2("fncall", t, func(c service.Client) {
		mustHaveDebugCalls(t, c)
		c.SetReturnValuesLoadConfig(&normalLoadConfig)
		state := <-c.Continue()
		assertNoError(state.Err, t, "Continue()")
		state, err := c.Call(-1, "callpanic()", false)
		assertNoError(err, t, "Call()")
		t.Logf("at: %s:%d", state.CurrentThread.File, state.CurrentThread.Line)
		if state.CurrentThread.ReturnValues == nil {
			t.Fatal("no return values on return from call")
		}
		t.Logf("Return values %v", state.CurrentThread.ReturnValues)
		if len(state.CurrentThread.ReturnValues) != 1 {
			t.Fatal("not enough return values")
		}
		if state.CurrentThread.ReturnValues[0].Name != "~panic" {
			t.Fatal("not a panic")
		}
		if state.CurrentThread.ReturnValues[0].Children[0].Value != "callpanic panicked" {
			t.Fatalf("wrong panic value %s", state.CurrentThread.ReturnValues[0].Children[0].Value)
		}
	})
}

func TestClientServerFunctionCallStacktrace(t *testing.T) {
	if goversion.VersionAfterOrEqual(runtime.Version(), 1, 15) {
		t.Skip("Go 1.15 executes function calls in a different goroutine so the stack trace will not contain main.main or runtime.main")
	}
	protest.MustSupportFunctionCalls(t, testBackend)
	withTestClient2("fncall", t, func(c service.Client) {
		mustHaveDebugCalls(t, c)
		c.SetReturnValuesLoadConfig(&api.LoadConfig{FollowPointers: false, MaxStringLen: 2048})
		state := <-c.Continue()
		assertNoError(state.Err, t, "Continue()")
		state, err := c.Call(-1, "callstacktrace()", false)
		assertNoError(err, t, "Call()")
		t.Logf("at: %s:%d", state.CurrentThread.File, state.CurrentThread.Line)
		if state.CurrentThread.ReturnValues == nil {
			t.Fatal("no return values on return from call")
		}
		if len(state.CurrentThread.ReturnValues) != 1 || state.CurrentThread.ReturnValues[0].Kind != reflect.String {
			t.Fatal("not enough return values")
		}
		st := state.CurrentThread.ReturnValues[0].Value
		t.Logf("Returned stacktrace:\n%s", st)

		if !strings.Contains(st, "main.callstacktrace") || !strings.Contains(st, "main.main") || !strings.Contains(st, "runtime.main") {
			t.Fatal("bad stacktrace returned")
		}
	})
}

func TestAncestors(t *testing.T) {
	if !goversion.VersionAfterOrEqual(runtime.Version(), 1, 11) {
		t.Skip("not supported on Go <= 1.10")
	}
	savedGodebug := os.Getenv("GODEBUG")
	os.Setenv("GODEBUG", "tracebackancestors=100")
	defer os.Setenv("GODEBUG", savedGodebug)
	withTestClient2("testnextprog", t, func(c service.Client) {
		_, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.testgoroutine", Line: -1})
		assertNoError(err, t, "CreateBreakpoin")
		state := <-c.Continue()
		assertNoError(state.Err, t, "Continue()")
		ancestors, err := c.Ancestors(-1, 1000, 1000)
		assertNoError(err, t, "Ancestors")
		t.Logf("ancestors: %#v\n", ancestors)
		if len(ancestors) != 1 {
			t.Fatalf("expected only one ancestor got %d", len(ancestors))
		}

		mainFound := false
		for _, ancestor := range ancestors {
			for _, frame := range ancestor.Stack {
				if frame.Function.Name() == "main.main" {
					mainFound = true
				}
			}
		}
		if !mainFound {
			t.Fatal("function main.main not found in any ancestor")
		}
	})
}

type brokenRPCClient struct {
	client *rpc.Client
}

func (c *brokenRPCClient) Detach(kill bool) error {
	defer c.client.Close()
	out := new(rpc2.DetachOut)
	return c.call("Detach", rpc2.DetachIn{Kill: kill}, out)
}

func (c *brokenRPCClient) call(method string, args, reply interface{}) error {
	return c.client.Call("RPCServer."+method, args, reply)
}

func TestUnknownMethodCall(t *testing.T) {
	clientConn, _ := startServer("continuetestprog", 0, t, [3]string{})
	client := &brokenRPCClient{jsonrpc.NewClient(clientConn)}
	client.call("SetApiVersion", api.SetAPIVersionIn{APIVersion: 2}, &api.SetAPIVersionOut{})
	defer client.Detach(true)
	var out int
	err := client.call("NonexistentRPCCall", nil, &out)
	assertError(err, t, "call()")
	if !strings.HasPrefix(err.Error(), "unknown method: ") {
		t.Errorf("wrong error message: %v", err)
	}
}

func TestIssue1703(t *testing.T) {
	// Calling Disassemble when there is no current goroutine should work.
	withTestClient2("testnextprog", t, func(c service.Client) {
		locs, err := c.FindLocation(api.EvalScope{GoroutineID: -1}, "main.main", true, nil)
		assertNoError(err, t, "FindLocation")
		t.Logf("FindLocation: %#v", locs)
		text, err := c.DisassemblePC(api.EvalScope{GoroutineID: -1}, locs[0].PC, api.IntelFlavour)
		assertNoError(err, t, "DisassemblePC")
		t.Logf("text: %#v\n", text)
	})
}

func TestRerecord(t *testing.T) {
	protest.AllowRecording(t)
	if testBackend != "rr" {
		t.Skip("only valid for recorded targets")
	}
	withTestClient2("testrerecord", t, func(c service.Client) {
		fp := testProgPath(t, "testrerecord")
		_, err := c.CreateBreakpoint(&api.Breakpoint{File: fp, Line: 10})
		assertNoError(err, t, "CreateBreakpoin")

		gett := func() int {
			state := <-c.Continue()
			if state.Err != nil {
				t.Fatalf("Unexpected error: %v, state: %#v", state.Err, state)
			}

			vart, err := c.EvalVariable(api.EvalScope{GoroutineID: -1}, "t", normalLoadConfig)
			assertNoError(err, t, "EvalVariable")
			if vart.Unreadable != "" {
				t.Fatalf("Could not read variable 't': %s\n", vart.Unreadable)
			}

			t.Logf("Value of t is %s\n", vart.Value)

			vartval, err := strconv.Atoi(vart.Value)
			assertNoError(err, t, "Parsing value of variable t")
			return vartval
		}

		t0 := gett()

		_, err = c.RestartFrom(false, "", false, nil, [3]string{}, false)
		assertNoError(err, t, "First restart")
		t1 := gett()

		if t0 != t1 {
			t.Fatalf("Expected same value for t after restarting (without rerecording) %d %d", t0, t1)
		}

		time.Sleep(2 * time.Second) // make sure that we're not running inside the same second

		_, err = c.RestartFrom(true, "", false, nil, [3]string{}, false)
		assertNoError(err, t, "Second restart")
		t2 := gett()

		if t0 == t2 {
			t.Fatalf("Expected new value for t after restarting (with rerecording) %d %d", t0, t2)
		}
	})
}

func TestIssue1787(t *testing.T) {
	// Calling FunctionReturnLocations without a selected goroutine should
	// work.
	withTestClient2("testnextprog", t, func(c service.Client) {
		if c, _ := c.(*rpc2.RPCClient); c != nil {
			c.FunctionReturnLocations("main.main")
		}
	})
}

func TestDoubleCreateBreakpoint(t *testing.T) {
	withTestClient2("testnextprog", t, func(c service.Client) {
		_, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.main", Line: 1, Name: "firstbreakpoint", Tracepoint: true})
		assertNoError(err, t, "CreateBreakpoint 1")

		bps, err := c.ListBreakpoints()
		assertNoError(err, t, "ListBreakpoints 1")

		t.Logf("breakpoints before second call:")
		for _, bp := range bps {
			t.Logf("\t%v", bp)
		}

		numBreakpoints := len(bps)

		_, err = c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.main", Line: 1, Name: "secondbreakpoint", Tracepoint: true})
		assertError(err, t, "CreateBreakpoint 2") // breakpoint exists

		bps, err = c.ListBreakpoints()
		assertNoError(err, t, "ListBreakpoints 2")

		t.Logf("breakpoints after second call:")
		for _, bp := range bps {
			t.Logf("\t%v", bp)
		}

		if len(bps) != numBreakpoints {
			t.Errorf("wrong number of breakpoints, got %d expected %d", len(bps), numBreakpoints)
		}
	})
}

func TestStopRecording(t *testing.T) {
	protest.AllowRecording(t)
	if testBackend != "rr" {
		t.Skip("only for rr backend")
	}
	withTestClient2("sleep", t, func(c service.Client) {
		time.Sleep(time.Second)
		c.StopRecording()
		_, err := c.GetState()
		assertNoError(err, t, "GetState()")

		// try rerecording
		go func() {
			c.RestartFrom(true, "", false, nil, [3]string{}, false)
		}()

		time.Sleep(time.Second) // hopefully the re-recording started...
		c.StopRecording()
		_, err = c.GetState()
		assertNoError(err, t, "GetState()")
	})
}

func TestClearLogicalBreakpoint(t *testing.T) {
	// Clearing a logical breakpoint should clear all associated physical
	// breakpoints.
	// Issue #1955.
	withTestClient2Extended("testinline", t, protest.EnableInlining, [3]string{}, func(c service.Client, fixture protest.Fixture) {
		bp, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.inlineThis"})
		assertNoError(err, t, "CreateBreakpoint()")
		t.Logf("breakpoint set at %#v", bp.Addrs)
		if len(bp.Addrs) < 2 {
			t.Fatal("Wrong number of addresses for main.inlineThis breakpoint")
		}
		_, err = c.ClearBreakpoint(bp.ID)
		assertNoError(err, t, "ClearBreakpoint()")
		bps, err := c.ListBreakpoints()
		assertNoError(err, t, "ListBreakpoints()")
		for _, curbp := range bps {
			if curbp.ID == bp.ID {
				t.Errorf("logical breakpoint still exists: %#v", curbp)
				break
			}
		}
	})
}

func TestRedirects(t *testing.T) {
	const (
		infile  = "redirect-input.txt"
		outfile = "redirect-output.txt"
	)
	protest.AllowRecording(t)
	withTestClient2Extended("redirect", t, 0, [3]string{infile, outfile, ""}, func(c service.Client, fixture protest.Fixture) {
		outpath := filepath.Join(fixture.BuildDir, outfile)
		<-c.Continue()
		buf, err := ioutil.ReadFile(outpath)
		assertNoError(err, t, "Reading output file")
		t.Logf("output %q", buf)
		if !strings.HasPrefix(string(buf), "Redirect test") {
			t.Fatalf("Wrong output %q", string(buf))
		}
		os.Remove(outpath)
		if testBackend != "rr" {
			_, err = c.Restart(false)
			assertNoError(err, t, "Restart")
			<-c.Continue()
			buf2, err := ioutil.ReadFile(outpath)
			t.Logf("output %q", buf2)
			assertNoError(err, t, "Reading output file (second time)")
			if !strings.HasPrefix(string(buf2), "Redirect test") {
				t.Fatalf("Wrong output %q", string(buf2))
			}
			if string(buf2) == string(buf) {
				t.Fatalf("Expected output change got %q and %q", string(buf), string(buf2))
			}
			os.Remove(outpath)
		}
	})
}

func TestIssue2162(t *testing.T) {
	if buildMode == "pie" || runtime.GOOS == "windows" {
		t.Skip("skip it for stepping into one place where no source for pc when on pie mode or windows")
	}
	withTestClient2("issue2162", t, func(c service.Client) {
		state, err := c.GetState()
		assertNoError(err, t, "GetState()")
		if state.CurrentThread.Function == nil {
			// Can't call Step if we don't have the source code of the current function
			return
		}

		_, err = c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.main"})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		_, err = c.Step()
		assertNoError(err, t, "Step()")
	})
}

func TestDetachLeaveRunning(t *testing.T) {
	// See https://github.com/go-delve/delve/issues/2259
	if testBackend == "rr" {
		return
	}

	listener, clientConn := service.ListenerPipe()
	defer listener.Close()
	var buildFlags protest.BuildFlags
	if buildMode == "pie" {
		buildFlags |= protest.BuildModePIE
	}
	fixture := protest.BuildFixture("testnextnethttp", buildFlags)

	cmd := exec.Command(fixture.Path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	assertNoError(cmd.Start(), t, "starting fixture")
	defer cmd.Process.Kill()

	// wait for testnextnethttp to start listening
	t0 := time.Now()
	for {
		conn, err := net.Dial("tcp", "127.0.0.1:9191")
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
		if time.Since(t0) > 10*time.Second {
			t.Fatal("fixture did not start")
		}
	}

	server := rpccommon.NewServer(&service.Config{
		Listener:   listener,
		APIVersion: 2,
		Debugger: debugger.Config{
			AttachPid:  cmd.Process.Pid,
			WorkingDir: ".",
			Backend:    testBackend,
		},
	})
	if err := server.Run(); err != nil {
		t.Fatal(err)
	}

	client := rpc2.NewClientFromConn(clientConn)
	defer server.Stop()
	assertNoError(client.Detach(false), t, "Detach")
}

func assertNoDuplicateBreakpoints(t *testing.T, c service.Client) {
	t.Helper()
	bps, _ := c.ListBreakpoints()
	seen := make(map[int]bool)
	for _, bp := range bps {
		t.Logf("%#v\n", bp)
		if seen[bp.ID] {
			t.Fatalf("duplicate breakpoint ID %d", bp.ID)
		}
		seen[bp.ID] = true
	}
}

func TestToggleBreakpointRestart(t *testing.T) {
	// Checks that breakpoints IDs do not overlap after Restart if there are disabled breakpoints.
	withTestClient2("testtoggle", t, func(c service.Client) {
		bp1, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.main", Line: 1, Name: "firstbreakpoint"})
		assertNoError(err, t, "CreateBreakpoint 1")
		_, err = c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.main", Line: 2, Name: "secondbreakpoint"})
		assertNoError(err, t, "CreateBreakpoint 2")
		_, err = c.ToggleBreakpoint(bp1.ID)
		assertNoError(err, t, "ToggleBreakpoint")
		_, err = c.Restart(false)
		assertNoError(err, t, "Restart")
		assertNoDuplicateBreakpoints(t, c)
		_, err = c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.main", Line: 3, Name: "thirdbreakpoint"})
		assertNoError(err, t, "CreateBreakpoint 3")
		assertNoDuplicateBreakpoints(t, c)
	})
}

func TestStopServerWithClosedListener(t *testing.T) {
	// Checks that the error erturned by listener.Accept() is ignored when we
	// are trying to shutdown. See issue #1633.
	if testBackend == "rr" || buildMode == "pie" {
		t.Skip("N/A")
	}
	listener, err := net.Listen("tcp", "localhost:0")
	assertNoError(err, t, "listener")
	fixture := protest.BuildFixture("math", 0)
	server := rpccommon.NewServer(&service.Config{
		Listener:           listener,
		AcceptMulti:        false,
		APIVersion:         2,
		CheckLocalConnUser: true,
		DisconnectChan:     make(chan struct{}),
		ProcessArgs:        []string{fixture.Path},
		Debugger: debugger.Config{
			WorkingDir:  ".",
			Backend:     "default",
			Foreground:  false,
			BuildFlags:  "",
			ExecuteKind: debugger.ExecutingGeneratedFile,
		},
	})
	assertNoError(server.Run(), t, "blah")
	time.Sleep(1 * time.Second) // let server start
	server.Stop()
	listener.Close()
	time.Sleep(1 * time.Second) // give time to server to panic
}

func TestGoroutinesGrouping(t *testing.T) {
	// Tests the goroutine grouping and filtering feature
	withTestClient2("goroutinegroup", t, func(c service.Client) {
		state := <-c.Continue()
		assertNoError(state.Err, t, "Continue")
		_, ggrp, _, _, err := c.ListGoroutinesWithFilter(0, 0, nil, &api.GoroutineGroupingOptions{GroupBy: api.GoroutineLabel, GroupByKey: "name", MaxGroupMembers: 5, MaxGroups: 10})
		assertNoError(err, t, "ListGoroutinesWithFilter (group by label)")
		t.Logf("%#v\n", ggrp)
		if len(ggrp) < 5 {
			t.Errorf("not enough groups %d\n", len(ggrp))
		}
		var unnamedCount int
		for i := range ggrp {
			if ggrp[i].Name == "name=" {
				unnamedCount = ggrp[i].Total
				break
			}
		}
		gs, _, _, _, err := c.ListGoroutinesWithFilter(0, 0, []api.ListGoroutinesFilter{{Kind: api.GoroutineLabel, Arg: "name="}}, nil)
		assertNoError(err, t, "ListGoroutinesWithFilter (filter unnamed)")
		if len(gs) != unnamedCount {
			t.Errorf("wrong number of goroutines returned by filter: %d (expected %d)\n", len(gs), unnamedCount)
		}
	})
}

func TestLongStringArg(t *testing.T) {
	// Test the ability to load more elements of a string argument, this could
	// be broken if registerized variables are not handled correctly.
	withTestClient2("morestringarg", t, func(c service.Client) {
		_, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.f"})
		assertNoError(err, t, "CreateBreakpoint")
		state := <-c.Continue()
		assertNoError(state.Err, t, "Continue")

		test := func(name, val1, val2 string) uint64 {
			var1, err := c.EvalVariable(api.EvalScope{GoroutineID: -1}, name, normalLoadConfig)
			assertNoError(err, t, "EvalVariable")
			t.Logf("%#v\n", var1)
			if var1.Value != val1 {
				t.Fatalf("wrong value for variable: %q", var1.Value)
			}
			var2, err := c.EvalVariable(api.EvalScope{GoroutineID: -1}, fmt.Sprintf("(*(*%q)(%#x))[64:]", var1.Type, var1.Addr), normalLoadConfig)
			assertNoError(err, t, "EvalVariable")
			t.Logf("%#v\n", var2)
			if var2.Value != val2 {
				t.Fatalf("wrong value for variable: %q", var2.Value)

			}
			return var1.Addr
		}

		saddr := test("s", "very long string 01234567890123456789012345678901234567890123456", "7890123456789012345678901234567890123456789X")
		test("q", "very long string B 012345678901234567890123456789012345678901234", "567890123456789012345678901234567890123456789X2")
		saddr2 := test("s", "very long string 01234567890123456789012345678901234567890123456", "7890123456789012345678901234567890123456789X")
		if saddr != saddr2 {
			t.Fatalf("address of s changed (%#x %#x)", saddr, saddr2)
		}
	})
}
