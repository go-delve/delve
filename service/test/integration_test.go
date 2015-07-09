package servicetest

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	protest "github.com/derekparker/delve/proc/test"

	"github.com/derekparker/delve/service"
	"github.com/derekparker/delve/service/api"
	"github.com/derekparker/delve/service/rpc"
)

func init() {
	runtime.GOMAXPROCS(2)
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
			t.Fatal("expected restarted process to have exited")
		}
	})
}

func TestRestart_duringStop(t *testing.T) {
	withTestClient("continuetestprog", t, func(c service.Client) {
		origPid := c.ProcessPid()
		_, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.main"})
		if err != nil {
			t.Fatal(err)
		}
		state := <-c.Continue()
		if state.Breakpoint == nil {
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
		if len(bps) != 0 {
			t.Fatal("breakpoint tabe not cleared")
		}
		state = <-c.Continue()
		if !state.Exited {
			t.Fatal("expected process to have exited")
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
	if err := server.Restart(nil, nil); err == nil {
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
		if err != nil {
			t.Fatal(err)
		}
		if state.CurrentThread == nil {
			t.Fatalf("Expected CurrentThread")
		}
		if e, a := true, state.Exited; e != a {
			t.Fatalf("Expected exited %v, got %v", e, a)
		}
	})
}

func TestClientServer_step(t *testing.T) {
	withTestClient("testprog", t, func(c service.Client) {
		_, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.helloworld"})
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
		bp, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: initialLocation})
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
	testnext(testcases, "main.testnext", t)
}

func TestNextGoroutine(t *testing.T) {
	testcases := []nextTest{
		{46, 47},
		{47, 42},
	}
	testnext(testcases, "main.testgoroutine", t)
}

func TestNextFunctionReturn(t *testing.T) {
	testcases := []nextTest{
		{13, 14},
		{14, 35},
	}
	testnext(testcases, "main.helloworld", t)
}

func TestClientServer_breakpointInMainThread(t *testing.T) {
	withTestClient("testprog", t, func(c service.Client) {
		bp, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.helloworld"})
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
		_, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.anotherthread"})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		state := <-c.Continue()
		if state.Err != nil {
			t.Fatalf("Unexpected error: %v, state: %#v", state.Err, state)
		}

		f, l := state.CurrentThread.File, state.CurrentThread.Line
		if f != "testthreads.go" && l != 8 {
			t.Fatal("Program did not hit breakpoint")
		}
	})
}

func TestClientServer_breakAtNonexistentPoint(t *testing.T) {
	withTestClient("testprog", t, func(c service.Client) {
		_, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "nowhere"})
		if err == nil {
			t.Fatal("Should not be able to break at non existent function")
		}
	})
}

func TestClientServer_clearBreakpoint(t *testing.T) {
	withTestClient("testprog", t, func(c service.Client) {
		bp, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.sleepytime"})
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

		_, err = c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.main"})
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
		locals, err := c.ListLocalVariables()
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
		locals, err := c.ListFunctionArgs()
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
			if state.Breakpoint != nil {
				count++

				t.Logf("%v", state)

				bpi := state.BreakpointInfo

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

				if bpi.Variables[0].Value != strconv.Itoa(count-1) {
					t.Fatalf("Wrong variable value %s (%d)", bpi.Variables[0].Value, count)
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
		bp1, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.main", Tracepoint: true})
		if err != nil {
			t.Fatalf("Unexpected error: %v\n", err)
		}
		bp2, err := c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.sayhi", Tracepoint: true})
		if err != nil {
			t.Fatalf("Unexpected error: %v\n", err)
		}
		countMain := 0
		countSayhi := 0
		contChan := c.Continue()
		for state := range contChan {
			if state.Breakpoint != nil {
				switch state.Breakpoint.ID {
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
