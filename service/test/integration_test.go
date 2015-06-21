package servicetest

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	protest "github.com/derekparker/delve/proc/test"

	"github.com/derekparker/delve/service"
	"github.com/derekparker/delve/service/api"
	"github.com/derekparker/delve/service/rest"
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
	// Test REST service
	restService := func() {
		fmt.Println("---- RUNNING TEST WITH REST CLIENT ----")
		server := rest.NewServer(&service.Config{
			Listener:    listener,
			ProcessArgs: []string{protest.BuildFixture(name).Path},
		}, false)
		go server.Run()
		client := rest.NewClient(listener.Addr().String())
		defer client.Detach(true)
		fn(client)
	}
	// Test RPC service
	rpcService := func() {
		fmt.Println("---- RUNNING TEST WITH RPC CLIENT ----")
		server := rpc.NewServer(&service.Config{
			Listener:    listener,
			ProcessArgs: []string{protest.BuildFixture(name).Path},
		}, false)
		go server.Run()
		client := rpc.NewClient(listener.Addr().String())
		defer client.Detach(true)
		fn(client)
	}
	rpcService()
	restService()
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
		state, err = c.Continue()
		if err == nil {
			t.Fatalf("Expected error after continue, got none")
		}
		if !strings.Contains(err.Error(), "exited") {
			t.Fatal("Expected exit message")
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

		stateBefore, err := c.Continue()
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
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

		state, err := c.Continue()
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
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

		state, err := c.Continue()
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

		state, err := c.Continue()
		if err != nil {
			t.Fatalf("Unexpected error: %v, state: %#v", err, state)
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
		state, err := c.Continue()
		if err != nil {
			t.Fatalf("Unexpected error: %v, state: %#v", err, state)
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
	withTestClient("testnextprog", t, func(c service.Client) {
		fp, err := filepath.Abs("_fixtures/testnextprog.go")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(fp); err != nil {
			fp, err = filepath.Abs("../../_fixtures/testnextprog.go")
			if err != nil {
				t.Fatal(err)
			}
		}
		_, err = c.CreateBreakpoint(&api.Breakpoint{File: fp, Line: 23})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		state, err := c.Continue()
		if err != nil {
			t.Fatalf("Unexpected error: %v, state: %#v", err, state)
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
		fp, err := filepath.Abs("_fixtures/testnextprog.go")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(fp); err != nil {
			fp, err = filepath.Abs("../../_fixtures/testnextprog.go")
			if err != nil {
				t.Fatal(err)
			}
		}
		_, err = c.CreateBreakpoint(&api.Breakpoint{File: fp, Line: 47})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		state, err := c.Continue()
		if err != nil {
			t.Fatalf("Unexpected error: %v, state: %#v", err, state)
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
