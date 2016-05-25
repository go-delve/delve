package proc

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Sirupsen/logrus"
	protest "github.com/derekparker/delve/proc/test"
)

var normalLoadConfig = LoadConfig{true, 1, 64, 64, -1}

func init() {
	runtime.GOMAXPROCS(4)
	os.Setenv("GOMAXPROCS", "4")
	logrus.SetLevel(logrus.DebugLevel)
}

func TestMain(m *testing.M) {
	os.Exit(protest.RunTestsWithFixtures(m))
}

func withTestProcess(name string, t testing.TB, fn func(p *Process, fixture protest.Fixture)) {
	fixture := protest.BuildFixture(name)
	p, err := Launch([]string{fixture.Path})
	if err != nil {
		t.Fatal("Launch():", err)
	}

	defer func() {
		log.Debug("after test kill/halt")
		if !p.Exited() {
			if err := Halt(p); err != nil {
				t.Fatal(err)
			}
			log.Debug("killing...")
			if err := Kill(p); err != nil {
				switch err.(type) {
				case ProcessExitedError:
				default:
					t.Fatal(err)
				}
			}
		}
	}()

	fn(p, fixture)
}

func withTestProcessArgs(name string, t testing.TB, fn func(p *Process, fixture protest.Fixture), args []string) {
	fixture := protest.BuildFixture(name)
	p, err := Launch(append([]string{fixture.Path}, args...))
	if err != nil {
		t.Fatal("Launch():", err)
	}

	defer func() {
		Halt(p)
		Kill(p)
	}()

	fn(p, fixture)
}

func getRegisters(p *Process, t *testing.T) Registers {
	regs, err := p.Registers()
	if err != nil {
		t.Fatal("Registers():", err)
	}

	return regs
}

func dataAtAddr(thread *Thread, addr uint64) ([]byte, error) {
	return thread.readMemory(uintptr(addr), 1)
}

func assertNoError(err error, t testing.TB, s string) {
	if err != nil {
		_, file, line, _ := runtime.Caller(1)
		fname := filepath.Base(file)
		t.Fatalf("failed assertion at %s:%d: %s - %s\n", fname, line, s, err)
	}
}

func currentPC(p *Process, t *testing.T) uint64 {
	pc, err := p.PC()
	if err != nil {
		t.Fatal(err)
	}

	return pc
}

func currentLineNumber(p *Process, t *testing.T) (string, int) {
	pc := currentPC(p, t)
	f, l, _ := p.Dwarf.PCToLine(pc)

	return f, l
}

func TestExit(t *testing.T) {
	withTestProcess("continuetestprog", t, func(p *Process, fixture protest.Fixture) {
		err := Continue(p)
		pe, ok := err.(ProcessExitedError)
		if !ok {
			t.Fatalf("Continue() returned unexpected error type %s", err)
		}
		if pe.Status != 0 {
			t.Errorf("Unexpected error status: %d", pe.Status)
		}
		if pe.Pid != p.Pid {
			t.Errorf("Unexpected process id: %d", pe.Pid)
		}
	})
}

func TestExitAfterContinue(t *testing.T) {
	withTestProcess("continuetestprog", t, func(p *Process, fixture protest.Fixture) {
		_, err := setFunctionBreakpoint(p, "main.sayhi")
		assertNoError(err, t, "setFunctionBreakpoint()")
		assertNoError(Continue(p), t, "First Continue()")
		err = Continue(p)
		pe, ok := err.(ProcessExitedError)
		if !ok {
			t.Fatalf("Continue() returned unexpected error type %s", err)
		}
		if pe.Status != 0 {
			t.Errorf("Unexpected error status: %d", pe.Status)
		}
		if pe.Pid != p.Pid {
			t.Errorf("Unexpected process id: %d", pe.Pid)
		}
	})
}

func setFunctionBreakpoint(p *Process, fname string) (*Breakpoint, error) {
	addr, err := FindFunctionLocation(p, fname, true, 0)
	if err != nil {
		return nil, err
	}
	return SetBreakpoint(p, addr)
}

func TestStop(t *testing.T) {
	withTestProcess("loopprog", t, func(p *Process, fixture protest.Fixture) {
		go func() {
			for {
				if p.Running() {
					if err := Stop(p); err != nil {
						t.Fatal(err)
					}
					break
				}
			}
		}()
		assertNoError(Continue(p), t, "Continue")
		// Loop through threads and make sure they are all
		// actually stopped, err will not be nil if the process
		// is still running.
		for _, th := range p.Threads {
			if !th.Stopped() {
				t.Fatal("expected thread to be stopped, but was not")
			}
			if th.running != false {
				t.Fatal("expected running = false for thread", th.ID)
			}
			_, err := th.Registers()
			assertNoError(err, t, "Registers")
		}
	})
}

func TestStepInstruction(t *testing.T) {
	withTestProcess("testprog", t, func(p *Process, fixture protest.Fixture) {
		helloworldfunc := p.Dwarf.LookupFunc("main.helloworld")
		helloworldaddr := helloworldfunc.Entry

		_, err := SetBreakpoint(p, helloworldaddr)
		assertNoError(err, t, "SetBreakpoint()")
		assertNoError(Continue(p), t, "Continue()")

		regs := getRegisters(p, t)
		rip := regs.PC()

		err = p.CurrentThread.StepInstruction()
		assertNoError(err, t, "StepInstruction()")

		regs = getRegisters(p, t)
		if rip >= regs.PC() {
			t.Errorf("Expected %#v to be greater than %#v", regs.PC(), rip)
		}
	})
}

