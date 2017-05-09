package proc_test

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/derekparker/delve/pkg/proc"
	"github.com/derekparker/delve/pkg/proc/gdbserial"
	"github.com/derekparker/delve/pkg/proc/native"
	protest "github.com/derekparker/delve/pkg/proc/test"
)

var normalLoadConfig = proc.LoadConfig{true, 1, 64, 64, -1}
var testBackend string

func init() {
	runtime.GOMAXPROCS(4)
	os.Setenv("GOMAXPROCS", "4")
}

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

func withTestProcess(name string, t testing.TB, fn func(p proc.Process, fixture protest.Fixture)) {
	fixture := protest.BuildFixture(name)
	var p proc.Process
	var err error
	var tracedir string
	switch testBackend {
	case "native":
		p, err = native.Launch([]string{fixture.Path}, ".")
	case "lldb":
		p, err = gdbserial.LLDBLaunch([]string{fixture.Path}, ".")
	case "rr":
		protest.MustHaveRecordingAllowed(t)
		t.Log("recording")
		p, tracedir, err = gdbserial.RecordAndReplay([]string{fixture.Path}, ".", true)
		t.Logf("replaying %q", tracedir)
	default:
		t.Fatalf("unknown backend %q", testBackend)
	}
	if err != nil {
		t.Fatal("Launch():", err)
	}

	defer func() {
		p.Halt()
		p.Detach(true)
		if tracedir != "" {
			protest.SafeRemoveAll(tracedir)
		}
	}()

	fn(p, fixture)
}

func withTestProcessArgs(name string, t testing.TB, wd string, fn func(p proc.Process, fixture protest.Fixture), args []string) {
	fixture := protest.BuildFixture(name)
	var p proc.Process
	var err error
	var tracedir string

	switch testBackend {
	case "native":
		p, err = native.Launch(append([]string{fixture.Path}, args...), wd)
	case "lldb":
		p, err = gdbserial.LLDBLaunch(append([]string{fixture.Path}, args...), wd)
	case "rr":
		protest.MustHaveRecordingAllowed(t)
		t.Log("recording")
		p, tracedir, err = gdbserial.RecordAndReplay([]string{fixture.Path}, wd, true)
		t.Logf("replaying %q", tracedir)
	default:
		t.Fatal("unknown backend")
	}
	if err != nil {
		t.Fatal("Launch():", err)
	}

	defer func() {
		p.Halt()
		p.Detach(true)
		if tracedir != "" {
			protest.SafeRemoveAll(tracedir)
		}
	}()

	fn(p, fixture)
}

func getRegisters(p proc.Process, t *testing.T) proc.Registers {
	regs, err := p.CurrentThread().Registers(false)
	if err != nil {
		t.Fatal("Registers():", err)
	}

	return regs
}

func dataAtAddr(thread proc.MemoryReadWriter, addr uint64) ([]byte, error) {
	data := make([]byte, 1)
	_, err := thread.ReadMemory(data, uintptr(addr))
	return data, err
}

func assertNoError(err error, t testing.TB, s string) {
	if err != nil {
		_, file, line, _ := runtime.Caller(1)
		fname := filepath.Base(file)
		t.Fatalf("failed assertion at %s:%d: %s - %s\n", fname, line, s, err)
	}
}

func currentPC(p proc.Process, t *testing.T) uint64 {
	regs, err := p.CurrentThread().Registers(false)
	if err != nil {
		t.Fatal(err)
	}

	return regs.PC()
}

func currentLineNumber(p proc.Process, t *testing.T) (string, int) {
	pc := currentPC(p, t)
	f, l, _ := p.BinInfo().PCToLine(pc)
	return f, l
}

func TestExit(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("continuetestprog", t, func(p proc.Process, fixture protest.Fixture) {
		err := proc.Continue(p)
		pe, ok := err.(proc.ProcessExitedError)
		if !ok {
			t.Fatalf("Continue() returned unexpected error type %s", err)
		}
		if pe.Status != 0 {
			t.Errorf("Unexpected error status: %d", pe.Status)
		}
		if pe.Pid != p.Pid() {
			t.Errorf("Unexpected process id: %d", pe.Pid)
		}
	})
}

func TestExitAfterContinue(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("continuetestprog", t, func(p proc.Process, fixture protest.Fixture) {
		_, err := setFunctionBreakpoint(p, "main.sayhi")
		assertNoError(err, t, "setFunctionBreakpoint()")
		assertNoError(proc.Continue(p), t, "First Continue()")
		err = proc.Continue(p)
		pe, ok := err.(proc.ProcessExitedError)
		if !ok {
			t.Fatalf("Continue() returned unexpected error type %s", pe)
		}
		if pe.Status != 0 {
			t.Errorf("Unexpected error status: %d", pe.Status)
		}
		if pe.Pid != p.Pid() {
			t.Errorf("Unexpected process id: %d", pe.Pid)
		}
	})
}

func setFunctionBreakpoint(p proc.Process, fname string) (*proc.Breakpoint, error) {
	addr, err := proc.FindFunctionLocation(p, fname, true, 0)
	if err != nil {
		return nil, err
	}
	return p.SetBreakpoint(addr, proc.UserBreakpoint, nil)
}

func setFileBreakpoint(p proc.Process, t *testing.T, fixture protest.Fixture, lineno int) *proc.Breakpoint {
	addr, err := proc.FindFileLocation(p, fixture.Source, lineno)
	if err != nil {
		t.Fatalf("FindFileLocation: %v", err)
	}
	bp, err := p.SetBreakpoint(addr, proc.UserBreakpoint, nil)
	if err != nil {
		t.Fatalf("SetBreakpoint: %v", err)
	}
	return bp
}

func TestHalt(t *testing.T) {
	stopChan := make(chan interface{})
	withTestProcess("loopprog", t, func(p proc.Process, fixture protest.Fixture) {
		_, err := setFunctionBreakpoint(p, "main.loop")
		assertNoError(err, t, "SetBreakpoint")
		assertNoError(proc.Continue(p), t, "Continue")
		if p.Running() {
			t.Fatal("process still running")
		}
		if p, ok := p.(*native.Process); ok {
			for _, th := range p.ThreadList() {
				_, err := th.Registers(false)
				assertNoError(err, t, "Registers")
			}
		}
		go func() {
			for {
				time.Sleep(100 * time.Millisecond)
				if p.Running() {
					if err := p.RequestManualStop(); err != nil {
						t.Fatal(err)
					}
					stopChan <- nil
					return
				}
			}
		}()
		assertNoError(proc.Continue(p), t, "Continue")
		<-stopChan
		// Loop through threads and make sure they are all
		// actually stopped, err will not be nil if the process
		// is still running.
		if p, ok := p.(*native.Process); ok {
			for _, th := range p.ThreadList() {
				if th, ok := th.(*native.Thread); ok {
					if !th.Stopped() {
						t.Fatal("expected thread to be stopped, but was not")
					}
				}
				_, err := th.Registers(false)
				assertNoError(err, t, "Registers")
			}
		}
	})
}

func TestStep(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("testprog", t, func(p proc.Process, fixture protest.Fixture) {
		helloworldaddr, err := proc.FindFunctionLocation(p, "main.helloworld", false, 0)
		assertNoError(err, t, "FindFunctionLocation")

		_, err = p.SetBreakpoint(helloworldaddr, proc.UserBreakpoint, nil)
		assertNoError(err, t, "SetBreakpoint()")
		assertNoError(proc.Continue(p), t, "Continue()")

		regs := getRegisters(p, t)
		rip := regs.PC()

		err = p.CurrentThread().StepInstruction()
		assertNoError(err, t, "Step()")

		regs = getRegisters(p, t)
		if rip >= regs.PC() {
			t.Errorf("Expected %#v to be greater than %#v", regs.PC(), rip)
		}
	})
}

func TestBreakpoint(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("testprog", t, func(p proc.Process, fixture protest.Fixture) {
		helloworldaddr, err := proc.FindFunctionLocation(p, "main.helloworld", false, 0)
		assertNoError(err, t, "FindFunctionLocation")

		bp, err := p.SetBreakpoint(helloworldaddr, proc.UserBreakpoint, nil)
		assertNoError(err, t, "SetBreakpoint()")
		assertNoError(proc.Continue(p), t, "Continue()")

		regs, err := p.CurrentThread().Registers(false)
		assertNoError(err, t, "Registers")
		pc := regs.PC()

		if bp.TotalHitCount != 1 {
			t.Fatalf("Breakpoint should be hit once, got %d\n", bp.TotalHitCount)
		}

		if pc-1 != bp.Addr && pc != bp.Addr {
			f, l, _ := p.BinInfo().PCToLine(pc)
			t.Fatalf("Break not respected:\nPC:%#v %s:%d\nFN:%#v \n", pc, f, l, bp.Addr)
		}
	})
}

func TestBreakpointInSeperateGoRoutine(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("testthreads", t, func(p proc.Process, fixture protest.Fixture) {
		fnentry, err := proc.FindFunctionLocation(p, "main.anotherthread", false, 0)
		assertNoError(err, t, "FindFunctionLocation")

		_, err = p.SetBreakpoint(fnentry, proc.UserBreakpoint, nil)
		assertNoError(err, t, "SetBreakpoint")

		assertNoError(proc.Continue(p), t, "Continue")

		regs, err := p.CurrentThread().Registers(false)
		assertNoError(err, t, "Registers")
		pc := regs.PC()

		f, l, _ := p.BinInfo().PCToLine(pc)
		if f != "testthreads.go" && l != 8 {
			t.Fatal("Program did not hit breakpoint")
		}
	})
}

func TestBreakpointWithNonExistantFunction(t *testing.T) {
	withTestProcess("testprog", t, func(p proc.Process, fixture protest.Fixture) {
		_, err := p.SetBreakpoint(0, proc.UserBreakpoint, nil)
		if err == nil {
			t.Fatal("Should not be able to break at non existant function")
		}
	})
}

func TestClearBreakpointBreakpoint(t *testing.T) {
	withTestProcess("testprog", t, func(p proc.Process, fixture protest.Fixture) {
		fnentry, err := proc.FindFunctionLocation(p, "main.sleepytime", false, 0)
		assertNoError(err, t, "FindFunctionLocation")
		bp, err := p.SetBreakpoint(fnentry, proc.UserBreakpoint, nil)
		assertNoError(err, t, "SetBreakpoint()")

		bp, err = p.ClearBreakpoint(fnentry)
		assertNoError(err, t, "ClearBreakpoint()")

		data, err := dataAtAddr(p.CurrentThread(), bp.Addr)
		assertNoError(err, t, "dataAtAddr")

		int3 := []byte{0xcc}
		if bytes.Equal(data, int3) {
			t.Fatalf("Breakpoint was not cleared data: %#v, int3: %#v", data, int3)
		}

		if countBreakpoints(p) != 0 {
			t.Fatal("Breakpoint not removed internally")
		}
	})
}

type nextTest struct {
	begin, end int
}

func countBreakpoints(p proc.Process) int {
	bpcount := 0
	for _, bp := range p.Breakpoints() {
		if bp.ID >= 0 {
			bpcount++
		}
	}
	return bpcount
}

type contFunc int

const (
	contNext contFunc = iota
	contStep
)

func testseq(program string, contFunc contFunc, testcases []nextTest, initialLocation string, t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess(program, t, func(p proc.Process, fixture protest.Fixture) {
		var bp *proc.Breakpoint
		var err error
		if initialLocation != "" {
			bp, err = setFunctionBreakpoint(p, initialLocation)
		} else {
			var pc uint64
			pc, err = proc.FindFileLocation(p, fixture.Source, testcases[0].begin)
			assertNoError(err, t, "FindFileLocation()")
			bp, err = p.SetBreakpoint(pc, proc.UserBreakpoint, nil)
		}
		assertNoError(err, t, "SetBreakpoint()")
		assertNoError(proc.Continue(p), t, "Continue()")
		p.ClearBreakpoint(bp.Addr)
		regs, err := p.CurrentThread().Registers(false)
		assertNoError(err, t, "Registers")
		if testBackend != "rr" {
			assertNoError(regs.SetPC(p.CurrentThread(), bp.Addr), t, "SetPC")
		}

		f, ln := currentLineNumber(p, t)
		for _, tc := range testcases {
			regs, _ := p.CurrentThread().Registers(false)
			pc := regs.PC()
			if ln != tc.begin {
				t.Fatalf("Program not stopped at correct spot expected %d was %s:%d", tc.begin, filepath.Base(f), ln)
			}

			switch contFunc {
			case contNext:
				assertNoError(proc.Next(p), t, "Next() returned an error")
			case contStep:
				assertNoError(proc.Step(p), t, "Step() returned an error")
			}

			f, ln = currentLineNumber(p, t)
			regs, _ = p.CurrentThread().Registers(false)
			pc = regs.PC()
			if ln != tc.end {
				t.Fatalf("Program did not continue to correct next location expected %d was %s:%d (%#x)", tc.end, filepath.Base(f), ln, pc)
			}
		}

		if countBreakpoints(p) != 0 {
			t.Fatal("Not all breakpoints were cleaned up", len(p.Breakpoints()))
		}
	})
}

