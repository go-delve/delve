package servicetest

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	protest "github.com/derekparker/delve/proc/test"

	"github.com/derekparker/delve/service"
	"github.com/derekparker/delve/service/api"
	"github.com/derekparker/delve/service/rpc"
)

func init() {
	runtime.GOMAXPROCS(2)
}

func assertNoError(err error, t *testing.T, s string) {
	if err != nil {
		_, file, line, _ := runtime.Caller(1)
		fname := filepath.Base(file)
		t.Fatalf("failed assertion at %s:%d: %s - %s\n", fname, line, s, err)
	}
}

func TestMain(m *testing.M) {
	os.Exit(protest.RunTestsWithFixtures(m))
}

func withTestClient(name string, t *testing.T, fn func(c service.Client)) {
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("couldn't start listener: %s\n", err)
	}
	defer listener.Close()
	server := rpc.NewServer(&service.Config{
		Listener:    listener,
		ProcessArgs: []string{protest.BuildFixture(name).Path},
	}, false)
	if err := server.Run(); err != nil {
		t.Fatal(err)
	}
	client := rpc.NewClient(listener.Addr().String())
	defer func() {
		client.Detach(true)
	}()

	fn(client)
}

func TestRunWithInvalidPath(t *testing.T) {
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("couldn't start listener: %s\n", err)
	}
	defer listener.Close()
	server := rpc.NewServer(&service.Config{
		Listener:    listener,
		ProcessArgs: []string{"invalid_path"},
	}, false)
	if err := server.Run(); err == nil {
		t.Fatal("Expected Run to return error for invalid program path")
	}
}