func TestBreakpoint(t *testing.T) {
	withTestProcess("testprog", t, func(p *Process, fixture protest.Fixture) {
		helloworldfunc := p.Dwarf.LookupFunc("main.helloworld")
		helloworldaddr := helloworldfunc.Entry

		bp, err := SetBreakpoint(p, helloworldaddr)
		assertNoError(err, t, "SetBreakpoint()")
		assertNoError(Continue(p), t, "Continue()")

		pc, err := p.PC()
		if err != nil {
			t.Fatal(err)
		}

		if bp.TotalHitCount != 1 {
			t.Fatalf("Breakpoint should be hit once, got %d\n", bp.TotalHitCount)
		}

		if pc-1 != bp.Addr && pc != bp.Addr {
			f, l, _ := p.Dwarf.PCToLine(pc)
			t.Fatalf("Break not respected:\nPC:%#v %s:%d\nFN:%#v \n", pc, f, l, bp.Addr)
		}
	})
}

func TestBreakpointInSeperateGoRoutine(t *testing.T) {
	withTestProcess("testthreads", t, func(p *Process, fixture protest.Fixture) {
		fn := p.Dwarf.LookupFunc("main.anotherthread")
		if fn == nil {
			t.Fatal("No fn exists")
		}

		_, err := SetBreakpoint(p, fn.Entry)
		if err != nil {
			t.Fatal(err)
		}

		err = Continue(p)
		if err != nil {
			t.Fatal(err)
		}

		pc, err := p.PC()
		if err != nil {
			t.Fatal(err)
		}

		f, l, _ := p.Dwarf.PCToLine(pc)
		if f != "testthreads.go" && l != 8 {
			t.Fatal("Program did not hit breakpoint")
		}
	})
}

func TestBreakpointWithNonExistantFunction(t *testing.T) {
	withTestProcess("testprog", t, func(p *Process, fixture protest.Fixture) {
		_, err := SetBreakpoint(p, 0)
		if err == nil {
			t.Fatal("Should not be able to break at non existant function")
		}
	})
}