func TestNextGeneral(t *testing.T) {
	var testcases []nextTest

	ver, _ := proc.ParseVersionString(runtime.Version())

	if ver.Major < 0 || ver.AfterOrEqual(proc.GoVersion{1, 7, -1, 0, 0, ""}) {
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

	testseq("testnextprog", contNext, testcases, "main.testnext", t)
}

func TestNextConcurrent(t *testing.T) {
	testcases := []nextTest{
		{8, 9},
		{9, 10},
		{10, 11},
	}
	protest.AllowRecording(t)
	withTestProcess("parallel_next", t, func(p proc.Process, fixture protest.Fixture) {
		bp, err := setFunctionBreakpoint(p, "main.sayhi")
		assertNoError(err, t, "SetBreakpoint")
		assertNoError(proc.Continue(p), t, "Continue")
		f, ln := currentLineNumber(p, t)
		initV, err := evalVariable(p, "n")
		initVval, _ := constant.Int64Val(initV.Value)
		assertNoError(err, t, "EvalVariable")
		_, err = p.ClearBreakpoint(bp.Addr)
		assertNoError(err, t, "ClearBreakpoint()")
		for _, tc := range testcases {
			g, err := proc.GetG(p.CurrentThread())
			assertNoError(err, t, "GetG()")
			if p.SelectedGoroutine().ID != g.ID {
				t.Fatalf("SelectedGoroutine not CurrentThread's goroutine: %d %d", g.ID, p.SelectedGoroutine().ID)
			}
			if ln != tc.begin {
				t.Fatalf("Program not stopped at correct spot expected %d was %s:%d", tc.begin, filepath.Base(f), ln)
			}
			assertNoError(proc.Next(p), t, "Next() returned an error")
			f, ln = currentLineNumber(p, t)
			if ln != tc.end {
				t.Fatalf("Program did not continue to correct next location expected %d was %s:%d", tc.end, filepath.Base(f), ln)
			}
			v, err := evalVariable(p, "n")
			assertNoError(err, t, "EvalVariable")
			vval, _ := constant.Int64Val(v.Value)
			if vval != initVval {
				t.Fatal("Did not end up on same goroutine")
			}
		}
	})
}

func TestNextConcurrentVariant2(t *testing.T) {
	// Just like TestNextConcurrent but instead of removing the initial breakpoint we check that when it happens is for other goroutines
	testcases := []nextTest{
		{8, 9},
		{9, 10},
		{10, 11},
	}
	protest.AllowRecording(t)
	withTestProcess("parallel_next", t, func(p proc.Process, fixture protest.Fixture) {
		_, err := setFunctionBreakpoint(p, "main.sayhi")
		assertNoError(err, t, "SetBreakpoint")
		assertNoError(proc.Continue(p), t, "Continue")
		f, ln := currentLineNumber(p, t)
		initV, err := evalVariable(p, "n")
		initVval, _ := constant.Int64Val(initV.Value)
		assertNoError(err, t, "EvalVariable")
		for _, tc := range testcases {
			t.Logf("test case %v", tc)
			g, err := proc.GetG(p.CurrentThread())
			assertNoError(err, t, "GetG()")
			if p.SelectedGoroutine().ID != g.ID {
				t.Fatalf("SelectedGoroutine not CurrentThread's goroutine: %d %d", g.ID, p.SelectedGoroutine().ID)
			}
			if ln != tc.begin {
				t.Fatalf("Program not stopped at correct spot expected %d was %s:%d", tc.begin, filepath.Base(f), ln)
			}
			assertNoError(proc.Next(p), t, "Next() returned an error")
			var vval int64
			for {
				v, err := evalVariable(p, "n")
				for _, thread := range p.ThreadList() {
					proc.GetG(thread)
				}
				assertNoError(err, t, "EvalVariable")
				vval, _ = constant.Int64Val(v.Value)
				if bp, _, _ := p.CurrentThread().Breakpoint(); bp == nil {
					if vval != initVval {
						t.Fatal("Did not end up on same goroutine")
					}
					break
				} else {
					if vval == initVval {
						t.Fatal("Initial breakpoint triggered twice for the same goroutine")
					}
					assertNoError(proc.Continue(p), t, "Continue 2")
				}
			}
			f, ln = currentLineNumber(p, t)
			if ln != tc.end {
				t.Fatalf("Program did not continue to correct next location expected %d was %s:%d", tc.end, filepath.Base(f), ln)
			}
		}
	})
}

func TestNextFunctionReturn(t *testing.T) {
	testcases := []nextTest{
		{13, 14},
		{14, 15},
		{15, 35},
	}
	protest.AllowRecording(t)
	testseq("testnextprog", contNext, testcases, "main.helloworld", t)
}

func TestNextFunctionReturnDefer(t *testing.T) {
	testcases := []nextTest{
		{5, 8},
		{8, 9},
		{9, 10},
		{10, 6},
		{6, 7},
		{7, 8},
	}
	protest.AllowRecording(t)
	testseq("testnextdefer", contNext, testcases, "main.main", t)
}

func TestNextNetHTTP(t *testing.T) {
	testcases := []nextTest{
		{11, 12},
		{12, 13},
	}
	withTestProcess("testnextnethttp", t, func(p proc.Process, fixture protest.Fixture) {
		go func() {
			for !p.Running() {
				time.Sleep(50 * time.Millisecond)
			}
			// Wait for program to start listening.
			for {
				conn, err := net.Dial("tcp", "localhost:9191")
				if err == nil {
					conn.Close()
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			http.Get("http://localhost:9191")
		}()
		if err := proc.Continue(p); err != nil {
			t.Fatal(err)
		}
		f, ln := currentLineNumber(p, t)
		for _, tc := range testcases {
			if ln != tc.begin {
				t.Fatalf("Program not stopped at correct spot expected %d was %s:%d", tc.begin, filepath.Base(f), ln)
			}

			assertNoError(proc.Next(p), t, "Next() returned an error")

			f, ln = currentLineNumber(p, t)
			if ln != tc.end {
				t.Fatalf("Program did not continue to correct next location expected %d was %s:%d", tc.end, filepath.Base(f), ln)
			}
		}
	})
}

func TestRuntimeBreakpoint(t *testing.T) {
	withTestProcess("testruntimebreakpoint", t, func(p proc.Process, fixture protest.Fixture) {
		err := proc.Continue(p)
		if err != nil {
			t.Fatal(err)
		}
		regs, err := p.CurrentThread().Registers(false)
		assertNoError(err, t, "Registers")
		pc := regs.PC()
		f, l, _ := p.BinInfo().PCToLine(pc)
		if l != 10 {
			t.Fatalf("did not respect breakpoint %s:%d", f, l)
		}
	})
}

func returnAddress(thread proc.Thread) (uint64, error) {
	locations, err := proc.ThreadStacktrace(thread, 2)
	if err != nil {
		return 0, err
	}
	if len(locations) < 2 {
		return 0, proc.NoReturnAddr{locations[0].Current.Fn.BaseName()}
	}
	return locations[1].Current.PC, nil
}

func TestFindReturnAddress(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("testnextprog", t, func(p proc.Process, fixture protest.Fixture) {
		start, _, err := p.BinInfo().LineToPC(fixture.Source, 24)
		if err != nil {
			t.Fatal(err)
		}
		_, err = p.SetBreakpoint(start, proc.UserBreakpoint, nil)
		if err != nil {
			t.Fatal(err)
		}
		err = proc.Continue(p)
		if err != nil {
			t.Fatal(err)
		}
		addr, err := returnAddress(p.CurrentThread())
		if err != nil {
			t.Fatal(err)
		}
		_, l, _ := p.BinInfo().PCToLine(addr)
		if l != 40 {
			t.Fatalf("return address not found correctly, expected line 40")
		}
	})
}

func TestFindReturnAddressTopOfStackFn(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("testreturnaddress", t, func(p proc.Process, fixture protest.Fixture) {
		fnName := "runtime.rt0_go"
		fnentry, err := proc.FindFunctionLocation(p, fnName, false, 0)
		assertNoError(err, t, "FindFunctionLocation")
		if _, err := p.SetBreakpoint(fnentry, proc.UserBreakpoint, nil); err != nil {
			t.Fatal(err)
		}
		if err := proc.Continue(p); err != nil {
			t.Fatal(err)
		}
		if _, err := returnAddress(p.CurrentThread()); err == nil {
			t.Fatal("expected error to be returned")
		}
	})
}

func TestSwitchThread(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("testnextprog", t, func(p proc.Process, fixture protest.Fixture) {
		// With invalid thread id
		err := p.SwitchThread(-1)
		if err == nil {
			t.Fatal("Expected error for invalid thread id")
		}
		pc, err := proc.FindFunctionLocation(p, "main.main", true, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, err = p.SetBreakpoint(pc, proc.UserBreakpoint, nil)
		if err != nil {
			t.Fatal(err)
		}
		err = proc.Continue(p)
		if err != nil {
			t.Fatal(err)
		}
		var nt int
		ct := p.CurrentThread().ThreadID()
		for _, thread := range p.ThreadList() {
			if thread.ThreadID() != ct {
				nt = thread.ThreadID()
				break
			}
		}
		if nt == 0 {
			t.Fatal("could not find thread to switch to")
		}
		// With valid thread id
		err = p.SwitchThread(nt)
		if err != nil {
			t.Fatal(err)
		}
		if p.CurrentThread().ThreadID() != nt {
			t.Fatal("Did not switch threads")
		}
	})
}

func TestCGONext(t *testing.T) {
	// Test if one can do 'next' in a cgo binary
	// On OSX with Go < 1.5 CGO is not supported due to: https://github.com/golang/go/issues/8973
	if runtime.GOOS == "darwin" && strings.Contains(runtime.Version(), "1.4") {
		return
	}
	if os.Getenv("CGO_ENABLED") == "" {
		return
	}

	protest.AllowRecording(t)
	withTestProcess("cgotest", t, func(p proc.Process, fixture protest.Fixture) {
		pc, err := proc.FindFunctionLocation(p, "main.main", true, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, err = p.SetBreakpoint(pc, proc.UserBreakpoint, nil)
		if err != nil {
			t.Fatal(err)
		}
		err = proc.Continue(p)
		if err != nil {
			t.Fatal(err)
		}
		err = proc.Next(p)
		if err != nil {
			t.Fatal(err)
		}
	})
}

type loc struct {
	line int
	fn   string
}

func (l1 *loc) match(l2 proc.Stackframe) bool {
	if l1.line >= 0 {
		if l1.line != l2.Call.Line {
			return false
		}
	}
	return l1.fn == l2.Call.Fn.Name
}

func TestStacktrace(t *testing.T) {
	stacks := [][]loc{
		{{4, "main.stacktraceme"}, {8, "main.func1"}, {16, "main.main"}},
		{{4, "main.stacktraceme"}, {8, "main.func1"}, {12, "main.func2"}, {17, "main.main"}},
	}
	protest.AllowRecording(t)
	withTestProcess("stacktraceprog", t, func(p proc.Process, fixture protest.Fixture) {
		bp, err := setFunctionBreakpoint(p, "main.stacktraceme")
		assertNoError(err, t, "BreakByLocation()")

		for i := range stacks {
			assertNoError(proc.Continue(p), t, "Continue()")
			locations, err := proc.ThreadStacktrace(p.CurrentThread(), 40)
			assertNoError(err, t, "Stacktrace()")

			if len(locations) != len(stacks[i])+2 {
				t.Fatalf("Wrong stack trace size %d %d\n", len(locations), len(stacks[i])+2)
			}

			t.Logf("Stacktrace %d:\n", i)
			for i := range locations {
				t.Logf("\t%s:%d\n", locations[i].Call.File, locations[i].Call.Line)
			}

			for j := range stacks[i] {
				if !stacks[i][j].match(locations[j]) {
					t.Fatalf("Wrong stack trace pos %d\n", j)
				}
			}
		}

		p.ClearBreakpoint(bp.Addr)
		proc.Continue(p)
	})
}

func TestStacktrace2(t *testing.T) {
	withTestProcess("retstack", t, func(p proc.Process, fixture protest.Fixture) {
		assertNoError(proc.Continue(p), t, "Continue()")

		locations, err := proc.ThreadStacktrace(p.CurrentThread(), 40)
		assertNoError(err, t, "Stacktrace()")
		if !stackMatch([]loc{{-1, "main.f"}, {16, "main.main"}}, locations, false) {
			for i := range locations {
				t.Logf("\t%s:%d [%s]\n", locations[i].Call.File, locations[i].Call.Line, locations[i].Call.Fn.Name)
			}
			t.Fatalf("Stack error at main.f()\n%v\n", locations)
		}

		assertNoError(proc.Continue(p), t, "Continue()")
		locations, err = proc.ThreadStacktrace(p.CurrentThread(), 40)
		assertNoError(err, t, "Stacktrace()")
		if !stackMatch([]loc{{-1, "main.g"}, {17, "main.main"}}, locations, false) {
			for i := range locations {
				t.Logf("\t%s:%d [%s]\n", locations[i].Call.File, locations[i].Call.Line, locations[i].Call.Fn.Name)
			}
			t.Fatalf("Stack error at main.g()\n%v\n", locations)
		}
	})

}

func stackMatch(stack []loc, locations []proc.Stackframe, skipRuntime bool) bool {
	if len(stack) > len(locations) {
		return false
	}
	i := 0
	for j := range locations {
		if i >= len(stack) {
			break
		}
		if skipRuntime {
			if locations[j].Call.Fn == nil || strings.HasPrefix(locations[j].Call.Fn.Name, "runtime.") {
				continue
			}
		}
		if !stack[i].match(locations[j]) {
			return false
		}
		i++
	}
	return i >= len(stack)
}

func TestStacktraceGoroutine(t *testing.T) {
	mainStack := []loc{{13, "main.stacktraceme"}, {26, "main.main"}}
	agoroutineStacks := [][]loc{[]loc{{8, "main.agoroutine"}}, []loc{{9, "main.agoroutine"}}, []loc{{10, "main.agoroutine"}}}

	protest.AllowRecording(t)
	withTestProcess("goroutinestackprog", t, func(p proc.Process, fixture protest.Fixture) {
		bp, err := setFunctionBreakpoint(p, "main.stacktraceme")
		assertNoError(err, t, "BreakByLocation()")

		assertNoError(proc.Continue(p), t, "Continue()")

		gs, err := proc.GoroutinesInfo(p)
		assertNoError(err, t, "GoroutinesInfo")

		agoroutineCount := 0
		mainCount := 0

		for i, g := range gs {
			locations, err := g.Stacktrace(40)
			if err != nil {
				// On windows we do not have frame information for goroutines doing system calls.
				t.Logf("Could not retrieve goroutine stack for goid=%d: %v", g.ID, err)
				continue
			}

			if stackMatch(mainStack, locations, false) {
				mainCount++
			}

			found := false
			for _, agoroutineStack := range agoroutineStacks {
				if stackMatch(agoroutineStack, locations, true) {
					found = true
				}
			}

			if found {
				agoroutineCount++
			} else {
				t.Logf("Non-goroutine stack: %d (%d)", i, len(locations))
				for i := range locations {
					name := ""
					if locations[i].Call.Fn != nil {
						name = locations[i].Call.Fn.Name
					}
					t.Logf("\t%s:%d %s\n", locations[i].Call.File, locations[i].Call.Line, name)
				}
			}
		}

		if mainCount != 1 {
			t.Fatalf("Main goroutine stack not found %d", mainCount)
		}

		if agoroutineCount != 10 {
			t.Fatalf("Goroutine stacks not found (%d)", agoroutineCount)
		}

		p.ClearBreakpoint(bp.Addr)
		proc.Continue(p)
	})
}

func TestKill(t *testing.T) {
	if testBackend == "lldb" {
		// k command presumably works but leaves the process around?
		return
	}
	withTestProcess("testprog", t, func(p proc.Process, fixture protest.Fixture) {
		if err := p.Kill(); err != nil {
			t.Fatal(err)
		}
		if p.Exited() != true {
			t.Fatal("expected process to have exited")
		}
		if runtime.GOOS == "linux" {
			_, err := os.Open(fmt.Sprintf("/proc/%d/", p.Pid()))
			if err == nil {
				t.Fatal("process has not exited", p.Pid())
			}
		}
	})
	withTestProcess("testprog", t, func(p proc.Process, fixture protest.Fixture) {
		if err := p.Detach(true); err != nil {
			t.Fatal(err)
		}
		if p.Exited() != true {
			t.Fatal("expected process to have exited")
		}
		if runtime.GOOS == "linux" {
			_, err := os.Open(fmt.Sprintf("/proc/%d/", p.Pid()))
			if err == nil {
				t.Fatal("process has not exited", p.Pid())
			}
		}
	})
}

func testGSupportFunc(name string, t *testing.T, p proc.Process, fixture protest.Fixture) {
	bp, err := setFunctionBreakpoint(p, "main.main")
	assertNoError(err, t, name+": BreakByLocation()")

	assertNoError(proc.Continue(p), t, name+": Continue()")

	g, err := proc.GetG(p.CurrentThread())
	assertNoError(err, t, name+": GetG()")

	if g == nil {
		t.Fatal(name + ": g was nil")
	}

	t.Logf(name+": g is: %v", g)

	p.ClearBreakpoint(bp.Addr)
}

func TestGetG(t *testing.T) {
	withTestProcess("testprog", t, func(p proc.Process, fixture protest.Fixture) {
		testGSupportFunc("nocgo", t, p, fixture)
	})

	// On OSX with Go < 1.5 CGO is not supported due to: https://github.com/golang/go/issues/8973
	if runtime.GOOS == "darwin" && strings.Contains(runtime.Version(), "1.4") {
		return
	}
	if os.Getenv("CGO_ENABLED") == "" {
		return
	}

	protest.AllowRecording(t)
	withTestProcess("cgotest", t, func(p proc.Process, fixture protest.Fixture) {
		testGSupportFunc("cgo", t, p, fixture)
	})
}

func TestContinueMulti(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("integrationprog", t, func(p proc.Process, fixture protest.Fixture) {
		bp1, err := setFunctionBreakpoint(p, "main.main")
		assertNoError(err, t, "BreakByLocation()")

		bp2, err := setFunctionBreakpoint(p, "main.sayhi")
		assertNoError(err, t, "BreakByLocation()")

		mainCount := 0
		sayhiCount := 0
		for {
			err := proc.Continue(p)
			if p.Exited() {
				break
			}
			assertNoError(err, t, "Continue()")

			if bp, _, _ := p.CurrentThread().Breakpoint(); bp.ID == bp1.ID {
				mainCount++
			}

			if bp, _, _ := p.CurrentThread().Breakpoint(); bp.ID == bp2.ID {
				sayhiCount++
			}
		}

		if mainCount != 1 {
			t.Fatalf("Main breakpoint hit wrong number of times: %d\n", mainCount)
		}

		if sayhiCount != 3 {
			t.Fatalf("Sayhi breakpoint hit wrong number of times: %d\n", sayhiCount)
		}
	})
}

func versionAfterOrEqual(t *testing.T, verStr string, ver proc.GoVersion) {
	pver, ok := proc.ParseVersionString(verStr)
	if !ok {
		t.Fatalf("Could not parse version string <%s>", verStr)
	}
	if !pver.AfterOrEqual(ver) {
		t.Fatalf("Version <%s> parsed as %v not after %v", verStr, pver, ver)
	}
	t.Logf("version string <%s> → %v", verStr, ver)
}

func TestParseVersionString(t *testing.T) {
	versionAfterOrEqual(t, "go1.4", proc.GoVersion{1, 4, 0, 0, 0, ""})
	versionAfterOrEqual(t, "go1.5.0", proc.GoVersion{1, 5, 0, 0, 0, ""})
	versionAfterOrEqual(t, "go1.4.2", proc.GoVersion{1, 4, 2, 0, 0, ""})
	versionAfterOrEqual(t, "go1.5beta2", proc.GoVersion{1, 5, -1, 2, 0, ""})
	versionAfterOrEqual(t, "go1.5rc2", proc.GoVersion{1, 5, -1, 0, 2, ""})
	versionAfterOrEqual(t, "go1.6.1 (appengine-1.9.37)", proc.GoVersion{1, 6, 1, 0, 0, ""})
	versionAfterOrEqual(t, "go1.8.1.typealias", proc.GoVersion{1, 6, 1, 0, 0, ""})
	ver, ok := proc.ParseVersionString("devel +17efbfc Tue Jul 28 17:39:19 2015 +0000 linux/amd64")
	if !ok {
		t.Fatalf("Could not parse devel version string")
	}
	if !ver.IsDevel() {
		t.Fatalf("Devel version string not correctly recognized")
	}
}

func TestBreakpointOnFunctionEntry(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("testprog", t, func(p proc.Process, fixture protest.Fixture) {
		addr, err := proc.FindFunctionLocation(p, "main.main", false, 0)
		assertNoError(err, t, "FindFunctionLocation()")
		_, err = p.SetBreakpoint(addr, proc.UserBreakpoint, nil)
		assertNoError(err, t, "SetBreakpoint()")
		assertNoError(proc.Continue(p), t, "Continue()")
		_, ln := currentLineNumber(p, t)
		if ln != 17 {
			t.Fatalf("Wrong line number: %d (expected: 17)\n", ln)
		}
	})
}

func TestProcessReceivesSIGCHLD(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("sigchldprog", t, func(p proc.Process, fixture protest.Fixture) {
		err := proc.Continue(p)
		_, ok := err.(proc.ProcessExitedError)
		if !ok {
			t.Fatalf("Continue() returned unexpected error type %v", err)
		}
	})
}

func TestIssue239(t *testing.T) {
	withTestProcess("is sue239", t, func(p proc.Process, fixture protest.Fixture) {
		pos, _, err := p.BinInfo().LineToPC(fixture.Source, 17)
		assertNoError(err, t, "LineToPC()")
		_, err = p.SetBreakpoint(pos, proc.UserBreakpoint, nil)
		assertNoError(err, t, fmt.Sprintf("SetBreakpoint(%d)", pos))
		assertNoError(proc.Continue(p), t, fmt.Sprintf("Continue()"))
	})
}

func findFirstNonRuntimeFrame(p proc.Process) (proc.Stackframe, error) {
	frames, err := proc.ThreadStacktrace(p.CurrentThread(), 10)
	if err != nil {
		return proc.Stackframe{}, err
	}

	for _, frame := range frames {
		if frame.Current.Fn != nil && !strings.HasPrefix(frame.Current.Fn.Name, "runtime.") {
			return frame, nil
		}
	}
	return proc.Stackframe{}, fmt.Errorf("non-runtime frame not found")
}

func evalVariable(p proc.Process, symbol string) (*proc.Variable, error) {
	var scope *proc.EvalScope
	var err error

	if testBackend == "rr" {
		var frame proc.Stackframe
		frame, err = findFirstNonRuntimeFrame(p)
		if err == nil {
			scope = proc.FrameToScope(p, frame)
		}
	} else {
		scope, err = proc.GoroutineScope(p.CurrentThread())
	}

	if err != nil {
		return nil, err
	}
	return scope.EvalVariable(symbol, normalLoadConfig)
}

func setVariable(p proc.Process, symbol, value string) error {
	scope, err := proc.GoroutineScope(p.CurrentThread())
	if err != nil {
		return err
	}
	return scope.SetVariable(symbol, value)
}

func TestVariableEvaluation(t *testing.T) {
	protest.AllowRecording(t)
	testcases := []struct {
		name        string
		st          reflect.Kind
		value       interface{}
		length, cap int64
		childrenlen int
	}{
		{"a1", reflect.String, "foofoofoofoofoofoo", 18, 0, 0},
		{"a11", reflect.Array, nil, 3, -1, 3},
		{"a12", reflect.Slice, nil, 2, 2, 2},
		{"a13", reflect.Slice, nil, 3, 3, 3},
		{"a2", reflect.Int, int64(6), 0, 0, 0},
		{"a3", reflect.Float64, float64(7.23), 0, 0, 0},
		{"a4", reflect.Array, nil, 2, -1, 2},
		{"a5", reflect.Slice, nil, 5, 5, 5},
		{"a6", reflect.Struct, nil, 2, 0, 2},
		{"a7", reflect.Ptr, nil, 1, 0, 1},
		{"a8", reflect.Struct, nil, 2, 0, 2},
		{"a9", reflect.Ptr, nil, 1, 0, 1},
		{"baz", reflect.String, "bazburzum", 9, 0, 0},
		{"neg", reflect.Int, int64(-1), 0, 0, 0},
		{"f32", reflect.Float32, float64(float32(1.2)), 0, 0, 0},
		{"c64", reflect.Complex64, complex128(complex64(1 + 2i)), 0, 0, 0},
		{"c128", reflect.Complex128, complex128(2 + 3i), 0, 0, 0},
		{"a6.Baz", reflect.Int, int64(8), 0, 0, 0},
		{"a7.Baz", reflect.Int, int64(5), 0, 0, 0},
		{"a8.Baz", reflect.String, "feh", 3, 0, 0},
		{"a8", reflect.Struct, nil, 2, 0, 2},
		{"i32", reflect.Array, nil, 2, -1, 2},
		{"b1", reflect.Bool, true, 0, 0, 0},
		{"b2", reflect.Bool, false, 0, 0, 0},
		{"f", reflect.Func, "main.barfoo", 0, 0, 0},
		{"ba", reflect.Slice, nil, 200, 200, 64},
	}

	withTestProcess("testvariables", t, func(p proc.Process, fixture protest.Fixture) {
		assertNoError(proc.Continue(p), t, "Continue() returned an error")

		for _, tc := range testcases {
			v, err := evalVariable(p, tc.name)
			assertNoError(err, t, fmt.Sprintf("EvalVariable(%s)", tc.name))

			if v.Kind != tc.st {
				t.Fatalf("%s simple type: expected: %s got: %s", tc.name, tc.st, v.Kind.String())
			}
			if v.Value == nil && tc.value != nil {
				t.Fatalf("%s value: expected: %v got: %v", tc.name, tc.value, v.Value)
			} else {
				switch v.Kind {
				case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
					x, _ := constant.Int64Val(v.Value)
					if y, ok := tc.value.(int64); !ok || x != y {
						t.Fatalf("%s value: expected: %v got: %v", tc.name, tc.value, v.Value)
					}
				case reflect.Float32, reflect.Float64:
					x, _ := constant.Float64Val(v.Value)
					if y, ok := tc.value.(float64); !ok || x != y {
						t.Fatalf("%s value: expected: %v got: %v", tc.name, tc.value, v.Value)
					}
				case reflect.Complex64, reflect.Complex128:
					xr, _ := constant.Float64Val(constant.Real(v.Value))
					xi, _ := constant.Float64Val(constant.Imag(v.Value))
					if y, ok := tc.value.(complex128); !ok || complex(xr, xi) != y {
						t.Fatalf("%s value: expected: %v got: %v", tc.name, tc.value, v.Value)
					}
				case reflect.String:
					if y, ok := tc.value.(string); !ok || constant.StringVal(v.Value) != y {
						t.Fatalf("%s value: expected: %v got: %v", tc.name, tc.value, v.Value)
					}
				}
			}
			if v.Len != tc.length {
				t.Fatalf("%s len: expected: %d got: %d", tc.name, tc.length, v.Len)
			}
			if v.Cap != tc.cap {
				t.Fatalf("%s cap: expected: %d got: %d", tc.name, tc.cap, v.Cap)
			}
			if len(v.Children) != tc.childrenlen {
				t.Fatalf("%s children len: expected %d got: %d", tc.name, tc.childrenlen, len(v.Children))
			}
		}
	})
}

func TestFrameEvaluation(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("goroutinestackprog", t, func(p proc.Process, fixture protest.Fixture) {
		_, err := setFunctionBreakpoint(p, "main.stacktraceme")
		assertNoError(err, t, "setFunctionBreakpoint")
		assertNoError(proc.Continue(p), t, "Continue()")

		// Testing evaluation on goroutines
		gs, err := proc.GoroutinesInfo(p)
		assertNoError(err, t, "GoroutinesInfo")
		found := make([]bool, 10)
		for _, g := range gs {
			frame := -1
			frames, err := g.Stacktrace(10)
			if err != nil {
				t.Logf("could not stacktrace goroutine %d: %v\n", g.ID, err)
				continue
			}
			for i := range frames {
				if frames[i].Call.Fn != nil && frames[i].Call.Fn.Name == "main.agoroutine" {
					frame = i
					break
				}
			}

			if frame < 0 {
				t.Logf("Goroutine %d: could not find correct frame", g.ID)
				continue
			}

			scope, err := proc.ConvertEvalScope(p, g.ID, frame)
			assertNoError(err, t, "ConvertEvalScope()")
			t.Logf("scope = %v", scope)
			v, err := scope.EvalVariable("i", normalLoadConfig)
			t.Logf("v = %v", v)
			if err != nil {
				t.Logf("Goroutine %d: %v\n", g.ID, err)
				continue
			}
			vval, _ := constant.Int64Val(v.Value)
			found[vval] = true
		}

		for i := range found {
			if !found[i] {
				t.Fatalf("Goroutine %d not found\n", i)
			}
		}

		// Testing evaluation on frames
		assertNoError(proc.Continue(p), t, "Continue() 2")
		g, err := proc.GetG(p.CurrentThread())
		assertNoError(err, t, "GetG()")

		for i := 0; i <= 3; i++ {
			scope, err := proc.ConvertEvalScope(p, g.ID, i+1)
			assertNoError(err, t, fmt.Sprintf("ConvertEvalScope() on frame %d", i+1))
			v, err := scope.EvalVariable("n", normalLoadConfig)
			assertNoError(err, t, fmt.Sprintf("EvalVariable() on frame %d", i+1))
			n, _ := constant.Int64Val(v.Value)
			t.Logf("frame %d n %d\n", i+1, n)
			if n != int64(3-i) {
				t.Fatalf("On frame %d value of n is %d (not %d)", i+1, n, 3-i)
			}
		}
	})
}

func TestPointerSetting(t *testing.T) {
	withTestProcess("testvariables2", t, func(p proc.Process, fixture protest.Fixture) {
		assertNoError(proc.Continue(p), t, "Continue() returned an error")

		pval := func(n int64) {
			variable, err := evalVariable(p, "p1")
			assertNoError(err, t, "EvalVariable()")
			c0val, _ := constant.Int64Val(variable.Children[0].Value)
			if c0val != n {
				t.Fatalf("Wrong value of p1, *%d expected *%d", c0val, n)
			}
		}

		pval(1)

		// change p1 to point to i2
		scope, err := proc.GoroutineScope(p.CurrentThread())
		assertNoError(err, t, "Scope()")
		i2addr, err := scope.EvalExpression("i2", normalLoadConfig)
		assertNoError(err, t, "EvalExpression()")
		assertNoError(setVariable(p, "p1", fmt.Sprintf("(*int)(0x%x)", i2addr.Addr)), t, "SetVariable()")
		pval(2)

		// change the value of i2 check that p1 also changes
		assertNoError(setVariable(p, "i2", "5"), t, "SetVariable()")
		pval(5)
	})
}

func TestVariableFunctionScoping(t *testing.T) {
	withTestProcess("testvariables", t, func(p proc.Process, fixture protest.Fixture) {
		err := proc.Continue(p)
		assertNoError(err, t, "Continue() returned an error")

		_, err = evalVariable(p, "a1")
		assertNoError(err, t, "Unable to find variable a1")

		_, err = evalVariable(p, "a2")
		assertNoError(err, t, "Unable to find variable a1")

		// Move scopes, a1 exists here by a2 does not
		err = proc.Continue(p)
		assertNoError(err, t, "Continue() returned an error")

		_, err = evalVariable(p, "a1")
		assertNoError(err, t, "Unable to find variable a1")

		_, err = evalVariable(p, "a2")
		if err == nil {
			t.Fatalf("Can eval out of scope variable a2")
		}
	})
}

func TestRecursiveStructure(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("testvariables2", t, func(p proc.Process, fixture protest.Fixture) {
		assertNoError(proc.Continue(p), t, "Continue()")
		v, err := evalVariable(p, "aas")
		assertNoError(err, t, "EvalVariable()")
		t.Logf("v: %v\n", v)
	})
}

func TestIssue316(t *testing.T) {
	// A pointer loop that includes one interface should not send dlv into an infinite loop
	protest.AllowRecording(t)
	withTestProcess("testvariables2", t, func(p proc.Process, fixture protest.Fixture) {
		assertNoError(proc.Continue(p), t, "Continue()")
		_, err := evalVariable(p, "iface5")
		assertNoError(err, t, "EvalVariable()")
	})
}

func TestIssue325(t *testing.T) {
	// nil pointer dereference when evaluating interfaces to function pointers
	protest.AllowRecording(t)
	withTestProcess("testvariables2", t, func(p proc.Process, fixture protest.Fixture) {
		assertNoError(proc.Continue(p), t, "Continue()")
		iface2fn1v, err := evalVariable(p, "iface2fn1")
		assertNoError(err, t, "EvalVariable()")
		t.Logf("iface2fn1: %v\n", iface2fn1v)

		iface2fn2v, err := evalVariable(p, "iface2fn2")
		assertNoError(err, t, "EvalVariable()")
		t.Logf("iface2fn2: %v\n", iface2fn2v)
	})
}

func TestBreakpointCounts(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("bpcountstest", t, func(p proc.Process, fixture protest.Fixture) {
		addr, _, err := p.BinInfo().LineToPC(fixture.Source, 12)
		assertNoError(err, t, "LineToPC")
		bp, err := p.SetBreakpoint(addr, proc.UserBreakpoint, nil)
		assertNoError(err, t, "SetBreakpoint()")

		for {
			if err := proc.Continue(p); err != nil {
				if _, exited := err.(proc.ProcessExitedError); exited {
					break
				}
				assertNoError(err, t, "Continue()")
			}
		}

		t.Logf("TotalHitCount: %d", bp.TotalHitCount)
		if bp.TotalHitCount != 200 {
			t.Fatalf("Wrong TotalHitCount for the breakpoint (%d)", bp.TotalHitCount)
		}

		if len(bp.HitCount) != 2 {
			t.Fatalf("Wrong number of goroutines for breakpoint (%d)", len(bp.HitCount))
		}

		for _, v := range bp.HitCount {
			if v != 100 {
				t.Fatalf("Wrong HitCount for breakpoint (%v)", bp.HitCount)
			}
		}
	})
}

func BenchmarkArray(b *testing.B) {
	// each bencharr struct is 128 bytes, bencharr is 64 elements long
	protest.AllowRecording(b)
	b.SetBytes(int64(64 * 128))
	withTestProcess("testvariables2", b, func(p proc.Process, fixture protest.Fixture) {
		assertNoError(proc.Continue(p), b, "Continue()")
		for i := 0; i < b.N; i++ {
			_, err := evalVariable(p, "bencharr")
			assertNoError(err, b, "EvalVariable()")
		}
	})
}

const doTestBreakpointCountsWithDetection = false

func TestBreakpointCountsWithDetection(t *testing.T) {
	if !doTestBreakpointCountsWithDetection {
		return
	}
	m := map[int64]int64{}
	protest.AllowRecording(t)
	withTestProcess("bpcountstest", t, func(p proc.Process, fixture protest.Fixture) {
		addr, _, err := p.BinInfo().LineToPC(fixture.Source, 12)
		assertNoError(err, t, "LineToPC")
		bp, err := p.SetBreakpoint(addr, proc.UserBreakpoint, nil)
		assertNoError(err, t, "SetBreakpoint()")

		for {
			if err := proc.Continue(p); err != nil {
				if _, exited := err.(proc.ProcessExitedError); exited {
					break
				}
				assertNoError(err, t, "Continue()")
			}
			for _, th := range p.ThreadList() {
				if bp, _, _ := th.Breakpoint(); bp == nil {
					continue
				}
				scope, err := proc.GoroutineScope(th)
				assertNoError(err, t, "Scope()")
				v, err := scope.EvalVariable("i", normalLoadConfig)
				assertNoError(err, t, "evalVariable")
				i, _ := constant.Int64Val(v.Value)
				v, err = scope.EvalVariable("id", normalLoadConfig)
				assertNoError(err, t, "evalVariable")
				id, _ := constant.Int64Val(v.Value)
				m[id] = i
			}

			total := int64(0)
			for i := range m {
				total += m[i] + 1
			}

			if uint64(total) != bp.TotalHitCount {
				t.Fatalf("Mismatched total count %d %d\n", total, bp.TotalHitCount)
			}
		}

		t.Logf("TotalHitCount: %d", bp.TotalHitCount)
		if bp.TotalHitCount != 200 {
			t.Fatalf("Wrong TotalHitCount for the breakpoint (%d)", bp.TotalHitCount)
		}

		if len(bp.HitCount) != 2 {
			t.Fatalf("Wrong number of goroutines for breakpoint (%d)", len(bp.HitCount))
		}

		for _, v := range bp.HitCount {
			if v != 100 {
				t.Fatalf("Wrong HitCount for breakpoint (%v)", bp.HitCount)
			}
		}
	})
}

func BenchmarkArrayPointer(b *testing.B) {
	// each bencharr struct is 128 bytes, benchparr is an array of 64 pointers to bencharr
	// each read will read 64 bencharr structs plus the 64 pointers of benchparr
	protest.AllowRecording(b)
	b.SetBytes(int64(64*128 + 64*8))
	withTestProcess("testvariables2", b, func(p proc.Process, fixture protest.Fixture) {
		assertNoError(proc.Continue(p), b, "Continue()")
		for i := 0; i < b.N; i++ {
			_, err := evalVariable(p, "bencharr")
			assertNoError(err, b, "EvalVariable()")
		}
	})
}

func BenchmarkMap(b *testing.B) {
	// m1 contains 41 entries, each one has a value that's 2 int values (2* 8 bytes) and a string key
	// each string key has an average of 9 character
	// reading strings and the map structure imposes a overhead that we ignore here
	protest.AllowRecording(b)
	b.SetBytes(int64(41 * (2*8 + 9)))
	withTestProcess("testvariables2", b, func(p proc.Process, fixture protest.Fixture) {
		assertNoError(proc.Continue(p), b, "Continue()")
		for i := 0; i < b.N; i++ {
			_, err := evalVariable(p, "m1")
			assertNoError(err, b, "EvalVariable()")
		}
	})
}

func BenchmarkGoroutinesInfo(b *testing.B) {
	protest.AllowRecording(b)
	withTestProcess("testvariables2", b, func(p proc.Process, fixture protest.Fixture) {
		assertNoError(proc.Continue(p), b, "Continue()")
		for i := 0; i < b.N; i++ {
			if p, ok := p.(proc.AllGCache); ok {
				allgcache := p.AllGCache()
				*allgcache = nil
			}
			_, err := proc.GoroutinesInfo(p)
			assertNoError(err, b, "GoroutinesInfo")
		}
	})
}

func TestIssue262(t *testing.T) {
	// Continue does not work when the current breakpoint is set on a NOP instruction
	protest.AllowRecording(t)
	withTestProcess("issue262", t, func(p proc.Process, fixture protest.Fixture) {
		addr, _, err := p.BinInfo().LineToPC(fixture.Source, 11)
		assertNoError(err, t, "LineToPC")
		_, err = p.SetBreakpoint(addr, proc.UserBreakpoint, nil)
		assertNoError(err, t, "SetBreakpoint()")

		assertNoError(proc.Continue(p), t, "Continue()")
		err = proc.Continue(p)
		if err == nil {
			t.Fatalf("No error on second continue")
		}
		_, exited := err.(proc.ProcessExitedError)
		if !exited {
			t.Fatalf("Process did not exit after second continue: %v", err)
		}
	})
}

func TestIssue305(t *testing.T) {
	// If 'next' hits a breakpoint on the goroutine it's stepping through
	// the internal breakpoints aren't cleared preventing further use of
	// 'next' command
	protest.AllowRecording(t)
	withTestProcess("issue305", t, func(p proc.Process, fixture protest.Fixture) {
		addr, _, err := p.BinInfo().LineToPC(fixture.Source, 5)
		assertNoError(err, t, "LineToPC()")
		_, err = p.SetBreakpoint(addr, proc.UserBreakpoint, nil)
		assertNoError(err, t, "SetBreakpoint()")

		assertNoError(proc.Continue(p), t, "Continue()")

		assertNoError(proc.Next(p), t, "Next() 1")
		assertNoError(proc.Next(p), t, "Next() 2")
		assertNoError(proc.Next(p), t, "Next() 3")
		assertNoError(proc.Next(p), t, "Next() 4")
		assertNoError(proc.Next(p), t, "Next() 5")
	})
}

func TestPointerLoops(t *testing.T) {
	// Pointer loops through map entries, pointers and slices
	// Regression test for issue #341
	protest.AllowRecording(t)
	withTestProcess("testvariables2", t, func(p proc.Process, fixture protest.Fixture) {
		assertNoError(proc.Continue(p), t, "Continue()")
		for _, expr := range []string{"mapinf", "ptrinf", "sliceinf"} {
			t.Logf("requesting %s", expr)
			v, err := evalVariable(p, expr)
			assertNoError(err, t, fmt.Sprintf("EvalVariable(%s)", expr))
			t.Logf("%s: %v\n", expr, v)
		}
	})
}

func BenchmarkLocalVariables(b *testing.B) {
	protest.AllowRecording(b)
	withTestProcess("testvariables", b, func(p proc.Process, fixture protest.Fixture) {
		assertNoError(proc.Continue(p), b, "Continue() returned an error")
		scope, err := proc.GoroutineScope(p.CurrentThread())
		assertNoError(err, b, "Scope()")
		for i := 0; i < b.N; i++ {
			_, err := scope.LocalVariables(normalLoadConfig)
			assertNoError(err, b, "LocalVariables()")
		}
	})
}

func TestCondBreakpoint(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("parallel_next", t, func(p proc.Process, fixture protest.Fixture) {
		addr, _, err := p.BinInfo().LineToPC(fixture.Source, 9)
		assertNoError(err, t, "LineToPC")
		bp, err := p.SetBreakpoint(addr, proc.UserBreakpoint, nil)
		assertNoError(err, t, "SetBreakpoint()")
		bp.Cond = &ast.BinaryExpr{
			Op: token.EQL,
			X:  &ast.Ident{Name: "n"},
			Y:  &ast.BasicLit{Kind: token.INT, Value: "7"},
		}

		assertNoError(proc.Continue(p), t, "Continue()")

		nvar, err := evalVariable(p, "n")
		assertNoError(err, t, "EvalVariable()")

		n, _ := constant.Int64Val(nvar.Value)
		if n != 7 {
			t.Fatalf("Stoppend on wrong goroutine %d\n", n)
		}
	})
}

func TestCondBreakpointError(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("parallel_next", t, func(p proc.Process, fixture protest.Fixture) {
		addr, _, err := p.BinInfo().LineToPC(fixture.Source, 9)
		assertNoError(err, t, "LineToPC")
		bp, err := p.SetBreakpoint(addr, proc.UserBreakpoint, nil)
		assertNoError(err, t, "SetBreakpoint()")
		bp.Cond = &ast.BinaryExpr{
			Op: token.EQL,
			X:  &ast.Ident{Name: "nonexistentvariable"},
			Y:  &ast.BasicLit{Kind: token.INT, Value: "7"},
		}

		err = proc.Continue(p)
		if err == nil {
			t.Fatalf("No error on first Continue()")
		}

		if err.Error() != "error evaluating expression: could not find symbol value for nonexistentvariable" && err.Error() != "multiple errors evaluating conditions" {
			t.Fatalf("Unexpected error on first Continue(): %v", err)
		}

		bp.Cond = &ast.BinaryExpr{
			Op: token.EQL,
			X:  &ast.Ident{Name: "n"},
			Y:  &ast.BasicLit{Kind: token.INT, Value: "7"},
		}

		err = proc.Continue(p)
		if err != nil {
			if _, exited := err.(proc.ProcessExitedError); !exited {
				t.Fatalf("Unexpected error on second Continue(): %v", err)
			}
		} else {
			nvar, err := evalVariable(p, "n")
			assertNoError(err, t, "EvalVariable()")

			n, _ := constant.Int64Val(nvar.Value)
			if n != 7 {
				t.Fatalf("Stoppend on wrong goroutine %d\n", n)
			}
		}
	})
}

func TestIssue356(t *testing.T) {
	// slice with a typedef does not get printed correctly
	protest.AllowRecording(t)
	withTestProcess("testvariables2", t, func(p proc.Process, fixture protest.Fixture) {
		assertNoError(proc.Continue(p), t, "Continue() returned an error")
		mmvar, err := evalVariable(p, "mainMenu")
		assertNoError(err, t, "EvalVariable()")
		if mmvar.Kind != reflect.Slice {
			t.Fatalf("Wrong kind for mainMenu: %v\n", mmvar.Kind)
		}
	})
}

func TestStepIntoFunction(t *testing.T) {
	withTestProcess("teststep", t, func(p proc.Process, fixture protest.Fixture) {
		// Continue until breakpoint
		assertNoError(proc.Continue(p), t, "Continue() returned an error")
		// Step into function
		assertNoError(proc.Step(p), t, "Step() returned an error")
		// We should now be inside the function.
		loc, err := p.CurrentThread().Location()
		if err != nil {
			t.Fatal(err)
		}
		if loc.Fn.Name != "main.callme" {
			t.Fatalf("expected to be within the 'callme' function, was in %s instead", loc.Fn.Name)
		}
		if !strings.Contains(loc.File, "teststep") {
			t.Fatalf("debugger stopped at incorrect location: %s:%d", loc.File, loc.Line)
		}
		if loc.Line != 8 {
			t.Fatalf("debugger stopped at incorrect line: %d", loc.Line)
		}
	})
}

func TestIssue384(t *testing.T) {
	// Crash related to reading uninitialized memory, introduced by the memory prefetching optimization
	protest.AllowRecording(t)
	withTestProcess("issue384", t, func(p proc.Process, fixture protest.Fixture) {
		start, _, err := p.BinInfo().LineToPC(fixture.Source, 13)
		assertNoError(err, t, "LineToPC()")
		_, err = p.SetBreakpoint(start, proc.UserBreakpoint, nil)
		assertNoError(err, t, "SetBreakpoint()")
		assertNoError(proc.Continue(p), t, "Continue()")
		_, err = evalVariable(p, "st")
		assertNoError(err, t, "EvalVariable()")
	})
}

func TestIssue332_Part1(t *testing.T) {
	// Next shouldn't step inside a function call
	protest.AllowRecording(t)
	withTestProcess("issue332", t, func(p proc.Process, fixture protest.Fixture) {
		start, _, err := p.BinInfo().LineToPC(fixture.Source, 8)
		assertNoError(err, t, "LineToPC()")
		_, err = p.SetBreakpoint(start, proc.UserBreakpoint, nil)
		assertNoError(err, t, "SetBreakpoint()")
		assertNoError(proc.Continue(p), t, "Continue()")
		assertNoError(proc.Next(p), t, "first Next()")
		locations, err := proc.ThreadStacktrace(p.CurrentThread(), 2)
		assertNoError(err, t, "Stacktrace()")
		if locations[0].Call.Fn == nil {
			t.Fatalf("Not on a function")
		}
		if locations[0].Call.Fn.Name != "main.main" {
			t.Fatalf("Not on main.main after Next: %s (%s:%d)", locations[0].Call.Fn.Name, locations[0].Call.File, locations[0].Call.Line)
		}
		if locations[0].Call.Line != 9 {
			t.Fatalf("Not on line 9 after Next: %s (%s:%d)", locations[0].Call.Fn.Name, locations[0].Call.File, locations[0].Call.Line)
		}
	})
}

func TestIssue332_Part2(t *testing.T) {
	// Step should skip a function's prologue
	// In some parts of the prologue, for some functions, the FDE data is incorrect
	// which leads to 'next' and 'stack' failing with error "could not find FDE for PC: <garbage>"
	// because the incorrect FDE data leads to reading the wrong stack address as the return address
	protest.AllowRecording(t)
	withTestProcess("issue332", t, func(p proc.Process, fixture protest.Fixture) {
		start, _, err := p.BinInfo().LineToPC(fixture.Source, 8)
		assertNoError(err, t, "LineToPC()")
		_, err = p.SetBreakpoint(start, proc.UserBreakpoint, nil)
		assertNoError(err, t, "SetBreakpoint()")
		assertNoError(proc.Continue(p), t, "Continue()")

		// step until we enter changeMe
		for {
			assertNoError(proc.Step(p), t, "Step()")
			locations, err := proc.ThreadStacktrace(p.CurrentThread(), 2)
			assertNoError(err, t, "Stacktrace()")
			if locations[0].Call.Fn == nil {
				t.Fatalf("Not on a function")
			}
			if locations[0].Call.Fn.Name == "main.changeMe" {
				break
			}
		}

		regs, err := p.CurrentThread().Registers(false)
		assertNoError(err, t, "Registers()")
		pc := regs.PC()
		pcAfterPrologue, err := proc.FindFunctionLocation(p, "main.changeMe", true, -1)
		assertNoError(err, t, "FindFunctionLocation()")
		pcEntry, err := proc.FindFunctionLocation(p, "main.changeMe", false, 0)
		if pcAfterPrologue == pcEntry {
			t.Fatalf("main.changeMe and main.changeMe:0 are the same (%x)", pcAfterPrologue)
		}
		if pc != pcAfterPrologue {
			t.Fatalf("Step did not skip the prologue: current pc: %x, first instruction after prologue: %x", pc, pcAfterPrologue)
		}

		assertNoError(proc.Next(p), t, "first Next()")
		assertNoError(proc.Next(p), t, "second Next()")
		assertNoError(proc.Next(p), t, "third Next()")
		err = proc.Continue(p)
		if _, exited := err.(proc.ProcessExitedError); !exited {
			assertNoError(err, t, "final Continue()")
		}
	})
}

func TestIssue396(t *testing.T) {
	withTestProcess("callme", t, func(p proc.Process, fixture protest.Fixture) {
		_, err := proc.FindFunctionLocation(p, "main.init", true, -1)
		assertNoError(err, t, "FindFunctionLocation()")
	})
}

func TestIssue414(t *testing.T) {
	// Stepping until the program exits
	protest.AllowRecording(t)
	withTestProcess("math", t, func(p proc.Process, fixture protest.Fixture) {
		start, _, err := p.BinInfo().LineToPC(fixture.Source, 9)
		assertNoError(err, t, "LineToPC()")
		_, err = p.SetBreakpoint(start, proc.UserBreakpoint, nil)
		assertNoError(err, t, "SetBreakpoint()")
		assertNoError(proc.Continue(p), t, "Continue()")
		for {
			err := proc.Step(p)
			if err != nil {
				if _, exited := err.(proc.ProcessExitedError); exited {
					break
				}
			}
			assertNoError(err, t, "Step()")
		}
	})
}

func TestPackageVariables(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("testvariables", t, func(p proc.Process, fixture protest.Fixture) {
		err := proc.Continue(p)
		assertNoError(err, t, "Continue()")
		scope, err := proc.GoroutineScope(p.CurrentThread())
		assertNoError(err, t, "Scope()")
		vars, err := scope.PackageVariables(normalLoadConfig)
		assertNoError(err, t, "PackageVariables()")
		failed := false
		for _, v := range vars {
			if v.Unreadable != nil {
				failed = true
				t.Logf("Unreadable variable %s: %v", v.Name, v.Unreadable)
			}
		}
		if failed {
			t.Fatalf("previous errors")
		}
	})
}

func TestIssue149(t *testing.T) {
	ver, _ := proc.ParseVersionString(runtime.Version())
	if ver.Major > 0 && !ver.AfterOrEqual(proc.GoVersion{1, 7, -1, 0, 0, ""}) {
		return
	}
	// setting breakpoint on break statement
	withTestProcess("break", t, func(p proc.Process, fixture protest.Fixture) {
		_, err := proc.FindFileLocation(p, fixture.Source, 8)
		assertNoError(err, t, "FindFileLocation()")
	})
}

func TestPanicBreakpoint(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("panic", t, func(p proc.Process, fixture protest.Fixture) {
		assertNoError(proc.Continue(p), t, "Continue()")
		bp, _, _ := p.CurrentThread().Breakpoint()
		if bp == nil || bp.Name != "unrecovered-panic" {
			t.Fatalf("not on unrecovered-panic breakpoint: %v", bp)
		}
	})
}

func TestCmdLineArgs(t *testing.T) {
	expectSuccess := func(p proc.Process, fixture protest.Fixture) {
		err := proc.Continue(p)
		bp, _, _ := p.CurrentThread().Breakpoint()
		if bp != nil && bp.Name == "unrecovered-panic" {
			t.Fatalf("testing args failed on unrecovered-panic breakpoint: %v", bp)
		}
		exit, exited := err.(proc.ProcessExitedError)
		if !exited {
			t.Fatalf("Process did not exit: %v", err)
		} else {
			if exit.Status != 0 {
				t.Fatalf("process exited with invalid status", exit.Status)
			}
		}
	}

	expectPanic := func(p proc.Process, fixture protest.Fixture) {
		proc.Continue(p)
		bp, _, _ := p.CurrentThread().Breakpoint()
		if bp == nil || bp.Name != "unrecovered-panic" {
			t.Fatalf("not on unrecovered-panic breakpoint: %v", bp)
		}
	}

	// make sure multiple arguments (including one with spaces) are passed to the binary correctly
	withTestProcessArgs("testargs", t, ".", expectSuccess, []string{"test"})
	withTestProcessArgs("testargs", t, ".", expectPanic, []string{"-test"})
	withTestProcessArgs("testargs", t, ".", expectSuccess, []string{"test", "pass flag"})
	// check that arguments with spaces are *only* passed correctly when correctly called
	withTestProcessArgs("testargs", t, ".", expectPanic, []string{"test pass", "flag"})
	withTestProcessArgs("testargs", t, ".", expectPanic, []string{"test", "pass", "flag"})
	withTestProcessArgs("testargs", t, ".", expectPanic, []string{"test pass flag"})
	// and that invalid cases (wrong arguments or no arguments) panic
	withTestProcess("testargs", t, expectPanic)
	withTestProcessArgs("testargs", t, ".", expectPanic, []string{"invalid"})
	withTestProcessArgs("testargs", t, ".", expectPanic, []string{"test", "invalid"})
	withTestProcessArgs("testargs", t, ".", expectPanic, []string{"invalid", "pass flag"})
}

func TestIssue462(t *testing.T) {
	// Stacktrace of Goroutine 0 fails with an error
	if runtime.GOOS == "windows" {
		return
	}
	withTestProcess("testnextnethttp", t, func(p proc.Process, fixture protest.Fixture) {
		go func() {
			for !p.Running() {
				time.Sleep(50 * time.Millisecond)
			}

			// Wait for program to start listening.
			for {
				conn, err := net.Dial("tcp", "localhost:9191")
				if err == nil {
					conn.Close()
					break
				}
				time.Sleep(50 * time.Millisecond)
			}

			p.RequestManualStop()
		}()

		assertNoError(proc.Continue(p), t, "Continue()")
		_, err := proc.ThreadStacktrace(p.CurrentThread(), 40)
		assertNoError(err, t, "Stacktrace()")
	})
}

func TestNextParked(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("parallel_next", t, func(p proc.Process, fixture protest.Fixture) {
		bp, err := setFunctionBreakpoint(p, "main.sayhi")
		assertNoError(err, t, "SetBreakpoint()")

		// continue until a parked goroutine exists
		var parkedg *proc.G
	LookForParkedG:
		for {
			err := proc.Continue(p)
			if _, exited := err.(proc.ProcessExitedError); exited {
				t.Log("could not find parked goroutine")
				return
			}
			assertNoError(err, t, "Continue()")

			gs, err := proc.GoroutinesInfo(p)
			assertNoError(err, t, "GoroutinesInfo()")

			for _, g := range gs {
				if g.Thread == nil {
					parkedg = g
					break LookForParkedG
				}
			}
		}

		assertNoError(p.SwitchGoroutine(parkedg.ID), t, "SwitchGoroutine()")
		p.ClearBreakpoint(bp.Addr)
		assertNoError(proc.Next(p), t, "Next()")

		if p.SelectedGoroutine().ID != parkedg.ID {
			t.Fatalf("Next did not continue on the selected goroutine, expected %d got %d", parkedg.ID, p.SelectedGoroutine().ID)
		}
	})
}

func TestStepParked(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("parallel_next", t, func(p proc.Process, fixture protest.Fixture) {
		bp, err := setFunctionBreakpoint(p, "main.sayhi")
		assertNoError(err, t, "SetBreakpoint()")

		// continue until a parked goroutine exists
		var parkedg *proc.G
	LookForParkedG:
		for {
			err := proc.Continue(p)
			if _, exited := err.(proc.ProcessExitedError); exited {
				t.Log("could not find parked goroutine")
				return
			}
			assertNoError(err, t, "Continue()")

			gs, err := proc.GoroutinesInfo(p)
			assertNoError(err, t, "GoroutinesInfo()")

			for _, g := range gs {
				if g.Thread == nil && g.CurrentLoc.Fn != nil && g.CurrentLoc.Fn.Name == "main.sayhi" {
					parkedg = g
					break LookForParkedG
				}
			}
		}

		t.Logf("Parked g is: %v\n", parkedg)
		frames, _ := parkedg.Stacktrace(20)
		for _, frame := range frames {
			name := ""
			if frame.Call.Fn != nil {
				name = frame.Call.Fn.Name
			}
			t.Logf("\t%s:%d in %s (%#x)", frame.Call.File, frame.Call.Line, name, frame.Current.PC)
		}

		assertNoError(p.SwitchGoroutine(parkedg.ID), t, "SwitchGoroutine()")
		p.ClearBreakpoint(bp.Addr)
		assertNoError(proc.Step(p), t, "Step()")

		if p.SelectedGoroutine().ID != parkedg.ID {
			t.Fatalf("Step did not continue on the selected goroutine, expected %d got %d", parkedg.ID, p.SelectedGoroutine().ID)
		}
	})
}

func TestIssue509(t *testing.T) {
	fixturesDir := protest.FindFixturesDir()
	nomaindir := filepath.Join(fixturesDir, "nomaindir")
	cmd := exec.Command("go", "build", "-gcflags=-N -l", "-o", "debug")
	cmd.Dir = nomaindir
	assertNoError(cmd.Run(), t, "go build")
	exepath := filepath.Join(nomaindir, "debug")
	_, err := native.Launch([]string{exepath}, ".")
	if err == nil {
		t.Fatalf("expected error but none was generated")
	}
	if err != proc.NotExecutableErr {
		t.Fatalf("expected error \"%v\" got \"%v\"", proc.NotExecutableErr, err)
	}
	os.Remove(exepath)
}

func TestUnsupportedArch(t *testing.T) {
	ver, _ := proc.ParseVersionString(runtime.Version())
	if ver.Major < 0 || !ver.AfterOrEqual(proc.GoVersion{1, 6, -1, 0, 0, ""}) || ver.AfterOrEqual(proc.GoVersion{1, 7, -1, 0, 0, ""}) {
		// cross compile (with -N?) works only on select versions of go
		return
	}

	fixturesDir := protest.FindFixturesDir()
	infile := filepath.Join(fixturesDir, "math.go")
	outfile := filepath.Join(fixturesDir, "_math_debug_386")

	cmd := exec.Command("go", "build", "-gcflags=-N -l", "-o", outfile, infile)
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "GOARCH=") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cmd.Env = append(cmd.Env, "GOARCH=386")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build failed: %v: %v", err, string(out))
	}
	defer os.Remove(outfile)

	p, err := native.Launch([]string{outfile}, ".")
	switch err {
	case proc.UnsupportedLinuxArchErr, proc.UnsupportedWindowsArchErr, proc.UnsupportedDarwinArchErr:
		// all good
	case nil:
		p.Halt()
		p.Kill()
		t.Fatal("Launch is expected to fail, but succeeded")
	default:
		t.Fatal(err)
	}
}

func TestIssue573(t *testing.T) {
	// calls to runtime.duffzero and runtime.duffcopy jump directly into the middle
	// of the function and the internal breakpoint set by StepInto may be missed.
	protest.AllowRecording(t)
	withTestProcess("issue573", t, func(p proc.Process, fixture protest.Fixture) {
		fentry, _ := proc.FindFunctionLocation(p, "main.foo", false, 0)
		_, err := p.SetBreakpoint(fentry, proc.UserBreakpoint, nil)
		assertNoError(err, t, "SetBreakpoint()")
		assertNoError(proc.Continue(p), t, "Continue()")
		assertNoError(proc.Step(p), t, "Step() #1")
		assertNoError(proc.Step(p), t, "Step() #2") // Bug exits here.
		assertNoError(proc.Step(p), t, "Step() #3") // Third step ought to be possible; program ought not have exited.
	})
}

func TestTestvariables2Prologue(t *testing.T) {
	withTestProcess("testvariables2", t, func(p proc.Process, fixture protest.Fixture) {
		addrEntry, err := proc.FindFunctionLocation(p, "main.main", false, 0)
		assertNoError(err, t, "FindFunctionLocation - entrypoint")
		addrPrologue, err := proc.FindFunctionLocation(p, "main.main", true, 0)
		assertNoError(err, t, "FindFunctionLocation - postprologue")
		if addrEntry == addrPrologue {
			t.Fatalf("Prologue detection failed on testvariables2.go/main.main")
		}
	})
}

func TestNextDeferReturnAndDirectCall(t *testing.T) {
	// Next should not step into a deferred function if it is called
	// directly, only if it is called through a panic or a deferreturn.
	// Here we test the case where the function is called by a deferreturn
	testseq("defercall", contNext, []nextTest{
		{9, 10},
		{10, 11},
		{11, 12},
		{12, 13},
		{13, 5},
		{5, 6},
		{6, 7},
		{7, 13},
		{13, 28}}, "main.callAndDeferReturn", t)
}

func TestNextPanicAndDirectCall(t *testing.T) {
	// Next should not step into a deferred function if it is called
	// directly, only if it is called through a panic or a deferreturn.
	// Here we test the case where the function is called by a panic
	testseq("defercall", contNext, []nextTest{
		{15, 16},
		{16, 17},
		{17, 18},
		{18, 5}}, "main.callAndPanic2", t)
}

func TestStepCall(t *testing.T) {
	testseq("testnextprog", contStep, []nextTest{
		{34, 13},
		{13, 14}}, "", t)
}

func TestStepCallPtr(t *testing.T) {
	// Tests that Step works correctly when calling functions with a
	// function pointer.
	testseq("teststepprog", contStep, []nextTest{
		{9, 10},
		{10, 5},
		{5, 6},
		{6, 7},
		{7, 11}}, "", t)
}

func TestStepReturnAndPanic(t *testing.T) {
	// Tests that Step works correctly when returning from functions
	// and when a deferred function is called when panic'ing.
	testseq("defercall", contStep, []nextTest{
		{17, 5},
		{5, 6},
		{6, 7},
		{7, 18},
		{18, 5},
		{5, 6},
		{6, 7}}, "", t)
}

func TestStepDeferReturn(t *testing.T) {
	// Tests that Step works correctly when a deferred function is
	// called during a return.
	testseq("defercall", contStep, []nextTest{
		{11, 5},
		{5, 6},
		{6, 7},
		{7, 12},
		{12, 13},
		{13, 5},
		{5, 6},
		{6, 7},
		{7, 13},
		{13, 28}}, "", t)
}

func TestStepIgnorePrivateRuntime(t *testing.T) {
	// Tests that Step will ignore calls to private runtime functions
	// (such as runtime.convT2E in this case)
	ver, _ := proc.ParseVersionString(runtime.Version())

	if ver.Major < 0 || ver.AfterOrEqual(proc.GoVersion{1, 7, -1, 0, 0, ""}) {
		testseq("teststepprog", contStep, []nextTest{
			{21, 13},
			{13, 14},
			{14, 15},
			{15, 14},
			{14, 17},
			{17, 22}}, "", t)
	} else {
		testseq("teststepprog", contStep, []nextTest{
			{21, 13},
			{13, 14},
			{14, 15},
			{15, 17},
			{17, 22}}, "", t)
	}
}

func TestIssue561(t *testing.T) {
	// Step fails to make progress when PC is at a CALL instruction
	// where a breakpoint is also set.
	protest.AllowRecording(t)
	withTestProcess("issue561", t, func(p proc.Process, fixture protest.Fixture) {
		setFileBreakpoint(p, t, fixture, 10)
		assertNoError(proc.Continue(p), t, "Continue()")
		assertNoError(proc.Step(p), t, "Step()")
		_, ln := currentLineNumber(p, t)
		if ln != 5 {
			t.Fatalf("wrong line number after Step, expected 5 got %d", ln)
		}
	})
}

func TestStepOut(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("testnextprog", t, func(p proc.Process, fixture protest.Fixture) {
		bp, err := setFunctionBreakpoint(p, "main.helloworld")
		assertNoError(err, t, "SetBreakpoint()")
		assertNoError(proc.Continue(p), t, "Continue()")
		p.ClearBreakpoint(bp.Addr)

		f, lno := currentLineNumber(p, t)
		if lno != 13 {
			t.Fatalf("wrong line number %s:%d, expected %d", f, lno, 13)
		}

		assertNoError(proc.StepOut(p), t, "StepOut()")

		f, lno = currentLineNumber(p, t)
		if lno != 35 {
			t.Fatalf("wrong line number %s:%d, expected %d", f, lno, 35)
		}
	})
}

func TestStepConcurrentDirect(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("teststepconcurrent", t, func(p proc.Process, fixture protest.Fixture) {
		pc, err := proc.FindFileLocation(p, fixture.Source, 37)
		assertNoError(err, t, "FindFileLocation()")
		bp, err := p.SetBreakpoint(pc, proc.UserBreakpoint, nil)
		assertNoError(err, t, "SetBreakpoint()")

		assertNoError(proc.Continue(p), t, "Continue()")
		_, err = p.ClearBreakpoint(bp.Addr)
		assertNoError(err, t, "ClearBreakpoint()")

		for _, b := range p.Breakpoints() {
			if b.Name == "unrecovered-panic" {
				_, err := p.ClearBreakpoint(b.Addr)
				assertNoError(err, t, "ClearBreakpoint(unrecovered-panic)")
				break
			}
		}

		gid := p.SelectedGoroutine().ID

		seq := []int{37, 38, 13, 15, 16, 38}

		i := 0
		count := 0
		for {
			anyerr := false
			if p.SelectedGoroutine().ID != gid {
				t.Errorf("Step switched to different goroutine %d %d\n", gid, p.SelectedGoroutine().ID)
				anyerr = true
			}
			f, ln := currentLineNumber(p, t)
			if ln != seq[i] {
				if i == 1 && ln == 40 {
					// loop exited
					break
				}
				frames, err := proc.ThreadStacktrace(p.CurrentThread(), 20)
				if err != nil {
					t.Errorf("Could not get stacktrace of goroutine %d\n", p.SelectedGoroutine().ID)
				} else {
					t.Logf("Goroutine %d (thread: %d):", p.SelectedGoroutine().ID, p.CurrentThread().ThreadID())
					for _, frame := range frames {
						t.Logf("\t%s:%d (%#x)", frame.Call.File, frame.Call.Line, frame.Current.PC)
					}
				}
				t.Errorf("Program did not continue at expected location (%d) %s:%d [i %d count %d]", seq[i], f, ln, i, count)
				anyerr = true
			}
			if anyerr {
				t.FailNow()
			}
			i = (i + 1) % len(seq)
			if i == 0 {
				count++
			}
			assertNoError(proc.Step(p), t, "Step()")
		}

		if count != 100 {
			t.Fatalf("Program did not loop expected number of times: %d", count)
		}
	})
}

func nextInProgress(p proc.Process) bool {
	for _, bp := range p.Breakpoints() {
		if bp.Internal() {
			return true
		}
	}
	return false
}

func TestStepConcurrentPtr(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("teststepconcurrent", t, func(p proc.Process, fixture protest.Fixture) {
		pc, err := proc.FindFileLocation(p, fixture.Source, 24)
		assertNoError(err, t, "FindFileLocation()")
		_, err = p.SetBreakpoint(pc, proc.UserBreakpoint, nil)
		assertNoError(err, t, "SetBreakpoint()")

		for _, b := range p.Breakpoints() {
			if b.Name == "unrecovered-panic" {
				_, err := p.ClearBreakpoint(b.Addr)
				assertNoError(err, t, "ClearBreakpoint(unrecovered-panic)")
				break
			}
		}

		kvals := map[int]int64{}
		count := 0
		for {
			err := proc.Continue(p)
			_, exited := err.(proc.ProcessExitedError)
			if exited {
				break
			}
			assertNoError(err, t, "Continue()")

			f, ln := currentLineNumber(p, t)
			if ln != 24 {
				for _, th := range p.ThreadList() {
					bp, bpactive, bperr := th.Breakpoint()
					t.Logf("thread %d stopped on breakpoint %v %v %v", th.ThreadID(), bp, bpactive, bperr)
				}
				curbp, _, _ := p.CurrentThread().Breakpoint()
				t.Fatalf("Program did not continue at expected location (24): %s:%d %#x [%v] (gid %d count %d)", f, ln, currentPC(p, t), curbp, p.SelectedGoroutine().ID, count)
			}

			gid := p.SelectedGoroutine().ID

			kvar, err := evalVariable(p, "k")
			assertNoError(err, t, "EvalVariable()")
			k, _ := constant.Int64Val(kvar.Value)

			if oldk, ok := kvals[gid]; ok {
				if oldk >= k {
					t.Fatalf("Goroutine %d did not make progress?")
				}
			}
			kvals[gid] = k

			assertNoError(proc.Step(p), t, "Step()")
			for nextInProgress(p) {
				if p.SelectedGoroutine().ID == gid {
					t.Fatalf("step did not step into function call (but internal breakpoints still active?) (%d %d)", gid, p.SelectedGoroutine().ID)
				}
				assertNoError(proc.Continue(p), t, "Continue()")
			}

			if p.SelectedGoroutine().ID != gid {
				t.Fatalf("Step switched goroutines (wanted: %d got: %d)", gid, p.SelectedGoroutine().ID)
			}

			f, ln = currentLineNumber(p, t)
			if ln != 13 {
				t.Fatalf("Step did not step into function call (13): %s:%d", f, ln)
			}

			count++
			if count > 50 {
				// this test could potentially go on for 10000 cycles, since that's
				// too slow we cut the execution after 50 cycles
				break
			}
		}

		if count == 0 {
			t.Fatalf("Breakpoint never hit")
		}
	})
}

func TestStepOutDefer(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("testnextdefer", t, func(p proc.Process, fixture protest.Fixture) {
		pc, err := proc.FindFileLocation(p, fixture.Source, 9)
		assertNoError(err, t, "FindFileLocation()")
		bp, err := p.SetBreakpoint(pc, proc.UserBreakpoint, nil)
		assertNoError(err, t, "SetBreakpoint()")
		assertNoError(proc.Continue(p), t, "Continue()")
		p.ClearBreakpoint(bp.Addr)

		f, lno := currentLineNumber(p, t)
		if lno != 9 {
			t.Fatalf("worng line number %s:%d, expected %d", f, lno, 5)
		}

		assertNoError(proc.StepOut(p), t, "StepOut()")

		f, l, _ := p.BinInfo().PCToLine(currentPC(p, t))
		if f == fixture.Source || l == 6 {
			t.Fatalf("wrong location %s:%d, expected to end somewhere in runtime", f, l)
		}
	})
}

func TestStepOutDeferReturnAndDirectCall(t *testing.T) {
	// StepOut should not step into a deferred function if it is called
	// directly, only if it is called through a panic.
	// Here we test the case where the function is called by a deferreturn
	protest.AllowRecording(t)
	withTestProcess("defercall", t, func(p proc.Process, fixture protest.Fixture) {
		bp := setFileBreakpoint(p, t, fixture, 11)
		assertNoError(proc.Continue(p), t, "Continue()")
		p.ClearBreakpoint(bp.Addr)

		assertNoError(proc.StepOut(p), t, "StepOut()")

		f, ln := currentLineNumber(p, t)
		if ln != 28 {
			t.Fatalf("wrong line number, expected %d got %s:%d", 28, f, ln)
		}
	})
}

const maxInstructionLength uint64 = 15

func TestStepOnCallPtrInstr(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("teststepprog", t, func(p proc.Process, fixture protest.Fixture) {
		pc, err := proc.FindFileLocation(p, fixture.Source, 10)
		assertNoError(err, t, "FindFileLocation()")
		_, err = p.SetBreakpoint(pc, proc.UserBreakpoint, nil)
		assertNoError(err, t, "SetBreakpoint()")

		assertNoError(proc.Continue(p), t, "Continue()")

		found := false

		for {
			_, ln := currentLineNumber(p, t)
			if ln != 10 {
				break
			}
			regs, err := p.CurrentThread().Registers(false)
			assertNoError(err, t, "Registers()")
			pc := regs.PC()
			text, err := proc.Disassemble(p, nil, pc, pc+maxInstructionLength)
			assertNoError(err, t, "Disassemble()")
			if text[0].IsCall() {
				found = true
				break
			}
			assertNoError(p.StepInstruction(), t, "StepInstruction()")
		}

		if !found {
			t.Fatal("Could not find CALL instruction")
		}

		assertNoError(proc.Step(p), t, "Step()")

		f, ln := currentLineNumber(p, t)
		if ln != 5 {
			t.Fatalf("Step continued to wrong line, expected 5 was %s:%d", f, ln)
		}
	})
}

func TestIssue594(t *testing.T) {
	if runtime.GOOS == "darwin" && testBackend == "lldb" {
		// debugserver will receive an EXC_BAD_ACCESS for this, at that point
		// there is no way to reconvert this exception into a unix signal and send
		// it to the process.
		// This is a bug in debugserver/lldb:
		//  https://bugs.llvm.org//show_bug.cgi?id=22868
		return
	}
	// Exceptions that aren't caused by breakpoints should be propagated
	// back to the target.
	// In particular the target should be able to cause a nil pointer
	// dereference panic and recover from it.
	protest.AllowRecording(t)
	withTestProcess("issue594", t, func(p proc.Process, fixture protest.Fixture) {
		assertNoError(proc.Continue(p), t, "Continue()")
		var f string
		var ln int
		if testBackend == "rr" {
			frame, err := findFirstNonRuntimeFrame(p)
			assertNoError(err, t, "findFirstNonRuntimeFrame")
			f, ln = frame.Current.File, frame.Current.Line
		} else {
			f, ln = currentLineNumber(p, t)
		}
		if ln != 21 {
			t.Fatalf("Program stopped at %s:%d, expected :21", f, ln)
		}
	})
}

func TestStepOutPanicAndDirectCall(t *testing.T) {
	// StepOut should not step into a deferred function if it is called
	// directly, only if it is called through a panic.
	// Here we test the case where the function is called by a panic
	protest.AllowRecording(t)
	withTestProcess("defercall", t, func(p proc.Process, fixture protest.Fixture) {
		bp := setFileBreakpoint(p, t, fixture, 17)
		assertNoError(proc.Continue(p), t, "Continue()")
		p.ClearBreakpoint(bp.Addr)

		assertNoError(proc.StepOut(p), t, "StepOut()")

		f, ln := currentLineNumber(p, t)
		if ln != 5 {
			t.Fatalf("wrong line number, expected %d got %s:%d", 5, f, ln)
		}
	})
}

func TestWorkDir(t *testing.T) {
	wd := os.TempDir()
	// For Darwin `os.TempDir()` returns `/tmp` which is symlink to `/private/tmp`.
	if runtime.GOOS == "darwin" {
		wd = "/private/tmp"
	}
	protest.AllowRecording(t)
	withTestProcessArgs("workdir", t, wd, func(p proc.Process, fixture protest.Fixture) {
		addr, _, err := p.BinInfo().LineToPC(fixture.Source, 14)
		assertNoError(err, t, "LineToPC")
		p.SetBreakpoint(addr, proc.UserBreakpoint, nil)
		proc.Continue(p)
		v, err := evalVariable(p, "pwd")
		assertNoError(err, t, "EvalVariable")
		str := constant.StringVal(v.Value)
		if wd != str {
			t.Fatalf("Expected %s got %s\n", wd, str)
		}
	}, []string{})
}

func TestNegativeIntEvaluation(t *testing.T) {
	testcases := []struct {
		name  string
		typ   string
		value interface{}
	}{
		{"ni8", "int8", int64(-5)},
		{"ni16", "int16", int64(-5)},
		{"ni32", "int32", int64(-5)},
	}
	protest.AllowRecording(t)
	withTestProcess("testvariables2", t, func(p proc.Process, fixture protest.Fixture) {
		assertNoError(proc.Continue(p), t, "Continue()")
		for _, tc := range testcases {
			v, err := evalVariable(p, tc.name)
			assertNoError(err, t, "EvalVariable()")
			if typ := v.RealType.String(); typ != tc.typ {
				t.Fatalf("Wrong type for variable %q: %q (expected: %q)", tc.name, typ, tc.typ)
			}
			if val, _ := constant.Int64Val(v.Value); val != tc.value {
				t.Fatalf("Wrong value for variable %q: %v (expected: %v)", tc.name, val, tc.value)
			}
		}
	})
}

func TestIssue683(t *testing.T) {
	// Step panics when source file can not be found
	protest.AllowRecording(t)
	withTestProcess("issue683", t, func(p proc.Process, fixture protest.Fixture) {
		_, err := setFunctionBreakpoint(p, "main.main")
		assertNoError(err, t, "setFunctionBreakpoint()")
		assertNoError(proc.Continue(p), t, "First Continue()")
		for i := 0; i < 20; i++ {
			// eventually an error about the source file not being found will be
			// returned, the important thing is that we shouldn't panic
			err := proc.Step(p)
			if err != nil {
				break
			}
		}
	})
}

func TestIssue664(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("issue664", t, func(p proc.Process, fixture protest.Fixture) {
		setFileBreakpoint(p, t, fixture, 4)
		assertNoError(proc.Continue(p), t, "Continue()")
		assertNoError(proc.Next(p), t, "Next()")
		f, ln := currentLineNumber(p, t)
		if ln != 5 {
			t.Fatalf("Did not continue to line 5: %s:%d", f, ln)
		}
	})
}

// Benchmarks (*Processs).Continue + (*Scope).FunctionArguments
func BenchmarkTrace(b *testing.B) {
	protest.AllowRecording(b)
	withTestProcess("traceperf", b, func(p proc.Process, fixture protest.Fixture) {
		_, err := setFunctionBreakpoint(p, "main.PerfCheck")
		assertNoError(err, b, "setFunctionBreakpoint()")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			assertNoError(proc.Continue(p), b, "Continue()")
			s, err := proc.GoroutineScope(p.CurrentThread())
			assertNoError(err, b, "Scope()")
			_, err = s.FunctionArguments(proc.LoadConfig{false, 0, 64, 0, 3})
			assertNoError(err, b, "FunctionArguments()")
		}
		b.StopTimer()
	})
}

func TestNextInDeferReturn(t *testing.T) {
	// runtime.deferreturn updates the G struct in a way that for one
	// instruction leaves the curg._defer field non-nil but with curg._defer.fn
	// field being nil.
	// We need to deal with this without panicing.
	protest.AllowRecording(t)
	withTestProcess("defercall", t, func(p proc.Process, fixture protest.Fixture) {
		_, err := setFunctionBreakpoint(p, "runtime.deferreturn")
		assertNoError(err, t, "setFunctionBreakpoint()")
		assertNoError(proc.Continue(p), t, "First Continue()")
		for i := 0; i < 20; i++ {
			assertNoError(proc.Next(p), t, fmt.Sprintf("Next() %d", i))
		}
	})
}

func getg(goid int, gs []*proc.G) *proc.G {
	for _, g := range gs {
		if g.ID == goid {
			return g
		}
	}
	return nil
}

func TestStacktraceWithBarriers(t *testing.T) {
	// Go's Garbage Collector will insert stack barriers into stacks.
	// This stack barrier is inserted by overwriting the return address for the
	// stack frame with the address of runtime.stackBarrier.
	// The original return address is saved into the stkbar slice inside the G
	// struct.

	// In Go 1.9 stack barriers have been removed and this test must be disabled.
	if ver, _ := proc.ParseVersionString(runtime.Version()); ver.Major < 0 || ver.AfterOrEqual(proc.GoVersion{1, 9, -1, 0, 0, ""}) {
		return
	}

	// In Go 1.8 stack barriers are not inserted by default, this enables them.
	godebugOld := os.Getenv("GODEBUG")
	defer os.Setenv("GODEBUG", godebugOld)
	os.Setenv("GODEBUG", "gcrescanstacks=1")

	withTestProcess("binarytrees", t, func(p proc.Process, fixture protest.Fixture) {
		// We want to get a user goroutine with a stack barrier, to get that we execute the program until runtime.gcInstallStackBarrier is executed AND the goroutine it was executed onto contains a call to main.bottomUpTree
		_, err := setFunctionBreakpoint(p, "runtime.gcInstallStackBarrier")
		assertNoError(err, t, "setFunctionBreakpoint()")
		stackBarrierGoids := []int{}
		for len(stackBarrierGoids) == 0 {
			err := proc.Continue(p)
			if _, exited := err.(proc.ProcessExitedError); exited {
				t.Logf("Could not run test")
				return
			}
			assertNoError(err, t, "Continue()")
			gs, err := proc.GoroutinesInfo(p)
			assertNoError(err, t, "GoroutinesInfo()")
			for _, th := range p.ThreadList() {
				if bp, _, _ := th.Breakpoint(); bp == nil {
					continue
				}

				goidVar, err := evalVariable(p, "gp.goid")
				assertNoError(err, t, "evalVariable")
				goid, _ := constant.Int64Val(goidVar.Value)

				if g := getg(int(goid), gs); g != nil {
					stack, err := g.Stacktrace(50)
					assertNoError(err, t, fmt.Sprintf("Stacktrace(goroutine = %d)", goid))
					for _, frame := range stack {
						if frame.Current.Fn != nil && frame.Current.Fn.Name == "main.bottomUpTree" {
							stackBarrierGoids = append(stackBarrierGoids, int(goid))
							break
						}
					}
				}
			}
		}

		if len(stackBarrierGoids) == 0 {
			t.Fatalf("Could not find a goroutine with stack barriers")
		}

		t.Logf("stack barrier goids: %v\n", stackBarrierGoids)

		assertNoError(proc.StepOut(p), t, "StepOut()")

		gs, err := proc.GoroutinesInfo(p)
		assertNoError(err, t, "GoroutinesInfo()")

		for _, goid := range stackBarrierGoids {
			g := getg(goid, gs)

			stack, err := g.Stacktrace(200)
			assertNoError(err, t, "Stacktrace()")

			// Check that either main.main or main.main.func1 appear in the
			// stacktrace of this goroutine, if we failed at resolving stack barriers
			// correctly the stacktrace will be truncated and neither main.main or
			// main.main.func1 will appear
			found := false
			for _, frame := range stack {
				if frame.Current.Fn == nil {
					continue
				}
				if name := frame.Current.Fn.Name; name == "main.main" || name == "main.main.func1" {
					found = true
				}
			}

			t.Logf("Stacktrace for %d:\n", goid)
			for _, frame := range stack {
				name := "<>"
				if frame.Current.Fn != nil {
					name = frame.Current.Fn.Name
				}
				t.Logf("\t%s [CFA: %x Ret: %x] at %s:%d", name, frame.CFA, frame.Ret, frame.Current.File, frame.Current.Line)
			}

			if !found {
				t.Log("Truncated stacktrace for %d\n", goid)
			}
		}
	})
}

func TestAttachDetach(t *testing.T) {
	if testBackend == "lldb" && runtime.GOOS == "linux" {
		bs, _ := ioutil.ReadFile("/proc/sys/kernel/yama/ptrace_scope")
		if bs == nil || strings.TrimSpace(string(bs)) != "0" {
			t.Logf("can not run TestAttachDetach: %v\n", bs)
			return
		}
	}
	if testBackend == "rr" {
		return
	}
	fixture := protest.BuildFixture("testnextnethttp")
	cmd := exec.Command(fixture.Path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	assertNoError(cmd.Start(), t, "starting fixture")

	// wait for testnextnethttp to start listening
	t0 := time.Now()
	for {
		conn, err := net.Dial("tcp", "localhost:9191")
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
		if time.Since(t0) > 10*time.Second {
			t.Fatal("fixture did not start")
		}
	}

	var p proc.Process
	var err error

	switch testBackend {
	case "native":
		p, err = native.Attach(cmd.Process.Pid)
	case "lldb":
		path := ""
		if runtime.GOOS == "darwin" {
			path = fixture.Path
		}
		p, err = gdbserial.LLDBAttach(cmd.Process.Pid, path)
	default:
		err = fmt.Errorf("unknown backend %q", testBackend)
	}

	assertNoError(err, t, "Attach")
	go func() {
		time.Sleep(1 * time.Second)
		http.Get("http://localhost:9191")
	}()

	assertNoError(proc.Continue(p), t, "Continue")

	f, ln := currentLineNumber(p, t)
	if ln != 11 {
		t.Fatalf("Expected line :11 got %s:%d", f, ln)
	}

	assertNoError(p.Detach(false), t, "Detach")

	resp, err := http.Get("http://localhost:9191/nobp")
	assertNoError(err, t, "Page request after detach")
	bs, err := ioutil.ReadAll(resp.Body)
	assertNoError(err, t, "Reading /nobp page")
	if out := string(bs); strings.Index(out, "hello, world!") < 0 {
		t.Fatalf("/nobp page does not contain \"hello, world!\": %q", out)
	}

	cmd.Process.Kill()
}

func TestVarSum(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("testvariables2", t, func(p proc.Process, fixture protest.Fixture) {
		assertNoError(proc.Continue(p), t, "Continue()")
		sumvar, err := evalVariable(p, "s1[0] + s1[1]")
		assertNoError(err, t, "EvalVariable")
		sumvarstr := constant.StringVal(sumvar.Value)
		if sumvarstr != "onetwo" {
			t.Fatalf("s1[0] + s1[1] == %q (expected \"onetwo\")", sumvarstr)
		}
		if sumvar.Len != int64(len(sumvarstr)) {
			t.Fatalf("sumvar.Len == %d (expected %d)", sumvar.Len, len(sumvarstr))
		}
	})
}

func TestPackageWithPathVar(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("pkgrenames", t, func(p proc.Process, fixture protest.Fixture) {
		assertNoError(proc.Continue(p), t, "Continue()")
		_, err := evalVariable(p, "pkg.SomeVar")
		assertNoError(err, t, "EvalVariable(pkg.SomeVar)")
		_, err = evalVariable(p, "pkg.SomeVar.X")
		assertNoError(err, t, "EvalVariable(pkg.SomeVar.X)")
	})
}

func TestEnvironment(t *testing.T) {
	protest.AllowRecording(t)
	os.Setenv("SOMEVAR", "bah")
	withTestProcess("testenv", t, func(p proc.Process, fixture protest.Fixture) {
		assertNoError(proc.Continue(p), t, "Continue()")
		v, err := evalVariable(p, "x")
		assertNoError(err, t, "EvalVariable()")
		vv := constant.StringVal(v.Value)
		t.Logf("v = %q", vv)
		if vv != "bah" {
			t.Fatalf("value of v is %q (expected \"bah\")", vv)
		}
	})
}

func getFrameOff(p proc.Process, t *testing.T) int64 {
	frameoffvar, err := evalVariable(p, "runtime.frameoff")
	assertNoError(err, t, "EvalVariable(runtime.frameoff)")
	frameoff, _ := constant.Int64Val(frameoffvar.Value)
	return frameoff
}

func TestRecursiveNext(t *testing.T) {
	protest.AllowRecording(t)
	testcases := []nextTest{
		{6, 7},
		{7, 10},
		{10, 11},
		{11, 17},
	}
	testseq("increment", contNext, testcases, "main.Increment", t)

	withTestProcess("increment", t, func(p proc.Process, fixture protest.Fixture) {
		bp, err := setFunctionBreakpoint(p, "main.Increment")
		assertNoError(err, t, "setFunctionBreakpoint")
		assertNoError(proc.Continue(p), t, "Continue")
		_, err = p.ClearBreakpoint(bp.Addr)
		assertNoError(err, t, "ClearBreakpoint")
		assertNoError(proc.Next(p), t, "Next 1")
		assertNoError(proc.Next(p), t, "Next 2")
		assertNoError(proc.Next(p), t, "Next 3")
		frameoff0 := getFrameOff(p, t)
		assertNoError(proc.Step(p), t, "Step")
		frameoff1 := getFrameOff(p, t)
		if frameoff0 == frameoff1 {
			t.Fatalf("did not step into function?")
		}
		_, ln := currentLineNumber(p, t)
		if ln != 6 {
			t.Fatalf("program did not continue to expected location %d", ln)
		}
		assertNoError(proc.Next(p), t, "Next 4")
		_, ln = currentLineNumber(p, t)
		if ln != 7 {
			t.Fatalf("program did not continue to expected location %d", ln)
		}
		assertNoError(proc.StepOut(p), t, "StepOut")
		_, ln = currentLineNumber(p, t)
		if ln != 11 {
			t.Fatalf("program did not continue to expected location %d", ln)
		}
		frameoff2 := getFrameOff(p, t)
		if frameoff0 != frameoff2 {
			t.Fatalf("frame offset mismatch %x != %x", frameoff0, frameoff2)
		}
	})
}