func TestRestart_afterExit(t *testing.T) {
	withTestClient("continuetestprog", t, func(c service.Client) {
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

func TestRestart_duringStop(t *testing.T) {
	withTestClient("continuetestprog", t, func(c service.Client) {
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

func TestRestart_attachPid(t *testing.T) {
	// Assert it does not work and returns error.
	// We cannot restart a process we did not spawn.
	server := rpc.NewServer(&service.Config{
		Listener:  nil,
		AttachPid: 999,
	}, false)
	if err := server.Restart(); err == nil {
		t.Fatal("expected error on restart after attaching to pid but got none")
	}
}

func TestClientServer_exit(t *testing.T) {
	withTestClient("continuetestprog", t, func(c service.Client) {
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
		state, err = c.GetState()
		if err == nil {
			t.Fatal("Expected error on querying state from exited process")
		}
	})
}

func TestClientServer_step(t *testing.T) {
	withTestClient("testprog", t, func(c service.Client) {
		_, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.helloworld", Line: 1})
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

type nextTest struct {
	begin, end int
}

func testnext(testcases []nextTest, initialLocation string, t *testing.T) {
	withTestClient("testnextprog", t, func(c service.Client) {
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
	testcases := []nextTest{
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
	testnext(testcases, "main.testnext", t)
}

func TestNextFunctionReturn(t *testing.T) {
	testcases := []nextTest{
		{14, 15},
		{15, 35},
	}
	testnext(testcases, "main.helloworld", t)
}

func TestClientServer_breakpointInMainThread(t *testing.T) {
	withTestClient("testprog", t, func(c service.Client) {
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
	withTestClient("testthreads", t, func(c service.Client) {
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
	withTestClient("testprog", t, func(c service.Client) {
		_, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "nowhere", Line: 1})
		if err == nil {
			t.Fatal("Should not be able to break at non existent function")
		}
	})
}

func TestClientServer_clearBreakpoint(t *testing.T) {
	withTestClient("testprog", t, func(c service.Client) {
		bp, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.sleepytime", Line: 1})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		bps, err := c.ListBreakpoints()
		if e, a := 1, len(bps); e != a {
			t.Fatalf("Expected breakpoint count %d, got %d", e, a)
		}

		deleted, err := c.ClearBreakpoint(bp.ID)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		if deleted.ID != bp.ID {
			t.Fatalf("Expected deleted breakpoint ID %v, got %v", bp.ID, deleted.ID)
		}

		bps, err = c.ListBreakpoints()
		if e, a := 0, len(bps); e != a {
			t.Fatalf("Expected breakpoint count %d, got %d", e, a)
		}
	})
}

func TestClientServer_switchThread(t *testing.T) {
	withTestClient("testnextprog", t, func(c service.Client) {
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

func testProgPath(t *testing.T, name string) string {
	fp, err := filepath.Abs(fmt.Sprintf("_fixtures/%s.go", name))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fp); err != nil {
		fp, err = filepath.Abs(fmt.Sprintf("../../_fixtures/%s.go", name))
		if err != nil {
			t.Fatal(err)
		}
	}
	return fp
}

func TestClientServer_infoLocals(t *testing.T) {
	withTestClient("testnextprog", t, func(c service.Client) {
		fp := testProgPath(t, "testnextprog")
		_, err := c.CreateBreakpoint(&api.Breakpoint{File: fp, Line: 23})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		state := <-c.Continue()
		if state.Err != nil {
			t.Fatalf("Unexpected error: %v, state: %#v", state.Err, state)
		}
		locals, err := c.ListLocalVariables(api.EvalScope{-1, 0})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if len(locals) != 3 {
			t.Fatalf("Expected 3 locals, got %d %#v", len(locals), locals)
		}
	})
}

func TestClientServer_infoArgs(t *testing.T) {
	withTestClient("testnextprog", t, func(c service.Client) {
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
		locals, err := c.ListFunctionArgs(api.EvalScope{-1, 0})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if len(locals) != 2 {
			t.Fatalf("Expected 2 function args, got %d %#v", len(locals), locals)
		}
	})
}

func TestClientServer_traceContinue(t *testing.T) {
	withTestClient("integrationprog", t, func(c service.Client) {
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
	withTestClient("integrationprog", t, func(c service.Client) {
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

func findLocationHelper(t *testing.T, c service.Client, loc string, shouldErr bool, count int, checkAddr uint64) []uint64 {
	locs, err := c.FindLocation(api.EvalScope{-1, 0}, loc)
	t.Logf("FindLocation(\"%s\") → %v\n", loc, locs)

	if shouldErr {
		if err == nil {
			t.Fatalf("Resolving location <%s> didn't return an error: %v", loc, locs)
		}
	} else {
		if err != nil {
			t.Fatalf("Error resolving location <%s>: %v", loc, err)
		}
	}

	if (count >= 0) && (len(locs) != count) {
		t.Fatalf("Wrong number of breakpoints returned for location <%s> (got %d, expected %d)", loc, len(locs), count)
	}

	if checkAddr != 0 && checkAddr != locs[0].PC {
		t.Fatalf("Wrong address returned for location <%s> (got %v, epected %v)", loc, locs[0].PC, checkAddr)
	}

	addrs := make([]uint64, len(locs))
	for i := range locs {
		addrs[i] = locs[i].PC
	}
	return addrs
}

func TestClientServer_FindLocations(t *testing.T) {
	withTestClient("locationsprog", t, func(c service.Client) {
		someFunctionCallAddr := findLocationHelper(t, c, "locationsprog.go:27", false, 1, 0)[0]
		findLocationHelper(t, c, "anotherFunction:1", false, 1, someFunctionCallAddr)
		findLocationHelper(t, c, "main.anotherFunction:1", false, 1, someFunctionCallAddr)
		findLocationHelper(t, c, "anotherFunction", false, 1, someFunctionCallAddr)
		findLocationHelper(t, c, "main.anotherFunction", false, 1, someFunctionCallAddr)
		findLocationHelper(t, c, fmt.Sprintf("*0x%x", someFunctionCallAddr), false, 1, someFunctionCallAddr)
		findLocationHelper(t, c, "sprog.go:26", true, 0, 0)

		findLocationHelper(t, c, "String", true, 0, 0)
		findLocationHelper(t, c, "main.String", true, 0, 0)

		someTypeStringFuncAddr := findLocationHelper(t, c, "locationsprog.go:15", false, 1, 0)[0]
		otherTypeStringFuncAddr := findLocationHelper(t, c, "locationsprog.go:19", false, 1, 0)[0]
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

		_, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.main", Line: 3, Tracepoint: false})
		if err != nil {
			t.Fatalf("CreateBreakpoint(): %v\n", err)
		}

		<-c.Continue()

		locationsprog34Addr := findLocationHelper(t, c, "locationsprog.go:34", false, 1, 0)[0]
		findLocationHelper(t, c, fmt.Sprintf("%s:34", testProgPath(t, "locationsprog")), false, 1, locationsprog34Addr)
		findLocationHelper(t, c, "+1", false, 1, locationsprog34Addr)
		findLocationHelper(t, c, "34", false, 1, locationsprog34Addr)
		findLocationHelper(t, c, "-1", false, 1, findLocationHelper(t, c, "locationsprog.go:32", false, 1, 0)[0])
	})

	withTestClient("testnextdefer", t, func(c service.Client) {
		firstMainLine := findLocationHelper(t, c, "testnextdefer.go:8", false, 1, 0)[0]
		findLocationHelper(t, c, "main.main", false, 1, firstMainLine)
	})

	withTestClient("stacktraceprog", t, func(c service.Client) {
		stacktracemeAddr := findLocationHelper(t, c, "stacktraceprog.go:4", false, 1, 0)[0]
		findLocationHelper(t, c, "main.stacktraceme", false, 1, stacktracemeAddr)
	})

	withTestClient("locationsUpperCase", t, func(c service.Client) {
		// Upper case
		findLocationHelper(t, c, "locationsUpperCase.go:6", false, 1, 0)

		// Fully qualified path
		path := protest.Fixtures["locationsUpperCase"].Source
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
	withTestClient("locationsprog2", t, func(c service.Client) {
		<-c.Continue()

		afunction := findLocationHelper(t, c, "main.afunction", false, 1, 0)[0]
		anonfunc := findLocationHelper(t, c, "locationsprog2.go:25", false, 1, 0)[0]

		findLocationHelper(t, c, "*fn1", false, 1, afunction)
		findLocationHelper(t, c, "*fn3", false, 1, anonfunc)
	})
}

func TestClientServer_EvalVariable(t *testing.T) {
	withTestClient("testvariables", t, func(c service.Client) {
		state := <-c.Continue()

		if state.Err != nil {
			t.Fatalf("Continue(): %v\n", state.Err)
		}

		var1, err := c.EvalVariable(api.EvalScope{-1, 0}, "a1")
		assertNoError(err, t, "EvalVariable")

		t.Logf("var1: %s", var1.SinglelineString())

		if var1.Value != "foofoofoofoofoofoo" {
			t.Fatalf("Wrong variable value: %s", var1.Value)
		}
	})
}

func TestClientServer_SetVariable(t *testing.T) {
	withTestClient("testvariables", t, func(c service.Client) {
		state := <-c.Continue()

		if state.Err != nil {
			t.Fatalf("Continue(): %v\n", state.Err)
		}

		assertNoError(c.SetVariable(api.EvalScope{-1, 0}, "a2", "8"), t, "SetVariable()")

		a2, err := c.EvalVariable(api.EvalScope{-1, 0}, "a2")

		t.Logf("a2: %v", a2)

		n, err := strconv.Atoi(a2.Value)

		if err != nil && n != 8 {
			t.Fatalf("Wrong variable value: %v", a2)
		}
	})
}

func TestClientServer_FullStacktrace(t *testing.T) {
	withTestClient("goroutinestackprog", t, func(c service.Client) {
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
			frames, err := c.Stacktrace(g.ID, 10, true)
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
					t.Logf("frame %d, variable i is %v\n", arg)
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

		frames, err := c.Stacktrace(-1, 10, true)
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
