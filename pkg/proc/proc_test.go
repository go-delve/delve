package proc_test

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"io/ioutil"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"text/tabwriter"
	"time"

	"github.com/go-delve/delve/pkg/dwarf/frame"
	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/regnum"
	"github.com/go-delve/delve/pkg/goversion"
	"github.com/go-delve/delve/pkg/logflags"
	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/core"
	"github.com/go-delve/delve/pkg/proc/gdbserial"
	"github.com/go-delve/delve/pkg/proc/native"
	protest "github.com/go-delve/delve/pkg/proc/test"
	"github.com/go-delve/delve/service/api"
)

var normalLoadConfig = proc.LoadConfig{true, 1, 64, 64, -1, 0}
var testBackend, buildMode string

func init() {
	runtime.GOMAXPROCS(4)
	os.Setenv("GOMAXPROCS", "4")
}

func TestMain(m *testing.M) {
	flag.StringVar(&testBackend, "backend", "", "selects backend")
	flag.StringVar(&buildMode, "test-buildmode", "", "selects build mode")
	var logConf string
	flag.StringVar(&logConf, "log", "", "configures logging")
	flag.Parse()
	protest.DefaultTestBackend(&testBackend)
	if buildMode != "" && buildMode != "pie" {
		fmt.Fprintf(os.Stderr, "unknown build mode %q", buildMode)
		os.Exit(1)
	}
	logflags.Setup(logConf != "", logConf, "")
	os.Exit(protest.RunTestsWithFixtures(m))
}

func matchSkipConditions(conditions ...string) bool {
	for _, cond := range conditions {
		condfound := false
		for _, s := range []string{runtime.GOOS, runtime.GOARCH, testBackend, buildMode} {
			if s == cond {
				condfound = true
				break
			}
		}
		if !condfound {
			return false
		}
	}
	return true
}

func skipOn(t testing.TB, reason string, conditions ...string) {
	if matchSkipConditions(conditions...) {
		t.Skipf("skipped on %s: %s", strings.Join(conditions, "/"), reason)
	}
}

func skipUnlessOn(t testing.TB, reason string, conditions ...string) {
	if !matchSkipConditions(conditions...) {
		t.Skipf("skipped on %s: %s", strings.Join(conditions, "/"), reason)
	}
}

func withTestProcess(name string, t testing.TB, fn func(p *proc.Target, fixture protest.Fixture)) {
	withTestProcessArgs(name, t, ".", []string{}, 0, fn)
}

func withTestProcessArgs(name string, t testing.TB, wd string, args []string, buildFlags protest.BuildFlags, fn func(p *proc.Target, fixture protest.Fixture)) {
	if buildMode == "pie" {
		buildFlags |= protest.BuildModePIE
	}
	fixture := protest.BuildFixture(name, buildFlags)
	var p *proc.Target
	var err error
	var tracedir string

	switch testBackend {
	case "native":
		p, err = native.Launch(append([]string{fixture.Path}, args...), wd, 0, []string{}, "", [3]string{})
	case "lldb":
		p, err = gdbserial.LLDBLaunch(append([]string{fixture.Path}, args...), wd, 0, []string{}, "", [3]string{})
	case "rr":
		protest.MustHaveRecordingAllowed(t)
		t.Log("recording")
		p, tracedir, err = gdbserial.RecordAndReplay(append([]string{fixture.Path}, args...), wd, true, []string{}, [3]string{})
		t.Logf("replaying %q", tracedir)
	default:
		t.Fatal("unknown backend")
	}
	if err != nil {
		t.Fatal("Launch():", err)
	}

	defer func() {
		p.Detach(true)
	}()

	fn(p, fixture)
}

func getRegisters(p *proc.Target, t *testing.T) proc.Registers {
	regs, err := p.CurrentThread().Registers()
	if err != nil {
		t.Fatal("Registers():", err)
	}

	return regs
}

func dataAtAddr(thread proc.MemoryReadWriter, addr uint64) ([]byte, error) {
	data := make([]byte, 1)
	_, err := thread.ReadMemory(data, addr)
	return data, err
}

func assertNoError(err error, t testing.TB, s string) {
	if err != nil {
		_, file, line, _ := runtime.Caller(1)
		fname := filepath.Base(file)
		t.Fatalf("failed assertion at %s:%d: %s - %s\n", fname, line, s, err)
	}
}

func currentPC(p *proc.Target, t *testing.T) uint64 {
	regs, err := p.CurrentThread().Registers()
	if err != nil {
		t.Fatal(err)
	}

	return regs.PC()
}

func currentLineNumber(p *proc.Target, t *testing.T) (string, int) {
	pc := currentPC(p, t)
	f, l, _ := p.BinInfo().PCToLine(pc)
	return f, l
}

func assertLineNumber(p *proc.Target, t *testing.T, lineno int, descr string) (string, int) {
	f, l := currentLineNumber(p, t)
	if l != lineno {
		_, callerFile, callerLine, _ := runtime.Caller(1)
		t.Fatalf("%s expected line :%d got %s:%d\n\tat %s:%d", descr, lineno, f, l, callerFile, callerLine)
	}
	return f, l
}