func TestClearBreakpointBreakpoint(t *testing.T) {
	withTestProcess("testprog", t, func(p *Process, fixture protest.Fixture) {
		fn := p.Dwarf.LookupFunc("main.sleepytime")
		bp, err := SetBreakpoint(p, fn.Entry)
		assertNoError(err, t, "SetBreakpoint()")

		bp, err = ClearBreakpoint(p, fn.Entry)
		assertNoError(err, t, "ClearBreakpoint()")

		data, err := dataAtAddr(p.CurrentThread, bp.Addr)
		if err != nil {
			t.Fatal(err)
		}

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

func countBreakpoints(p *Process) int {
	bpcount := 0
	for _, bp := range p.Breakpoints {
		if bp.ID >= 0 {
			bpcount++
		}
	}
	return bpcount
}

func testnext(program string, testcases []nextTest, initialLocation string, t *testing.T) {
	withTestProcess(program, t, func(p *Process, fixture protest.Fixture) {
		bp, err := setFunctionBreakpoint(p, initialLocation)
		assertNoError(err, t, "SetBreakpoint()")
		assertNoError(Continue(p), t, "Continue()")
		ClearBreakpoint(p, bp.Addr)
		p.CurrentThread.SetPC(bp.Addr)

		f, ln := currentLineNumber(p, t)
		for _, tc := range testcases {
			if ln != tc.begin {
				t.Fatalf("Program not stopped at correct spot expected %d was %s:%d", tc.begin, filepath.Base(f), ln)
			}

			assertNoError(Next(p), t, "Next() returned an error")

			f, ln = currentLineNumber(p, t)
			if ln != tc.end {
				t.Fatalf("Program did not continue to correct next location expected %d was %s:%d", tc.end, filepath.Base(f), ln)
			}
		}

		if countBreakpoints(p) != 0 {
			t.Fatal("Not all breakpoints were cleaned up", len(p.Breakpoints))
		}
	})
}

func TestNextGeneral(t *testing.T) {
	var testcases []nextTest

	ver, _ := ParseVersionString(runtime.Version())

	if ver.Major < 0 || ver.AfterOrEqual(GoVersion{1, 7, 0, 0, 0}) {
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

	testnext("testnextprog", testcases, "main.testnext", t)
}

func TestNextConcurrent(t *testing.T) {
	testcases := []nextTest{
		{8, 9},
		{9, 10},
		{10, 11},
	}
	withTestProcess("parallel_next", t, func(p *Process, fixture protest.Fixture) {
		bp, err := setFunctionBreakpoint(p, "main.sayhi")
		assertNoError(err, t, "SetBreakpoint")

		assertNoError(Continue(p), t, "Continue")
		f, ln := currentLineNumber(p, t)

		_, err = ClearBreakpoint(p, bp.Addr)
		assertNoError(err, t, "ClearBreakpoint()")

		initV, err := evalVariable(p, "n")
		initVval, _ := constant.Int64Val(initV.Value)
		assertNoError(err, t, "EvalVariable")
		for _, tc := range testcases {
			g, err := p.CurrentThread.GetG()
			assertNoError(err, t, "GetG()")
			if p.SelectedGoroutine.ID != g.ID {
				t.Fatalf("SelectedGoroutine not CurrentThread's goroutine: %d %d", g.ID, p.SelectedGoroutine.ID)
			}
			if ln != tc.begin {
				t.Fatalf("Program not stopped at correct spot expected %d was %s:%d", tc.begin, filepath.Base(f), ln)
			}
			assertNoError(Next(p), t, "Next() returned an error")
			v, err := evalVariable(p, "n")
			assertNoError(err, t, "EvalVariable")
			vval, _ := constant.Int64Val(v.Value)
			if vval != initVval {
				log.Debug("not same goroutine")
				t.Fatalf("Did not end up on same goroutine, wanted: %d, got: %d", initVval, vval)
			}
			f, ln = currentLineNumber(p, t)
			if ln != tc.end {
				t.Fatalf("Program did not continue to correct next location expected %d was %s:%d", tc.end, filepath.Base(f), ln)
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
	withTestProcess("parallel_next", t, func(p *Process, fixture protest.Fixture) {
		_, err := setFunctionBreakpoint(p, "main.sayhi")
		assertNoError(err, t, "SetBreakpoint")
		assertNoError(Continue(p), t, "Continue")
		f, ln := currentLineNumber(p, t)
		initV, err := evalVariable(p, "n")
		initVval, _ := constant.Int64Val(initV.Value)
		assertNoError(err, t, "EvalVariable")
		for _, tc := range testcases {
			log.WithFields(logrus.Fields{"begin": tc.begin, "end": tc.end}).Debug("beginning next")
			g, err := p.CurrentThread.GetG()
			assertNoError(err, t, "GetG()")
			if p.SelectedGoroutine.ID != g.ID {
				t.Fatalf("SelectedGoroutine not CurrentThread's goroutine: %d %d", g.ID, p.SelectedGoroutine.ID)
			}
			if ln != tc.begin {
				t.Fatalf("Program not stopped at correct spot expected %d was %s:%d", tc.begin, filepath.Base(f), ln)
			}
			assertNoError(Next(p), t, "Next() returned an error")
			var vval int64
			for {
				v, err := evalVariable(p, "n")
				assertNoError(err, t, "EvalVariable")
				vval, _ = constant.Int64Val(v.Value)
				if p.CurrentThread.CurrentBreakpoint == nil {
					if vval != initVval {
						t.Fatal("Did not end up on same goroutine")
					}
					break
				} else {
					if vval == initVval {
						t.Fatal("Initial breakpoint triggered twice for the same goroutine")
					}
					assertNoError(Continue(p), t, "Continue 2")
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
	testnext("testnextprog", testcases, "main.helloworld", t)
}

func TestNextFunctionReturnDefer(t *testing.T) {
	testcases := []nextTest{
		{5, 8},
		{8, 9},
		{9, 10},
		{10, 7},
		{7, 8},
	}
	testnext("testnextdefer", testcases, "main.main", t)
}

func TestNextNetHTTP(t *testing.T) {
	testcases := []nextTest{
		{11, 12},
		{12, 13},
	}
	withTestProcess("testnextnethttp", t, func(p *Process, fixture protest.Fixture) {
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
		if err := Continue(p); err != nil {
			t.Fatal(err)
		}
		f, ln := currentLineNumber(p, t)
		for _, tc := range testcases {
			if ln != tc.begin {
				t.Fatalf("Program not stopped at correct spot expected %d was %s:%d", tc.begin, filepath.Base(f), ln)
			}

			assertNoError(Next(p), t, "Next() returned an error")

			f, ln = currentLineNumber(p, t)
			if ln != tc.end {
				t.Fatalf("Program did not continue to correct next location expected %d was %s:%d", tc.end, filepath.Base(f), ln)
			}
		}
	})
}

func TestRuntimeBreakpoint(t *testing.T) {
	withTestProcess("testruntimebreakpoint", t, func(p *Process, fixture protest.Fixture) {
		err := Continue(p)
		if err != nil {
			t.Fatal(err)
		}
		pc, err := p.PC()
		if err != nil {
			t.Fatal(err)
		}
		_, l, _ := p.Dwarf.PCToLine(pc)
		if l != 10 {
			t.Fatal("did not respect breakpoint")
		}
	})
}

func TestFindReturnAddress(t *testing.T) {
	withTestProcess("testnextprog", t, func(p *Process, fixture protest.Fixture) {
		start, _, err := p.Dwarf.LineToPC(fixture.Source, 24)
		if err != nil {
			t.Fatal(err)
		}
		_, err = SetBreakpoint(p, start)
		if err != nil {
			t.Fatal(err)
		}
		err = Continue(p)
		if err != nil {
			t.Fatal(err)
		}
		addr, err := p.CurrentThread.ReturnAddress()
		if err != nil {
			t.Fatal(err)
		}
		_, l, _ := p.Dwarf.PCToLine(addr)
		if l != 40 {
			t.Fatalf("return address not found correctly, expected line 40")
		}
	})
}

func TestFindReturnAddressTopOfStackFn(t *testing.T) {
	withTestProcess("testreturnaddress", t, func(p *Process, fixture protest.Fixture) {
		fnName := "runtime.rt0_go"
		fn := p.Dwarf.LookupFunc(fnName)
		if fn == nil {
			t.Fatalf("could not find function %s", fnName)
		}
		if _, err := SetBreakpoint(p, fn.Entry); err != nil {
			t.Fatal(err)
		}
		if err := Continue(p); err != nil {
			t.Fatal(err)
		}
		if _, err := p.CurrentThread.ReturnAddress(); err == nil {
			t.Fatal("expected error to be returned")
		}
	})
}

func TestSetActiveThread(t *testing.T) {
	withTestProcess("testnextprog", t, func(p *Process, fixture protest.Fixture) {
		// With invalid thread id
		pc, err := FindFunctionLocation(p, "main.main", true, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, err = SetBreakpoint(p, pc)
		if err != nil {
			t.Fatal(err)
		}
		err = Continue(p)
		if err != nil {
			t.Fatal(err)
		}
		var nt *Thread
		ct := p.CurrentThread.ID
		for _, th := range p.Threads {
			if th.ID != ct {
				nt = th
				break
			}
		}
		if nt == nil {
			t.Fatal("could not find thread to switch to")
		}
		// With valid thread id
		err = p.SetActiveThread(nt)
		if err != nil {
			t.Fatal(err)
		}
		if p.CurrentThread.ID != nt.ID {
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

	withTestProcess("cgotest", t, func(p *Process, fixture protest.Fixture) {
		pc, err := FindFunctionLocation(p, "main.main", true, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, err = SetBreakpoint(p, pc)
		if err != nil {
			t.Fatal(err)
		}
		err = Continue(p)
		if err != nil {
			t.Fatal(err)
		}
		err = Next(p)
		if err != nil {
			t.Fatal(err)
		}
	})
}

type loc struct {
	line int
	fn   string
}

func (l1 *loc) match(l2 Stackframe) bool {
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
	withTestProcess("stacktraceprog", t, func(p *Process, fixture protest.Fixture) {
		bp, err := setFunctionBreakpoint(p, "main.stacktraceme")
		assertNoError(err, t, "BreakByLocation()")

		for i := range stacks {
			assertNoError(Continue(p), t, "Continue()")
			locations, err := p.CurrentThread.Stacktrace(40)
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

		ClearBreakpoint(p, bp.Addr)
		Continue(p)
	})
}

func TestStacktrace2(t *testing.T) {
	withTestProcess("retstack", t, func(p *Process, fixture protest.Fixture) {
		assertNoError(Continue(p), t, "Continue()")

		locations, err := p.CurrentThread.Stacktrace(40)
		assertNoError(err, t, "Stacktrace()")
		if !stackMatch([]loc{{-1, "main.f"}, {16, "main.main"}}, locations, false) {
			for i := range locations {
				t.Logf("\t%s:%d [%s]\n", locations[i].Call.File, locations[i].Call.Line, locations[i].Call.Fn.Name)
			}
			t.Fatalf("Stack error at main.f()\n%v\n", locations)
		}

		assertNoError(Continue(p), t, "Continue()")
		locations, err = p.CurrentThread.Stacktrace(40)
		assertNoError(err, t, "Stacktrace()")
		if !stackMatch([]loc{{-1, "main.g"}, {17, "main.main"}}, locations, false) {
			for i := range locations {
				t.Logf("\t%s:%d [%s]\n", locations[i].Call.File, locations[i].Call.Line, locations[i].Call.Fn.Name)
			}
			t.Fatalf("Stack error at main.g()\n%v\n", locations)
		}
	})

}

func stackMatch(stack []loc, locations []Stackframe, skipRuntime bool) bool {
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
	agoroutineStackA := []loc{{9, "main.agoroutine"}}
	agoroutineStackB := []loc{{10, "main.agoroutine"}}

	withTestProcess("goroutinestackprog", t, func(p *Process, fixture protest.Fixture) {
		bp, err := setFunctionBreakpoint(p, "main.stacktraceme")
		assertNoError(err, t, "BreakByLocation()")

		assertNoError(Continue(p), t, "Continue()")

		gs, err := p.GoroutinesInfo()
		assertNoError(err, t, "GoroutinesInfo")

		agoroutineCount := 0
		mainCount := 0

		for i, g := range gs {
			locations, err := g.Stacktrace(40)
			assertNoError(err, t, "GoroutineStacktrace()")

			if stackMatch(mainStack, locations, false) {
				mainCount++
			}

			if stackMatch(agoroutineStackA, locations, true) {
				agoroutineCount++
			} else if stackMatch(agoroutineStackB, locations, true) {
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

		ClearBreakpoint(p, bp.Addr)
		Continue(p)
	})
}

func TestKill(t *testing.T) {
	withTestProcess("testprog", t, func(p *Process, fixture protest.Fixture) {
		err := Kill(p)
		if err != nil {
			t.Fatal(err)
		}
		if p.Exited() != true {
			t.Fatal("expected process to have exited")
		}
	})
}

func testGSupportFunc(name string, t *testing.T, p *Process, fixture protest.Fixture) {
	bp, err := setFunctionBreakpoint(p, "main.main")
	assertNoError(err, t, name+": BreakByLocation()")

	assertNoError(Continue(p), t, name+": Continue()")

	g, err := p.CurrentThread.GetG()
	assertNoError(err, t, name+": GetG()")

	if g == nil {
		t.Fatal(name + ": g was nil")
	}

	t.Logf(name+": g is: %v", g)

	ClearBreakpoint(p, bp.Addr)
}

func TestGetG(t *testing.T) {
	withTestProcess("testprog", t, func(p *Process, fixture protest.Fixture) {
		testGSupportFunc("nocgo", t, p, fixture)
	})

	// On OSX with Go < 1.5 CGO is not supported due to: https://github.com/golang/go/issues/8973
	if runtime.GOOS == "darwin" && strings.Contains(runtime.Version(), "1.4") {
		return
	}

	withTestProcess("cgotest", t, func(p *Process, fixture protest.Fixture) {
		testGSupportFunc("cgo", t, p, fixture)
	})
}

func TestContinueMulti(t *testing.T) {
	withTestProcess("integrationprog", t, func(p *Process, fixture protest.Fixture) {
		bp1, err := setFunctionBreakpoint(p, "main.main")
		assertNoError(err, t, "BreakByLocation()")

		bp2, err := setFunctionBreakpoint(p, "main.sayhi")
		assertNoError(err, t, "BreakByLocation()")

		mainCount := 0
		sayhiCount := 0
		for {
			err := Continue(p)
			if p.Exited() {
				break
			}
			assertNoError(err, t, "Continue()")

			if p.CurrentBreakpoint().ID == bp1.ID {
				mainCount++
			}

			if p.CurrentBreakpoint().ID == bp2.ID {
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

func versionAfterOrEqual(t *testing.T, verStr string, ver GoVersion) {
	pver, ok := ParseVersionString(verStr)
	if !ok {
		t.Fatalf("Could not parse version string <%s>", verStr)
	}
	if !pver.AfterOrEqual(ver) {
		t.Fatalf("Version <%s> parsed as %v not after %v", verStr, pver, ver)
	}
	t.Logf("version string <%s> → %v", verStr, ver)
}

func TestParseVersionString(t *testing.T) {
	versionAfterOrEqual(t, "go1.4", GoVersion{1, 4, 0, 0, 0})
	versionAfterOrEqual(t, "go1.5.0", GoVersion{1, 5, 0, 0, 0})
	versionAfterOrEqual(t, "go1.4.2", GoVersion{1, 4, 2, 0, 0})
	versionAfterOrEqual(t, "go1.5beta2", GoVersion{1, 5, -1, 2, 0})
	versionAfterOrEqual(t, "go1.5rc2", GoVersion{1, 5, -1, 0, 2})
	ver, ok := ParseVersionString("devel +17efbfc Tue Jul 28 17:39:19 2015 +0000 linux/amd64")
	if !ok {
		t.Fatalf("Could not parse devel version string")
	}
	if !ver.IsDevel() {
		t.Fatalf("Devel version string not correctly recognized")
	}
}

func TestBreakpointOnFunctionEntry(t *testing.T) {
	withTestProcess("testprog", t, func(p *Process, fixture protest.Fixture) {
		addr, err := FindFunctionLocation(p, "main.main", false, 0)
		assertNoError(err, t, "FindFunctionLocation()")
		_, err = SetBreakpoint(p, addr)
		assertNoError(err, t, "SetBreakpoint()")
		assertNoError(Continue(p), t, "Continue()")
		_, ln := currentLineNumber(p, t)
		if ln != 17 {
			t.Fatalf("Wrong line number: %d (expected: 17)\n", ln)
		}
	})
}

func TestProcessReceivesSIGCHLD(t *testing.T) {
	withTestProcess("sigchldprog", t, func(p *Process, fixture protest.Fixture) {
		err := Continue(p)
		_, ok := err.(ProcessExitedError)
		if !ok {
			t.Fatalf("Continue() returned unexpected error type %v", err)
		}
	})
}

func TestIssue239(t *testing.T) {
	withTestProcess("is sue239", t, func(p *Process, fixture protest.Fixture) {
		pos, _, err := p.Dwarf.LineToPC(fixture.Source, 17)
		assertNoError(err, t, "LineToPC()")
		_, err = SetBreakpoint(p, pos)
		assertNoError(err, t, fmt.Sprintf("SetBreakpoint(%d)", pos))
		assertNoError(Continue(p), t, fmt.Sprintf("Continue()"))
	})
}

func evalVariable(p *Process, symbol string) (*Variable, error) {
	scope, err := p.CurrentThread.Scope()
	if err != nil {
		return nil, err
	}
	return scope.EvalVariable(symbol, normalLoadConfig)
}

func setVariable(p *Process, symbol, value string) error {
	scope, err := p.CurrentThread.Scope()
	if err != nil {
		return err
	}
	return scope.SetVariable(symbol, value)
}

func TestVariableEvaluation(t *testing.T) {
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

	withTestProcess("testvariables", t, func(p *Process, fixture protest.Fixture) {
		assertNoError(Continue(p), t, "Continue() returned an error")

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
	withTestProcess("goroutinestackprog", t, func(p *Process, fixture protest.Fixture) {
		_, err := setFunctionBreakpoint(p, "main.stacktraceme")
		assertNoError(err, t, "setFunctionBreakpoint")
		assertNoError(Continue(p), t, "Continue()")

		// Testing evaluation on goroutines
		gs, err := p.GoroutinesInfo()
		assertNoError(err, t, "GoroutinesInfo")
		found := make([]bool, 10)
		for _, g := range gs {
			frame := -1
			frames, err := g.Stacktrace(10)
			assertNoError(err, t, "GoroutineStacktrace()")
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

			scope, err := ConvertEvalScope(p, g.ID, frame)
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
		assertNoError(Continue(p), t, "Continue() 2")
		g, err := p.CurrentThread.GetG()
		assertNoError(err, t, "GetG()")

		for i := 0; i <= 3; i++ {
			scope, err := ConvertEvalScope(p, g.ID, i+1)
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
	withTestProcess("testvariables2", t, func(p *Process, fixture protest.Fixture) {
		assertNoError(Continue(p), t, "Continue() returned an error")

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
		scope, err := p.CurrentThread.Scope()
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
	withTestProcess("testvariables", t, func(p *Process, fixture protest.Fixture) {
		err := Continue(p)
		assertNoError(err, t, "Continue() returned an error")

		_, err = evalVariable(p, "a1")
		assertNoError(err, t, "Unable to find variable a1")

		_, err = evalVariable(p, "a2")
		assertNoError(err, t, "Unable to find variable a1")

		// Move scopes, a1 exists here by a2 does not
		err = Continue(p)
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
	withTestProcess("testvariables2", t, func(p *Process, fixture protest.Fixture) {
		assertNoError(Continue(p), t, "Continue()")
		v, err := evalVariable(p, "aas")
		assertNoError(err, t, "EvalVariable()")
		t.Logf("v: %v\n", v)
	})
}

func TestIssue316(t *testing.T) {
	// A pointer loop that includes one interface should not send dlv into an infinite loop
	withTestProcess("testvariables2", t, func(p *Process, fixture protest.Fixture) {
		assertNoError(Continue(p), t, "Continue()")
		_, err := evalVariable(p, "iface5")
		assertNoError(err, t, "EvalVariable()")
	})
}

func TestIssue325(t *testing.T) {
	// nil pointer dereference when evaluating interfaces to function pointers
	withTestProcess("testvariables2", t, func(p *Process, fixture protest.Fixture) {
		assertNoError(Continue(p), t, "Continue()")
		iface2fn1v, err := evalVariable(p, "iface2fn1")
		assertNoError(err, t, "EvalVariable()")
		t.Logf("iface2fn1: %v\n", iface2fn1v)

		iface2fn2v, err := evalVariable(p, "iface2fn2")
		assertNoError(err, t, "EvalVariable()")
		t.Logf("iface2fn2: %v\n", iface2fn2v)
	})
}

func TestBreakpointCounts(t *testing.T) {
	withTestProcess("bpcountstest", t, func(p *Process, fixture protest.Fixture) {
		addr, _, err := p.Dwarf.LineToPC(fixture.Source, 12)
		assertNoError(err, t, "LineToPC")
		bp, err := SetBreakpoint(p, addr)
		assertNoError(err, t, "SetBreakpoint()")

		for {
			if err := Continue(p); err != nil {
				if _, exited := err.(ProcessExitedError); exited {
					break
				}
				assertNoError(err, t, "Continue()")
			}
		}

		if len(bp.HitCount) != 2 {
			t.Fatalf("Wrong number of goroutines for breakpoint (%d)", len(bp.HitCount))
		}

		for _, v := range bp.HitCount {
			if v != 100 {
				t.Fatalf("Wrong HitCount for breakpoint (%v)", bp.HitCount)
			}
		}

		t.Logf("TotalHitCount: %d", bp.TotalHitCount)
		if bp.TotalHitCount != 200 {
			t.Fatalf("Wrong TotalHitCount for the breakpoint (%d), expected %d", bp.TotalHitCount, 200)
		}
	})
}

func BenchmarkArray(b *testing.B) {
	// each bencharr struct is 128 bytes, bencharr is 64 elements long
	b.SetBytes(int64(64 * 128))
	withTestProcess("testvariables2", b, func(p *Process, fixture protest.Fixture) {
		assertNoError(Continue(p), b, "Continue()")
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
	withTestProcess("bpcountstest", t, func(p *Process, fixture protest.Fixture) {
		addr, _, err := p.Dwarf.LineToPC(fixture.Source, 12)
		assertNoError(err, t, "LineToPC")
		bp, err := SetBreakpoint(p, addr)
		assertNoError(err, t, "SetBreakpoint()")

		for {
			if err := Continue(p); err != nil {
				if _, exited := err.(ProcessExitedError); exited {
					break
				}
				assertNoError(err, t, "Continue()")
			}
			fmt.Printf("Continue returned %d\n", bp.TotalHitCount)
			for _, th := range p.Threads {
				if th.CurrentBreakpoint == nil {
					continue
				}
				scope, err := th.Scope()
				assertNoError(err, t, "Scope()")
				v, err := scope.EvalVariable("i", normalLoadConfig)
				assertNoError(err, t, "evalVariable")
				i, _ := constant.Int64Val(v.Value)
				v, err = scope.EvalVariable("id", normalLoadConfig)
				assertNoError(err, t, "evalVariable")
				id, _ := constant.Int64Val(v.Value)
				m[id] = i
				fmt.Printf("\tgoroutine (%d) %d: %d\n", th.ID, id, i)
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
	b.SetBytes(int64(64*128 + 64*8))
	withTestProcess("testvariables2", b, func(p *Process, fixture protest.Fixture) {
		assertNoError(Continue(p), b, "Continue()")
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
	b.SetBytes(int64(41 * (2*8 + 9)))
	withTestProcess("testvariables2", b, func(p *Process, fixture protest.Fixture) {
		assertNoError(Continue(p), b, "Continue()")
		for i := 0; i < b.N; i++ {
			_, err := evalVariable(p, "m1")
			assertNoError(err, b, "EvalVariable()")
		}
	})
}

func BenchmarkGoroutinesInfo(b *testing.B) {
	withTestProcess("testvariables2", b, func(p *Process, fixture protest.Fixture) {
		assertNoError(Continue(p), b, "Continue()")
		for i := 0; i < b.N; i++ {
			p.allGCache = nil
			_, err := p.GoroutinesInfo()
			assertNoError(err, b, "GoroutinesInfo")
		}
	})
}

func TestIssue262(t *testing.T) {
	// Continue does not work when the current breakpoint is set on a NOP instruction
	withTestProcess("issue262", t, func(p *Process, fixture protest.Fixture) {
		addr, _, err := p.Dwarf.LineToPC(fixture.Source, 11)
		assertNoError(err, t, "LineToPC")
		_, err = SetBreakpoint(p, addr)
		assertNoError(err, t, "SetBreakpoint()")

		assertNoError(Continue(p), t, "Continue()")
		err = Continue(p)
		if err == nil {
			t.Fatalf("No error on second continue")
		}
		_, exited := err.(ProcessExitedError)
		if !exited {
			t.Fatalf("Process did not exit after second continue: %v", err)
		}
	})
}

func TestIssue305(t *testing.T) {
	// If 'next' hits a breakpoint on the goroutine it's stepping through the temp breakpoints aren't cleared
	// preventing further use of 'next' command
	withTestProcess("issue305", t, func(p *Process, fixture protest.Fixture) {
		addr, _, err := p.Dwarf.LineToPC(fixture.Source, 5)
		assertNoError(err, t, "LineToPC()")
		_, err = SetBreakpoint(p, addr)
		assertNoError(err, t, "SetBreakpoint()")

		assertNoError(Continue(p), t, "Continue()")

		assertNoError(Next(p), t, "Next() 1")
		assertNoError(Next(p), t, "Next() 2")
		assertNoError(Next(p), t, "Next() 3")
		assertNoError(Next(p), t, "Next() 4")
		assertNoError(Next(p), t, "Next() 5")
	})
}

func TestIssue341(t *testing.T) {
	// pointer loop through map entries
	withTestProcess("testvariables2", t, func(p *Process, fixture protest.Fixture) {
		assertNoError(Continue(p), t, "Continue()")
		t.Logf("requesting mapinf")
		mapinf, err := evalVariable(p, "mapinf")
		assertNoError(err, t, "EvalVariable()")
		t.Logf("mapinf: %v\n", mapinf)
	})
}

func BenchmarkLocalVariables(b *testing.B) {
	withTestProcess("testvariables", b, func(p *Process, fixture protest.Fixture) {
		assertNoError(Continue(p), b, "Continue() returned an error")
		scope, err := p.CurrentThread.Scope()
		assertNoError(err, b, "Scope()")
		for i := 0; i < b.N; i++ {
			_, err := scope.LocalVariables(normalLoadConfig)
			assertNoError(err, b, "LocalVariables()")
		}
	})
}

func TestCondBreakpoint(t *testing.T) {
	withTestProcess("parallel_next", t, func(p *Process, fixture protest.Fixture) {
		addr, _, err := p.Dwarf.LineToPC(fixture.Source, 9)
		assertNoError(err, t, "LineToPC")
		bp, err := SetBreakpoint(p, addr)
		assertNoError(err, t, "SetBreakpoint()")
		bp.Cond = &ast.BinaryExpr{
			Op: token.EQL,
			X:  &ast.Ident{Name: "n"},
			Y:  &ast.BasicLit{Kind: token.INT, Value: "7"},
		}

		assertNoError(Continue(p), t, "Continue()")

		nvar, err := evalVariable(p, "n")
		assertNoError(err, t, "EvalVariable()")

		n, _ := constant.Int64Val(nvar.Value)
		if n != 7 {
			t.Fatalf("Stoppend on wrong goroutine %d\n", n)
		}
	})
}

func TestCondBreakpointError(t *testing.T) {
	withTestProcess("parallel_next", t, func(p *Process, fixture protest.Fixture) {
		addr, _, err := p.Dwarf.LineToPC(fixture.Source, 9)
		assertNoError(err, t, "LineToPC")
		bp, err := SetBreakpoint(p, addr)
		assertNoError(err, t, "SetBreakpoint()")
		bp.Cond = &ast.BinaryExpr{
			Op: token.EQL,
			X:  &ast.Ident{Name: "nonexistentvariable"},
			Y:  &ast.BasicLit{Kind: token.INT, Value: "7"},
		}

		err = Continue(p)
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

		err = Continue(p)
		if err != nil {
			if _, exited := err.(ProcessExitedError); !exited {
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
	withTestProcess("testvariables2", t, func(p *Process, fixture protest.Fixture) {
		assertNoError(Continue(p), t, "Continue() returned an error")
		mmvar, err := evalVariable(p, "mainMenu")
		assertNoError(err, t, "EvalVariable()")
		if mmvar.Kind != reflect.Slice {
			t.Fatalf("Wrong kind for mainMenu: %v\n", mmvar.Kind)
		}
	})
}

func TestStepInto(t *testing.T) {
	withTestProcess("teststep", t, func(p *Process, fixture protest.Fixture) {
		// Continue until breakpoint
		assertNoError(Continue(p), t, "Continue() returned an error")
		// Step into function
		assertNoError(Step(p), t, "Step() returned an error")
		// We should now be inside the function.
		loc, err := p.CurrentLocation()
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
	withTestProcess("issue384", t, func(p *Process, fixture protest.Fixture) {
		start, _, err := p.Dwarf.LineToPC(fixture.Source, 13)
		assertNoError(err, t, "LineToPC()")
		_, err = SetBreakpoint(p, start)
		assertNoError(err, t, "SetBreakpoint()")
		assertNoError(Continue(p), t, "Continue()")
		_, err = evalVariable(p, "st")
		assertNoError(err, t, "EvalVariable()")
	})
}

func TestIssue332_Part1(t *testing.T) {
	// Next shouldn't step inside a function call
	withTestProcess("issue332", t, func(p *Process, fixture protest.Fixture) {
		start, _, err := p.Dwarf.LineToPC(fixture.Source, 8)
		assertNoError(err, t, "LineToPC()")
		_, err = SetBreakpoint(p, start)
		assertNoError(err, t, "SetBreakpoint()")
		assertNoError(Continue(p), t, "Continue()")

		locations, err := p.CurrentThread.Stacktrace(2)
		assertNoError(err, t, "Stacktrace()")
		if locations[0].Call.Fn == nil {
			t.Fatalf("Not on a function")
		}
		if locations[0].Call.Fn.Name != "main.main" {
			t.Fatalf("Not on main.main after Continue: %s (%s:%d)", locations[0].Call.Fn.Name, locations[0].Call.File, locations[0].Call.Line)
		}

		assertNoError(Next(p), t, "first Next()")
		locations, err = p.CurrentThread.Stacktrace(2)
		assertNoError(err, t, "Stacktrace()")
		if locations[0].Call.Fn == nil {
			t.Fatalf("Not on a function")
		}
		if locations[0].Call.Fn.Name != "main.main" {
			t.Fatalf("Not on main.main after Next: %#v - %s (%s:%d)", locations[0].Call.PC, locations[0].Call.Fn.Name, locations[0].Call.File, locations[0].Call.Line)
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
	withTestProcess("issue332", t, func(p *Process, fixture protest.Fixture) {
		start, _, err := p.Dwarf.LineToPC(fixture.Source, 8)
		assertNoError(err, t, "LineToPC()")
		_, err = SetBreakpoint(p, start)
		assertNoError(err, t, "SetBreakpoint()")
		assertNoError(Continue(p), t, "Continue()")

		// step until we enter changeMe
		for {
			assertNoError(Step(p), t, "Step()")
			locations, err := p.CurrentThread.Stacktrace(2)
			assertNoError(err, t, "Stacktrace()")
			if locations[0].Call.Fn == nil {
				t.Fatalf("Not on a function")
			}
			if locations[0].Call.Fn.Name == "main.changeMe" {
				break
			}
		}

		pc, err := p.CurrentThread.PC()
		assertNoError(err, t, "PC()")
		pcAfterPrologue, err := FindFunctionLocation(p, "main.changeMe", true, -1)
		assertNoError(err, t, "FindFunctionLocation()")
		pcEntry, err := FindFunctionLocation(p, "main.changeMe", false, 0)
		if pcAfterPrologue == pcEntry {
			t.Fatalf("main.changeMe and main.changeMe:0 are the same (%x)", pcAfterPrologue)
		}
		if pc != pcAfterPrologue {
			t.Fatalf("Step did not skip the prologue: current pc: %x, first instruction after prologue: %x", pc, pcAfterPrologue)
		}

		assertNoError(Next(p), t, "first Next()")
		assertNoError(Next(p), t, "second Next()")
		assertNoError(Next(p), t, "third Next()")
		err = Continue(p)
		if _, exited := err.(ProcessExitedError); !exited {
			assertNoError(err, t, "final Continue()")
		}
	})
}

func TestIssue396(t *testing.T) {
	withTestProcess("callme", t, func(p *Process, fixture protest.Fixture) {
		_, err := FindFunctionLocation(p, "main.init", true, -1)
		assertNoError(err, t, "FindFunctionLocation()")
	})
}

func TestIssue414(t *testing.T) {
	// Stepping until the program exits
	withTestProcess("math", t, func(p *Process, fixture protest.Fixture) {
		start, _, err := p.Dwarf.LineToPC(fixture.Source, 9)
		assertNoError(err, t, "LineToPC()")
		_, err = SetBreakpoint(p, start)
		assertNoError(err, t, "SetBreakpoint()")
		assertNoError(Continue(p), t, "Continue()")
		for {
			err := StepInstruction(p)
			if err != nil {
				if _, exited := err.(ProcessExitedError); exited {
					break
				}
			}
			assertNoError(err, t, "Step()")
		}
	})
}

func TestPackageVariables(t *testing.T) {
	withTestProcess("testvariables", t, func(p *Process, fixture protest.Fixture) {
		err := Continue(p)
		assertNoError(err, t, "Continue()")
		scope, err := p.CurrentThread.Scope()
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
	ver, _ := ParseVersionString(runtime.Version())
	if ver.Major > 0 && !ver.AfterOrEqual(GoVersion{1, 7, 0, 0, 0}) {
		return
	}
	// setting breakpoint on break statement
	withTestProcess("break", t, func(p *Process, fixture protest.Fixture) {
		_, err := FindFileLocation(p, fixture.Source, 8)
		assertNoError(err, t, "FindFileLocation()")
	})
}

func TestPanicBreakpoint(t *testing.T) {
	withTestProcess("panic", t, func(p *Process, fixture protest.Fixture) {
		assertNoError(Continue(p), t, "Continue()")
		bp := p.CurrentBreakpoint()
		if bp == nil || bp.Name != "unrecovered-panic" {
			t.Fatalf("not on unrecovered-panic breakpoint: %v", p.CurrentBreakpoint)
		}
	})
}

func TestCmdLineArgs(t *testing.T) {
	expectSuccess := func(p *Process, fixture protest.Fixture) {
		err := Continue(p)
		bp := p.CurrentBreakpoint()
		if bp != nil && bp.Name == "unrecovered-panic" {
			t.Fatalf("testing args failed on unrecovered-panic breakpoint: %v", p.CurrentBreakpoint)
		}
		exit, exited := err.(ProcessExitedError)
		if !exited {
			t.Fatalf("Process did not exit: %v", err)
		} else {
			if exit.Status != 0 {
				t.Fatalf("process exited with invalid status", exit.Status)
			}
		}
	}

	expectPanic := func(p *Process, fixture protest.Fixture) {
		Continue(p)
		bp := p.CurrentBreakpoint()
		if bp == nil || bp.Name != "unrecovered-panic" {
			t.Fatalf("not on unrecovered-panic breakpoint: %v", p.CurrentBreakpoint)
		}
	}

	// make sure multiple arguments (including one with spaces) are passed to the binary correctly
	withTestProcessArgs("testargs", t, expectSuccess, []string{"test"})
	withTestProcessArgs("testargs", t, expectSuccess, []string{"test", "pass flag"})
	// check that arguments with spaces are *only* passed correctly when correctly called
	withTestProcessArgs("testargs", t, expectPanic, []string{"test pass", "flag"})
	withTestProcessArgs("testargs", t, expectPanic, []string{"test", "pass", "flag"})
	withTestProcessArgs("testargs", t, expectPanic, []string{"test pass flag"})
	// and that invalid cases (wrong arguments or no arguments) panic
	withTestProcess("testargs", t, expectPanic)
	withTestProcessArgs("testargs", t, expectPanic, []string{"invalid"})
	withTestProcessArgs("testargs", t, expectPanic, []string{"test", "invalid"})
	withTestProcessArgs("testargs", t, expectPanic, []string{"invalid", "pass flag"})
}

func TestIssue462(t *testing.T) {
	// Stacktrace of Goroutine 0 fails with an error
	if runtime.GOOS == "windows" {
		return
	}
	withTestProcess("testnextnethttp", t, func(p *Process, fixture protest.Fixture) {
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

			Stop(p)
		}()

		assertNoError(Continue(p), t, "Continue()")
		_, err := p.CurrentThread.Stacktrace(40)
		assertNoError(err, t, "Stacktrace()")
	})
}