func TestExit(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("continuetestprog", t, func(p *proc.Target, fixture protest.Fixture) {
		err := p.Continue()
		pe, ok := err.(proc.ErrProcessExited)
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
	withTestProcess("continuetestprog", t, func(p *proc.Target, fixture protest.Fixture) {
		setFunctionBreakpoint(p, t, "main.sayhi")
		assertNoError(p.Continue(), t, "First Continue()")
		err := p.Continue()
		pe, ok := err.(proc.ErrProcessExited)
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

func setFunctionBreakpoint(p *proc.Target, t testing.TB, fname string) *proc.Breakpoint {
	_, f, l, _ := runtime.Caller(1)
	f = filepath.Base(f)

	addrs, err := proc.FindFunctionLocation(p, fname, 0)
	if err != nil {
		t.Fatalf("%s:%d: FindFunctionLocation(%s): %v", f, l, fname, err)
	}
	if len(addrs) != 1 {
		t.Fatalf("%s:%d: setFunctionBreakpoint(%s): too many results %v", f, l, fname, addrs)
	}
	bp, err := p.SetBreakpoint(int(addrs[0]), addrs[0], proc.UserBreakpoint, nil)
	if err != nil {
		t.Fatalf("%s:%d: FindFunctionLocation(%s): %v", f, l, fname, err)
	}
	return bp
}

func setFileBreakpoint(p *proc.Target, t testing.TB, path string, lineno int) *proc.Breakpoint {
	_, f, l, _ := runtime.Caller(1)
	f = filepath.Base(f)

	addrs, err := proc.FindFileLocation(p, path, lineno)
	if err != nil {
		t.Fatalf("%s:%d: FindFileLocation(%s, %d): %v", f, l, path, lineno, err)
	}
	if len(addrs) != 1 {
		t.Fatalf("%s:%d: setFileLineBreakpoint(%s, %d): too many (or not enough) results %v", f, l, path, lineno, addrs)
	}
	bp, err := p.SetBreakpoint(int(addrs[0]), addrs[0], proc.UserBreakpoint, nil)
	if err != nil {
		t.Fatalf("%s:%d: SetBreakpoint: %v", f, l, err)
	}
	return bp
}

func findFunctionLocation(p *proc.Target, t *testing.T, fnname string) uint64 {
	_, f, l, _ := runtime.Caller(1)
	f = filepath.Base(f)
	addrs, err := proc.FindFunctionLocation(p, fnname, 0)
	if err != nil {
		t.Fatalf("%s:%d: FindFunctionLocation(%s): %v", f, l, fnname, err)
	}
	if len(addrs) != 1 {
		t.Fatalf("%s:%d: FindFunctionLocation(%s): too many results %v", f, l, fnname, addrs)
	}
	return addrs[0]
}

func findFileLocation(p *proc.Target, t *testing.T, file string, lineno int) uint64 {
	_, f, l, _ := runtime.Caller(1)
	f = filepath.Base(f)
	addrs, err := proc.FindFileLocation(p, file, lineno)
	if err != nil {
		t.Fatalf("%s:%d: FindFileLocation(%s, %d): %v", f, l, file, lineno, err)
	}
	if len(addrs) != 1 {
		t.Fatalf("%s:%d: FindFileLocation(%s, %d): too many results %v", f, l, file, lineno, addrs)
	}
	return addrs[0]
}

func TestHalt(t *testing.T) {
	stopChan := make(chan interface{}, 1)
	withTestProcess("loopprog", t, func(p *proc.Target, fixture protest.Fixture) {
		grp := proc.NewGroup(p)
		setFunctionBreakpoint(p, t, "main.loop")
		assertNoError(grp.Continue(), t, "Continue")
		resumeChan := make(chan struct{}, 1)
		go func() {
			<-resumeChan
			time.Sleep(100 * time.Millisecond)
			stopChan <- grp.RequestManualStop()
		}()
		grp.ResumeNotify(resumeChan)
		assertNoError(grp.Continue(), t, "Continue")
		retVal := <-stopChan

		if err, ok := retVal.(error); ok && err != nil {
			t.Fatal()
		}
	})
}

func TestStep(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("testprog", t, func(p *proc.Target, fixture protest.Fixture) {
		setFunctionBreakpoint(p, t, "main.helloworld")
		assertNoError(p.Continue(), t, "Continue()")

		regs := getRegisters(p, t)
		rip := regs.PC()

		err := p.CurrentThread().StepInstruction()
		assertNoError(err, t, "Step()")

		regs = getRegisters(p, t)
		if rip >= regs.PC() {
			t.Errorf("Expected %#v to be greater than %#v", regs.PC(), rip)
		}
	})
}

func TestBreakpoint(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("testprog", t, func(p *proc.Target, fixture protest.Fixture) {
		bp := setFunctionBreakpoint(p, t, "main.helloworld")
		assertNoError(p.Continue(), t, "Continue()")

		regs, err := p.CurrentThread().Registers()
		assertNoError(err, t, "Registers")
		pc := regs.PC()

		if bp.Logical.TotalHitCount != 1 {
			t.Fatalf("Breakpoint should be hit once, got %d\n", bp.Logical.TotalHitCount)
		}

		if pc-1 != bp.Addr && pc != bp.Addr {
			f, l, _ := p.BinInfo().PCToLine(pc)
			t.Fatalf("Break not respected:\nPC:%#v %s:%d\nFN:%#v \n", pc, f, l, bp.Addr)
		}
	})
}

func TestBreakpointInSeparateGoRoutine(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("testthreads", t, func(p *proc.Target, fixture protest.Fixture) {
		setFunctionBreakpoint(p, t, "main.anotherthread")

		assertNoError(p.Continue(), t, "Continue")

		regs, err := p.CurrentThread().Registers()
		assertNoError(err, t, "Registers")
		pc := regs.PC()

		f, l, _ := p.BinInfo().PCToLine(pc)
		if f != "testthreads.go" && l != 8 {
			t.Fatal("Program did not hit breakpoint")
		}
	})
}

func TestBreakpointWithNonExistantFunction(t *testing.T) {
	withTestProcess("testprog", t, func(p *proc.Target, fixture protest.Fixture) {
		_, err := p.SetBreakpoint(0, 0, proc.UserBreakpoint, nil)
		if err == nil {
			t.Fatal("Should not be able to break at non existent function")
		}
	})
}

func TestClearBreakpointBreakpoint(t *testing.T) {
	withTestProcess("testprog", t, func(p *proc.Target, fixture protest.Fixture) {
		bp := setFunctionBreakpoint(p, t, "main.sleepytime")

		err := p.ClearBreakpoint(bp.Addr)
		assertNoError(err, t, "ClearBreakpoint()")

		data, err := dataAtAddr(p.Memory(), bp.Addr)
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

func countBreakpoints(p *proc.Target) int {
	bpcount := 0
	for _, bp := range p.Breakpoints().M {
		if bp.LogicalID() >= 0 {
			bpcount++
		}
	}
	return bpcount
}

type contFunc int

const (
	contContinue contFunc = iota
	contNext
	contStep
	contStepout
	contReverseNext
	contReverseStep
	contReverseStepout
	contContinueToBreakpoint
)

type seqTest struct {
	cf  contFunc
	pos interface{}
}

func testseq(program string, contFunc contFunc, testcases []nextTest, initialLocation string, t *testing.T) {
	seqTestcases := make([]seqTest, len(testcases)+1)
	seqTestcases[0] = seqTest{contContinue, testcases[0].begin}
	for i := range testcases {
		if i > 0 {
			if testcases[i-1].end != testcases[i].begin {
				panic(fmt.Errorf("begin/end mismatch at index %d", i))
			}
		}
		seqTestcases[i+1] = seqTest{contFunc, testcases[i].end}
	}
	testseq2(t, program, initialLocation, seqTestcases)
}

const traceTestseq2 = true

func testseq2(t *testing.T, program string, initialLocation string, testcases []seqTest) {
	testseq2Args(".", []string{}, 0, t, program, initialLocation, testcases)
}

func testseq2Args(wd string, args []string, buildFlags protest.BuildFlags, t *testing.T, program string, initialLocation string, testcases []seqTest) {
	protest.AllowRecording(t)
	withTestProcessArgs(program, t, wd, args, buildFlags, func(p *proc.Target, fixture protest.Fixture) {
		var bp *proc.Breakpoint
		if initialLocation != "" {
			bp = setFunctionBreakpoint(p, t, initialLocation)
		} else if testcases[0].cf == contContinue {
			bp = setFileBreakpoint(p, t, fixture.Source, testcases[0].pos.(int))
		} else {
			panic("testseq2 can not set initial breakpoint")
		}
		if traceTestseq2 {
			t.Logf("initial breakpoint %v", bp)
		}
		regs, err := p.CurrentThread().Registers()
		assertNoError(err, t, "Registers")

		f, ln := currentLineNumber(p, t)
		for i, tc := range testcases {
			switch tc.cf {
			case contNext:
				if traceTestseq2 {
					t.Log("next")
				}
				assertNoError(p.Next(), t, "Next() returned an error")
			case contStep:
				if traceTestseq2 {
					t.Log("step")
				}
				assertNoError(p.Step(), t, "Step() returned an error")
			case contStepout:
				if traceTestseq2 {
					t.Log("stepout")
				}
				assertNoError(p.StepOut(), t, "StepOut() returned an error")
			case contContinue:
				if traceTestseq2 {
					t.Log("continue")
				}
				assertNoError(p.Continue(), t, "Continue() returned an error")
				if i == 0 {
					if traceTestseq2 {
						t.Log("clearing initial breakpoint")
					}
					err := p.ClearBreakpoint(bp.Addr)
					assertNoError(err, t, "ClearBreakpoint() returned an error")
				}
			case contReverseNext:
				if traceTestseq2 {
					t.Log("reverse-next")
				}
				assertNoError(p.ChangeDirection(proc.Backward), t, "direction switch")
				assertNoError(p.Next(), t, "reverse Next() returned an error")
				assertNoError(p.ChangeDirection(proc.Forward), t, "direction switch")
			case contReverseStep:
				if traceTestseq2 {
					t.Log("reverse-step")
				}
				assertNoError(p.ChangeDirection(proc.Backward), t, "direction switch")
				assertNoError(p.Step(), t, "reverse Step() returned an error")
				assertNoError(p.ChangeDirection(proc.Forward), t, "direction switch")
			case contReverseStepout:
				if traceTestseq2 {
					t.Log("reverse-stepout")
				}
				assertNoError(p.ChangeDirection(proc.Backward), t, "direction switch")
				assertNoError(p.StepOut(), t, "reverse StepOut() returned an error")
				assertNoError(p.ChangeDirection(proc.Forward), t, "direction switch")
			case contContinueToBreakpoint:
				bp := setFileBreakpoint(p, t, fixture.Source, tc.pos.(int))
				if traceTestseq2 {
					t.Log("continue")
				}
				assertNoError(p.Continue(), t, "Continue() returned an error")
				err := p.ClearBreakpoint(bp.Addr)
				assertNoError(err, t, "ClearBreakpoint() returned an error")
			}

			f, ln = currentLineNumber(p, t)
			regs, _ = p.CurrentThread().Registers()
			pc := regs.PC()

			if traceTestseq2 {
				t.Logf("at %#x %s:%d", pc, f, ln)
				fmt.Printf("at %#x %s:%d\n", pc, f, ln)
			}
			switch pos := tc.pos.(type) {
			case int:
				if pos >= 0 && ln != pos {
					t.Fatalf("Program did not continue to correct next location expected %d was %s:%d (%#x) (testcase %d)", pos, filepath.Base(f), ln, pc, i)
				}
			case string:
				v := strings.Split(pos, ":")
				tgtln, _ := strconv.Atoi(v[1])
				if !strings.HasSuffix(f, v[0]) || (ln != tgtln) {
					t.Fatalf("Program did not continue to correct next location, expected %s was %s:%d (%#x) (testcase %d)", pos, filepath.Base(f), ln, pc, i)
				}
			}
		}

		if countBreakpoints(p) != 0 {
			t.Fatal("Not all breakpoints were cleaned up", len(p.Breakpoints().M))
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

	testseq("testnextprog", contNext, testcases, "main.testnext", t)
}

func TestNextConcurrent(t *testing.T) {
	testcases := []nextTest{
		{8, 9},
		{9, 10},
		{10, 11},
	}
	protest.AllowRecording(t)
	withTestProcess("parallel_next", t, func(p *proc.Target, fixture protest.Fixture) {
		bp := setFunctionBreakpoint(p, t, "main.sayhi")
		assertNoError(p.Continue(), t, "Continue")
		f, ln := currentLineNumber(p, t)
		initV := evalVariable(p, t, "n")
		initVval, _ := constant.Int64Val(initV.Value)
		err := p.ClearBreakpoint(bp.Addr)
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
			assertNoError(p.Next(), t, "Next() returned an error")
			f, ln = assertLineNumber(p, t, tc.end, "Program did not continue to the expected location")
			v := evalVariable(p, t, "n")
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
	withTestProcess("parallel_next", t, func(p *proc.Target, fixture protest.Fixture) {
		setFunctionBreakpoint(p, t, "main.sayhi")
		assertNoError(p.Continue(), t, "Continue")
		f, ln := currentLineNumber(p, t)
		initV := evalVariable(p, t, "n")
		initVval, _ := constant.Int64Val(initV.Value)
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
			assertNoError(p.Next(), t, "Next() returned an error")
			var vval int64
			for {
				v := evalVariable(p, t, "n")
				for _, thread := range p.ThreadList() {
					proc.GetG(thread)
				}
				vval, _ = constant.Int64Val(v.Value)
				if bpstate := p.CurrentThread().Breakpoint(); bpstate.Breakpoint == nil {
					if vval != initVval {
						t.Fatal("Did not end up on same goroutine")
					}
					break
				} else {
					if vval == initVval {
						t.Fatal("Initial breakpoint triggered twice for the same goroutine")
					}
					assertNoError(p.Continue(), t, "Continue 2")
				}
			}
			f, ln = assertLineNumber(p, t, tc.end, "Program did not continue to the expected location")
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
	var testcases []nextTest

	ver, _ := goversion.Parse(runtime.Version())

	if ver.Major < 0 || ver.AfterOrEqual(goversion.GoVersion{Major: 1, Minor: 9, Rev: -1}) {
		testcases = []nextTest{
			{5, 6},
			{6, 9},
			{9, 10},
		}
	} else {
		testcases = []nextTest{
			{5, 8},
			{8, 9},
			{9, 10},
		}
	}
	protest.AllowRecording(t)
	testseq("testnextdefer", contNext, testcases, "main.main", t)
}

func TestNextNetHTTP(t *testing.T) {
	testcases := []nextTest{
		{11, 12},
		{12, 13},
	}
	withTestProcess("testnextnethttp", t, func(p *proc.Target, fixture protest.Fixture) {
		go func() {
			// Wait for program to start listening.
			for {
				conn, err := net.Dial("tcp", "127.0.0.1:9191")
				if err == nil {
					conn.Close()
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			http.Get("http://127.0.0.1:9191")
		}()
		if err := p.Continue(); err != nil {
			t.Fatal(err)
		}
		f, ln := currentLineNumber(p, t)
		for _, tc := range testcases {
			if ln != tc.begin {
				t.Fatalf("Program not stopped at correct spot expected %d was %s:%d", tc.begin, filepath.Base(f), ln)
			}

			assertNoError(p.Next(), t, "Next() returned an error")

			f, ln = assertLineNumber(p, t, tc.end, "Program did not continue to correct next location")
		}
	})
}

func TestRuntimeBreakpoint(t *testing.T) {
	withTestProcess("testruntimebreakpoint", t, func(p *proc.Target, fixture protest.Fixture) {
		err := p.Continue()
		if err != nil {
			t.Fatal(err)
		}
		regs, err := p.CurrentThread().Registers()
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
		return 0, fmt.Errorf("no return address for function: %s", locations[0].Current.Fn.BaseName())
	}
	return locations[1].Current.PC, nil
}

func TestFindReturnAddress(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("testnextprog", t, func(p *proc.Target, fixture protest.Fixture) {
		setFileBreakpoint(p, t, fixture.Source, 24)
		err := p.Continue()
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
	withTestProcess("testreturnaddress", t, func(p *proc.Target, fixture protest.Fixture) {
		fnName := "runtime.rt0_go"
		setFunctionBreakpoint(p, t, fnName)
		if err := p.Continue(); err != nil {
			t.Fatal(err)
		}
		if _, err := returnAddress(p.CurrentThread()); err == nil {
			t.Fatal("expected error to be returned")
		}
	})
}

func TestSwitchThread(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("testnextprog", t, func(p *proc.Target, fixture protest.Fixture) {
		// With invalid thread id
		err := p.SwitchThread(-1)
		if err == nil {
			t.Fatal("Expected error for invalid thread id")
		}
		setFunctionBreakpoint(p, t, "main.main")
		err = p.Continue()
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
	if !goversion.VersionAfterOrEqual(runtime.Version(), 1, 5) {
		skipOn(t, "upstream issue", "darwin")
	}
	protest.MustHaveCgo(t)

	skipOn(t, "broken - cgo stacktraces", "darwin", "arm64")
	skipOn(t, "broken - see https://github.com/go-delve/delve/issues/3158", "darwin", "amd64")

	protest.AllowRecording(t)
	withTestProcess("cgotest", t, func(p *proc.Target, fixture protest.Fixture) {
		setFunctionBreakpoint(p, t, "main.main")
		assertNoError(p.Continue(), t, "Continue()")
		assertNoError(p.Next(), t, "Next()")
	})
}

func TestCGOBreakpointLocation(t *testing.T) {
	protest.MustHaveCgo(t)
	protest.AllowRecording(t)

	withTestProcess("cgotest", t, func(p *proc.Target, fixture protest.Fixture) {
		bp := setFunctionBreakpoint(p, t, "C.foo")
		if !strings.Contains(bp.File, "cgotest.go") {
			t.Fatalf("incorrect breakpoint location, expected cgotest.go got %s", bp.File)
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
	withTestProcess("stacktraceprog", t, func(p *proc.Target, fixture protest.Fixture) {
		bp := setFunctionBreakpoint(p, t, "main.stacktraceme")

		for i := range stacks {
			assertNoError(p.Continue(), t, "Continue()")
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
		p.Continue()
	})
}

func TestStacktrace2(t *testing.T) {
	withTestProcess("retstack", t, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue()")

		locations, err := proc.ThreadStacktrace(p.CurrentThread(), 40)
		assertNoError(err, t, "Stacktrace()")
		if !stackMatch([]loc{{-1, "main.f"}, {16, "main.main"}}, locations, false) {
			for i := range locations {
				t.Logf("\t%s:%d [%s]\n", locations[i].Call.File, locations[i].Call.Line, locations[i].Call.Fn.Name)
			}
			t.Fatalf("Stack error at main.f()\n%v\n", locations)
		}

		assertNoError(p.Continue(), t, "Continue()")
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
	skipOn(t, "broken - cgo stacktraces", "darwin", "arm64")

	mainStack := []loc{{14, "main.stacktraceme"}, {29, "main.main"}}
	if goversion.VersionAfterOrEqual(runtime.Version(), 1, 11) {
		mainStack[0].line = 15
	}
	agoroutineStacks := [][]loc{
		{{8, "main.agoroutine"}},
		{{9, "main.agoroutine"}},
		{{10, "main.agoroutine"}},
	}

	lenient := 0
	if runtime.GOOS == "windows" {
		lenient = 1
	}

	protest.AllowRecording(t)
	withTestProcess("goroutinestackprog", t, func(p *proc.Target, fixture protest.Fixture) {
		bp := setFunctionBreakpoint(p, t, "main.stacktraceme")

		assertNoError(p.Continue(), t, "Continue()")

		gs, _, err := proc.GoroutinesInfo(p, 0, 0)
		assertNoError(err, t, "GoroutinesInfo")

		agoroutineCount := 0
		mainCount := 0

		for i, g := range gs {
			locations, err := g.Stacktrace(40, 0)
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
					t.Logf("\t%s:%d %s (%#x) %x %v\n", locations[i].Call.File, locations[i].Call.Line, name, locations[i].Current.PC, locations[i].FrameOffset(), locations[i].SystemStack)
				}
			}
		}

		if mainCount != 1 {
			t.Fatalf("Main goroutine stack not found %d", mainCount)
		}

		if agoroutineCount < 10-lenient {
			t.Fatalf("Goroutine stacks not found (%d)", agoroutineCount)
		}

		p.ClearBreakpoint(bp.Addr)
		p.Continue()
	})
}

func TestKill(t *testing.T) {
	skipOn(t, "N/A", "lldb") // k command presumably works but leaves the process around?
	withTestProcess("testprog", t, func(p *proc.Target, fixture protest.Fixture) {
		if err := p.Detach(true); err != nil {
			t.Fatal(err)
		}
		if valid, _ := p.Valid(); valid {
			t.Fatal("expected process to have exited")
		}
		if runtime.GOOS == "linux" {
			if runtime.GOARCH == "arm64" {
				// there is no any sync between signal sended(tracee handled) and open /proc/%d/. It may fail on arm64
				return
			}
			_, err := os.Open(fmt.Sprintf("/proc/%d/", p.Pid()))
			if err == nil {
				t.Fatal("process has not exited", p.Pid())
			}
		}
	})
}

func testGSupportFunc(name string, t *testing.T, p *proc.Target, fixture protest.Fixture) {
	bp := setFunctionBreakpoint(p, t, "main.main")

	assertNoError(p.Continue(), t, name+": Continue()")

	g, err := proc.GetG(p.CurrentThread())
	assertNoError(err, t, name+": GetG()")

	if g == nil {
		t.Fatal(name + ": g was nil")
	}

	t.Logf(name+": g is: %v", g)

	p.ClearBreakpoint(bp.Addr)
}

func TestGetG(t *testing.T) {
	withTestProcess("testprog", t, func(p *proc.Target, fixture protest.Fixture) {
		testGSupportFunc("nocgo", t, p, fixture)
	})

	// On OSX with Go < 1.5 CGO is not supported due to: https://github.com/golang/go/issues/8973
	if !goversion.VersionAfterOrEqual(runtime.Version(), 1, 5) {
		skipOn(t, "upstream issue", "darwin")
	}
	protest.MustHaveCgo(t)

	protest.AllowRecording(t)
	withTestProcess("cgotest", t, func(p *proc.Target, fixture protest.Fixture) {
		testGSupportFunc("cgo", t, p, fixture)
	})
}

func TestContinueMulti(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("integrationprog", t, func(p *proc.Target, fixture protest.Fixture) {
		bp1 := setFunctionBreakpoint(p, t, "main.main")
		bp2 := setFunctionBreakpoint(p, t, "main.sayhi")

		mainCount := 0
		sayhiCount := 0
		for {
			err := p.Continue()
			if valid, _ := p.Valid(); !valid {
				break
			}
			assertNoError(err, t, "Continue()")

			if bp := p.CurrentThread().Breakpoint(); bp.LogicalID() == bp1.LogicalID() {
				mainCount++
			}

			if bp := p.CurrentThread().Breakpoint(); bp.LogicalID() == bp2.LogicalID() {
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

func TestBreakpointOnFunctionEntry(t *testing.T) {
	testseq2(t, "testprog", "main.main", []seqTest{{contContinue, 17}})
}

func TestProcessReceivesSIGCHLD(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("sigchldprog", t, func(p *proc.Target, fixture protest.Fixture) {
		err := p.Continue()
		_, ok := err.(proc.ErrProcessExited)
		if !ok {
			t.Fatalf("Continue() returned unexpected error type %v", err)
		}
	})
}

func TestIssue239(t *testing.T) {
	withTestProcess("is sue239", t, func(p *proc.Target, fixture protest.Fixture) {
		setFileBreakpoint(p, t, fixture.Source, 17)
		assertNoError(p.Continue(), t, "Continue()")
	})
}

func findFirstNonRuntimeFrame(p *proc.Target) (proc.Stackframe, error) {
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

func evalVariableOrError(p *proc.Target, symbol string) (*proc.Variable, error) {
	var scope *proc.EvalScope
	var err error

	if testBackend == "rr" {
		var frame proc.Stackframe
		frame, err = findFirstNonRuntimeFrame(p)
		if err == nil {
			scope = proc.FrameToScope(p, p.Memory(), nil, frame)
		}
	} else {
		scope, err = proc.GoroutineScope(p, p.CurrentThread())
	}

	if err != nil {
		return nil, err
	}
	return scope.EvalExpression(symbol, normalLoadConfig)
}

func evalVariable(p *proc.Target, t testing.TB, symbol string) *proc.Variable {
	v, err := evalVariableOrError(p, symbol)
	if err != nil {
		_, file, line, _ := runtime.Caller(1)
		fname := filepath.Base(file)
		t.Fatalf("%s:%d: EvalVariable(%q): %v", fname, line, symbol, err)
	}
	return v
}

func setVariable(p *proc.Target, symbol, value string) error {
	scope, err := proc.GoroutineScope(p, p.CurrentThread())
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

	withTestProcess("testvariables", t, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue() returned an error")

		for _, tc := range testcases {
			v := evalVariable(p, t, tc.name)

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
	lenient := false
	if runtime.GOOS == "windows" {
		lenient = true
	}
	withTestProcess("goroutinestackprog", t, func(p *proc.Target, fixture protest.Fixture) {
		setFunctionBreakpoint(p, t, "main.stacktraceme")
		assertNoError(p.Continue(), t, "Continue()")

		t.Logf("stopped on thread %d, goroutine: %#v", p.CurrentThread().ThreadID(), p.SelectedGoroutine())

		// Testing evaluation on goroutines
		gs, _, err := proc.GoroutinesInfo(p, 0, 0)
		assertNoError(err, t, "GoroutinesInfo")
		found := make([]bool, 10)
		for _, g := range gs {
			frame := -1
			frames, err := g.Stacktrace(40, 0)
			if err != nil {
				t.Logf("could not stacktrace goroutine %d: %v\n", g.ID, err)
				continue
			}
			t.Logf("Goroutine %d %#v", g.ID, g.Thread)
			logStacktrace(t, p, frames)
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

			scope, err := proc.ConvertEvalScope(p, g.ID, frame, 0)
			assertNoError(err, t, "ConvertEvalScope()")
			t.Logf("scope = %v", scope)
			v, err := scope.EvalExpression("i", normalLoadConfig)
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
				if lenient {
					lenient = false
				} else {
					t.Fatalf("Goroutine %d not found\n", i)
				}
			}
		}

		// Testing evaluation on frames
		assertNoError(p.Continue(), t, "Continue() 2")
		g, err := proc.GetG(p.CurrentThread())
		assertNoError(err, t, "GetG()")

		frames, err := g.Stacktrace(40, 0)
		t.Logf("Goroutine %d %#v", g.ID, g.Thread)
		logStacktrace(t, p, frames)

		for i := 0; i <= 3; i++ {
			scope, err := proc.ConvertEvalScope(p, g.ID, i+1, 0)
			assertNoError(err, t, fmt.Sprintf("ConvertEvalScope() on frame %d", i+1))
			v, err := scope.EvalExpression("n", normalLoadConfig)
			assertNoError(err, t, fmt.Sprintf("EvalVariable() on frame %d", i+1))
			n, _ := constant.Int64Val(v.Value)
			t.Logf("frame %d n %d\n", i+1, n)
			if n != int64(3-i) {
				t.Fatalf("On frame %d value of n is %d (not %d)", i+1, n, 3-i)
			}
		}
	})
}

func TestThreadFrameEvaluation(t *testing.T) {
	skipOn(t, "upstream issue - https://github.com/golang/go/issues/29322", "pie")
	deadlockBp := proc.FatalThrow
	if !goversion.VersionAfterOrEqual(runtime.Version(), 1, 11) {
		t.SkipNow()
	}
	withTestProcess("testdeadlock", t, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue()")

		bp := p.CurrentThread().Breakpoint()
		if bp.Breakpoint == nil || bp.Logical.Name != deadlockBp {
			t.Fatalf("did not stop at deadlock breakpoint %v", bp)
		}

		// There is no selected goroutine during a deadlock, so the scope will
		// be a thread scope.
		scope, err := proc.ConvertEvalScope(p, 0, 0, 0)
		assertNoError(err, t, "ConvertEvalScope() on frame 0")
		_, err = scope.EvalExpression("s", normalLoadConfig)
		assertNoError(err, t, "EvalVariable(\"s\") on frame 0")
	})
}

func TestPointerSetting(t *testing.T) {
	withTestProcess("testvariables2", t, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue() returned an error")

		pval := func(n int64) {
			variable := evalVariable(p, t, "p1")
			c0val, _ := constant.Int64Val(variable.Children[0].Value)
			if c0val != n {
				t.Fatalf("Wrong value of p1, *%d expected *%d", c0val, n)
			}
		}

		pval(1)

		// change p1 to point to i2
		scope, err := proc.GoroutineScope(p, p.CurrentThread())
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
	withTestProcess("testvariables", t, func(p *proc.Target, fixture protest.Fixture) {
		err := p.Continue()
		assertNoError(err, t, "Continue() returned an error")

		evalVariable(p, t, "a1")
		evalVariable(p, t, "a2")

		// Move scopes, a1 exists here by a2 does not
		err = p.Continue()
		assertNoError(err, t, "Continue() returned an error")

		evalVariable(p, t, "a1")

		_, err = evalVariableOrError(p, "a2")
		if err == nil {
			t.Fatalf("Can eval out of scope variable a2")
		}
	})
}

func TestRecursiveStructure(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("testvariables2", t, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue()")
		v := evalVariable(p, t, "aas")
		t.Logf("v: %v\n", v)
	})
}

func TestIssue316(t *testing.T) {
	// A pointer loop that includes one interface should not send dlv into an infinite loop
	protest.AllowRecording(t)
	withTestProcess("testvariables2", t, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue()")
		evalVariable(p, t, "iface5")
	})
}

func TestIssue325(t *testing.T) {
	// nil pointer dereference when evaluating interfaces to function pointers
	protest.AllowRecording(t)
	withTestProcess("testvariables2", t, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue()")
		iface2fn1v := evalVariable(p, t, "iface2fn1")
		t.Logf("iface2fn1: %v\n", iface2fn1v)

		iface2fn2v := evalVariable(p, t, "iface2fn2")
		t.Logf("iface2fn2: %v\n", iface2fn2v)
	})
}

func TestBreakpointCounts(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("bpcountstest", t, func(p *proc.Target, fixture protest.Fixture) {
		bp := setFileBreakpoint(p, t, fixture.Source, 12)

		for {
			if err := p.Continue(); err != nil {
				if _, exited := err.(proc.ErrProcessExited); exited {
					break
				}
				assertNoError(err, t, "Continue()")
			}
		}

		t.Logf("TotalHitCount: %d", bp.Logical.TotalHitCount)
		if bp.Logical.TotalHitCount != 200 {
			t.Fatalf("Wrong TotalHitCount for the breakpoint (%d)", bp.Logical.TotalHitCount)
		}

		if len(bp.Logical.HitCount) != 2 {
			t.Fatalf("Wrong number of goroutines for breakpoint (%d)", len(bp.Logical.HitCount))
		}

		for _, v := range bp.Logical.HitCount {
			if v != 100 {
				t.Fatalf("Wrong HitCount for breakpoint (%v)", bp.Logical.HitCount)
			}
		}
	})
}

func TestHardcodedBreakpointCounts(t *testing.T) {
	withTestProcess("hcbpcountstest", t, func(p *proc.Target, fixture protest.Fixture) {
		counts := map[int64]int{}
		for {
			if err := p.Continue(); err != nil {
				if _, exited := err.(proc.ErrProcessExited); exited {
					break
				}
				assertNoError(err, t, "Continue()")
			}

			for _, th := range p.ThreadList() {
				bp := th.Breakpoint().Breakpoint
				if bp == nil {
					continue
				}
				if bp.Logical.Name != proc.HardcodedBreakpoint {
					t.Fatalf("wrong breakpoint name %s", bp.Logical.Name)
				}
				g, err := proc.GetG(th)
				assertNoError(err, t, "GetG")
				counts[g.ID]++
			}
		}

		if len(counts) != 2 {
			t.Fatalf("Wrong number of goroutines for hardcoded breakpoint (%d)", len(counts))
		}

		for goid, count := range counts {
			if count != 100 {
				t.Fatalf("Wrong hit count for hardcoded breakpoint (%d) on goroutine %d", count, goid)
			}
		}
	})
}

func BenchmarkArray(b *testing.B) {
	// each bencharr struct is 128 bytes, bencharr is 64 elements long
	b.SetBytes(int64(64 * 128))
	withTestProcess("testvariables2", b, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), b, "Continue()")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			evalVariable(p, b, "bencharr")
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
	withTestProcess("bpcountstest", t, func(p *proc.Target, fixture protest.Fixture) {
		bp := setFileBreakpoint(p, t, fixture.Source, 12)

		for {
			if err := p.Continue(); err != nil {
				if _, exited := err.(proc.ErrProcessExited); exited {
					break
				}
				assertNoError(err, t, "Continue()")
			}
			for _, th := range p.ThreadList() {
				if bp := th.Breakpoint(); bp.Breakpoint == nil {
					continue
				}
				scope, err := proc.GoroutineScope(p, th)
				assertNoError(err, t, "Scope()")
				v, err := scope.EvalExpression("i", normalLoadConfig)
				assertNoError(err, t, "evalVariable")
				i, _ := constant.Int64Val(v.Value)
				v, err = scope.EvalExpression("id", normalLoadConfig)
				assertNoError(err, t, "evalVariable")
				id, _ := constant.Int64Val(v.Value)
				m[id] = i
			}

			total := int64(0)
			for i := range m {
				total += m[i] + 1
			}

			if uint64(total) != bp.Logical.TotalHitCount {
				t.Fatalf("Mismatched total count %d %d\n", total, bp.Logical.TotalHitCount)
			}
		}

		t.Logf("TotalHitCount: %d", bp.Logical.TotalHitCount)
		if bp.Logical.TotalHitCount != 200 {
			t.Fatalf("Wrong TotalHitCount for the breakpoint (%d)", bp.Logical.TotalHitCount)
		}

		if len(bp.Logical.HitCount) != 2 {
			t.Fatalf("Wrong number of goroutines for breakpoint (%d)", len(bp.Logical.HitCount))
		}

		for _, v := range bp.Logical.HitCount {
			if v != 100 {
				t.Fatalf("Wrong HitCount for breakpoint (%v)", bp.Logical.HitCount)
			}
		}
	})
}

func BenchmarkArrayPointer(b *testing.B) {
	// each bencharr struct is 128 bytes, benchparr is an array of 64 pointers to bencharr
	// each read will read 64 bencharr structs plus the 64 pointers of benchparr
	b.SetBytes(int64(64*128 + 64*8))
	withTestProcess("testvariables2", b, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), b, "Continue()")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			evalVariable(p, b, "bencharr")
		}
	})
}

func BenchmarkMap(b *testing.B) {
	// m1 contains 41 entries, each one has a value that's 2 int values (2* 8 bytes) and a string key
	// each string key has an average of 9 character
	// reading strings and the map structure imposes a overhead that we ignore here
	b.SetBytes(int64(41 * (2*8 + 9)))
	withTestProcess("testvariables2", b, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), b, "Continue()")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			evalVariable(p, b, "m1")
		}
	})
}

func BenchmarkGoroutinesInfo(b *testing.B) {
	withTestProcess("testvariables2", b, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), b, "Continue()")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			p.ClearCaches()
			_, _, err := proc.GoroutinesInfo(p, 0, 0)
			assertNoError(err, b, "GoroutinesInfo")
		}
	})
}

func TestIssue262(t *testing.T) {
	// Continue does not work when the current breakpoint is set on a NOP instruction
	protest.AllowRecording(t)
	withTestProcess("issue262", t, func(p *proc.Target, fixture protest.Fixture) {
		setFileBreakpoint(p, t, fixture.Source, 11)

		assertNoError(p.Continue(), t, "Continue()")
		err := p.Continue()
		if err == nil {
			t.Fatalf("No error on second continue")
		}
		_, exited := err.(proc.ErrProcessExited)
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
	withTestProcess("issue305", t, func(p *proc.Target, fixture protest.Fixture) {
		setFileBreakpoint(p, t, fixture.Source, 5)

		assertNoError(p.Continue(), t, "Continue()")

		assertNoError(p.Next(), t, "Next() 1")
		assertNoError(p.Next(), t, "Next() 2")
		assertNoError(p.Next(), t, "Next() 3")
		assertNoError(p.Next(), t, "Next() 4")
		assertNoError(p.Next(), t, "Next() 5")
	})
}

func TestPointerLoops(t *testing.T) {
	// Pointer loops through map entries, pointers and slices
	// Regression test for issue #341
	protest.AllowRecording(t)
	withTestProcess("testvariables2", t, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue()")
		for _, expr := range []string{"mapinf", "ptrinf", "sliceinf"} {
			t.Logf("requesting %s", expr)
			v := evalVariable(p, t, expr)
			t.Logf("%s: %v\n", expr, v)
		}
	})
}

func BenchmarkLocalVariables(b *testing.B) {
	withTestProcess("testvariables", b, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), b, "Continue() returned an error")
		scope, err := proc.GoroutineScope(p, p.CurrentThread())
		assertNoError(err, b, "Scope()")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := scope.LocalVariables(normalLoadConfig)
			assertNoError(err, b, "LocalVariables()")
		}
	})
}

func TestCondBreakpoint(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("parallel_next", t, func(p *proc.Target, fixture protest.Fixture) {
		bp := setFileBreakpoint(p, t, fixture.Source, 9)
		bp.UserBreaklet().Cond = &ast.BinaryExpr{
			Op: token.EQL,
			X:  &ast.Ident{Name: "n"},
			Y:  &ast.BasicLit{Kind: token.INT, Value: "7"},
		}

		assertNoError(p.Continue(), t, "Continue()")

		nvar := evalVariable(p, t, "n")

		n, _ := constant.Int64Val(nvar.Value)
		if n != 7 {
			t.Fatalf("Stopped on wrong goroutine %d\n", n)
		}
	})
}

func TestCondBreakpointError(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("parallel_next", t, func(p *proc.Target, fixture protest.Fixture) {
		bp := setFileBreakpoint(p, t, fixture.Source, 9)
		bp.UserBreaklet().Cond = &ast.BinaryExpr{
			Op: token.EQL,
			X:  &ast.Ident{Name: "nonexistentvariable"},
			Y:  &ast.BasicLit{Kind: token.INT, Value: "7"},
		}

		err := p.Continue()
		if err == nil {
			t.Fatalf("No error on first Continue()")
		}

		if err.Error() != "error evaluating expression: could not find symbol value for nonexistentvariable" && err.Error() != "multiple errors evaluating conditions" {
			t.Fatalf("Unexpected error on first Continue(): %v", err)
		}

		bp.UserBreaklet().Cond = &ast.BinaryExpr{
			Op: token.EQL,
			X:  &ast.Ident{Name: "n"},
			Y:  &ast.BasicLit{Kind: token.INT, Value: "7"},
		}

		err = p.Continue()
		if err != nil {
			if _, exited := err.(proc.ErrProcessExited); !exited {
				t.Fatalf("Unexpected error on second Continue(): %v", err)
			}
		} else {
			nvar := evalVariable(p, t, "n")

			n, _ := constant.Int64Val(nvar.Value)
			if n != 7 {
				t.Fatalf("Stopped on wrong goroutine %d\n", n)
			}
		}
	})
}

func TestHitCondBreakpointEQ(t *testing.T) {
	withTestProcess("break", t, func(p *proc.Target, fixture protest.Fixture) {
		bp := setFileBreakpoint(p, t, fixture.Source, 7)
		bp.Logical.HitCond = &struct {
			Op  token.Token
			Val int
		}{token.EQL, 3}

		assertNoError(p.Continue(), t, "Continue()")
		ivar := evalVariable(p, t, "i")

		i, _ := constant.Int64Val(ivar.Value)
		if i != 3 {
			t.Fatalf("Stopped on wrong hitcount %d\n", i)
		}

		err := p.Continue()
		if _, exited := err.(proc.ErrProcessExited); !exited {
			t.Fatalf("Unexpected error on Continue(): %v", err)
		}
	})
}

func TestHitCondBreakpointGEQ(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("break", t, func(p *proc.Target, fixture protest.Fixture) {
		bp := setFileBreakpoint(p, t, fixture.Source, 7)
		bp.Logical.HitCond = &struct {
			Op  token.Token
			Val int
		}{token.GEQ, 3}

		for it := 3; it <= 10; it++ {
			assertNoError(p.Continue(), t, "Continue()")
			ivar := evalVariable(p, t, "i")

			i, _ := constant.Int64Val(ivar.Value)
			if int(i) != it {
				t.Fatalf("Stopped on wrong hitcount %d\n", i)
			}
		}

		assertNoError(p.Continue(), t, "Continue()")
	})
}

func TestHitCondBreakpointREM(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("break", t, func(p *proc.Target, fixture protest.Fixture) {
		bp := setFileBreakpoint(p, t, fixture.Source, 7)
		bp.Logical.HitCond = &struct {
			Op  token.Token
			Val int
		}{token.REM, 2}

		for it := 2; it <= 10; it += 2 {
			assertNoError(p.Continue(), t, "Continue()")
			ivar := evalVariable(p, t, "i")

			i, _ := constant.Int64Val(ivar.Value)
			if int(i) != it {
				t.Fatalf("Stopped on wrong hitcount %d\n", i)
			}
		}

		err := p.Continue()
		if _, exited := err.(proc.ErrProcessExited); !exited {
			t.Fatalf("Unexpected error on Continue(): %v", err)
		}
	})
}

func TestIssue356(t *testing.T) {
	// slice with a typedef does not get printed correctly
	protest.AllowRecording(t)
	withTestProcess("testvariables2", t, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue() returned an error")
		mmvar := evalVariable(p, t, "mainMenu")
		if mmvar.Kind != reflect.Slice {
			t.Fatalf("Wrong kind for mainMenu: %v\n", mmvar.Kind)
		}
	})
}

func TestStepIntoFunction(t *testing.T) {
	withTestProcess("teststep", t, func(p *proc.Target, fixture protest.Fixture) {
		// Continue until breakpoint
		assertNoError(p.Continue(), t, "Continue() returned an error")
		// Step into function
		assertNoError(p.Step(), t, "Step() returned an error")
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

func TestIssue332_Part1(t *testing.T) {
	// Next shouldn't step inside a function call
	protest.AllowRecording(t)
	withTestProcess("issue332", t, func(p *proc.Target, fixture protest.Fixture) {
		setFileBreakpoint(p, t, fixture.Source, 8)
		assertNoError(p.Continue(), t, "Continue()")
		assertNoError(p.Next(), t, "first Next()")
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
	withTestProcess("issue332", t, func(p *proc.Target, fixture protest.Fixture) {
		setFileBreakpoint(p, t, fixture.Source, 8)
		assertNoError(p.Continue(), t, "Continue()")

		// step until we enter changeMe
		for {
			assertNoError(p.Step(), t, "Step()")
			locations, err := proc.ThreadStacktrace(p.CurrentThread(), 2)
			assertNoError(err, t, "Stacktrace()")
			if locations[0].Call.Fn == nil {
				t.Fatalf("Not on a function")
			}
			if locations[0].Call.Fn.Name == "main.changeMe" {
				break
			}
		}

		regs, err := p.CurrentThread().Registers()
		assertNoError(err, t, "Registers()")
		pc := regs.PC()
		pcAfterPrologue := findFunctionLocation(p, t, "main.changeMe")
		if pcAfterPrologue == p.BinInfo().LookupFunc["main.changeMe"].Entry {
			t.Fatalf("main.changeMe and main.changeMe:0 are the same (%x)", pcAfterPrologue)
		}
		if pc != pcAfterPrologue {
			t.Fatalf("Step did not skip the prologue: current pc: %x, first instruction after prologue: %x", pc, pcAfterPrologue)
		}

		assertNoError(p.Next(), t, "first Next()")
		assertNoError(p.Next(), t, "second Next()")
		assertNoError(p.Next(), t, "third Next()")
		err = p.Continue()
		if _, exited := err.(proc.ErrProcessExited); !exited {
			assertNoError(err, t, "final Continue()")
		}
	})
}

func TestIssue414(t *testing.T) {
	skipOn(t, "broken", "linux", "386", "pie") // test occasionally hangs on linux/386/pie
	// Stepping until the program exits
	protest.AllowRecording(t)
	withTestProcess("math", t, func(p *proc.Target, fixture protest.Fixture) {
		setFileBreakpoint(p, t, fixture.Source, 9)
		assertNoError(p.Continue(), t, "Continue()")
		for {
			pc := currentPC(p, t)
			f, ln := currentLineNumber(p, t)
			t.Logf("at %s:%d %#x\n", f, ln, pc)
			var err error
			// Stepping through the runtime is not generally safe so after we are out
			// of main.main just use Next.
			// See: https://github.com/go-delve/delve/pull/2082
			if f == fixture.Source {
				err = p.Step()
			} else {
				err = p.Next()
			}
			if err != nil {
				if _, exited := err.(proc.ErrProcessExited); exited {
					break
				}
			}
			assertNoError(err, t, "Step()")
		}
	})
}

func TestPackageVariables(t *testing.T) {
	var skippedVariable = map[string]bool{
		"runtime.uint16Eface": true,
		"runtime.uint32Eface": true,
		"runtime.uint64Eface": true,
		"runtime.stringEface": true,
		"runtime.sliceEface":  true,
	}

	protest.AllowRecording(t)
	withTestProcess("testvariables", t, func(p *proc.Target, fixture protest.Fixture) {
		err := p.Continue()
		assertNoError(err, t, "Continue()")
		scope, err := proc.GoroutineScope(p, p.CurrentThread())
		assertNoError(err, t, "Scope()")
		vars, err := scope.PackageVariables(normalLoadConfig)
		assertNoError(err, t, "PackageVariables()")
		failed := false
		for _, v := range vars {
			if skippedVariable[v.Name] {
				continue
			}
			if v.Unreadable != nil && v.Unreadable.Error() != "no location attribute Location" {
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
	ver, _ := goversion.Parse(runtime.Version())
	if ver.Major > 0 && !ver.AfterOrEqual(goversion.GoVersion{Major: 1, Minor: 7, Rev: -1}) {
		return
	}
	// setting breakpoint on break statement
	withTestProcess("break", t, func(p *proc.Target, fixture protest.Fixture) {
		findFileLocation(p, t, fixture.Source, 8)
	})
}

func TestPanicBreakpoint(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("panic", t, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue()")
		bp := p.CurrentThread().Breakpoint()
		if bp.Breakpoint == nil || bp.Logical.Name != proc.UnrecoveredPanic {
			t.Fatalf("not on unrecovered-panic breakpoint: %v", bp)
		}
	})
}

func TestCmdLineArgs(t *testing.T) {
	expectSuccess := func(p *proc.Target, fixture protest.Fixture) {
		err := p.Continue()
		bp := p.CurrentThread().Breakpoint()
		if bp.Breakpoint != nil && bp.Logical.Name == proc.UnrecoveredPanic {
			t.Fatalf("testing args failed on unrecovered-panic breakpoint: %v", bp)
		}
		exit, exited := err.(proc.ErrProcessExited)
		if !exited {
			t.Fatalf("Process did not exit: %v", err)
		} else {
			if exit.Status != 0 {
				t.Fatalf("process exited with invalid status %d", exit.Status)
			}
		}
	}

	expectPanic := func(p *proc.Target, fixture protest.Fixture) {
		p.Continue()
		bp := p.CurrentThread().Breakpoint()
		if bp.Breakpoint == nil || bp.Logical.Name != proc.UnrecoveredPanic {
			t.Fatalf("not on unrecovered-panic breakpoint: %v", bp)
		}
	}

	// make sure multiple arguments (including one with spaces) are passed to the binary correctly
	withTestProcessArgs("testargs", t, ".", []string{"test"}, 0, expectSuccess)
	withTestProcessArgs("testargs", t, ".", []string{"-test"}, 0, expectPanic)
	withTestProcessArgs("testargs", t, ".", []string{"test", "pass flag"}, 0, expectSuccess)
	// check that arguments with spaces are *only* passed correctly when correctly called
	withTestProcessArgs("testargs", t, ".", []string{"test pass", "flag"}, 0, expectPanic)
	withTestProcessArgs("testargs", t, ".", []string{"test", "pass", "flag"}, 0, expectPanic)
	withTestProcessArgs("testargs", t, ".", []string{"test pass flag"}, 0, expectPanic)
	// and that invalid cases (wrong arguments or no arguments) panic
	withTestProcess("testargs", t, expectPanic)
	withTestProcessArgs("testargs", t, ".", []string{"invalid"}, 0, expectPanic)
	withTestProcessArgs("testargs", t, ".", []string{"test", "invalid"}, 0, expectPanic)
	withTestProcessArgs("testargs", t, ".", []string{"invalid", "pass flag"}, 0, expectPanic)
}

func TestIssue462(t *testing.T) {
	skipOn(t, "broken", "windows") // Stacktrace of Goroutine 0 fails with an error
	withTestProcess("testnextnethttp", t, func(p *proc.Target, fixture protest.Fixture) {
		grp := proc.NewGroup(p)
		go func() {
			// Wait for program to start listening.
			for {
				conn, err := net.Dial("tcp", "127.0.0.1:9191")
				if err == nil {
					conn.Close()
					break
				}
				time.Sleep(50 * time.Millisecond)
			}

			grp.RequestManualStop()
		}()

		assertNoError(grp.Continue(), t, "Continue()")
		_, err := proc.ThreadStacktrace(p.CurrentThread(), 40)
		assertNoError(err, t, "Stacktrace()")
	})
}

func TestNextParked(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("parallel_next", t, func(p *proc.Target, fixture protest.Fixture) {
		bp := setFunctionBreakpoint(p, t, "main.sayhi")

		// continue until a parked goroutine exists
		var parkedg *proc.G
		for parkedg == nil {
			err := p.Continue()
			if _, exited := err.(proc.ErrProcessExited); exited {
				t.Log("could not find parked goroutine")
				return
			}
			assertNoError(err, t, "Continue()")

			gs, _, err := proc.GoroutinesInfo(p, 0, 0)
			assertNoError(err, t, "GoroutinesInfo()")

			// Search for a parked goroutine that we know for sure will have to be
			// resumed before the program can exit. This is a parked goroutine that:
			// 1. is executing main.sayhi
			// 2. hasn't called wg.Done yet
			for _, g := range gs {
				if g.Thread != nil {
					continue
				}
				frames, _ := g.Stacktrace(5, 0)
				for _, frame := range frames {
					// line 11 is the line where wg.Done is called
					if frame.Current.Fn != nil && frame.Current.Fn.Name == "main.sayhi" && frame.Current.Line < 11 {
						parkedg = g
						break
					}
				}
				if parkedg != nil {
					break
				}
			}
		}

		assertNoError(p.SwitchGoroutine(parkedg), t, "SwitchGoroutine()")
		p.ClearBreakpoint(bp.Addr)
		assertNoError(p.Next(), t, "Next()")

		if p.SelectedGoroutine().ID != parkedg.ID {
			t.Fatalf("Next did not continue on the selected goroutine, expected %d got %d", parkedg.ID, p.SelectedGoroutine().ID)
		}
	})
}

func TestStepParked(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("parallel_next", t, func(p *proc.Target, fixture protest.Fixture) {
		bp := setFunctionBreakpoint(p, t, "main.sayhi")

		// continue until a parked goroutine exists
		var parkedg *proc.G
	LookForParkedG:
		for {
			err := p.Continue()
			if _, exited := err.(proc.ErrProcessExited); exited {
				t.Log("could not find parked goroutine")
				return
			}
			assertNoError(err, t, "Continue()")

			gs, _, err := proc.GoroutinesInfo(p, 0, 0)
			assertNoError(err, t, "GoroutinesInfo()")

			for _, g := range gs {
				if g.Thread == nil && g.CurrentLoc.Fn != nil && g.CurrentLoc.Fn.Name == "main.sayhi" {
					parkedg = g
					break LookForParkedG
				}
			}
		}

		t.Logf("Parked g is: %v\n", parkedg)
		frames, _ := parkedg.Stacktrace(20, 0)
		for _, frame := range frames {
			name := ""
			if frame.Call.Fn != nil {
				name = frame.Call.Fn.Name
			}
			t.Logf("\t%s:%d in %s (%#x)", frame.Call.File, frame.Call.Line, name, frame.Current.PC)
		}

		assertNoError(p.SwitchGoroutine(parkedg), t, "SwitchGoroutine()")
		p.ClearBreakpoint(bp.Addr)
		assertNoError(p.Step(), t, "Step()")

		if p.SelectedGoroutine().ID != parkedg.ID {
			t.Fatalf("Step did not continue on the selected goroutine, expected %d got %d", parkedg.ID, p.SelectedGoroutine().ID)
		}
	})
}

func TestUnsupportedArch(t *testing.T) {
	ver, _ := goversion.Parse(runtime.Version())
	if ver.Major < 0 || !ver.AfterOrEqual(goversion.GoVersion{Major: 1, Minor: 6, Rev: -1}) || ver.AfterOrEqual(goversion.GoVersion{Major: 1, Minor: 7, Rev: -1}) {
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

	var p *proc.Target

	switch testBackend {
	case "native":
		p, err = native.Launch([]string{outfile}, ".", 0, []string{}, "", [3]string{})
	case "lldb":
		p, err = gdbserial.LLDBLaunch([]string{outfile}, ".", 0, []string{}, "", [3]string{})
	default:
		t.Skip("test not valid for this backend")
	}

	if err == nil {
		p.Detach(true)
		t.Fatal("Launch is expected to fail, but succeeded")
	}

	if _, ok := err.(*proc.ErrUnsupportedArch); ok {
		// all good
		return
	}

	t.Fatal(err)
}

func TestIssue573(t *testing.T) {
	// calls to runtime.duffzero and runtime.duffcopy jump directly into the middle
	// of the function and the internal breakpoint set by StepInto may be missed.
	protest.AllowRecording(t)
	withTestProcess("issue573", t, func(p *proc.Target, fixture protest.Fixture) {
		setFunctionBreakpoint(p, t, "main.foo")
		assertNoError(p.Continue(), t, "Continue()")
		assertNoError(p.Step(), t, "Step() #1")
		assertNoError(p.Step(), t, "Step() #2") // Bug exits here.
		assertNoError(p.Step(), t, "Step() #3") // Third step ought to be possible; program ought not have exited.
	})
}

func TestTestvariables2Prologue(t *testing.T) {
	withTestProcess("testvariables2", t, func(p *proc.Target, fixture protest.Fixture) {
		addrEntry := p.BinInfo().LookupFunc["main.main"].Entry
		addrPrologue := findFunctionLocation(p, t, "main.main")
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
		{13, 28}}, "main.callAndDeferReturn", t)
}

func TestNextPanicAndDirectCall(t *testing.T) {
	// Next should not step into a deferred function if it is called
	// directly, only if it is called through a panic or a deferreturn.
	// Here we test the case where the function is called by a panic
	if goversion.VersionAfterOrEqual(runtime.Version(), 1, 11) {
		testseq("defercall", contNext, []nextTest{
			{15, 16},
			{16, 17},
			{17, 18},
			{18, 6}}, "main.callAndPanic2", t)
	} else {
		testseq("defercall", contNext, []nextTest{
			{15, 16},
			{16, 17},
			{17, 18},
			{18, 5}}, "main.callAndPanic2", t)
	}
}

func TestStepCall(t *testing.T) {
	testseq("testnextprog", contStep, []nextTest{
		{34, 13},
		{13, 14}}, "", t)
}

func TestStepCallPtr(t *testing.T) {
	// Tests that Step works correctly when calling functions with a
	// function pointer.
	if goversion.VersionAfterOrEqual(runtime.Version(), 1, 11) && !protest.RegabiSupported() {
		testseq("teststepprog", contStep, []nextTest{
			{9, 10},
			{10, 6},
			{6, 7},
			{7, 11}}, "", t)
	} else {
		testseq("teststepprog", contStep, []nextTest{
			{9, 10},
			{10, 5},
			{5, 6},
			{6, 7},
			{7, 11}}, "", t)
	}
}

func TestStepReturnAndPanic(t *testing.T) {
	// Tests that Step works correctly when returning from functions
	// and when a deferred function is called when panic'ing.
	switch {
	case goversion.VersionAfterOrEqual(runtime.Version(), 1, 11):
		testseq("defercall", contStep, []nextTest{
			{17, 6},
			{6, 7},
			{7, 18},
			{18, 6},
			{6, 7}}, "", t)
	case goversion.VersionAfterOrEqual(runtime.Version(), 1, 10):
		testseq("defercall", contStep, []nextTest{
			{17, 5},
			{5, 6},
			{6, 7},
			{7, 18},
			{18, 5},
			{5, 6},
			{6, 7}}, "", t)
	case goversion.VersionAfterOrEqual(runtime.Version(), 1, 9):
		testseq("defercall", contStep, []nextTest{
			{17, 5},
			{5, 6},
			{6, 7},
			{7, 17},
			{17, 18},
			{18, 5},
			{5, 6},
			{6, 7}}, "", t)
	default:
		testseq("defercall", contStep, []nextTest{
			{17, 5},
			{5, 6},
			{6, 7},
			{7, 18},
			{18, 5},
			{5, 6},
			{6, 7}}, "", t)
	}
}

func TestStepDeferReturn(t *testing.T) {
	// Tests that Step works correctly when a deferred function is
	// called during a return.
	if goversion.VersionAfterOrEqual(runtime.Version(), 1, 11) {
		testseq("defercall", contStep, []nextTest{
			{11, 6},
			{6, 7},
			{7, 12},
			{12, 13},
			{13, 6},
			{6, 7},
			{7, 13},
			{13, 28}}, "", t)
	} else {
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
}

func TestStepIgnorePrivateRuntime(t *testing.T) {
	// Tests that Step will ignore calls to private runtime functions
	// (such as runtime.convT2E in this case)
	switch {
	case goversion.VersionAfterOrEqual(runtime.Version(), 1, 17) && protest.RegabiSupported():
		testseq("teststepprog", contStep, []nextTest{
			{21, 13},
			{13, 14},
			{14, 15},
			{15, 17},
			{17, 22}}, "", t)
	case goversion.VersionAfterOrEqual(runtime.Version(), 1, 17):
		testseq("teststepprog", contStep, []nextTest{
			{21, 14},
			{14, 15},
			{15, 17},
			{17, 22}}, "", t)
	case goversion.VersionAfterOrEqual(runtime.Version(), 1, 11):
		testseq("teststepprog", contStep, []nextTest{
			{21, 14},
			{14, 15},
			{15, 22}}, "", t)
	case goversion.VersionAfterOrEqual(runtime.Version(), 1, 10):
		testseq("teststepprog", contStep, []nextTest{
			{21, 13},
			{13, 14},
			{14, 15},
			{15, 22}}, "", t)
	case goversion.VersionAfterOrEqual(runtime.Version(), 1, 7):
		testseq("teststepprog", contStep, []nextTest{
			{21, 13},
			{13, 14},
			{14, 15},
			{15, 14},
			{14, 17},
			{17, 22}}, "", t)
	default:
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
	withTestProcess("issue561", t, func(p *proc.Target, fixture protest.Fixture) {
		setFileBreakpoint(p, t, fixture.Source, 10)
		assertNoError(p.Continue(), t, "Continue()")
		assertNoError(p.Step(), t, "Step()")
		assertLineNumber(p, t, 5, "wrong line number after Step,")
	})
}

func TestGoroutineLables(t *testing.T) {
	withTestProcess("goroutineLabels", t, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue()")
		g, err := proc.GetG(p.CurrentThread())
		assertNoError(err, t, "GetG()")
		if len(g.Labels()) != 0 {
			t.Fatalf("No labels expected")
		}

		assertNoError(p.Continue(), t, "Continue()")
		g, err = proc.GetG(p.CurrentThread())
		assertNoError(err, t, "GetG()")
		labels := g.Labels()
		if v := labels["k1"]; v != "v1" {
			t.Errorf("Unexpected label value k1=%v", v)
		}
		if v := labels["k2"]; v != "v2" {
			t.Errorf("Unexpected label value k2=%v", v)
		}
	})
}

func TestStepOut(t *testing.T) {
	testseq2(t, "testnextprog", "main.helloworld", []seqTest{{contContinue, 13}, {contStepout, 35}})
}

func TestStepConcurrentDirect(t *testing.T) {
	skipOn(t, "broken - step concurrent", "windows", "arm64")
	protest.AllowRecording(t)
	withTestProcess("teststepconcurrent", t, func(p *proc.Target, fixture protest.Fixture) {
		bp := setFileBreakpoint(p, t, fixture.Source, 37)

		assertNoError(p.Continue(), t, "Continue()")
		err := p.ClearBreakpoint(bp.Addr)
		assertNoError(err, t, "ClearBreakpoint()")

		for _, b := range p.Breakpoints().M {
			if b.Logical.Name == proc.UnrecoveredPanic {
				err := p.ClearBreakpoint(b.Addr)
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
			assertNoError(p.Step(), t, "Step()")
		}

		if count != 100 {
			t.Fatalf("Program did not loop expected number of times: %d", count)
		}
	})
}

func TestStepConcurrentPtr(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("teststepconcurrent", t, func(p *proc.Target, fixture protest.Fixture) {
		setFileBreakpoint(p, t, fixture.Source, 24)

		for _, b := range p.Breakpoints().M {
			if b.Logical.Name == proc.UnrecoveredPanic {
				err := p.ClearBreakpoint(b.Addr)
				assertNoError(err, t, "ClearBreakpoint(unrecovered-panic)")
				break
			}
		}

		kvals := map[int64]int64{}
		count := 0
		for {
			err := p.Continue()
			_, exited := err.(proc.ErrProcessExited)
			if exited {
				break
			}
			assertNoError(err, t, "Continue()")

			f, ln := currentLineNumber(p, t)
			if ln != 24 {
				for _, th := range p.ThreadList() {
					t.Logf("thread %d stopped on breakpoint %v", th.ThreadID(), th.Breakpoint())
				}
				curbp := p.CurrentThread().Breakpoint()
				t.Fatalf("Program did not continue at expected location (24): %s:%d %#x [%v] (gid %d count %d)", f, ln, currentPC(p, t), curbp, p.SelectedGoroutine().ID, count)
			}

			gid := p.SelectedGoroutine().ID

			kvar := evalVariable(p, t, "k")
			k, _ := constant.Int64Val(kvar.Value)

			if oldk, ok := kvals[gid]; ok {
				if oldk >= k {
					t.Fatalf("Goroutine %d did not make progress?", gid)
				}
			}
			kvals[gid] = k

			assertNoError(p.Step(), t, "Step()")
			for p.Breakpoints().HasSteppingBreakpoints() {
				if p.SelectedGoroutine().ID == gid {
					t.Fatalf("step did not step into function call (but internal breakpoints still active?) (%d %d)", gid, p.SelectedGoroutine().ID)
				}
				assertNoError(p.Continue(), t, "Continue()")
			}

			if p.SelectedGoroutine().ID != gid {
				t.Fatalf("Step switched goroutines (wanted: %d got: %d)", gid, p.SelectedGoroutine().ID)
			}

			f, ln = assertLineNumber(p, t, 13, "Step did not step into function call")

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

func TestStepOutBreakpoint(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("testnextprog", t, func(p *proc.Target, fixture protest.Fixture) {
		bp := setFileBreakpoint(p, t, fixture.Source, 13)
		assertNoError(p.Continue(), t, "Continue()")
		p.ClearBreakpoint(bp.Addr)

		// StepOut should be interrupted by a breakpoint on the same goroutine.
		setFileBreakpoint(p, t, fixture.Source, 14)
		assertNoError(p.StepOut(), t, "StepOut()")
		assertLineNumber(p, t, 14, "wrong line number")
		if p.Breakpoints().HasSteppingBreakpoints() {
			t.Fatal("has internal breakpoints after hitting breakpoint on same goroutine")
		}
	})
}

func TestNextBreakpoint(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("testnextprog", t, func(p *proc.Target, fixture protest.Fixture) {
		bp := setFileBreakpoint(p, t, fixture.Source, 34)
		assertNoError(p.Continue(), t, "Continue()")
		p.ClearBreakpoint(bp.Addr)

		// Next should be interrupted by a breakpoint on the same goroutine.
		setFileBreakpoint(p, t, fixture.Source, 14)
		assertNoError(p.Next(), t, "Next()")
		assertLineNumber(p, t, 14, "wrong line number")
		if p.Breakpoints().HasSteppingBreakpoints() {
			t.Fatal("has internal breakpoints after hitting breakpoint on same goroutine")
		}
	})
}

func TestNextBreakpointKeepsSteppingBreakpoints(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("testnextprog", t, func(p *proc.Target, fixture protest.Fixture) {
		grp := proc.NewGroup(p)
		grp.KeepSteppingBreakpoints = proc.TracepointKeepsSteppingBreakpoints
		bp := setFileBreakpoint(p, t, fixture.Source, 34)
		assertNoError(grp.Continue(), t, "Continue()")
		p.ClearBreakpoint(bp.Addr)

		// Next should be interrupted by a tracepoint on the same goroutine.
		bp = setFileBreakpoint(p, t, fixture.Source, 14)
		bp.Logical.Tracepoint = true
		assertNoError(grp.Next(), t, "Next()")
		assertLineNumber(p, t, 14, "wrong line number")
		if !p.Breakpoints().HasSteppingBreakpoints() {
			t.Fatal("does not have internal breakpoints after hitting tracepoint on same goroutine")
		}

		// Continue to complete next.
		assertNoError(grp.Continue(), t, "Continue()")
		assertLineNumber(p, t, 35, "wrong line number")
		if p.Breakpoints().HasSteppingBreakpoints() {
			t.Fatal("has internal breakpoints after completing next")
		}
	})
}

func TestStepOutDefer(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("testnextdefer", t, func(p *proc.Target, fixture protest.Fixture) {
		bp := setFileBreakpoint(p, t, fixture.Source, 9)
		assertNoError(p.Continue(), t, "Continue()")
		p.ClearBreakpoint(bp.Addr)

		assertLineNumber(p, t, 9, "wrong line number")

		assertNoError(p.StepOut(), t, "StepOut()")

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
	testseq2(t, "defercall", "", []seqTest{
		{contContinue, 11},
		{contStepout, 28}})
}

func TestStepInstructionOnBreakpoint(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skipf("skipping since not amd64")
	}
	// StepInstruction should step one instruction forward when
	// PC is on a 1 byte instruction with a software breakpoint.
	protest.AllowRecording(t)
	withTestProcess("break/", t, func(p *proc.Target, fixture protest.Fixture) {
		setFileBreakpoint(p, t, filepath.ToSlash(filepath.Join(fixture.BuildDir, "break_amd64.s")), 4)

		assertNoError(p.Continue(), t, "Continue()")

		pc := getRegisters(p, t).PC()
		assertNoError(p.StepInstruction(), t, "StepInstruction()")
		if pc == getRegisters(p, t).PC() {
			t.Fatal("Could not step a single instruction")
		}
	})
}

func TestStepOnCallPtrInstr(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("teststepprog", t, func(p *proc.Target, fixture protest.Fixture) {
		setFileBreakpoint(p, t, fixture.Source, 10)

		assertNoError(p.Continue(), t, "Continue()")

		found := false

		for {
			_, ln := currentLineNumber(p, t)
			if ln != 10 {
				break
			}
			regs, err := p.CurrentThread().Registers()
			assertNoError(err, t, "Registers()")
			pc := regs.PC()
			text, err := proc.Disassemble(p.Memory(), regs, p.Breakpoints(), p.BinInfo(), pc, pc+uint64(p.BinInfo().Arch.MaxInstructionLength()))
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

		assertNoError(p.Step(), t, "Step()")

		if goversion.VersionAfterOrEqual(runtime.Version(), 1, 11) && !protest.RegabiSupported() {
			assertLineNumber(p, t, 6, "Step continued to wrong line,")
		} else {
			assertLineNumber(p, t, 5, "Step continued to wrong line,")
		}
	})
}

func TestIssue594(t *testing.T) {
	skipOn(t, "upstream issue", "darwin", "lldb")
	// debugserver will receive an EXC_BAD_ACCESS for this, at that point
	// there is no way to reconvert this exception into a unix signal and send
	// it to the process.
	// This is a bug in debugserver/lldb:
	//  https://bugs.llvm.org//show_bug.cgi?id=22868

	// Exceptions that aren't caused by breakpoints should be propagated
	// back to the target.
	// In particular the target should be able to cause a nil pointer
	// dereference panic and recover from it.
	protest.AllowRecording(t)
	withTestProcess("issue594", t, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue()")
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
	if goversion.VersionAfterOrEqual(runtime.Version(), 1, 11) {
		testseq2(t, "defercall", "", []seqTest{
			{contContinue, 17},
			{contStepout, 6}})
	} else {
		testseq2(t, "defercall", "", []seqTest{
			{contContinue, 17},
			{contStepout, 5}})
	}
}

func TestWorkDir(t *testing.T) {
	wd := os.TempDir()
	// For Darwin `os.TempDir()` returns `/tmp` which is symlink to `/private/tmp`.
	if runtime.GOOS == "darwin" {
		wd = "/private/tmp"
	}
	protest.AllowRecording(t)
	withTestProcessArgs("workdir", t, wd, []string{}, 0, func(p *proc.Target, fixture protest.Fixture) {
		setFileBreakpoint(p, t, fixture.Source, 14)
		p.Continue()
		v := evalVariable(p, t, "pwd")
		str := constant.StringVal(v.Value)
		if wd != str {
			t.Fatalf("Expected %s got %s\n", wd, str)
		}
	})
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
	withTestProcess("testvariables2", t, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue()")
		for _, tc := range testcases {
			v := evalVariable(p, t, tc.name)
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
	withTestProcess("issue683", t, func(p *proc.Target, fixture protest.Fixture) {
		setFunctionBreakpoint(p, t, "main.main")
		assertNoError(p.Continue(), t, "First Continue()")
		for i := 0; i < 20; i++ {
			// eventually an error about the source file not being found will be
			// returned, the important thing is that we shouldn't panic
			err := p.Step()
			if err != nil {
				break
			}
		}
	})
}

func TestIssue664(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("issue664", t, func(p *proc.Target, fixture protest.Fixture) {
		setFileBreakpoint(p, t, fixture.Source, 4)
		assertNoError(p.Continue(), t, "Continue()")
		assertNoError(p.Next(), t, "Next()")
		assertLineNumber(p, t, 5, "Did not continue to correct location,")
	})
}

// Benchmarks (*Process).Continue + (*Scope).FunctionArguments
func BenchmarkTrace(b *testing.B) {
	withTestProcess("traceperf", b, func(p *proc.Target, fixture protest.Fixture) {
		setFunctionBreakpoint(p, b, "main.PerfCheck")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			assertNoError(p.Continue(), b, "Continue()")
			s, err := proc.GoroutineScope(p, p.CurrentThread())
			assertNoError(err, b, "Scope()")
			_, err = s.FunctionArguments(proc.LoadConfig{false, 0, 64, 0, 3, 0})
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
	withTestProcess("defercall", t, func(p *proc.Target, fixture protest.Fixture) {
		setFunctionBreakpoint(p, t, "runtime.deferreturn")
		assertNoError(p.Continue(), t, "First Continue()")

		// Set a breakpoint on the deferred function so that the following loop
		// can not step out of the runtime.deferreturn and all the way to the
		// point where the target program panics.
		setFunctionBreakpoint(p, t, "main.sampleFunction")
		for i := 0; i < 20; i++ {
			loc, err := p.CurrentThread().Location()
			assertNoError(err, t, "CurrentThread().Location()")
			t.Logf("at %#x %s:%d", loc.PC, loc.File, loc.Line)
			if loc.Fn != nil && loc.Fn.Name == "main.sampleFunction" {
				break
			}
			assertNoError(p.Next(), t, fmt.Sprintf("Next() %d", i))
		}
	})
}

func getg(goid int64, gs []*proc.G) *proc.G {
	for _, g := range gs {
		if g.ID == goid {
			return g
		}
	}
	return nil
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
	var buildFlags protest.BuildFlags
	if buildMode == "pie" {
		buildFlags |= protest.BuildModePIE
	}
	fixture := protest.BuildFixture("testnextnethttp", buildFlags)
	cmd := exec.Command(fixture.Path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	assertNoError(cmd.Start(), t, "starting fixture")

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

	var p *proc.Target
	var err error

	switch testBackend {
	case "native":
		p, err = native.Attach(cmd.Process.Pid, []string{})
	case "lldb":
		path := ""
		if runtime.GOOS == "darwin" {
			path = fixture.Path
		}
		p, err = gdbserial.LLDBAttach(cmd.Process.Pid, path, []string{})
	default:
		err = fmt.Errorf("unknown backend %q", testBackend)
	}

	assertNoError(err, t, "Attach")
	go func() {
		time.Sleep(1 * time.Second)
		http.Get("http://127.0.0.1:9191")
	}()

	assertNoError(p.Continue(), t, "Continue")
	assertLineNumber(p, t, 11, "Did not continue to correct location,")

	assertNoError(p.Detach(false), t, "Detach")

	if runtime.GOOS != "darwin" {
		// Debugserver sometimes will leave a zombie process after detaching, this
		// seems to be a bug with debugserver.
		resp, err := http.Get("http://127.0.0.1:9191/nobp")
		assertNoError(err, t, "Page request after detach")
		bs, err := ioutil.ReadAll(resp.Body)
		assertNoError(err, t, "Reading /nobp page")
		if out := string(bs); !strings.Contains(out, "hello, world!") {
			t.Fatalf("/nobp page does not contain \"hello, world!\": %q", out)
		}
	}

	cmd.Process.Kill()
}

func TestVarSum(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("testvariables2", t, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue()")
		sumvar := evalVariable(p, t, "s1[0] + s1[1]")
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
	withTestProcess("pkgrenames", t, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue()")
		evalVariable(p, t, "pkg.SomeVar")
		evalVariable(p, t, "pkg.SomeVar.X")
	})
}

func TestEnvironment(t *testing.T) {
	protest.AllowRecording(t)
	os.Setenv("SOMEVAR", "bah")
	withTestProcess("testenv", t, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue()")
		v := evalVariable(p, t, "x")
		vv := constant.StringVal(v.Value)
		t.Logf("v = %q", vv)
		if vv != "bah" {
			t.Fatalf("value of v is %q (expected \"bah\")", vv)
		}
	})
}

func getFrameOff(p *proc.Target, t *testing.T) int64 {
	frameoffvar := evalVariable(p, t, "runtime.frameoff")
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

	withTestProcess("increment", t, func(p *proc.Target, fixture protest.Fixture) {
		bp := setFunctionBreakpoint(p, t, "main.Increment")
		assertNoError(p.Continue(), t, "Continue")
		err := p.ClearBreakpoint(bp.Addr)
		assertNoError(err, t, "ClearBreakpoint")
		assertNoError(p.Next(), t, "Next 1")
		assertNoError(p.Next(), t, "Next 2")
		assertNoError(p.Next(), t, "Next 3")
		frameoff0 := getFrameOff(p, t)
		assertNoError(p.Step(), t, "Step")
		frameoff1 := getFrameOff(p, t)
		if frameoff0 == frameoff1 {
			t.Fatalf("did not step into function?")
		}
		assertLineNumber(p, t, 6, "program did not continue to expected location,")
		assertNoError(p.Next(), t, "Next 4")
		assertLineNumber(p, t, 7, "program did not continue to expected location,")
		assertNoError(p.StepOut(), t, "StepOut")
		assertLineNumber(p, t, 11, "program did not continue to expected location,")
		frameoff2 := getFrameOff(p, t)
		if frameoff0 != frameoff2 {
			t.Fatalf("frame offset mismatch %x != %x", frameoff0, frameoff2)
		}
	})
}

// TestIssue877 ensures that the environment variables starting with DYLD_ and LD_
// are passed when executing the binary on OSX via debugserver
func TestIssue877(t *testing.T) {
	if runtime.GOOS != "darwin" && testBackend == "lldb" {
		return
	}
	if os.Getenv("TRAVIS") == "true" && runtime.GOOS == "darwin" {
		// Something changed on Travis side that makes the Go compiler fail if
		// DYLD_LIBRARY_PATH is set.
		t.Skip("broken")
	}
	const envval = "/usr/local/lib"
	os.Setenv("DYLD_LIBRARY_PATH", envval)
	withTestProcess("issue877", t, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue()")
		v := evalVariable(p, t, "dyldenv")
		vv := constant.StringVal(v.Value)
		t.Logf("v = %q", vv)
		if vv != envval {
			t.Fatalf("value of v is %q (expected %q)", vv, envval)
		}
	})
}

func TestIssue893(t *testing.T) {
	// Test what happens when next is called immediately after launching the
	// executable, acceptable behaviors are: (a) no error, (b) no source at PC
	// error, (c) program runs to completion
	protest.AllowRecording(t)
	withTestProcess("increment", t, func(p *proc.Target, fixture protest.Fixture) {
		err := p.Next()
		if err == nil {
			return
		}
		if _, ok := err.(*frame.ErrNoFDEForPC); ok {
			return
		}
		if _, ok := err.(*proc.ErrNoSourceForPC); ok {
			return
		}
		if _, ok := err.(proc.ErrProcessExited); ok {
			return
		}
		assertNoError(err, t, "Next")
	})
}

func TestStepInstructionNoGoroutine(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("increment", t, func(p *proc.Target, fixture protest.Fixture) {
		// Call StepInstruction immediately after launching the program, it should
		// work even though no goroutine is selected.
		assertNoError(p.StepInstruction(), t, "StepInstruction")
	})
}

func TestIssue871(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("issue871", t, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue")

		var scope *proc.EvalScope
		var err error
		if testBackend == "rr" {
			var frame proc.Stackframe
			frame, err = findFirstNonRuntimeFrame(p)
			if err == nil {
				scope = proc.FrameToScope(p, p.Memory(), nil, frame)
			}
		} else {
			scope, err = proc.GoroutineScope(p, p.CurrentThread())
		}
		assertNoError(err, t, "scope")

		locals, err := scope.LocalVariables(normalLoadConfig)
		assertNoError(err, t, "LocalVariables")

		foundA, foundB := false, false

		for _, v := range locals {
			t.Logf("local %v", v)
			switch v.Name {
			case "a":
				foundA = true
				if v.Flags&proc.VariableEscaped == 0 {
					t.Errorf("variable a not flagged as escaped")
				}
			case "b":
				foundB = true
			}
		}

		if !foundA {
			t.Errorf("variable a not found")
		}

		if !foundB {
			t.Errorf("variable b not found")
		}
	})
}

func TestShadowedFlag(t *testing.T) {
	if ver, _ := goversion.Parse(runtime.Version()); ver.Major >= 0 && !ver.AfterOrEqual(goversion.GoVersion{Major: 1, Minor: 9, Rev: -1}) {
		return
	}
	withTestProcess("testshadow", t, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue")
		scope, err := proc.GoroutineScope(p, p.CurrentThread())
		assertNoError(err, t, "GoroutineScope")
		locals, err := scope.LocalVariables(normalLoadConfig)
		assertNoError(err, t, "LocalVariables")
		foundShadowed := false
		foundNonShadowed := false
		for _, v := range locals {
			if v.Flags&proc.VariableShadowed != 0 {
				if v.Name != "a" {
					t.Errorf("wrong shadowed variable %s", v.Name)
				}
				foundShadowed = true
				if n, _ := constant.Int64Val(v.Value); n != 0 {
					t.Errorf("wrong value for shadowed variable a: %d", n)
				}
			} else {
				if v.Name != "a" {
					t.Errorf("wrong non-shadowed variable %s", v.Name)
				}
				foundNonShadowed = true
				if n, _ := constant.Int64Val(v.Value); n != 1 {
					t.Errorf("wrong value for non-shadowed variable a: %d", n)
				}
			}
		}
		if !foundShadowed {
			t.Error("could not find any shadowed variable")
		}
		if !foundNonShadowed {
			t.Error("could not find any non-shadowed variable")
		}
	})
}

func TestAttachStripped(t *testing.T) {
	if testBackend == "lldb" && runtime.GOOS == "linux" {
		bs, _ := ioutil.ReadFile("/proc/sys/kernel/yama/ptrace_scope")
		if bs == nil || strings.TrimSpace(string(bs)) != "0" {
			t.Logf("can not run TestAttachStripped: %v\n", bs)
			return
		}
	}
	if testBackend == "rr" {
		return
	}
	if runtime.GOOS == "darwin" {
		t.Log("-s does not produce stripped executables on macOS")
		return
	}
	if buildMode != "" {
		t.Skip("not enabled with buildmode=PIE")
	}
	fixture := protest.BuildFixture("testnextnethttp", protest.LinkStrip)
	cmd := exec.Command(fixture.Path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	assertNoError(cmd.Start(), t, "starting fixture")

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

	var p *proc.Target
	var err error

	switch testBackend {
	case "native":
		p, err = native.Attach(cmd.Process.Pid, []string{})
	case "lldb":
		path := ""
		if runtime.GOOS == "darwin" {
			path = fixture.Path
		}
		p, err = gdbserial.LLDBAttach(cmd.Process.Pid, path, []string{})
	default:
		t.Fatalf("unknown backend %q", testBackend)
	}

	t.Logf("error is %v", err)

	if err == nil {
		p.Detach(true)
		t.Fatalf("expected error after attach, got nothing")
	} else {
		cmd.Process.Kill()
	}
	os.Remove(fixture.Path)
}

func TestIssue844(t *testing.T) {
	// Conditional breakpoints should not prevent next from working if their
	// condition isn't met.
	withTestProcess("nextcond", t, func(p *proc.Target, fixture protest.Fixture) {
		setFileBreakpoint(p, t, fixture.Source, 9)
		condbp := setFileBreakpoint(p, t, fixture.Source, 10)
		condbp.UserBreaklet().Cond = &ast.BinaryExpr{
			Op: token.EQL,
			X:  &ast.Ident{Name: "n"},
			Y:  &ast.BasicLit{Kind: token.INT, Value: "11"},
		}
		assertNoError(p.Continue(), t, "Continue")
		assertNoError(p.Next(), t, "Next")
		assertLineNumber(p, t, 10, "continued to wrong location,")
	})
}

func logStacktrace(t *testing.T, p *proc.Target, frames []proc.Stackframe) {
	w := tabwriter.NewWriter(os.Stderr, 0, 0, 3, ' ', 0)
	fmt.Fprintf(w, "\n%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t\n", "Call PC", "Frame Offset", "Frame Pointer Offset", "PC", "Return", "Function", "Location", "Top Defer", "Defers")
	for j := range frames {
		name := "?"
		if frames[j].Current.Fn != nil {
			name = frames[j].Current.Fn.Name
		}
		if frames[j].Call.Fn != nil && frames[j].Current.Fn != frames[j].Call.Fn {
			name = fmt.Sprintf("%s inlined in %s", frames[j].Call.Fn.Name, frames[j].Current.Fn.Name)
		}

		topmostdefer := ""
		if frames[j].TopmostDefer != nil {
			_, _, fn := frames[j].TopmostDefer.DeferredFunc(p)
			fnname := ""
			if fn != nil {
				fnname = fn.Name
			}
			topmostdefer = fmt.Sprintf("%#x %s", frames[j].TopmostDefer.DwrapPC, fnname)
		}

		defers := ""
		for deferIdx, _defer := range frames[j].Defers {
			_, _, fn := _defer.DeferredFunc(p)
			fnname := ""
			if fn != nil {
				fnname = fn.Name
			}
			defers += fmt.Sprintf("%d %#x %s |", deferIdx, _defer.DwrapPC, fnname)
		}

		frame := frames[j]
		fmt.Fprintf(w, "%#x\t%#x\t%#x\t%#x\t%#x\t%s\t%s:%d\t%s\t%s\t\n",
			frame.Call.PC, frame.FrameOffset(), frame.FramePointerOffset(), frame.Current.PC, frame.Ret,
			name, filepath.Base(frame.Call.File), frame.Call.Line, topmostdefer, defers)

	}
	w.Flush()
}

// stacktraceCheck checks that all the functions listed in tc appear in
// frames in the same order.
// Checks that all the functions in tc starting with "C." or with "!" are in
// a systemstack frame.
// Returns a slice m where m[i] is the index in frames of the function tc[i]
// or nil if any check fails.
func stacktraceCheck(t *testing.T, tc []string, frames []proc.Stackframe) []int {
	m := make([]int, len(tc))
	i, j := 0, 0
	for i < len(tc) {
		tcname := tc[i]
		tcsystem := strings.HasPrefix(tcname, "C.")
		if tcname[0] == '!' {
			tcsystem = true
			tcname = tcname[1:]
		}
		for j < len(frames) {
			name := "?"
			if frames[j].Current.Fn != nil {
				name = frames[j].Current.Fn.Name
			}
			if name == tcname {
				m[i] = j
				if tcsystem != frames[j].SystemStack {
					t.Logf("system stack check failed for frame %d (expected %v got %v)", j, tcsystem, frames[j].SystemStack)
					t.Logf("expected: %v\n", tc)
					return nil
				}
				break
			}

			j++
		}
		if j >= len(frames) {
			t.Logf("couldn't find frame %d %s", i, tc)
			t.Logf("expected: %v\n", tc)
			return nil
		}

		i++
	}
	return m
}

func frameInFile(frame proc.Stackframe, file string) bool {
	for _, loc := range []proc.Location{frame.Current, frame.Call} {
		if !strings.HasSuffix(loc.File, file) && !strings.HasSuffix(loc.File, "/"+file) && !strings.HasSuffix(loc.File, "\\"+file) {
			return false
		}
		if loc.Line <= 0 {
			return false
		}
	}
	return true
}

func TestCgoStacktrace(t *testing.T) {
	if runtime.GOOS == "windows" {
		ver, _ := goversion.Parse(runtime.Version())
		if ver.Major > 0 && !ver.AfterOrEqual(goversion.GoVersion{Major: 1, Minor: 9, Rev: -1}) {
			t.Skip("disabled on windows with go before version 1.9")
		}
	}
	if runtime.GOOS == "darwin" {
		ver, _ := goversion.Parse(runtime.Version())
		if ver.Major > 0 && !ver.AfterOrEqual(goversion.GoVersion{Major: 1, Minor: 8, Rev: -1}) {
			t.Skip("disabled on macOS with go before version 1.8")
		}
	}

	skipOn(t, "broken - cgo stacktraces", "386")
	skipOn(t, "broken - cgo stacktraces", "windows", "arm64")
	protest.MustHaveCgo(t)

	// Tests that:
	// a) we correctly identify the goroutine while we are executing cgo code
	// b) that we can stitch together the system stack (where cgo code
	// executes) and the normal goroutine stack

	// Each test case describes how the stack trace should appear after a
	// continue. The first function on each test case is the topmost function
	// that should be found on the stack, the actual stack trace can have more
	// frame than those listed here but all the frames listed must appear in
	// the specified order.
	testCases := [][]string{
		{"main.main"},
		{"C.helloworld_pt2", "C.helloworld", "main.main"},
		{"main.helloWorldS", "main.helloWorld", "C.helloworld_pt2", "C.helloworld", "main.main"},
		{"C.helloworld_pt4", "C.helloworld_pt3", "main.helloWorldS", "main.helloWorld", "C.helloworld_pt2", "C.helloworld", "main.main"},
		{"main.helloWorld2", "C.helloworld_pt4", "C.helloworld_pt3", "main.helloWorldS", "main.helloWorld", "C.helloworld_pt2", "C.helloworld", "main.main"}}

	var gid int64

	frameOffs := map[string]int64{}
	framePointerOffs := map[string]int64{}

	withTestProcess("cgostacktest/", t, func(p *proc.Target, fixture protest.Fixture) {
		for itidx, tc := range testCases {
			t.Logf("iteration step %d", itidx)

			assertNoError(p.Continue(), t, fmt.Sprintf("Continue at iteration step %d", itidx))

			g, err := proc.GetG(p.CurrentThread())
			assertNoError(err, t, fmt.Sprintf("GetG at iteration step %d", itidx))

			if itidx == 0 {
				gid = g.ID
			} else {
				if gid != g.ID {
					t.Fatalf("wrong goroutine id at iteration step %d (expected %d got %d)", itidx, gid, g.ID)
				}
			}

			frames, err := g.Stacktrace(100, 0)
			assertNoError(err, t, fmt.Sprintf("Stacktrace at iteration step %d", itidx))

			logStacktrace(t, p, frames)

			m := stacktraceCheck(t, tc, frames)
			mismatch := (m == nil)

			for i, j := range m {
				if strings.HasPrefix(tc[i], "C.hellow") {
					if !frameInFile(frames[j], "hello.c") {
						t.Logf("position in %q is %s:%d (call %s:%d)", tc[i], frames[j].Current.File, frames[j].Current.Line, frames[j].Call.File, frames[j].Call.Line)
						mismatch = true
						break
					}
				}
				if frameOff, ok := frameOffs[tc[i]]; ok {
					if frameOff != frames[j].FrameOffset() {
						t.Logf("frame %s offset mismatch", tc[i])
					}
					if framePointerOffs[tc[i]] != frames[j].FramePointerOffset() {
						t.Logf("frame %s pointer offset mismatch, expected: %#v actual: %#v", tc[i], framePointerOffs[tc[i]], frames[j].FramePointerOffset())
					}
				} else {
					frameOffs[tc[i]] = frames[j].FrameOffset()
					framePointerOffs[tc[i]] = frames[j].FramePointerOffset()
				}
			}

			// also check that ThreadStacktrace produces the same list of frames
			threadFrames, err := proc.ThreadStacktrace(p.CurrentThread(), 100)
			assertNoError(err, t, fmt.Sprintf("ThreadStacktrace at iteration step %d", itidx))

			if len(threadFrames) != len(frames) {
				mismatch = true
			} else {
				for j := range frames {
					if frames[j].Current.File != threadFrames[j].Current.File || frames[j].Current.Line != threadFrames[j].Current.Line {
						t.Logf("stack mismatch between goroutine stacktrace and thread stacktrace")
						t.Logf("thread stacktrace:")
						logStacktrace(t, p, threadFrames)
						mismatch = true
						break
					}
				}
			}
			if mismatch {
				t.Fatal("see previous loglines")
			}
		}
	})
}

func TestCgoSources(t *testing.T) {
	if runtime.GOOS == "windows" {
		ver, _ := goversion.Parse(runtime.Version())
		if ver.Major > 0 && !ver.AfterOrEqual(goversion.GoVersion{Major: 1, Minor: 9, Rev: -1}) {
			t.Skip("disabled on windows with go before version 1.9")
		}
	}

	if runtime.GOARCH == "386" {
		t.Skip("cgo stacktraces not supported on i386 for now")
	}

	protest.MustHaveCgo(t)

	withTestProcess("cgostacktest/", t, func(p *proc.Target, fixture protest.Fixture) {
		sources := p.BinInfo().Sources
		for _, needle := range []string{"main.go", "hello.c"} {
			found := false
			for _, k := range sources {
				if strings.HasSuffix(k, needle) || strings.HasSuffix(k, "/"+needle) || strings.HasSuffix(k, "\\"+needle) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("File %s not found", needle)
			}
		}
	})
}

func TestSystemstackStacktrace(t *testing.T) {
	// check that we can follow a stack switch initiated by runtime.systemstack()
	withTestProcess("panic", t, func(p *proc.Target, fixture protest.Fixture) {
		setFunctionBreakpoint(p, t, "runtime.startpanic_m")
		assertNoError(p.Continue(), t, "first continue")
		assertNoError(p.Continue(), t, "second continue")
		g, err := proc.GetG(p.CurrentThread())
		assertNoError(err, t, "GetG")
		frames, err := g.Stacktrace(100, 0)
		assertNoError(err, t, "stacktrace")
		logStacktrace(t, p, frames)
		m := stacktraceCheck(t, []string{"!runtime.startpanic_m", "runtime.gopanic", "main.main"}, frames)
		if m == nil {
			t.Fatal("see previous loglines")
		}
	})
}

func TestSystemstackOnRuntimeNewstack(t *testing.T) {
	// The bug being tested here manifests as follows:
	// - set a breakpoint somewhere or interrupt the program with Ctrl-C
	// - try to look at stacktraces of other goroutines
	// If one of the other goroutines is resizing its own stack the stack
	// command won't work for it.
	withTestProcess("binarytrees", t, func(p *proc.Target, fixture protest.Fixture) {
		setFunctionBreakpoint(p, t, "main.main")
		assertNoError(p.Continue(), t, "first continue")

		g, err := proc.GetG(p.CurrentThread())
		assertNoError(err, t, "GetG")
		mainGoroutineID := g.ID

		setFunctionBreakpoint(p, t, "runtime.newstack")
		for {
			assertNoError(p.Continue(), t, "second continue")
			g, err = proc.GetG(p.CurrentThread())
			assertNoError(err, t, "GetG")
			if g.ID == mainGoroutineID {
				break
			}
		}
		frames, err := g.Stacktrace(100, 0)
		assertNoError(err, t, "stacktrace")
		logStacktrace(t, p, frames)
		m := stacktraceCheck(t, []string{"!runtime.newstack", "main.main"}, frames)
		if m == nil {
			t.Fatal("see previous loglines")
		}
	})
}

func TestIssue1034(t *testing.T) {
	skipOn(t, "broken - cgo stacktraces", "386")
	protest.MustHaveCgo(t)

	// The external linker on macOS produces an abbrev for DW_TAG_subprogram
	// without the "has children" flag, we should support this.
	withTestProcess("cgostacktest/", t, func(p *proc.Target, fixture protest.Fixture) {
		setFunctionBreakpoint(p, t, "main.main")
		assertNoError(p.Continue(), t, "Continue()")
		frames, err := p.SelectedGoroutine().Stacktrace(10, 0)
		assertNoError(err, t, "Stacktrace")
		scope := proc.FrameToScope(p, p.Memory(), nil, frames[2:]...)
		args, _ := scope.FunctionArguments(normalLoadConfig)
		assertNoError(err, t, "FunctionArguments()")
		if len(args) > 0 {
			t.Fatalf("wrong number of arguments for frame %v (%d)", frames[2], len(args))
		}
	})
}

func TestIssue1008(t *testing.T) {
	skipOn(t, "broken - cgo stacktraces", "386")
	protest.MustHaveCgo(t)

	// The external linker on macOS inserts "end of sequence" extended opcodes
	// in debug_line. which we should support correctly.
	withTestProcess("cgostacktest/", t, func(p *proc.Target, fixture protest.Fixture) {
		setFunctionBreakpoint(p, t, "main.main")
		assertNoError(p.Continue(), t, "Continue()")
		loc, err := p.CurrentThread().Location()
		assertNoError(err, t, "CurrentThread().Location()")
		t.Logf("location %v\n", loc)
		if !strings.HasSuffix(loc.File, "/main.go") {
			t.Errorf("unexpected location %s:%d\n", loc.File, loc.Line)
		}
		if loc.Line > 35 {
			t.Errorf("unexpected location %s:%d (file only has 34 lines)\n", loc.File, loc.Line)
		}
	})
}

func testDeclLineCount(t *testing.T, p *proc.Target, lineno int, tgtvars []string) {
	sort.Strings(tgtvars)

	assertLineNumber(p, t, lineno, "Program did not continue to correct next location")
	scope, err := proc.GoroutineScope(p, p.CurrentThread())
	assertNoError(err, t, fmt.Sprintf("GoroutineScope (:%d)", lineno))
	vars, err := scope.Locals(0)
	assertNoError(err, t, fmt.Sprintf("Locals (:%d)", lineno))
	if len(vars) != len(tgtvars) {
		t.Fatalf("wrong number of variables %d (:%d)", len(vars), lineno)
	}
	outvars := make([]string, len(vars))
	for i, v := range vars {
		outvars[i] = v.Name
	}
	sort.Strings(outvars)

	for i := range outvars {
		if tgtvars[i] != outvars[i] {
			t.Fatalf("wrong variables, got: %q expected %q\n", outvars, tgtvars)
		}
	}
}

func TestDeclLine(t *testing.T) {
	ver, _ := goversion.Parse(runtime.Version())
	if ver.Major > 0 && !ver.AfterOrEqual(goversion.GoVersion{Major: 1, Minor: 10, Rev: -1}) {
		t.Skip("go 1.9 and prior versions do not emit DW_AT_decl_line")
	}

	withTestProcess("decllinetest", t, func(p *proc.Target, fixture protest.Fixture) {
		setFileBreakpoint(p, t, fixture.Source, 8)
		setFileBreakpoint(p, t, fixture.Source, 9)
		setFileBreakpoint(p, t, fixture.Source, 10)
		setFileBreakpoint(p, t, fixture.Source, 11)
		setFileBreakpoint(p, t, fixture.Source, 14)

		assertNoError(p.Continue(), t, "Continue 1")
		if goversion.VersionAfterOrEqual(runtime.Version(), 1, 15) {
			testDeclLineCount(t, p, 8, []string{})
		} else {
			testDeclLineCount(t, p, 8, []string{"a"})
		}

		assertNoError(p.Continue(), t, "Continue 2")
		testDeclLineCount(t, p, 9, []string{"a"})

		assertNoError(p.Continue(), t, "Continue 3")
		if goversion.VersionAfterOrEqual(runtime.Version(), 1, 15) {
			testDeclLineCount(t, p, 10, []string{"a"})
		} else {
			testDeclLineCount(t, p, 10, []string{"a", "b"})
		}

		assertNoError(p.Continue(), t, "Continue 4")
		testDeclLineCount(t, p, 11, []string{"a", "b"})

		assertNoError(p.Continue(), t, "Continue 5")
		testDeclLineCount(t, p, 14, []string{"a", "b"})
	})
}

func TestIssue1137(t *testing.T) {
	withTestProcess("dotpackagesiface", t, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue()")
		v := evalVariable(p, t, "iface")
		assertNoError(v.Unreadable, t, "iface unreadable")
		v2 := evalVariable(p, t, "iface2")
		assertNoError(v2.Unreadable, t, "iface2 unreadable")
	})
}

func TestIssue1101(t *testing.T) {
	// If a breakpoint is hit close to process death on a thread that isn't the
	// group leader the process could die while we are trying to stop it.
	//
	// This can be easily reproduced by having the goroutine that's executing
	// main.main (which will almost always run on the thread group leader) wait
	// for a second goroutine before exiting, then setting a breakpoint on the
	// second goroutine and stepping through it (see TestIssue1101 in
	// proc_test.go).
	//
	// When stepping over the return instruction of main.f the deferred
	// wg.Done() call will be executed which will cause the main goroutine to
	// resume and proceed to exit. Both the temporary breakpoint on wg.Done and
	// the temporary breakpoint on the return address of main.f will be in
	// close proximity to main.main calling os.Exit() and causing the death of
	// the thread group leader.

	withTestProcess("issue1101", t, func(p *proc.Target, fixture protest.Fixture) {
		setFunctionBreakpoint(p, t, "main.f")
		assertNoError(p.Continue(), t, "Continue()")
		assertNoError(p.Next(), t, "Next() 1")
		assertNoError(p.Next(), t, "Next() 2")
		lastCmd := "Next() 3"
		exitErr := p.Next()
		if exitErr == nil {
			lastCmd = "final Continue()"
			exitErr = p.Continue()
		}
		if pexit, exited := exitErr.(proc.ErrProcessExited); exited {
			if pexit.Status != 2 && testBackend != "lldb" && (runtime.GOOS != "linux" || runtime.GOARCH != "386") {
				// Looks like there's a bug with debugserver on macOS that sometimes
				// will report exit status 0 instead of the proper exit status.
				//
				// Also it seems that sometimes on linux/386 we will not receive the
				// exit status. This happens if the process exits at the same time as it
				// receives a signal.
				t.Fatalf("process exited status %d (expected 2)", pexit.Status)
			}
		} else {
			assertNoError(exitErr, t, lastCmd)
			t.Fatalf("process did not exit after %s", lastCmd)
		}
	})
}

func TestIssue1145(t *testing.T) {
	withTestProcess("sleep", t, func(p *proc.Target, fixture protest.Fixture) {
		grp := proc.NewGroup(p)
		setFileBreakpoint(p, t, fixture.Source, 18)
		assertNoError(grp.Continue(), t, "Continue()")
		resumeChan := make(chan struct{}, 1)
		grp.ResumeNotify(resumeChan)
		go func() {
			<-resumeChan
			time.Sleep(100 * time.Millisecond)
			grp.RequestManualStop()
		}()

		assertNoError(grp.Next(), t, "Next()")
		if p.Breakpoints().HasSteppingBreakpoints() {
			t.Fatal("has internal breakpoints after manual stop request")
		}
	})
}

func TestHaltKeepsSteppingBreakpoints(t *testing.T) {
	withTestProcess("sleep", t, func(p *proc.Target, fixture protest.Fixture) {
		grp := proc.NewGroup(p)
		grp.KeepSteppingBreakpoints = proc.HaltKeepsSteppingBreakpoints
		setFileBreakpoint(p, t, fixture.Source, 18)
		assertNoError(grp.Continue(), t, "Continue()")
		resumeChan := make(chan struct{}, 1)
		grp.ResumeNotify(resumeChan)
		go func() {
			<-resumeChan
			time.Sleep(100 * time.Millisecond)
			grp.RequestManualStop()
		}()

		assertNoError(grp.Next(), t, "Next()")
		if !p.Breakpoints().HasSteppingBreakpoints() {
			t.Fatal("does not have internal breakpoints after manual stop request")
		}
	})
}

func TestDisassembleGlobalVars(t *testing.T) {
	skipOn(t, "broken - global variable symbolication", "arm64") // On ARM64 symLookup can't look up variables due to how they are loaded, see issue #1778
	// On 386 linux when pie, the genered code use __x86.get_pc_thunk to ensure position-independent.
	// Locate global variable by
	//    `CALL __x86.get_pc_thunk.ax(SB) 0xb0f7f
	//     LEAL 0xc0a19(AX), AX`
	// dynamically.
	if runtime.GOARCH == "386" && runtime.GOOS == "linux" && buildMode == "pie" {
		t.Skip("On 386 linux when pie, symLookup can't look up global variables")
	}
	withTestProcess("teststepconcurrent", t, func(p *proc.Target, fixture protest.Fixture) {
		mainfn := p.BinInfo().LookupFunc["main.main"]
		regs, _ := p.CurrentThread().Registers()
		text, err := proc.Disassemble(p.Memory(), regs, p.Breakpoints(), p.BinInfo(), mainfn.Entry, mainfn.End)
		assertNoError(err, t, "Disassemble")
		found := false
		for i := range text {
			if strings.Index(text[i].Text(proc.IntelFlavour, p.BinInfo()), "main.v") > 0 {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("could not find main.v reference in disassembly")
		}
	})
}

func checkFrame(frame proc.Stackframe, fnname, file string, line int, inlined bool) error {
	if frame.Call.Fn == nil || frame.Call.Fn.Name != fnname {
		return fmt.Errorf("wrong function name: %s", fnname)
	}
	if file != "" {
		if frame.Call.File != file || frame.Call.Line != line {
			return fmt.Errorf("wrong file:line %s:%d", frame.Call.File, frame.Call.Line)
		}
	}
	if frame.Inlined != inlined {
		if inlined {
			return fmt.Errorf("not inlined")
		}
		return fmt.Errorf("inlined")
	}
	return nil
}

func TestAllPCsForFileLines(t *testing.T) {
	if ver, _ := goversion.Parse(runtime.Version()); ver.Major >= 0 && !ver.AfterOrEqual(goversion.GoVersion{Major: 1, Minor: 10, Rev: -1}) {
		// Versions of go before 1.10 do not have DWARF information for inlined calls
		t.Skip("inlining not supported")
	}
	withTestProcessArgs("testinline", t, ".", []string{}, protest.EnableInlining, func(p *proc.Target, fixture protest.Fixture) {
		l2pcs := p.BinInfo().AllPCsForFileLines(fixture.Source, []int{7, 20})
		if len(l2pcs) != 2 {
			t.Fatalf("expected two map entries for %s:{%d,%d} (got %d: %v)", fixture.Source, 7, 20, len(l2pcs), l2pcs)
		}
		pcs := l2pcs[20]
		if len(pcs) < 1 {
			t.Fatalf("expected at least one location for %s:%d (got %d: %#x)", fixture.Source, 20, len(pcs), pcs)
		}
		pcs = l2pcs[7]
		if len(pcs) < 2 {
			t.Fatalf("expected at least two locations for %s:%d (got %d: %#x)", fixture.Source, 7, len(pcs), pcs)
		}
	})
}

func TestInlinedStacktraceAndVariables(t *testing.T) {
	if ver, _ := goversion.Parse(runtime.Version()); ver.Major >= 0 && !ver.AfterOrEqual(goversion.GoVersion{Major: 1, Minor: 10, Rev: -1}) {
		// Versions of go before 1.10 do not have DWARF information for inlined calls
		t.Skip("inlining not supported")
	}

	firstCallCheck := &scopeCheck{
		line: 7,
		ok:   false,
		varChecks: []varCheck{
			{
				name:   "a",
				typ:    "int",
				kind:   reflect.Int,
				hasVal: true,
				intVal: 3,
			},
			{
				name:   "z",
				typ:    "int",
				kind:   reflect.Int,
				hasVal: true,
				intVal: 9,
			},
		},
	}

	secondCallCheck := &scopeCheck{
		line: 7,
		ok:   false,
		varChecks: []varCheck{
			{
				name:   "a",
				typ:    "int",
				kind:   reflect.Int,
				hasVal: true,
				intVal: 4,
			},
			{
				name:   "z",
				typ:    "int",
				kind:   reflect.Int,
				hasVal: true,
				intVal: 16,
			},
		},
	}

	withTestProcessArgs("testinline", t, ".", []string{}, protest.EnableInlining, func(p *proc.Target, fixture protest.Fixture) {
		pcs, err := proc.FindFileLocation(p, fixture.Source, 7)
		assertNoError(err, t, "LineToPC")
		if len(pcs) < 2 {
			t.Fatalf("expected at least two locations for %s:%d (got %d: %#x)", fixture.Source, 7, len(pcs), pcs)
		}
		for _, pc := range pcs {
			t.Logf("setting breakpoint at %#x\n", pc)
			_, err := p.SetBreakpoint(0, pc, proc.UserBreakpoint, nil)
			assertNoError(err, t, fmt.Sprintf("SetBreakpoint(%#x)", pc))
		}

		// first inlined call
		assertNoError(p.Continue(), t, "Continue")
		frames, err := proc.ThreadStacktrace(p.CurrentThread(), 20)
		assertNoError(err, t, "ThreadStacktrace")
		t.Logf("Stacktrace:\n")
		for i := range frames {
			t.Logf("\t%s at %s:%d (%#x)\n", frames[i].Call.Fn.Name, frames[i].Call.File, frames[i].Call.Line, frames[i].Current.PC)
		}

		if err := checkFrame(frames[0], "main.inlineThis", fixture.Source, 7, true); err != nil {
			t.Fatalf("Wrong frame 0: %v", err)
		}
		if err := checkFrame(frames[1], "main.main", fixture.Source, 18, false); err != nil {
			t.Fatalf("Wrong frame 1: %v", err)
		}

		if avar, _ := constant.Int64Val(evalVariable(p, t, "a").Value); avar != 3 {
			t.Fatalf("value of 'a' variable is not 3 (%d)", avar)
		}
		if zvar, _ := constant.Int64Val(evalVariable(p, t, "z").Value); zvar != 9 {
			t.Fatalf("value of 'z' variable is not 9 (%d)", zvar)
		}

		if _, ok := firstCallCheck.checkLocalsAndArgs(p, t); !ok {
			t.Fatalf("exiting for past errors")
		}

		// second inlined call
		assertNoError(p.Continue(), t, "Continue")
		frames, err = proc.ThreadStacktrace(p.CurrentThread(), 20)
		assertNoError(err, t, "ThreadStacktrace (2)")
		t.Logf("Stacktrace 2:\n")
		for i := range frames {
			t.Logf("\t%s at %s:%d (%#x)\n", frames[i].Call.Fn.Name, frames[i].Call.File, frames[i].Call.Line, frames[i].Current.PC)
		}

		if err := checkFrame(frames[0], "main.inlineThis", fixture.Source, 7, true); err != nil {
			t.Fatalf("Wrong frame 0: %v", err)
		}
		if err := checkFrame(frames[1], "main.main", fixture.Source, 19, false); err != nil {
			t.Fatalf("Wrong frame 1: %v", err)
		}

		if avar, _ := constant.Int64Val(evalVariable(p, t, "a").Value); avar != 4 {
			t.Fatalf("value of 'a' variable is not 3 (%d)", avar)
		}
		if zvar, _ := constant.Int64Val(evalVariable(p, t, "z").Value); zvar != 16 {
			t.Fatalf("value of 'z' variable is not 9 (%d)", zvar)
		}
		if bvar, err := evalVariableOrError(p, "b"); err == nil {
			t.Fatalf("expected error evaluating 'b', but it succeeded instead: %v", bvar)
		}

		if _, ok := secondCallCheck.checkLocalsAndArgs(p, t); !ok {
			t.Fatalf("exiting for past errors")
		}
	})
}

func TestInlineStep(t *testing.T) {
	if ver, _ := goversion.Parse(runtime.Version()); ver.Major >= 0 && !ver.AfterOrEqual(goversion.GoVersion{Major: 1, Minor: 10, Rev: -1}) {
		// Versions of go before 1.10 do not have DWARF information for inlined calls
		t.Skip("inlining not supported")
	}
	testseq2Args(".", []string{}, protest.EnableInlining, t, "testinline", "", []seqTest{
		{contContinue, 18},
		{contStep, 6},
		{contStep, 7},
		{contStep, 24},
		{contStep, 25},
		{contStep, 7},
		{contStep, 18},
		{contStep, 19},
	})
}

func TestInlineNext(t *testing.T) {
	if ver, _ := goversion.Parse(runtime.Version()); ver.Major >= 0 && !ver.AfterOrEqual(goversion.GoVersion{Major: 1, Minor: 10, Rev: -1}) {
		// Versions of go before 1.10 do not have DWARF information for inlined calls
		t.Skip("inlining not supported")
	}
	testseq2Args(".", []string{}, protest.EnableInlining, t, "testinline", "", []seqTest{
		{contContinue, 18},
		{contStep, 6},
		{contNext, 7},
		{contNext, 18},
		{contNext, 19},
	})
}

func TestInlineStepOver(t *testing.T) {
	if ver, _ := goversion.Parse(runtime.Version()); ver.Major >= 0 && !ver.AfterOrEqual(goversion.GoVersion{Major: 1, Minor: 10, Rev: -1}) {
		// Versions of go before 1.10 do not have DWARF information for inlined calls
		t.Skip("inlining not supported")
	}
	testseq2Args(".", []string{}, protest.EnableInlining, t, "testinline", "", []seqTest{
		{contContinue, 18},
		{contNext, 19},
		{contNext, 20},
	})
}

func TestInlineStepOut(t *testing.T) {
	if ver, _ := goversion.Parse(runtime.Version()); ver.Major >= 0 && !ver.AfterOrEqual(goversion.GoVersion{Major: 1, Minor: 10, Rev: -1}) {
		// Versions of go before 1.10 do not have DWARF information for inlined calls
		t.Skip("inlining not supported")
	}
	testseq2Args(".", []string{}, protest.EnableInlining, t, "testinline", "", []seqTest{
		{contContinue, 18},
		{contStep, 6},
		{contStepout, 18},
	})
}

func TestInlineFunctionList(t *testing.T) {
	// We should be able to list all functions, even inlined ones.
	ver, _ := goversion.Parse(runtime.Version())
	if ver.Major >= 0 && !ver.AfterOrEqual(goversion.GoVersion{Major: 1, Minor: 10, Rev: -1}) {
		// Versions of go before 1.10 do not have DWARF information for inlined calls
		t.Skip("inlining not supported")
	}
	if runtime.GOOS == "windows" && runtime.GOARCH == "arm64" {
		// TODO(qmuntal): seems to be an upstream issue, investigate.
		t.Skip("inlining not supported")
	}
	withTestProcessArgs("testinline", t, ".", []string{}, protest.EnableInlining|protest.EnableOptimization, func(p *proc.Target, fixture protest.Fixture) {
		var found bool
		for _, fn := range p.BinInfo().Functions {
			if strings.Contains(fn.Name, "inlineThis") {
				found = true
				break
			}
		}
		if !found {
			t.Fatal("inline function not returned")
		}
	})
}

func TestInlineBreakpoint(t *testing.T) {
	// We should be able to set a breakpoint on the call site of an inlined function.
	if ver, _ := goversion.Parse(runtime.Version()); ver.Major >= 0 && !ver.AfterOrEqual(goversion.GoVersion{Major: 1, Minor: 10, Rev: -1}) {
		// Versions of go before 1.10 do not have DWARF information for inlined calls
		t.Skip("inlining not supported")
	}
	withTestProcessArgs("testinline", t, ".", []string{}, protest.EnableInlining|protest.EnableOptimization, func(p *proc.Target, fixture protest.Fixture) {
		pcs, err := proc.FindFileLocation(p, fixture.Source, 17)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%#v\n", pcs)
		if len(pcs) != 1 {
			t.Fatalf("unable to get PC for inlined function call: %v", pcs)
		}
		fn := p.BinInfo().PCToFunc(pcs[0])
		expectedFn := "main.main"
		if fn.Name != expectedFn {
			t.Fatalf("incorrect function returned, expected %s, got %s", expectedFn, fn.Name)
		}
		_, err = p.SetBreakpoint(0, pcs[0], proc.UserBreakpoint, nil)
		if err != nil {
			t.Fatalf("unable to set breakpoint: %v", err)
		}
	})
}

func TestDoubleInlineBreakpoint(t *testing.T) {
	// We should be able to set a breakpoint on an inlined function that
	// has been inlined within an inlined function.
	if ver, _ := goversion.Parse(runtime.Version()); ver.Major >= 0 && !ver.AfterOrEqual(goversion.GoVersion{Major: 1, Minor: 10, Rev: -1}) {
		// Versions of go before 1.10 do not have DWARF information for inlined calls
		t.Skip("inlining not supported")
	}
	withTestProcessArgs("doubleinline", t, ".", []string{}, protest.EnableInlining|protest.EnableOptimization, func(p *proc.Target, fixture protest.Fixture) {
		fns, err := p.BinInfo().FindFunction("main.(*Rectangle).Height")
		if err != nil {
			t.Fatal(err)
		}
		if len(fns) != 1 {
			t.Fatalf("expected one function for Height, got %d", len(fns))
		}
		if len(fns[0].InlinedCalls) != 1 {
			t.Fatalf("expected one inlined call for Height, got %d", len(fns[0].InlinedCalls))
		}
	})
}

func TestIssue951(t *testing.T) {
	if ver, _ := goversion.Parse(runtime.Version()); ver.Major >= 0 && !ver.AfterOrEqual(goversion.GoVersion{Major: 1, Minor: 9, Rev: -1}) {
		t.Skip("scopes not implemented in <=go1.8")
	}

	withTestProcess("issue951", t, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue()")
		scope, err := proc.GoroutineScope(p, p.CurrentThread())
		assertNoError(err, t, "GoroutineScope")
		args, err := scope.FunctionArguments(normalLoadConfig)
		assertNoError(err, t, "FunctionArguments")
		t.Logf("%#v", args[0])
		if args[0].Flags&proc.VariableShadowed == 0 {
			t.Error("argument is not shadowed")
		}
		vars, err := scope.LocalVariables(normalLoadConfig)
		assertNoError(err, t, "LocalVariables")
		shadowed, notShadowed := 0, 0
		for i := range vars {
			t.Logf("var %d: %#v\n", i, vars[i])
			if vars[i].Flags&proc.VariableShadowed != 0 {
				shadowed++
			} else {
				notShadowed++
			}
		}
		if shadowed != 1 || notShadowed != 1 {
			t.Errorf("Wrong number of shadowed/non-shadowed local variables: %d %d", shadowed, notShadowed)
		}
	})
}

func TestDWZCompression(t *testing.T) {
	// If dwz is not available in the system, skip this test
	if _, err := exec.LookPath("dwz"); err != nil {
		t.Skip("dwz not installed")
	}

	withTestProcessArgs("dwzcompression", t, ".", []string{}, protest.EnableDWZCompression, func(p *proc.Target, fixture protest.Fixture) {
		setFunctionBreakpoint(p, t, "C.fortytwo")
		assertNoError(p.Continue(), t, "first Continue()")
		val := evalVariable(p, t, "stdin")
		if val.RealType == nil {
			t.Errorf("Can't find type for \"stdin\" global variable")
		}
	})
}

func TestMapLoadConfigWithReslice(t *testing.T) {
	// Check that load configuration is respected for resliced maps.
	withTestProcess("testvariables2", t, func(p *proc.Target, fixture protest.Fixture) {
		zolotovLoadCfg := proc.LoadConfig{FollowPointers: true, MaxStructFields: -1, MaxVariableRecurse: 3, MaxStringLen: 10, MaxArrayValues: 10}
		assertNoError(p.Continue(), t, "First Continue()")
		scope, err := proc.GoroutineScope(p, p.CurrentThread())
		assertNoError(err, t, "GoroutineScope")
		m1, err := scope.EvalExpression("m1", zolotovLoadCfg)
		assertNoError(err, t, "EvalVariable")
		t.Logf("m1 returned children %d (%d)", len(m1.Children)/2, m1.Len)

		expr := fmt.Sprintf("(*(*%q)(%d))[10:]", m1.DwarfType.String(), m1.Addr)
		t.Logf("expr %q\n", expr)

		m1cont, err := scope.EvalExpression(expr, zolotovLoadCfg)
		assertNoError(err, t, "EvalVariable")

		t.Logf("m1cont returned children %d", len(m1cont.Children)/2)

		if len(m1cont.Children) != 20 {
			t.Fatalf("wrong number of children returned %d\n", len(m1cont.Children)/2)
		}
	})
}

func TestStepOutReturn(t *testing.T) {
	ver, _ := goversion.Parse(runtime.Version())
	if ver.Major >= 0 && !ver.AfterOrEqual(goversion.GoVersion{Major: 1, Minor: 10, Rev: -1}) {
		t.Skip("return variables aren't marked on 1.9 or earlier")
	}
	withTestProcess("stepoutret", t, func(p *proc.Target, fixture protest.Fixture) {
		setFunctionBreakpoint(p, t, "main.stepout")
		assertNoError(p.Continue(), t, "Continue")
		assertNoError(p.StepOut(), t, "StepOut")
		ret := p.CurrentThread().Common().ReturnValues(normalLoadConfig)
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
		if s := constant.StringVal(ret[stridx].Value); s != "return 47" {
			t.Fatalf("(str) bad return value %q", s)
		}

		if ret[numidx].Name != "num" {
			t.Fatalf("(num) bad return value name %s", ret[numidx].Name)
		}
		if ret[numidx].Kind != reflect.Int {
			t.Fatalf("(num) bad return value kind %v", ret[numidx].Kind)
		}
		if n, _ := constant.Int64Val(ret[numidx].Value); n != 48 {
			t.Fatalf("(num) bad return value %d", n)
		}
	})
}

func TestOptimizationCheck(t *testing.T) {
	withTestProcess("continuetestprog", t, func(p *proc.Target, fixture protest.Fixture) {
		fn := p.BinInfo().LookupFunc["main.main"]
		if fn.Optimized() {
			t.Fatalf("main.main is optimized")
		}
	})

	if goversion.VersionAfterOrEqual(runtime.Version(), 1, 10) {
		withTestProcessArgs("continuetestprog", t, ".", []string{}, protest.EnableOptimization|protest.EnableInlining, func(p *proc.Target, fixture protest.Fixture) {
			fn := p.BinInfo().LookupFunc["main.main"]
			if !fn.Optimized() {
				t.Fatalf("main.main is not optimized")
			}
		})
	}
}

func TestIssue1264(t *testing.T) {
	// It should be possible to set a breakpoint condition that consists only
	// of evaluating a single boolean variable.
	withTestProcess("issue1264", t, func(p *proc.Target, fixture protest.Fixture) {
		bp := setFileBreakpoint(p, t, fixture.Source, 8)
		bp.UserBreaklet().Cond = &ast.Ident{Name: "equalsTwo"}
		assertNoError(p.Continue(), t, "Continue()")
		assertLineNumber(p, t, 8, "after continue")
	})
}

func TestReadDefer(t *testing.T) {
	withTestProcess("deferstack", t, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue")
		frames, err := p.SelectedGoroutine().Stacktrace(10, proc.StacktraceReadDefers)
		assertNoError(err, t, "Stacktrace")

		logStacktrace(t, p, frames)

		examples := []struct {
			frameIdx     int
			topmostDefer string
			defers       []string
		}{
			// main.call3 (defers nothing, topmost defer main.f2)
			{0, "main.f2", []string{}},

			// main.call2 (defers main.f2, main.f3, topmost defer main.f2)
			{1, "main.f2", []string{"main.f2", "main.f3"}},

			// main.call1 (defers main.f1, main.f2, topmost defer main.f1)
			{2, "main.f1", []string{"main.f1", "main.f2"}},

			// main.main (defers nothing)
			{3, "", []string{}}}

		defercheck := func(d *proc.Defer, deferName, tgt string, frameIdx int) {
			if d == nil {
				t.Fatalf("expected %q as %s of frame %d, got nothing", tgt, deferName, frameIdx)
			}
			if d.Unreadable != nil {
				t.Fatalf("expected %q as %s of frame %d, got unreadable defer: %v", tgt, deferName, frameIdx, d.Unreadable)
			}
			_, _, dfn := d.DeferredFunc(p)
			if dfn == nil {
				t.Fatalf("expected %q as %s of frame %d, got %#x", tgt, deferName, frameIdx, d.DwrapPC)
			}
			if dfn.Name != tgt {
				t.Fatalf("expected %q as %s of frame %d, got %q", tgt, deferName, frameIdx, dfn.Name)
			}
		}

		for _, example := range examples {
			frame := &frames[example.frameIdx]

			if example.topmostDefer != "" {
				defercheck(frame.TopmostDefer, "topmost defer", example.topmostDefer, example.frameIdx)
			}

			if len(example.defers) != len(frames[example.frameIdx].Defers) {
				t.Fatalf("expected %d defers for %d, got %v", len(example.defers), example.frameIdx, frame.Defers)
			}

			for deferIdx := range example.defers {
				defercheck(frame.Defers[deferIdx], fmt.Sprintf("defer %d", deferIdx), example.defers[deferIdx], example.frameIdx)
			}
		}
	})
}

func TestNextUnknownInstr(t *testing.T) {
	skipUnlessOn(t, "amd64 only", "amd64")
	if !goversion.VersionAfterOrEqual(runtime.Version(), 1, 10) {
		t.Skip("versions of Go before 1.10 can't assemble the instruction VPUNPCKLWD")
	}
	withTestProcess("nodisasm/", t, func(p *proc.Target, fixture protest.Fixture) {
		setFunctionBreakpoint(p, t, "main.asmFunc")
		assertNoError(p.Continue(), t, "Continue()")
		assertNoError(p.Next(), t, "Next()")
	})
}

func TestReadDeferArgs(t *testing.T) {
	if goversion.VersionAfterOrEqual(runtime.Version(), 1, 17) {
		// When regabi is enabled in Go 1.17 and later, reading arguments of
		// deferred functions becomes significantly more complicated because of
		// the autogenerated code used to unpack the argument frame stored in
		// runtime._defer into registers.
		// We either need to know how to do the translation, implementing the ABI1
		// rules in Delve, or have some assistance from the compiler (for example
		// have the dwrap function contain entries for each of the captured
		// variables with a location describing their offset from DX).
		// Ultimately this feature is unimportant enough that we can leave it
		// disabled for now.
		t.Skip("unsupported")
	}
	var tests = []struct {
		frame, deferCall int
		a, b             int64
	}{
		{1, 1, 42, 61},
		{2, 2, 1, -1},
	}

	withTestProcess("deferstack", t, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue()")

		for _, test := range tests {
			scope, err := proc.ConvertEvalScope(p, -1, test.frame, test.deferCall)
			assertNoError(err, t, fmt.Sprintf("ConvertEvalScope(-1, %d, %d)", test.frame, test.deferCall))

			if !goversion.VersionAfterOrEqual(runtime.Version(), 1, 17) {
				// In Go 1.17 deferred function calls can end up inside a wrapper, and
				// the scope for this evaluation needs to be the wrapper.
				if scope.Fn.Name != "main.f2" {
					t.Fatalf("expected function \"main.f2\" got %q", scope.Fn.Name)
				}
			}

			avar, err := scope.EvalExpression("a", normalLoadConfig)
			if err != nil {
				t.Fatal(err)
			}
			bvar, err := scope.EvalExpression("b", normalLoadConfig)
			if err != nil {
				t.Fatal(err)
			}

			a, _ := constant.Int64Val(avar.Value)
			b, _ := constant.Int64Val(bvar.Value)

			if a != test.a {
				t.Errorf("value of argument 'a' at frame %d, deferred call %d: %d (expected %d)", test.frame, test.deferCall, a, test.a)
			}

			if b != test.b {
				t.Errorf("value of argument 'b' at frame %d, deferred call %d: %d (expected %d)", test.frame, test.deferCall, b, test.b)
			}
		}
	})
}

func TestIssue1374(t *testing.T) {
	// Continue did not work when stopped at a breakpoint immediately after calling CallFunction.
	protest.MustSupportFunctionCalls(t, testBackend)
	withTestProcess("issue1374", t, func(p *proc.Target, fixture protest.Fixture) {
		grp := proc.NewGroup(p)
		setFileBreakpoint(p, t, fixture.Source, 7)
		assertNoError(grp.Continue(), t, "First Continue")
		assertLineNumber(p, t, 7, "Did not continue to correct location (first continue),")
		assertNoError(proc.EvalExpressionWithCalls(grp, p.SelectedGoroutine(), "getNum()", normalLoadConfig, true), t, "Call")
		err := grp.Continue()
		if _, isexited := err.(proc.ErrProcessExited); !isexited {
			regs, _ := p.CurrentThread().Registers()
			f, l, _ := p.BinInfo().PCToLine(regs.PC())
			t.Fatalf("expected process exited error got %v at %s:%d", err, f, l)
		}
	})
}

func TestIssue1432(t *testing.T) {
	// Check that taking the address of a struct, casting it into a pointer to
	// the struct's type and then accessing a member field will still:
	// - perform auto-dereferencing on struct member access
	// - yield a Variable that's ultimately assignable (i.e. has an address)
	withTestProcess("issue1432", t, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue")
		svar := evalVariable(p, t, "s")
		t.Logf("%#x", svar.Addr)

		scope, err := proc.GoroutineScope(p, p.CurrentThread())
		assertNoError(err, t, "GoroutineScope()")

		err = scope.SetVariable(fmt.Sprintf("(*\"main.s\")(%#x).i", svar.Addr), "10")
		assertNoError(err, t, "SetVariable")
	})
}

func TestGoroutinesInfoLimit(t *testing.T) {
	withTestProcess("teststepconcurrent", t, func(p *proc.Target, fixture protest.Fixture) {
		setFileBreakpoint(p, t, fixture.Source, 37)
		assertNoError(p.Continue(), t, "Continue()")

		gcount := 0
		nextg := 0
		const goroutinesInfoLimit = 10
		for nextg >= 0 {
			oldnextg := nextg
			var gs []*proc.G
			var err error
			gs, nextg, err = proc.GoroutinesInfo(p, nextg, goroutinesInfoLimit)
			assertNoError(err, t, fmt.Sprintf("GoroutinesInfo(%d, %d)", oldnextg, goroutinesInfoLimit))
			gcount += len(gs)
			t.Logf("got %d goroutines\n", len(gs))
		}

		t.Logf("number of goroutines: %d\n", gcount)

		gs, _, err := proc.GoroutinesInfo(p, 0, 0)
		assertNoError(err, t, "GoroutinesInfo(0, 0)")
		t.Logf("number of goroutines (full scan): %d\n", gcount)
		if len(gs) != gcount {
			t.Fatalf("mismatch in the number of goroutines %d %d\n", gcount, len(gs))
		}
	})
}

func TestIssue1469(t *testing.T) {
	withTestProcess("issue1469", t, func(p *proc.Target, fixture protest.Fixture) {
		setFileBreakpoint(p, t, fixture.Source, 13)
		assertNoError(p.Continue(), t, "Continue()")

		gid2thread := make(map[int64][]proc.Thread)
		for _, thread := range p.ThreadList() {
			g, _ := proc.GetG(thread)
			if g == nil {
				continue
			}
			gid2thread[g.ID] = append(gid2thread[g.ID], thread)
		}

		for gid := range gid2thread {
			if len(gid2thread[gid]) > 1 {
				t.Logf("too many threads running goroutine %d", gid)
				for _, thread := range gid2thread[gid] {
					t.Logf("\tThread %d", thread.ThreadID())
					frames, err := proc.ThreadStacktrace(thread, 20)
					if err != nil {
						t.Logf("\t\tcould not get stacktrace %v", err)
					}
					for _, frame := range frames {
						t.Logf("\t\t%#x at %s:%d (systemstack: %v)", frame.Call.PC, frame.Call.File, frame.Call.Line, frame.SystemStack)
					}
				}
			}
		}
	})
}

func TestDeadlockBreakpoint(t *testing.T) {
	skipOn(t, "upstream issue - https://github.com/golang/go/issues/29322", "pie")
	deadlockBp := proc.FatalThrow
	if !goversion.VersionAfterOrEqual(runtime.Version(), 1, 11) {
		deadlockBp = proc.UnrecoveredPanic
	}
	withTestProcess("testdeadlock", t, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue()")

		bp := p.CurrentThread().Breakpoint()
		if bp.Breakpoint == nil || bp.Logical.Name != deadlockBp {
			t.Fatalf("did not stop at deadlock breakpoint %v", bp)
		}
	})
}

func findSource(source string, sources []string) bool {
	for _, s := range sources {
		if s == source {
			return true
		}
	}
	return false
}

func TestListImages(t *testing.T) {
	pluginFixtures := protest.WithPlugins(t, protest.AllNonOptimized, "plugin1/", "plugin2/")

	withTestProcessArgs("plugintest", t, ".", []string{pluginFixtures[0].Path, pluginFixtures[1].Path}, protest.AllNonOptimized, func(p *proc.Target, fixture protest.Fixture) {
		if !findSource(fixture.Source, p.BinInfo().Sources) {
			t.Fatalf("could not find %s in sources: %q\n", fixture.Source, p.BinInfo().Sources)
		}

		assertNoError(p.Continue(), t, "first continue")
		f, l := currentLineNumber(p, t)
		plugin1Found := false
		t.Logf("Libraries before %s:%d:", f, l)
		for _, image := range p.BinInfo().Images {
			t.Logf("\t%#x %q err:%v", image.StaticBase, image.Path, image.LoadError())
			if image.Path == pluginFixtures[0].Path {
				plugin1Found = true
			}
		}
		if !plugin1Found {
			t.Fatalf("Could not find plugin1")
		}
		if !findSource(fixture.Source, p.BinInfo().Sources) {
			// Source files for the base program must be available even after a plugin is loaded. Issue #2074.
			t.Fatalf("could not find %s in sources (after loading plugin): %q\n", fixture.Source, p.BinInfo().Sources)
		}
		assertNoError(p.Continue(), t, "second continue")
		f, l = currentLineNumber(p, t)
		plugin1Found, plugin2Found := false, false
		t.Logf("Libraries after %s:%d:", f, l)
		for _, image := range p.BinInfo().Images {
			t.Logf("\t%#x %q err:%v", image.StaticBase, image.Path, image.LoadError())
			switch image.Path {
			case pluginFixtures[0].Path:
				plugin1Found = true
			case pluginFixtures[1].Path:
				plugin2Found = true
			}
		}
		if !plugin1Found {
			t.Fatalf("Could not find plugin1")
		}
		if !plugin2Found {
			t.Fatalf("Could not find plugin2")
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
	withTestProcess("testnextprog", t, func(p *proc.Target, fixture protest.Fixture) {
		setFunctionBreakpoint(p, t, "main.testgoroutine")
		assertNoError(p.Continue(), t, "Continue()")
		as, err := proc.Ancestors(p, p.SelectedGoroutine(), 1000)
		assertNoError(err, t, "Ancestors")
		t.Logf("ancestors: %#v\n", as)
		if len(as) != 1 {
			t.Fatalf("expected only one ancestor got %d", len(as))
		}
		mainFound := false
		for i, a := range as {
			astack, err := a.Stack(100)
			assertNoError(err, t, fmt.Sprintf("Ancestor %d stack", i))
			t.Logf("ancestor %d\n", i)
			logStacktrace(t, p, astack)
			for _, frame := range astack {
				if frame.Current.Fn != nil && frame.Current.Fn.Name == "main.main" {
					mainFound = true
				}
			}
		}
		if !mainFound {
			t.Fatal("could not find main.main function in ancestors")
		}
	})
}

func testCallConcurrentCheckReturns(p *proc.Target, t *testing.T, gid1, gid2 int64) int {
	found := 0
	for _, thread := range p.ThreadList() {
		g, _ := proc.GetG(thread)
		if g == nil || (g.ID != gid1 && g.ID != gid2) {
			continue
		}
		retvals := thread.Common().ReturnValues(normalLoadConfig)
		if len(retvals) == 0 {
			continue
		}
		n, _ := constant.Int64Val(retvals[0].Value)
		t.Logf("injection on goroutine %d (thread %d) returned %v\n", g.ID, thread.ThreadID(), n)
		switch g.ID {
		case gid1:
			if n != 11 {
				t.Errorf("wrong return value for goroutine %d", g.ID)
			}
			found++
		case gid2:
			if n != 12 {
				t.Errorf("wrong return value for goroutine %d", g.ID)
			}
			found++
		}
	}
	return found
}

func TestCallConcurrent(t *testing.T) {
	protest.MustSupportFunctionCalls(t, testBackend)
	withTestProcess("teststepconcurrent", t, func(p *proc.Target, fixture protest.Fixture) {
		grp := proc.NewGroup(p)
		bp := setFileBreakpoint(p, t, fixture.Source, 24)
		assertNoError(grp.Continue(), t, "Continue()")
		//_, err := p.ClearBreakpoint(bp.Addr)
		//assertNoError(err, t, "ClearBreakpoint() returned an error")

		gid1 := p.SelectedGoroutine().ID
		t.Logf("starting injection in %d / %d", p.SelectedGoroutine().ID, p.CurrentThread().ThreadID())
		assertNoError(proc.EvalExpressionWithCalls(grp, p.SelectedGoroutine(), "Foo(10, 1)", normalLoadConfig, false), t, "EvalExpressionWithCalls()")

		returned := testCallConcurrentCheckReturns(p, t, gid1, -1)

		curthread := p.CurrentThread()
		if curbp := curthread.Breakpoint(); curbp.Breakpoint == nil || curbp.LogicalID() != bp.LogicalID() || returned > 0 {
			t.Logf("skipping test, the call injection terminated before we hit a breakpoint in a different thread")
			return
		}

		err := p.ClearBreakpoint(bp.Addr)
		assertNoError(err, t, "ClearBreakpoint() returned an error")

		gid2 := p.SelectedGoroutine().ID
		t.Logf("starting second injection in %d / %d", p.SelectedGoroutine().ID, p.CurrentThread().ThreadID())
		assertNoError(proc.EvalExpressionWithCalls(grp, p.SelectedGoroutine(), "Foo(10, 2)", normalLoadConfig, false), t, "EvalExpressioniWithCalls")

		for {
			returned += testCallConcurrentCheckReturns(p, t, gid1, gid2)
			if returned >= 2 {
				break
			}
			t.Logf("Continuing... %d", returned)
			assertNoError(grp.Continue(), t, "Continue()")
		}

		grp.Continue()
	})
}

func TestPluginStepping(t *testing.T) {
	pluginFixtures := protest.WithPlugins(t, protest.AllNonOptimized, "plugin1/", "plugin2/")

	testseq2Args(".", []string{pluginFixtures[0].Path, pluginFixtures[1].Path}, protest.AllNonOptimized, t, "plugintest2", "", []seqTest{
		{contContinue, 41},
		{contStep, "plugin1.go:9"},
		{contStep, "plugin1.go:10"},
		{contStep, "plugin1.go:11"},
		{contNext, "plugin1.go:12"},
		{contNext, "plugintest2.go:41"},
		{contNext, "plugintest2.go:42"},
		{contStep, "plugin2.go:22"},
		{contNext, "plugin2.go:23"},
		{contNext, "plugin2.go:26"},
		{contNext, "plugintest2.go:42"}})
}

func TestIssue1601(t *testing.T) {
	protest.MustHaveCgo(t)
	// Tests that recursive types involving C qualifiers and typedefs are parsed correctly
	withTestProcess("issue1601", t, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue")
		evalVariable(p, t, "C.globalq")
	})
}

func TestIssue1615(t *testing.T) {
	// A breakpoint condition that tests for string equality with a constant string shouldn't fail with 'string too long for comparison' error

	withTestProcess("issue1615", t, func(p *proc.Target, fixture protest.Fixture) {
		bp := setFileBreakpoint(p, t, fixture.Source, 19)
		bp.UserBreaklet().Cond = &ast.BinaryExpr{
			Op: token.EQL,
			X:  &ast.Ident{Name: "s"},
			Y:  &ast.BasicLit{Kind: token.STRING, Value: `"projects/my-gcp-project-id-string/locations/us-central1/queues/my-task-queue-name"`},
		}

		assertNoError(p.Continue(), t, "Continue")
		assertLineNumber(p, t, 19, "")
	})
}

func TestCgoStacktrace2(t *testing.T) {
	skipOn(t, "upstream issue", "windows")
	skipOn(t, "broken", "386")
	skipOn(t, "broken", "arm64")
	protest.MustHaveCgo(t)
	// If a panic happens during cgo execution the stacktrace should show the C
	// function that caused the problem.
	withTestProcess("cgosigsegvstack", t, func(p *proc.Target, fixture protest.Fixture) {
		p.Continue()
		frames, err := proc.ThreadStacktrace(p.CurrentThread(), 100)
		assertNoError(err, t, "Stacktrace()")
		logStacktrace(t, p, frames)
		m := stacktraceCheck(t, []string{"C.sigsegv", "C.testfn", "main.main"}, frames)
		if m == nil {
			t.Fatal("see previous loglines")
		}
	})
}

func TestIssue1656(t *testing.T) {
	skipUnlessOn(t, "amd64 only", "amd64")
	withTestProcess("issue1656/", t, func(p *proc.Target, fixture protest.Fixture) {
		setFileBreakpoint(p, t, filepath.ToSlash(filepath.Join(fixture.BuildDir, "main.s")), 5)
		assertNoError(p.Continue(), t, "Continue()")
		t.Logf("step1\n")
		assertNoError(p.Step(), t, "Step()")
		assertLineNumber(p, t, 8, "wrong line number after first step")
		t.Logf("step2\n")
		assertNoError(p.Step(), t, "Step()")
		assertLineNumber(p, t, 9, "wrong line number after second step")
	})
}

func TestBreakpointConfusionOnResume(t *testing.T) {
	// Checks that SetCurrentBreakpoint, (*Thread).StepInstruction and
	// native.(*Thread).singleStep all agree on which breakpoint the thread is
	// stopped at.
	// This test checks for a regression introduced when fixing Issue #1656
	skipUnlessOn(t, "amd64 only", "amd64")
	withTestProcess("nopbreakpoint/", t, func(p *proc.Target, fixture protest.Fixture) {
		maindots := filepath.ToSlash(filepath.Join(fixture.BuildDir, "main.s"))
		maindotgo := filepath.ToSlash(filepath.Join(fixture.BuildDir, "main.go"))
		setFileBreakpoint(p, t, maindots, 5) // line immediately after the NOP
		assertNoError(p.Continue(), t, "First Continue")
		assertLineNumber(p, t, 5, "not on main.s:5")
		setFileBreakpoint(p, t, maindots, 4)   // sets a breakpoint on the NOP line, which will be one byte before the breakpoint we currently are stopped at.
		setFileBreakpoint(p, t, maindotgo, 18) // set one extra breakpoint so that we can recover execution and check the global variable g
		assertNoError(p.Continue(), t, "Second Continue")
		gvar := evalVariable(p, t, "g")
		if n, _ := constant.Int64Val(gvar.Value); n != 1 {
			t.Fatalf("wrong value of global variable 'g': %v (expected 1)", gvar.Value)
		}
	})
}

func TestIssue1736(t *testing.T) {
	withTestProcess("testvariables2", t, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue()")
		ch1BufVar := evalVariable(p, t, "*(ch1.buf)")
		q := fmt.Sprintf("*(*%q)(%d)", ch1BufVar.DwarfType.Common().Name, ch1BufVar.Addr)
		t.Logf("%s", q)
		ch1BufVar2 := evalVariable(p, t, q)
		if ch1BufVar2.Unreadable != nil {
			t.Fatal(ch1BufVar2.Unreadable)
		}
	})
}

func TestIssue1817(t *testing.T) {
	// Setting a breakpoint on a line that doesn't have any PC addresses marked
	// is_stmt should work.
	withTestProcess("issue1817", t, func(p *proc.Target, fixture protest.Fixture) {
		setFileBreakpoint(p, t, fixture.Source, 16)
	})
}

func TestListPackagesBuildInfo(t *testing.T) {
	withTestProcess("pkgrenames", t, func(p *proc.Target, fixture protest.Fixture) {
		pkgs := p.BinInfo().ListPackagesBuildInfo(true)
		t.Logf("returned %d", len(pkgs))
		if len(pkgs) < 10 {
			t.Errorf("very few packages returned")
		}
		for _, pkg := range pkgs {
			t.Logf("%q %q", pkg.ImportPath, pkg.DirectoryPath)
			const _fixtures = "_fixtures"
			fidx := strings.Index(pkg.ImportPath, _fixtures)
			if fidx < 0 {
				continue
			}
			if !strings.HasSuffix(strings.Replace(pkg.DirectoryPath, "\\", "/", -1), pkg.ImportPath[fidx:]) {
				t.Errorf("unexpected suffix: %q %q", pkg.ImportPath, pkg.DirectoryPath)
			}
		}
	})
}

func TestIssue1795(t *testing.T) {
	// When doing midstack inlining the Go compiler sometimes (always?) emits
	// the toplevel inlined call with ranges that do not cover the inlining of
	// other nested inlined calls.
	// For example if a function A calls B which calls C and both the calls to
	// B and C are inlined the DW_AT_inlined_subroutine entry for A might have
	// ranges that do not cover the ranges of the inlined call to C.
	// This is probably a violation of the DWARF standard (it's unclear) but we
	// might as well support it as best as possible anyway.
	if !goversion.VersionAfterOrEqual(runtime.Version(), 1, 13) {
		t.Skip("Test not relevant to Go < 1.13")
	}
	withTestProcessArgs("issue1795", t, ".", []string{}, protest.EnableInlining|protest.EnableOptimization, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue()")
		assertLineNumber(p, t, 12, "wrong line number after Continue,")
		assertNoError(p.Next(), t, "Next()")
		assertLineNumber(p, t, 13, "wrong line number after Next,")
	})
	withTestProcessArgs("issue1795", t, ".", []string{}, protest.EnableInlining|protest.EnableOptimization, func(p *proc.Target, fixture protest.Fixture) {
		setFunctionBreakpoint(p, t, "regexp.(*Regexp).doExecute")
		assertNoError(p.Continue(), t, "Continue()")
		assertLineNumber(p, t, 12, "wrong line number after Continue (1),")
		assertNoError(p.Continue(), t, "Continue()")
		frames, err := proc.ThreadStacktrace(p.CurrentThread(), 40)
		assertNoError(err, t, "ThreadStacktrace()")
		logStacktrace(t, p, frames)
		if err := checkFrame(frames[0], "regexp.(*Regexp).doExecute", "", 0, false); err != nil {
			t.Errorf("Wrong frame 0: %v", err)
		}
		if err := checkFrame(frames[1], "regexp.(*Regexp).doMatch", "", 0, true); err != nil {
			t.Errorf("Wrong frame 1: %v", err)
		}
		if err := checkFrame(frames[2], "regexp.(*Regexp).MatchString", "", 0, true); err != nil {
			t.Errorf("Wrong frame 2: %v", err)
		}
		if err := checkFrame(frames[3], "main.main", fixture.Source, 12, false); err != nil {
			t.Errorf("Wrong frame 3: %v", err)
		}
	})
}

func BenchmarkConditionalBreakpoints(b *testing.B) {
	b.N = 1
	withTestProcess("issue1549", b, func(p *proc.Target, fixture protest.Fixture) {
		bp := setFileBreakpoint(p, b, fixture.Source, 12)
		bp.UserBreaklet().Cond = &ast.BinaryExpr{
			Op: token.EQL,
			X:  &ast.Ident{Name: "value"},
			Y:  &ast.BasicLit{Kind: token.INT, Value: "-1"},
		}
		err := p.Continue()
		if _, exited := err.(proc.ErrProcessExited); !exited {
			b.Fatalf("Unexpected error on Continue(): %v", err)
		}
	})
}

func TestBackwardNextGeneral(t *testing.T) {
	if testBackend != "rr" {
		t.Skip("Reverse stepping test needs rr")
	}
	testseq2(t, "testnextprog", "main.helloworld", []seqTest{
		{contContinue, 13},
		{contNext, 14},
		{contReverseNext, 13},
		{contReverseNext, 34},
		{contReverseNext, 28},
		{contReverseNext, 27},
		{contReverseNext, 26},
		{contReverseNext, 24},
		{contReverseNext, 23},
		{contReverseNext, 31},
		{contReverseNext, 26},
		{contReverseNext, 24},
		{contReverseNext, 23},
		{contReverseNext, 31},
		{contReverseNext, 26},
		{contReverseNext, 24},
		{contReverseNext, 23},
		{contReverseNext, 20},
		{contReverseNext, 19},
		{contReverseNext, 17},
		{contReverseNext, 39},
		{contReverseNext, 38},
		{contReverseNext, 37},
	})
}

func TestBackwardStepOutGeneral(t *testing.T) {
	if testBackend != "rr" {
		t.Skip("Reverse stepping test needs rr")
	}
	testseq2(t, "testnextprog", "main.helloworld", []seqTest{
		{contContinue, 13},
		{contNext, 14},
		{contReverseStepout, 34},
		{contReverseStepout, 39},
	})
}

func TestBackwardStepGeneral(t *testing.T) {
	if testBackend != "rr" {
		t.Skip("Reverse stepping test needs rr")
	}
	testseq2(t, "testnextprog", "main.helloworld", []seqTest{
		{contContinue, 13},
		{contNext, 14},
		{contReverseStep, 13},
		{contReverseStep, 34},
		{contReverseStep, 28},
		{contReverseNext, 27}, // skip fmt.Printf
		{contReverseStep, 26},
		{contReverseStep, 24},
		{contReverseStep, 23},
		{contReverseStep, 11},
		{contReverseNext, 10}, // skip time.Sleep
		{contReverseStep, 9},

		{contReverseStep, 31},
		{contReverseStep, 26},
		{contReverseStep, 24},
		{contReverseStep, 23},
		{contReverseStep, 11},
		{contReverseNext, 10}, // skip time.Sleep
		{contReverseStep, 9},

		{contReverseStep, 31},
		{contReverseStep, 26},
		{contReverseStep, 24},
		{contReverseStep, 23},
		{contReverseStep, 20},
		{contReverseStep, 19},
		{contReverseStep, 17},
		{contReverseStep, 39},
		{contReverseStep, 38},
		{contReverseStep, 37},
	})
}

func TestBackwardNextDeferPanic(t *testing.T) {
	if testBackend != "rr" {
		t.Skip("Reverse stepping test needs rr")
	}
	if goversion.VersionAfterOrEqual(runtime.Version(), 1, 18) {
		testseq2(t, "defercall", "", []seqTest{
			{contContinue, 12},
			{contReverseNext, 11},
			{contReverseNext, 10},
			{contReverseNext, 9},
			{contReverseNext, 27},

			{contContinueToBreakpoint, 12}, // skip first call to sampleFunction
			{contContinueToBreakpoint, 6},  // go to call to sampleFunction through deferreturn
			{contReverseNext, -1},          // runtime.deferreturn, maybe we should try to skip this
			{contReverseStepout, 13},
			{contReverseNext, 12},
			{contReverseNext, 11},
			{contReverseNext, 10},
			{contReverseNext, 9},
			{contReverseNext, 27},

			{contContinueToBreakpoint, 18}, // go to panic call
			{contNext, 6},                  // panic so the deferred call happens
			{contReverseNext, 18},
			{contReverseNext, 17},
			{contReverseNext, 16},
			{contReverseNext, 15},
			{contReverseNext, 23},
			{contReverseNext, 22},
			{contReverseNext, 21},
			{contReverseNext, 28},
		})
	} else {
		testseq2(t, "defercall", "", []seqTest{
			{contContinue, 12},
			{contReverseNext, 11},
			{contReverseNext, 10},
			{contReverseNext, 9},
			{contReverseNext, 27},

			{contContinueToBreakpoint, 12}, // skip first call to sampleFunction
			{contContinueToBreakpoint, 6},  // go to call to sampleFunction through deferreturn
			{contReverseNext, 13},
			{contReverseNext, 12},
			{contReverseNext, 11},
			{contReverseNext, 10},
			{contReverseNext, 9},
			{contReverseNext, 27},

			{contContinueToBreakpoint, 18}, // go to panic call
			{contNext, 6},                  // panic so the deferred call happens
			{contReverseNext, 18},
			{contReverseNext, 17},
			{contReverseNext, 16},
			{contReverseNext, 15},
			{contReverseNext, 23},
			{contReverseNext, 22},
			{contReverseNext, 21},
			{contReverseNext, 28},
		})
	}
}

func TestIssue1925(t *testing.T) {
	// Calling a function should not leave cached goroutine information in an
	// inconsistent state.
	// In particular the stepInstructionOut function called at the end of a
	// 'call' procedure should clean the G cache like every other function
	// altering the state of the target process.
	protest.MustSupportFunctionCalls(t, testBackend)
	withTestProcess("testvariables2", t, func(p *proc.Target, fixture protest.Fixture) {
		grp := proc.NewGroup(p)
		assertNoError(grp.Continue(), t, "Continue()")
		assertNoError(proc.EvalExpressionWithCalls(grp, p.SelectedGoroutine(), "afunc(2)", normalLoadConfig, true), t, "Call")
		t.Logf("%v\n", p.SelectedGoroutine().CurrentLoc)
		if loc := p.SelectedGoroutine().CurrentLoc; loc.File != fixture.Source {
			t.Errorf("wrong location for selected goroutine after call: %s:%d", loc.File, loc.Line)
		}
	})
}

func TestStepIntoWrapperForEmbeddedPointer(t *testing.T) {
	skipOn(t, "N/A", "linux", "386", "pie") // skipping wrappers doesn't work on linux/386/PIE due to the use of get_pc_thunk
	// Under some circumstances (when using an interface to call a method on an
	// embedded field, see _fixtures/ifaceembcall.go) the compiler will
	// autogenerate a wrapper function that uses a tail call (i.e. it ends in
	// an unconditional jump instruction to a different function).
	// Delve should be able to step into this tail call.
	testseq2(t, "ifaceembcall", "", []seqTest{
		{contContinue, 28}, // main.main, the line calling iface.PtrReceiver()
		{contStep, 18},     // main.(*A).PtrReceiver
		{contStep, 19},
		{contStepout, 28},
		{contContinueToBreakpoint, 29}, // main.main, the line calling iface.NonPtrReceiver()
		{contStep, 22},                 // main.(A).NonPtrReceiver
		{contStep, 23},
		{contStepout, 29}})

	// same test but with next instead of stepout
	if goversion.VersionAfterOrEqual(runtime.Version(), 1, 14) && runtime.GOARCH != "386" && !goversion.VersionAfterOrEqualRev(runtime.Version(), 1, 15, 4) {
		// Line numbers generated for versions 1.14 through 1.15.3 on any system except linux/386
		testseq2(t, "ifaceembcall", "", []seqTest{
			{contContinue, 28}, // main.main, the line calling iface.PtrReceiver()
			{contStep, 18},     // main.(*A).PtrReceiver
			{contNext, 19},
			{contNext, 19},
			{contNext, 28},
			{contContinueToBreakpoint, 29}, // main.main, the line calling iface.NonPtrReceiver()
			{contStep, 22},
			{contNext, 23},
			{contNext, 23},
			{contNext, 29}})
	} else {
		testseq2(t, "ifaceembcall", "", []seqTest{
			{contContinue, 28}, // main.main, the line calling iface.PtrReceiver()
			{contStep, 18},     // main.(*A).PtrReceiver
			{contNext, 19},
			{contNext, 28},
			{contContinueToBreakpoint, 29}, // main.main, the line calling iface.NonPtrReceiver()
			{contStep, 22},
			{contNext, 23},
			{contNext, 29}})

	}
}

func TestRefreshCurThreadSelGAfterContinueOnceError(t *testing.T) {
	// Issue #2078:
	// Tests that on macOS/lldb the current thread/selected goroutine are
	// refreshed after ContinueOnce returns an error due to a segmentation
	// fault.

	skipUnlessOn(t, "N/A", "darwin", "lldb")

	withTestProcess("issue2078", t, func(p *proc.Target, fixture protest.Fixture) {
		setFileBreakpoint(p, t, fixture.Source, 4)
		assertNoError(p.Continue(), t, "Continue() (first)")
		if p.Continue() == nil {
			t.Fatalf("Second continue did not return an error")
		}
		g := p.SelectedGoroutine()
		if g.CurrentLoc.Line != 9 {
			t.Fatalf("wrong current location %s:%d (expected :9)", g.CurrentLoc.File, g.CurrentLoc.Line)
		}
	})
}

func TestStepoutOneliner(t *testing.T) {
	// The heuristic detecting autogenerated wrappers when stepping out should
	// not skip oneliner functions.
	withTestProcess("issue2086", t, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue()")
		assertLineNumber(p, t, 15, "after first continue")
		assertNoError(p.StepOut(), t, "StepOut()")
		if fn := p.BinInfo().PCToFunc(currentPC(p, t)); fn == nil || fn.Name != "main.T.m" {
			t.Fatalf("wrong function after stepout %#v", fn)
		}
		assertNoError(p.StepOut(), t, "second StepOut()")
		if fn := p.BinInfo().PCToFunc(currentPC(p, t)); fn == nil || fn.Name != "main.main" {
			t.Fatalf("wrong fnuction after second stepout %#v", fn)
		}
	})
}

func TestRequestManualStopWhileStopped(t *testing.T) {
	// Requesting a manual stop while stopped shouldn't cause problems (issue #2138).
	withTestProcess("issue2138", t, func(p *proc.Target, fixture protest.Fixture) {
		grp := proc.NewGroup(p)
		resumed := make(chan struct{})
		setFileBreakpoint(p, t, fixture.Source, 8)
		assertNoError(grp.Continue(), t, "Continue() 1")
		grp.ResumeNotify(resumed)
		go func() {
			<-resumed
			time.Sleep(1 * time.Second)
			grp.RequestManualStop()
		}()
		t.Logf("at time.Sleep call")
		assertNoError(grp.Continue(), t, "Continue() 2")
		t.Logf("manually stopped")
		grp.RequestManualStop()
		grp.RequestManualStop()
		grp.RequestManualStop()

		resumed = make(chan struct{})
		grp.ResumeNotify(resumed)
		go func() {
			<-resumed
			time.Sleep(1 * time.Second)
			grp.RequestManualStop()
		}()
		t.Logf("resuming sleep")
		assertNoError(grp.Continue(), t, "Continue() 3")
		t.Logf("done")
	})
}

func TestStepOutPreservesGoroutine(t *testing.T) {
	// Checks that StepOut preserves the currently selected goroutine.
	rand.Seed(time.Now().Unix())
	withTestProcess("issue2113", t, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue()")

		logState := func() {
			g := p.SelectedGoroutine()
			var goid int64 = -42
			if g != nil {
				goid = g.ID
			}
			pc := currentPC(p, t)
			f, l, fn := p.BinInfo().PCToLine(pc)
			var fnname string = "???"
			if fn != nil {
				fnname = fn.Name
			}
			t.Logf("goroutine %d at %s:%d in %s", goid, f, l, fnname)
		}

		logState()

		gs, _, err := proc.GoroutinesInfo(p, 0, 0)
		assertNoError(err, t, "GoroutinesInfo")
		candg := []*proc.G{}
		bestg := []*proc.G{}
		for _, g := range gs {
			frames, err := g.Stacktrace(20, 0)
			assertNoError(err, t, "Stacktrace")
			for _, frame := range frames {
				if frame.Call.Fn != nil && frame.Call.Fn.Name == "main.coroutine" {
					candg = append(candg, g)
					if g.Thread != nil && frames[0].Call.Fn != nil && strings.HasPrefix(frames[0].Call.Fn.Name, "runtime.") {
						bestg = append(bestg, g)
					}
					break
				}
			}
		}
		var pickg *proc.G
		if len(bestg) > 0 {
			pickg = bestg[rand.Intn(len(bestg))]
			t.Logf("selected goroutine %d (best)\n", pickg.ID)
		} else {
			pickg = candg[rand.Intn(len(candg))]
			t.Logf("selected goroutine %d\n", pickg.ID)

		}
		goid := pickg.ID
		assertNoError(p.SwitchGoroutine(pickg), t, "SwitchGoroutine")

		logState()

		err = p.StepOut()
		if err != nil {
			_, isexited := err.(proc.ErrProcessExited)
			if !isexited {
				assertNoError(err, t, "StepOut()")
			} else {
				return
			}
		}

		logState()

		g2 := p.SelectedGoroutine()
		if g2 == nil {
			t.Fatalf("no selected goroutine after stepout")
		} else if g2.ID != goid {
			t.Fatalf("unexpected selected goroutine %d", g2.ID)
		}
	})
}

func TestIssue2319(t *testing.T) {
	// Check to make sure we don't crash on startup when the target is
	// a binary with a mix of DWARF-5 C++ compilation units and
	// DWARF-4 Go compilation units.

	// Require CGO, since we need to use the external linker for this test.
	protest.MustHaveCgo(t)

	// The test fixture uses linux/amd64 assembly and a *.syso file
	// that is linux/amd64, so skip for other architectures.
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skipf("skipping since not linux/amd64")
	}

	// Skip unless on 1.14 or later. The test fixture uses a *.syso
	// file, which in 1.13 is not loaded unless we're in internal
	// linking mode (we need external linking here).
	if !goversion.VersionAfterOrEqual(runtime.Version(), 1, 14) {
		t.Skip("test contains fixture that is specific to go 1.14+")
	}

	fixture := protest.BuildFixture("issue2319/", protest.BuildModeExternalLinker)

	// Load up the binary and make sure there are no crashes.
	bi := proc.NewBinaryInfo("linux", "amd64")
	assertNoError(bi.LoadBinaryInfo(fixture.Path, 0, nil), t, "LoadBinaryInfo")
}

func TestDump(t *testing.T) {
	if runtime.GOOS == "freebsd" || (runtime.GOOS == "darwin" && testBackend == "native") || (runtime.GOOS == "windows" && runtime.GOARCH != "amd64") {
		t.Skip("not supported")
	}

	convertRegisters := func(arch *proc.Arch, dregs op.DwarfRegisters) string {
		dregs.Reg(^uint64(0))
		buf := new(bytes.Buffer)
		for i := 0; i < dregs.CurrentSize(); i++ {
			reg := dregs.Reg(uint64(i))
			if reg == nil {
				continue
			}
			name, _, repr := arch.DwarfRegisterToString(i, reg)
			fmt.Fprintf(buf, " %s=%s", name, repr)
		}
		return buf.String()
	}

	convertThread := func(thread proc.Thread) string {
		regs, err := thread.Registers()
		assertNoError(err, t, fmt.Sprintf("Thread registers %d", thread.ThreadID()))
		arch := thread.BinInfo().Arch
		dregs := arch.RegistersToDwarfRegisters(0, regs)
		return fmt.Sprintf("%08d %s", thread.ThreadID(), convertRegisters(arch, *dregs))
	}

	convertThreads := func(threads []proc.Thread) []string {
		r := make([]string, len(threads))
		for i := range threads {
			r[i] = convertThread(threads[i])
		}
		sort.Strings(r)
		return r
	}

	convertGoroutine := func(g *proc.G) string {
		threadID := 0
		if g.Thread != nil {
			threadID = g.Thread.ThreadID()
		}
		return fmt.Sprintf("%d pc=%#x sp=%#x bp=%#x lr=%#x gopc=%#x startpc=%#x systemstack=%v thread=%d", g.ID, g.PC, g.SP, g.BP, g.LR, g.GoPC, g.StartPC, g.SystemStack, threadID)
	}

	convertFrame := func(arch *proc.Arch, frame *proc.Stackframe) string {
		return fmt.Sprintf("currentPC=%#x callPC=%#x frameOff=%#x\n", frame.Current.PC, frame.Call.PC, frame.FrameOffset())
	}

	makeDump := func(p *proc.Target, corePath, exePath string, flags proc.DumpFlags) *proc.Target {
		fh, err := os.Create(corePath)
		assertNoError(err, t, "Create()")
		var state proc.DumpState
		p.Dump(fh, flags, &state)
		assertNoError(state.Err, t, "Dump()")
		if state.ThreadsDone != state.ThreadsTotal || state.MemDone != state.MemTotal || !state.AllDone || state.Dumping || state.Canceled {
			t.Fatalf("bad DumpState %#v", &state)
		}
		c, err := core.OpenCore(corePath, exePath, nil)
		assertNoError(err, t, "OpenCore()")
		return c
	}

	testDump := func(p, c *proc.Target) {
		if p.Pid() != c.Pid() {
			t.Errorf("Pid mismatch %x %x", p.Pid(), c.Pid())
		}

		threads := convertThreads(p.ThreadList())
		cthreads := convertThreads(c.ThreadList())

		if len(threads) != len(cthreads) {
			t.Errorf("Thread number mismatch %d %d", len(threads), len(cthreads))
		}

		for i := range threads {
			if threads[i] != cthreads[i] {
				t.Errorf("Thread mismatch\nlive:\t%s\ncore:\t%s", threads[i], cthreads[i])
			}
		}

		gos, _, err := proc.GoroutinesInfo(p, 0, 0)
		assertNoError(err, t, "GoroutinesInfo() - live process")
		cgos, _, err := proc.GoroutinesInfo(c, 0, 0)
		assertNoError(err, t, "GoroutinesInfo() - core dump")

		if len(gos) != len(cgos) {
			t.Errorf("Goroutine number mismatch %d %d", len(gos), len(cgos))
		}

		var scope, cscope *proc.EvalScope

		for i := range gos {
			if convertGoroutine(gos[i]) != convertGoroutine(cgos[i]) {
				t.Errorf("Goroutine mismatch\nlive:\t%s\ncore:\t%s", convertGoroutine(gos[i]), convertGoroutine(cgos[i]))
			}

			frames, err := gos[i].Stacktrace(20, 0)
			assertNoError(err, t, fmt.Sprintf("Stacktrace for goroutine %d - live process", gos[i].ID))
			cframes, err := cgos[i].Stacktrace(20, 0)
			assertNoError(err, t, fmt.Sprintf("Stacktrace for goroutine %d - core dump", gos[i].ID))

			if len(frames) != len(cframes) {
				t.Errorf("Frame number mismatch for goroutine %d: %d %d", gos[i].ID, len(frames), len(cframes))
			}

			for j := range frames {
				if convertFrame(p.BinInfo().Arch, &frames[j]) != convertFrame(p.BinInfo().Arch, &cframes[j]) {
					t.Errorf("Frame mismatch %d.%d\nlive:\t%s\ncore:\t%s", gos[i].ID, j, convertFrame(p.BinInfo().Arch, &frames[j]), convertFrame(p.BinInfo().Arch, &cframes[j]))
				}
				if frames[j].Call.Fn != nil && frames[j].Call.Fn.Name == "main.main" {
					scope = proc.FrameToScope(p, p.Memory(), gos[i], frames[j:]...)
					cscope = proc.FrameToScope(c, c.Memory(), cgos[i], cframes[j:]...)
				}
			}
		}

		vars, err := scope.LocalVariables(normalLoadConfig)
		assertNoError(err, t, "LocalVariables - live process")
		cvars, err := cscope.LocalVariables(normalLoadConfig)
		assertNoError(err, t, "LocalVariables - core dump")

		if len(vars) != len(cvars) {
			t.Errorf("Variable number mismatch %d %d", len(vars), len(cvars))
		}

		for i := range vars {
			varstr := vars[i].Name + "=" + api.ConvertVar(vars[i]).SinglelineString()
			cvarstr := cvars[i].Name + "=" + api.ConvertVar(cvars[i]).SinglelineString()
			if strings.Contains(varstr, "(unreadable") {
				// errors reading from unmapped memory differ between live process and core
				continue
			}
			if varstr != cvarstr {
				t.Errorf("Variable mismatch %s %s", varstr, cvarstr)
			}
		}
	}

	withTestProcess("testvariables2", t, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue()")
		corePath := filepath.Join(fixture.BuildDir, "coredump")
		corePathPlatIndep := filepath.Join(fixture.BuildDir, "coredump-indep")

		t.Logf("testing normal dump")

		c := makeDump(p, corePath, fixture.Path, 0)
		defer os.Remove(corePath)
		testDump(p, c)

		if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
			// No reason to do this test on other goos/goarch because they use the
			// platform-independent format anyway.
			t.Logf("testing platform-independent dump")
			c2 := makeDump(p, corePathPlatIndep, fixture.Path, proc.DumpPlatformIndependent)
			defer os.Remove(corePathPlatIndep)
			testDump(p, c2)
		}
	})
}

func TestCompositeMemoryWrite(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("only valid on amd64")
	}
	skipOn(t, "not implemented", "freebsd")
	withTestProcess("fputest/", t, func(p *proc.Target, fixture protest.Fixture) {
		getregs := func() (pc, rax, xmm1 uint64) {
			regs, err := p.CurrentThread().Registers()
			assertNoError(err, t, "Registers")
			fmtregs, err := regs.Slice(true)
			assertNoError(err, t, "register slice")

			var xmm1buf []byte

			for _, reg := range fmtregs {
				switch strings.ToLower(reg.Name) {
				case "rax":
					rax = reg.Reg.Uint64Val
				case "xmm1":
					xmm1buf = reg.Reg.Bytes
				}
			}

			xmm1 = binary.LittleEndian.Uint64(xmm1buf[:8])

			return regs.PC(), rax, xmm1
		}

		const fakeAddress = 0xbeef0000

		getmem := func(mem proc.MemoryReader) uint64 {
			buf := make([]byte, 8)
			_, err := mem.ReadMemory(buf, fakeAddress)
			assertNoError(err, t, "ReadMemory")
			return binary.LittleEndian.Uint64(buf)
		}

		assertNoError(p.Continue(), t, "Continue()")
		oldPc, oldRax, oldXmm1 := getregs()
		t.Logf("PC %#x AX %#x XMM1 %#x", oldPc, oldRax, oldXmm1)

		memRax, err := proc.NewCompositeMemory(p, []op.Piece{{Size: 0, Val: 0, Kind: op.RegPiece}}, fakeAddress)
		assertNoError(err, t, "NewCompositeMemory (rax)")
		memXmm1, err := proc.NewCompositeMemory(p, []op.Piece{{Size: 0, Val: 18, Kind: op.RegPiece}}, fakeAddress)
		assertNoError(err, t, "NewCompositeMemory (xmm1)")

		if memRax := getmem(memRax); memRax != oldRax {
			t.Errorf("reading rax memory, expected %#x got %#x", oldRax, memRax)
		}
		if memXmm1 := getmem(memXmm1); memXmm1 != oldXmm1 {
			t.Errorf("reading xmm1 memory, expected %#x got %#x", oldXmm1, memXmm1)
		}

		_, err = memRax.WriteMemory(0xbeef0000, []byte{0xef, 0xbe, 0x0d, 0xf0, 0xef, 0xbe, 0x0d, 0xf0})
		assertNoError(err, t, "WriteMemory (rax)")
		_, err = memXmm1.WriteMemory(0xbeef0000, []byte{0xef, 0xbe, 0x0d, 0xf0, 0xef, 0xbe, 0x0d, 0xf0})
		assertNoError(err, t, "WriteMemory (xmm1)")

		newPc, newRax, newXmm1 := getregs()
		t.Logf("PC %#x AX %#x XMM1 %#x", newPc, newRax, newXmm1)

		const tgt = 0xf00dbeeff00dbeef
		if newRax != tgt {
			t.Errorf("reading rax register, expected %#x, got %#x", uint64(tgt), newRax)
		}
		if newXmm1 != tgt {
			t.Errorf("reading xmm1 register, expected %#x, got %#x", uint64(tgt), newXmm1)
		}
	})
}

func TestVariablesWithExternalLinking(t *testing.T) {
	protest.MustHaveCgo(t)
	// Tests that macOSDebugFrameBugWorkaround works.
	// See:
	//  https://github.com/golang/go/issues/25841
	//  https://github.com/go-delve/delve/issues/2346
	withTestProcessArgs("testvariables2", t, ".", []string{}, protest.BuildModeExternalLinker, func(p *proc.Target, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue()")
		str1Var := evalVariable(p, t, "str1")
		if str1Var.Unreadable != nil {
			t.Fatalf("variable str1 is unreadable: %v", str1Var.Unreadable)
		}
		t.Logf("%#v", str1Var)
		if constant.StringVal(str1Var.Value) != "01234567890" {
			t.Fatalf("wrong value for str1: %v", str1Var.Value)
		}
	})
}

func TestWatchpointsBasic(t *testing.T) {
	skipOn(t, "not implemented", "freebsd")
	skipOn(t, "not implemented", "386")
	skipOn(t, "see https://github.com/go-delve/delve/issues/2768", "windows")
	protest.AllowRecording(t)

	position1 := 19
	position5 := 41

	if runtime.GOARCH == "arm64" {
		position1 = 18
		position5 = 40
	}

	withTestProcess("databpeasy", t, func(p *proc.Target, fixture protest.Fixture) {
		setFunctionBreakpoint(p, t, "main.main")
		setFileBreakpoint(p, t, fixture.Source, 21) // Position 2 breakpoint
		setFileBreakpoint(p, t, fixture.Source, 27) // Position 4 breakpoint
		assertNoError(p.Continue(), t, "Continue 0")
		assertLineNumber(p, t, 13, "Continue 0") // Position 0

		scope, err := proc.GoroutineScope(p, p.CurrentThread())
		assertNoError(err, t, "GoroutineScope")

		bp, err := p.SetWatchpoint(0, scope, "globalvar1", proc.WatchWrite, nil)
		assertNoError(err, t, "SetDataBreakpoint(write-only)")

		assertNoError(p.Continue(), t, "Continue 1")
		assertLineNumber(p, t, position1, "Continue 1") // Position 1

		if curbp := p.CurrentThread().Breakpoint().Breakpoint; curbp == nil || (curbp.LogicalID() != bp.LogicalID()) {
			t.Fatal("breakpoint not set")
		}

		assertNoError(p.ClearBreakpoint(bp.Addr), t, "ClearBreakpoint")

		assertNoError(p.Continue(), t, "Continue 2")
		assertLineNumber(p, t, 21, "Continue 2") // Position 2

		_, err = p.SetWatchpoint(0, scope, "globalvar1", proc.WatchWrite|proc.WatchRead, nil)
		assertNoError(err, t, "SetDataBreakpoint(read-write)")

		assertNoError(p.Continue(), t, "Continue 3")
		assertLineNumber(p, t, 22, "Continue 3") // Position 3

		p.ClearBreakpoint(bp.Addr)

		assertNoError(p.Continue(), t, "Continue 4")
		assertLineNumber(p, t, 27, "Continue 4") // Position 4

		t.Logf("setting final breakpoint")
		_, err = p.SetWatchpoint(0, scope, "globalvar1", proc.WatchWrite, nil)
		assertNoError(err, t, "SetDataBreakpoint(write-only, again)")

		assertNoError(p.Continue(), t, "Continue 5")
		assertLineNumber(p, t, position5, "Continue 5") // Position 5
	})
}

func TestWatchpointCounts(t *testing.T) {
	skipOn(t, "not implemented", "freebsd")
	skipOn(t, "not implemented", "386")
	skipOn(t, "see https://github.com/go-delve/delve/issues/2768", "windows")
	protest.AllowRecording(t)

	withTestProcess("databpcountstest", t, func(p *proc.Target, fixture protest.Fixture) {
		setFunctionBreakpoint(p, t, "main.main")
		assertNoError(p.Continue(), t, "Continue 0")

		scope, err := proc.GoroutineScope(p, p.CurrentThread())
		assertNoError(err, t, "GoroutineScope")

		bp, err := p.SetWatchpoint(0, scope, "globalvar1", proc.WatchWrite, nil)
		assertNoError(err, t, "SetWatchpoint(write-only)")

		for {
			if err := p.Continue(); err != nil {
				if _, exited := err.(proc.ErrProcessExited); exited {
					break
				}
				assertNoError(err, t, "Continue()")
			}
		}

		t.Logf("TotalHitCount: %d", bp.Logical.TotalHitCount)
		if bp.Logical.TotalHitCount != 200 {
			t.Fatalf("Wrong TotalHitCount for the breakpoint (%d)", bp.Logical.TotalHitCount)
		}

		if len(bp.Logical.HitCount) != 2 {
			t.Fatalf("Wrong number of goroutines for breakpoint (%d)", len(bp.Logical.HitCount))
		}

		for _, v := range bp.Logical.HitCount {
			if v != 100 {
				t.Fatalf("Wrong HitCount for breakpoint (%v)", bp.Logical.HitCount)
			}
		}
	})
}

func TestManualStopWhileStopped(t *testing.T) {
	// Checks that RequestManualStop sent to a stopped thread does not cause the target process to die.
	withTestProcess("loopprog", t, func(p *proc.Target, fixture protest.Fixture) {
		grp := proc.NewGroup(p)
		asyncCont := func(done chan struct{}) {
			defer close(done)
			err := grp.Continue()
			t.Logf("%v\n", err)
			if err != nil {
				panic(err)
			}
			for _, th := range p.ThreadList() {
				if th.Breakpoint().Breakpoint != nil {
					t.Logf("unexpected stop at breakpoint: %v", th.Breakpoint().Breakpoint)
					panic("unexpected stop at breakpoint")
				}
			}
		}

		const (
			repeatsSlow = 3
			repeatsFast = 5
		)

		for i := 0; i < repeatsSlow; i++ {
			t.Logf("Continue %d (slow)", i)
			done := make(chan struct{})
			go asyncCont(done)
			time.Sleep(1 * time.Second)
			grp.RequestManualStop()
			time.Sleep(1 * time.Second)
			grp.RequestManualStop()
			time.Sleep(1 * time.Second)
			<-done
		}
		for i := 0; i < repeatsFast; i++ {
			t.Logf("Continue %d (fast)", i)
			rch := make(chan struct{})
			done := make(chan struct{})
			grp.ResumeNotify(rch)
			go asyncCont(done)
			<-rch
			grp.RequestManualStop()
			grp.RequestManualStop()
			<-done
		}
	})
}

func TestDwrapStartLocation(t *testing.T) {
	// Tests that the start location of a goroutine is unwrapped in Go 1.17 and later.
	withTestProcess("goroutinestackprog", t, func(p *proc.Target, fixture protest.Fixture) {
		setFunctionBreakpoint(p, t, "main.stacktraceme")
		assertNoError(p.Continue(), t, "Continue()")
		gs, _, err := proc.GoroutinesInfo(p, 0, 0)
		assertNoError(err, t, "GoroutinesInfo")
		found := false
		for _, g := range gs {
			startLoc := g.StartLoc(p)
			if startLoc.Fn == nil {
				continue
			}
			t.Logf("%#v\n", startLoc.Fn.Name)
			if startLoc.Fn.Name == "main.agoroutine" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("could not find any goroutine with a start location of main.agoroutine")
		}
	})
}

func TestWatchpointStack(t *testing.T) {
	skipOn(t, "not implemented", "freebsd")
	skipOn(t, "not implemented", "386")
	skipOn(t, "see https://github.com/go-delve/delve/issues/2768", "windows")
	protest.AllowRecording(t)

	position1 := 17

	if runtime.GOARCH == "arm64" {
		position1 = 16
	}

	withTestProcess("databpstack", t, func(p *proc.Target, fixture protest.Fixture) {
		setFileBreakpoint(p, t, fixture.Source, 11) // Position 0 breakpoint
		clearlen := len(p.Breakpoints().M)

		assertNoError(p.Continue(), t, "Continue 0")
		assertLineNumber(p, t, 11, "Continue 0") // Position 0

		scope, err := proc.GoroutineScope(p, p.CurrentThread())
		assertNoError(err, t, "GoroutineScope")

		_, err = p.SetWatchpoint(0, scope, "w", proc.WatchWrite, nil)
		assertNoError(err, t, "SetDataBreakpoint(write-only)")

		watchbpnum := 3
		if recorded, _ := p.Recorded(); recorded {
			watchbpnum = 4
		}

		if len(p.Breakpoints().M) != clearlen+watchbpnum {
			// want 1 watchpoint, 1 stack resize breakpoint, 1 out of scope sentinel (2 if recorded)
			t.Errorf("wrong number of breakpoints after setting watchpoint: %d", len(p.Breakpoints().M)-clearlen)
		}

		var retaddr uint64
		for _, bp := range p.Breakpoints().M {
			for _, breaklet := range bp.Breaklets {
				if breaklet.Kind&proc.WatchOutOfScopeBreakpoint != 0 {
					retaddr = bp.Addr
					break
				}
			}
		}

		// Note: for recorded processes retaddr will not always be the return
		// address, ~50% of the times it will be the address of the CALL
		// instruction preceding the return address, this does not matter for this
		// test.

		_, err = p.SetBreakpoint(0, retaddr, proc.UserBreakpoint, nil)
		assertNoError(err, t, "SetBreakpoint")

		if len(p.Breakpoints().M) != clearlen+watchbpnum {
			// want 1 watchpoint, 1 stack resize breakpoint, 1 out of scope sentinel (which is also a user breakpoint) (and another out of scope sentinel if recorded)
			t.Errorf("wrong number of breakpoints after setting watchpoint: %d", len(p.Breakpoints().M)-clearlen)
		}

		assertNoError(p.Continue(), t, "Continue 1")
		assertLineNumber(p, t, position1, "Continue 1") // Position 1

		assertNoError(p.Continue(), t, "Continue 2")
		t.Logf("%#v", p.CurrentThread().Breakpoint().Breakpoint)
		assertLineNumber(p, t, 24, "Continue 2") // Position 2 (watchpoint gone out of scope)

		if len(p.Breakpoints().M) != clearlen+1 {
			// want 1 user breakpoint set at retaddr
			t.Errorf("wrong number of breakpoints after watchpoint goes out of scope: %d", len(p.Breakpoints().M)-clearlen)
		}

		if len(p.Breakpoints().WatchOutOfScope) != 1 {
			t.Errorf("wrong number of out-of-scope watchpoints after watchpoint goes out of scope: %d", len(p.Breakpoints().WatchOutOfScope))
		}

		err = p.ClearBreakpoint(retaddr)
		assertNoError(err, t, "ClearBreakpoint")

		if len(p.Breakpoints().M) != clearlen {
			// want 1 user breakpoint set at retaddr
			t.Errorf("wrong number of breakpoints after removing user breakpoint: %d", len(p.Breakpoints().M)-clearlen)
		}
	})
}

func TestWatchpointStackBackwardsOutOfScope(t *testing.T) {
	skipUnlessOn(t, "only for recorded targets", "rr")
	protest.AllowRecording(t)

	withTestProcess("databpstack", t, func(p *proc.Target, fixture protest.Fixture) {
		setFileBreakpoint(p, t, fixture.Source, 11) // Position 0 breakpoint
		clearlen := len(p.Breakpoints().M)

		assertNoError(p.Continue(), t, "Continue 0")
		assertLineNumber(p, t, 11, "Continue 0") // Position 0

		scope, err := proc.GoroutineScope(p, p.CurrentThread())
		assertNoError(err, t, "GoroutineScope")

		_, err = p.SetWatchpoint(0, scope, "w", proc.WatchWrite, nil)
		assertNoError(err, t, "SetDataBreakpoint(write-only)")

		assertNoError(p.Continue(), t, "Continue 1")
		assertLineNumber(p, t, 17, "Continue 1") // Position 1

		p.ChangeDirection(proc.Backward)

		assertNoError(p.Continue(), t, "Continue 2")
		t.Logf("%#v", p.CurrentThread().Breakpoint().Breakpoint)
		assertLineNumber(p, t, 16, "Continue 2") // Position 1 again (because of inverted movement)

		assertNoError(p.Continue(), t, "Continue 3")
		t.Logf("%#v", p.CurrentThread().Breakpoint().Breakpoint)
		assertLineNumber(p, t, 11, "Continue 3") // Position 0 (breakpoint 1 hit)

		assertNoError(p.Continue(), t, "Continue 4")
		t.Logf("%#v", p.CurrentThread().Breakpoint().Breakpoint)
		assertLineNumber(p, t, 23, "Continue 4") // Position 2 (watchpoint gone out of scope)

		if len(p.Breakpoints().M) != clearlen {
			t.Errorf("wrong number of breakpoints after watchpoint goes out of scope: %d", len(p.Breakpoints().M)-clearlen)
		}

		if len(p.Breakpoints().WatchOutOfScope) != 1 {
			t.Errorf("wrong number of out-of-scope watchpoints after watchpoint goes out of scope: %d", len(p.Breakpoints().WatchOutOfScope))
		}

		if len(p.Breakpoints().M) != clearlen {
			// want 1 user breakpoint set at retaddr
			t.Errorf("wrong number of breakpoints after removing user breakpoint: %d", len(p.Breakpoints().M)-clearlen)
		}
	})
}

func TestSetOnFunctions(t *testing.T) {
	// The set command between function variables should fail with an error
	// Issue #2691
	withTestProcess("goroutinestackprog", t, func(p *proc.Target, fixture protest.Fixture) {
		setFunctionBreakpoint(p, t, "main.main")
		assertNoError(p.Continue(), t, "Continue()")
		scope, err := proc.GoroutineScope(p, p.CurrentThread())
		assertNoError(err, t, "GoroutineScope")
		err = scope.SetVariable("main.func1", "main.func2")
		if err == nil {
			t.Fatal("expected error when assigning between function variables")
		}
	})
}

func TestSetYMMRegister(t *testing.T) {
	skipUnlessOn(t, "N/A", "darwin", "amd64")
	// Checks that setting a XMM register works. This checks that the
	// workaround for a bug in debugserver works.
	// See issue #2767.
	withTestProcess("setymmreg/", t, func(p *proc.Target, fixture protest.Fixture) {
		setFunctionBreakpoint(p, t, "main.asmFunc")
		assertNoError(p.Continue(), t, "Continue()")

		getReg := func(pos string) *op.DwarfRegister {
			regs := getRegisters(p, t)

			arch := p.BinInfo().Arch
			dregs := arch.RegistersToDwarfRegisters(0, regs)

			r := dregs.Reg(regnum.AMD64_XMM0)
			t.Logf("%s: %#v", pos, r)
			return r
		}

		getReg("before")

		p.CurrentThread().SetReg(regnum.AMD64_XMM0, op.DwarfRegisterFromBytes([]byte{
			0x44, 0x44, 0x44, 0x44, 0x44, 0x44, 0x44, 0x44,
			0x44, 0x44, 0x44, 0x44, 0x44, 0x44, 0x44, 0x44,
			0x44, 0x44, 0x44, 0x44, 0x44, 0x44, 0x44, 0x44,
			0x44, 0x44, 0x44, 0x44, 0x44, 0x44, 0x44, 0x44}))
		assertNoError(p.CurrentThread().StepInstruction(), t, "SetpInstruction")

		xmm0 := getReg("after")

		for i := range xmm0.Bytes {
			if xmm0.Bytes[i] != 0x44 {
				t.Fatalf("wrong register value")
			}
		}
	})
}

func TestNilPtrDerefInBreakInstr(t *testing.T) {
	// Checks that having a breakpoint on the exact instruction that causes a
	// nil pointer dereference does not cause problems.

	var asmfile string
	switch runtime.GOARCH {
	case "amd64":
		asmfile = "main_amd64.s"
	case "arm64":
		asmfile = "main_arm64.s"
	case "386":
		asmfile = "main_386.s"
	default:
		t.Fatalf("assembly file for %s not provided", runtime.GOARCH)
	}

	withTestProcess("asmnilptr/", t, func(p *proc.Target, fixture protest.Fixture) {
		f := filepath.Join(fixture.BuildDir, asmfile)
		f = strings.Replace(f, "\\", "/", -1)
		setFileBreakpoint(p, t, f, 5)
		t.Logf("first continue")
		assertNoError(p.Continue(), t, "Continue()")
		t.Logf("second continue")
		err := p.Continue()
		if runtime.GOOS == "darwin" && err != nil && err.Error() == "bad access" {
			// this is also ok
			return
		}
		t.Logf("third continue")
		assertNoError(err, t, "Continue()")
		bp := p.CurrentThread().Breakpoint()
		if bp != nil {
			t.Logf("%#v\n", bp.Breakpoint)
		}
		if bp == nil || (bp.Logical.Name != proc.UnrecoveredPanic) {
			t.Fatalf("no breakpoint hit or wrong breakpoint hit: %#v", bp)
		}
	})
}

func TestStepIntoAutogeneratedSkip(t *testing.T) {
	// Tests that autogenerated functions are skipped with the new naming
	// scheme for autogenerated functions (issue #2948).
	withTestProcess("stepintobug", t, func(p *proc.Target, fixture protest.Fixture) {
		setFileBreakpoint(p, t, fixture.Source, 9)
		assertNoError(p.Continue(), t, "Continue()")
		assertNoError(p.Step(), t, "Step")
		assertLineNumber(p, t, 12, "After step")
	})
}

func TestCallInjectionFlagCorruption(t *testing.T) {
	// debugCallV2 has a bug in amd64 where its tail corrupts the FLAGS register by running an ADD instruction.
	// Since this problem exists in many versions of Go, instead of fixing
	// debugCallV2, we work around this problem by restoring FLAGS, one extra
	// time, after stepping out of debugCallV2.
	// Fixes issue https://github.com/go-delve/delve/issues/2985
	skipUnlessOn(t, "not relevant", "amd64")
	protest.MustSupportFunctionCalls(t, testBackend)

	withTestProcessArgs("badflags", t, ".", []string{"0"}, 0, func(p *proc.Target, fixture protest.Fixture) {
		mainfn := p.BinInfo().LookupFunc["main.main"]

		grp := proc.NewGroup(p)

		// Find JNZ instruction on line :14
		var addr uint64
		text, err := proc.Disassemble(p.Memory(), nil, p.Breakpoints(), p.BinInfo(), mainfn.Entry, mainfn.End)
		assertNoError(err, t, "Disassemble")
		for _, instr := range text {
			if instr.Loc.Line != 14 {
				continue
			}
			if proc.IsJNZ(instr.Inst) {
				addr = instr.Loc.PC
			}
		}
		if addr == 0 {
			t.Fatalf("Could not find JNZ instruction at line :14")
		}

		// Create breakpoint
		_, err = p.SetBreakpoint(0, addr, proc.UserBreakpoint, nil)
		assertNoError(err, t, "SetBreakpoint")

		// Continue to breakpoint
		assertNoError(grp.Continue(), t, "Continue()")
		assertLineNumber(p, t, 14, "expected line :14")

		// Save RFLAGS register
		rflagsBeforeCall := p.BinInfo().Arch.RegistersToDwarfRegisters(0, getRegisters(p, t)).Uint64Val(regnum.AMD64_Rflags)
		t.Logf("rflags before = %#x", rflagsBeforeCall)

		// Inject call to main.g()
		assertNoError(proc.EvalExpressionWithCalls(grp, p.SelectedGoroutine(), "g()", normalLoadConfig, true), t, "Call")

		// Check RFLAGS register after the call
		rflagsAfterCall := p.BinInfo().Arch.RegistersToDwarfRegisters(0, getRegisters(p, t)).Uint64Val(regnum.AMD64_Rflags)
		t.Logf("rflags after = %#x", rflagsAfterCall)

		if rflagsBeforeCall != rflagsAfterCall {
			t.Errorf("mismatched rflags value")
		}

		// Single step and check where we end up
		assertNoError(grp.Step(), t, "Step()")
		assertLineNumber(p, t, 17, "expected line :17") // since we passed "0" as argument we should be going into the false branch at line :17
	})
}

func TestGnuDebuglink(t *testing.T) {
	skipUnlessOn(t, "N/A", "linux")

	// build math.go and make a copy of the executable
	fixture := protest.BuildFixture("math", 0)
	buf, err := ioutil.ReadFile(fixture.Path)
	assertNoError(err, t, "ReadFile")
	debuglinkPath := fixture.Path + "-gnu_debuglink"
	assertNoError(ioutil.WriteFile(debuglinkPath, buf, 0666), t, "WriteFile")
	defer os.Remove(debuglinkPath)

	run := func(exe string, args ...string) {
		cmd := exec.Command(exe, args...)
		out, err := cmd.CombinedOutput()
		assertNoError(err, t, fmt.Sprintf("%s %q: %s", cmd, strings.Join(args, " "), out))
	}

	// convert the executable copy to use .gnu_debuglink
	debuglinkDwoPath := debuglinkPath + ".dwo"
	run("objcopy", "--only-keep-debug", debuglinkPath, debuglinkDwoPath)
	defer os.Remove(debuglinkDwoPath)
	run("objcopy", "--strip-debug", debuglinkPath)
	run("objcopy", "--add-gnu-debuglink="+debuglinkDwoPath, debuglinkPath)

	// open original executable
	normalBinInfo := proc.NewBinaryInfo(runtime.GOOS, runtime.GOARCH)
	assertNoError(normalBinInfo.LoadBinaryInfo(fixture.Path, 0, []string{"/debugdir"}), t, "LoadBinaryInfo (normal exe)")

	// open .gnu_debuglink executable
	debuglinkBinInfo := proc.NewBinaryInfo(runtime.GOOS, runtime.GOARCH)
	assertNoError(debuglinkBinInfo.LoadBinaryInfo(debuglinkPath, 0, []string{"/debugdir"}), t, "LoadBinaryInfo (gnu_debuglink exe)")

	if len(normalBinInfo.Functions) != len(debuglinkBinInfo.Functions) {
		t.Fatalf("function list mismatch")
	}

	for i := range normalBinInfo.Functions {
		normalFn := normalBinInfo.Functions[i]
		debuglinkFn := debuglinkBinInfo.Functions[i]
		if normalFn.Entry != debuglinkFn.Entry || normalFn.Name != normalFn.Name {
			t.Fatalf("function definition mismatch")
		}
	}
}

func TestStacktraceExtlinkMac(t *testing.T) {
	// Tests stacktrace for programs built using external linker.
	// See issue #3194
	skipUnlessOn(t, "darwin only", "darwin")
	withTestProcess("issue3194", t, func(p *proc.Target, fixture protest.Fixture) {
		setFunctionBreakpoint(p, t, "main.main")
		assertNoError(p.Continue(), t, "First Continue()")
		frames, err := proc.ThreadStacktrace(p.CurrentThread(), 10)
		assertNoError(err, t, "ThreadStacktrace")
		logStacktrace(t, p, frames)
		if len(frames) < 2 || frames[0].Call.Fn.Name != "main.main" || frames[1].Call.Fn.Name != "runtime.main" {
			t.Fatalf("bad stacktrace")
		}
	})
}
