package proc_test

import (
	"fmt"
	"go/constant"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/go-delve/delve/pkg/goversion"
	"github.com/go-delve/delve/pkg/proc"
	protest "github.com/go-delve/delve/pkg/proc/test"
	"github.com/go-delve/delve/service/api"
)

type nextTest struct {
	begin, end int
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
	contNothing
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
	t.Helper()
	withTestProcessArgs(program, t, wd, args, buildFlags, func(p *proc.Target, grp *proc.TargetGroup, fixture protest.Fixture) {
		checkBreakpointClear := true
		var bp *proc.Breakpoint
		if initialLocation != "" {
			bp = setFunctionBreakpoint(p, t, initialLocation)
		} else if testcases[0].cf == contContinue {
			bp = setFileBreakpoint(p, t, fixture.Source, testcases[0].pos.(int))
		} else if testcases[0].cf == contNothing {
			// Do nothing
			checkBreakpointClear = false
		} else {
			panic("testseq2 can not set initial breakpoint")
		}
		if traceTestseq2 {
			t.Logf("initial breakpoint %v", bp)
		}

		testseq2intl(t, fixture, grp, p, bp, testcases)

		if countBreakpoints(p) != 0 && checkBreakpointClear {
			t.Fatal("Not all breakpoints were cleaned up", len(p.Breakpoints().M))
		}
	})
}

func testseq2intl(t *testing.T, fixture protest.Fixture, grp *proc.TargetGroup, p *proc.Target, bp *proc.Breakpoint, testcases []seqTest) {
	f, ln := currentLineNumber(p, t)
	for i, tc := range testcases {
		switch tc.cf {
		case contNext:
			if traceTestseq2 {
				t.Log("next")
			}
			assertNoError(grp.Next(), t, "Next() returned an error")
		case contStep:
			if traceTestseq2 {
				t.Log("step")
			}
			assertNoError(grp.Step(), t, "Step() returned an error")
		case contStepout:
			if traceTestseq2 {
				t.Log("stepout")
			}
			assertNoError(grp.StepOut(), t, "StepOut() returned an error")
		case contContinue:
			if traceTestseq2 {
				t.Log("continue")
			}
			assertNoError(grp.Continue(), t, "Continue() returned an error")
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
			assertNoError(grp.ChangeDirection(proc.Backward), t, "direction switch")
			assertNoError(grp.Next(), t, "reverse Next() returned an error")
			assertNoError(grp.ChangeDirection(proc.Forward), t, "direction switch")
		case contReverseStep:
			if traceTestseq2 {
				t.Log("reverse-step")
			}
			assertNoError(grp.ChangeDirection(proc.Backward), t, "direction switch")
			assertNoError(grp.Step(), t, "reverse Step() returned an error")
			assertNoError(grp.ChangeDirection(proc.Forward), t, "direction switch")
		case contReverseStepout:
			if traceTestseq2 {
				t.Log("reverse-stepout")
			}
			assertNoError(grp.ChangeDirection(proc.Backward), t, "direction switch")
			assertNoError(grp.StepOut(), t, "reverse StepOut() returned an error")
			assertNoError(grp.ChangeDirection(proc.Forward), t, "direction switch")
		case contContinueToBreakpoint:
			bp := setFileBreakpoint(p, t, fixture.Source, tc.pos.(int))
			if traceTestseq2 {
				t.Log("continue")
			}
			assertNoError(grp.Continue(), t, "Continue() returned an error")
			err := p.ClearBreakpoint(bp.Addr)
			assertNoError(err, t, "ClearBreakpoint() returned an error")
		case contNothing:
			// do nothing
		}

		if err := p.CurrentThread().Breakpoint().CondError; err != nil {
			t.Logf("breakpoint condition error: %v", err)
		}

		f, ln = currentLineNumber(p, t)
		regs, _ := p.CurrentThread().Registers()
		pc := regs.PC()
		_, _, fn := p.BinInfo().PCToLine(pc)

		if traceTestseq2 {
			fnname := "?"
			if fn != nil {
				fnname = fn.Name
			}
			t.Logf("at %#x (%s) %s:%d", pc, fnname, f, ln)
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
		case func(*proc.Target):
			pos(p)
		case func(*proc.TargetGroup, *proc.Target):
			pos(grp, p)
		default:
			panic(fmt.Errorf("unexpected type %T", pos))
		}
	}
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
	testseq("defercall", contNext, []nextTest{
		{15, 16},
		{16, 17},
		{17, 18},
		{18, 6}}, "main.callAndPanic2", t)
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
		if runtime.GOOS == "linux" && runtime.GOARCH == "ppc64le" && buildMode == "pie" {
			testseq("teststepprog", contStep, []nextTest{
				{9, 10},
				{10, 5},
				{5, 6},
				{6, 7},
				{7, 10},
				{10, 11}}, "", t)
		} else {
			testseq("teststepprog", contStep, []nextTest{
				{9, 10},
				{10, 5},
				{5, 6},
				{6, 7},
				{7, 11}}, "", t)
		}
	}
}

func TestStepReturnAndPanic(t *testing.T) {
	// Tests that Step works correctly when returning from functions
	// and when a deferred function is called when panic'ing.
	testseq("defercall", contStep, []nextTest{
		{17, 6},
		{6, 7},
		{7, 18},
		{18, 6},
		{6, 7}}, "", t)
}

func TestStepDeferReturn(t *testing.T) {
	// Tests that Step works correctly when a deferred function is
	// called during a return.
	testseq("defercall", contStep, []nextTest{
		{11, 6},
		{6, 7},
		{7, 12},
		{12, 13},
		{13, 6},
		{6, 7},
		{7, 13},
		{13, 28}}, "", t)
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
	default:
		panic("too old")
	}
}

func TestInlineStep(t *testing.T) {
	skipOn(t, "broken", "ppc64le")
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
			{contNext, 29},
		})
	} else {
		testseq2(t, "ifaceembcall", "", []seqTest{
			{contContinue, 28}, // main.main, the line calling iface.PtrReceiver()
			{contStep, 18},     // main.(*A).PtrReceiver
			{contNext, 19},
			{contNext, 28},
			{contContinueToBreakpoint, 29}, // main.main, the line calling iface.NonPtrReceiver()
			{contStep, 22},
			{contNext, 23},
			{contNext, 29},
		})
	}
}

func TestNextGenericMethodThroughInterface(t *testing.T) {
	// Tests that autogenerated wrappers for generic methods called through an
	// interface are skipped.

	varcheck := func(p *proc.Target) {
		yvar := evalVariable(p, t, "y")
		yval, _ := constant.Int64Val(yvar.Value)
		if yval != 2 {
			t.Errorf("expected 2 got %#v", yvar.Value)
		}
	}

	if runtime.GOOS == "linux" && runtime.GOARCH == "386" {
		testseq2(t, "genericintoiface", "main.callf", []seqTest{
			{contContinue, 17},
			{contStep, 18},
			{contStep, 10},
			{contNothing, varcheck},
			{contNext, 11},
			{contNext, 19},
		})
	} else {
		testseq2(t, "genericintoiface", "main.callf", []seqTest{
			{contContinue, 17},
			{contStep, 18},
			{contStep, 9},
			{contNext, 10},
			{contNothing, varcheck},
			{contNext, 11},
			{contNext, 19},
		})
	}
}

func TestRangeOverFuncNext(t *testing.T) {
	if !goversion.VersionAfterOrEqual(runtime.Version(), 1, 23) {
		t.Skip("N/A")
	}

	var bp *proc.Breakpoint

	funcBreak := func(t *testing.T, fnname string) seqTest {
		return seqTest{
			contNothing,
			func(p *proc.Target) {
				bp = setFunctionBreakpoint(p, t, fnname)
			}}
	}

	clearBreak := func(t *testing.T) seqTest {
		return seqTest{
			contNothing,
			func(p *proc.Target) {
				assertNoError(p.ClearBreakpoint(bp.Addr), t, "ClearBreakpoint")
			}}
	}

	notAtEntryPoint := func(t *testing.T) seqTest {
		return seqTest{contNothing, func(p *proc.Target) {
			pc := currentPC(p, t)
			fn := p.BinInfo().PCToFunc(pc)
			if pc == fn.Entry {
				t.Fatalf("current PC is entry point")
			}
		}}
	}

	nx := func(n int) seqTest {
		return seqTest{contNext, n}
	}

	assertLocals := func(t *testing.T, varnames ...string) seqTest {
		return seqTest{
			contNothing,
			func(p *proc.Target) {
				scope, err := proc.GoroutineScope(p, p.CurrentThread())
				assertNoError(err, t, "GoroutineScope")
				vars, err := scope.Locals(0, "")
				assertNoError(err, t, "Locals")

				gotnames := make([]string, len(vars))
				for i := range vars {
					gotnames[i] = vars[i].Name
				}

				ok := true
				if len(vars) != len(varnames) {
					ok = false
				} else {
					for i := range vars {
						if vars[i].Name != varnames[i] {
							ok = false
							break
						}
					}
				}
				if !ok {
					t.Errorf("Wrong variable names, expected %q, got %q", varnames, gotnames)
				}
			},
		}
	}

	assertEval := func(t *testing.T, exprvals ...string) seqTest {
		return seqTest{
			contNothing,
			func(p *proc.Target) {
				scope, err := proc.GoroutineScope(p, p.CurrentThread())
				assertNoError(err, t, "GoroutineScope")
				for i := 0; i < len(exprvals); i += 2 {
					expr, tgt := exprvals[i], exprvals[i+1]
					v, err := scope.EvalExpression(expr, normalLoadConfig)
					if err != nil {
						t.Errorf("Could not evaluate %q: %v", expr, err)
					} else {
						out := api.ConvertVar(v).SinglelineString()
						if out != tgt {
							t.Errorf("Wrong value for %q, got %q expected %q", expr, out, tgt)
						}
					}
				}
			},
		}
	}

	assertFunc := func(t *testing.T, fname string) seqTest {
		return seqTest{
			contNothing,
			func(p *proc.Target) {
				pc := currentPC(p, t)
				fn := p.BinInfo().PCToFunc(pc)
				if fn.Name != fname {
					t.Errorf("Wrong function name, expected %s got %s", fname, fn.Name)
				}
			},
		}
	}

	withTestProcessArgs("rangeoverfunc", t, ".", []string{}, 0, func(p *proc.Target, grp *proc.TargetGroup, fixture protest.Fixture) {
		t.Run("TestTrickyIterAll1", func(t *testing.T) {
			testseq2intl(t, fixture, grp, p, nil, []seqTest{
				funcBreak(t, "main.TestTrickyIterAll"),
				{contContinue, 24}, // TestTrickyIterAll
				nx(25),
				nx(26),
				nx(27), // for _, x := range ...
				assertLocals(t, "trickItAll", "i"),
				assertEval(t, "i", "0"),
				nx(28), // i += x
				assertLocals(t, "trickItAll", "i", "x"),
				assertEval(t,
					"i", "0",
					"x", "30"),
				nx(29), // if i >= 36 {
				nx(32),
				nx(27), // for _, x := range ...
				notAtEntryPoint(t),
				nx(28), // i += x
				assertEval(t,
					"i", "30",
					"x", "7"),
				nx(29), // if i >= 36 {
				nx(30), // break
				nx(32),
				nx(34), // fmt.Println
			})
		})

		t.Run("TestTrickyIterAll2", func(t *testing.T) {
			testseq2intl(t, fixture, grp, p, nil, []seqTest{
				funcBreak(t, "main.TestTrickyIterAll2"),
				{contContinue, 37}, // TestTrickyIterAll2
				nx(38),
				nx(39),
				nx(40), // for _, x := range...
				nx(41),
				nx(42),
				nx(40),
				notAtEntryPoint(t),
				nx(41),
				nx(42),
				nx(42), // different function from the one above...
				nx(43),
			})
		})

		t.Run("TestBreak1", func(t *testing.T) {
			testseq2intl(t, fixture, grp, p, nil, []seqTest{
				funcBreak(t, "main.TestBreak1"),
				{contContinue, 46}, // TestBreak1
				nx(47),
				nx(48), // for _, x := range... (x == -1)
				nx(49), // if x == -4
				assertLocals(t, "result", "x"),
				assertEval(t,
					"result", "[]int len: 0, cap: 0, nil",
					"x", "-1"),

				nx(52), // for _, y := range... (y == 1)
				nx(53), // if y == 3
				assertLocals(t, "result", "x", "y"),
				assertEval(t,
					"result", "[]int len: 0, cap: 0, nil",
					"x", "-1",
					"y", "1"),
				nx(56), // result = append(result, y)
				nx(57),
				nx(52), // for _, y := range... (y == 2)
				notAtEntryPoint(t),
				nx(53), // if y == 3
				assertEval(t,
					"x", "-1",
					"y", "2"),
				nx(56), // result = append(result, y)
				nx(57),
				nx(52), // for _, y := range... (y == 3)
				nx(53), // if y == 3
				assertEval(t,
					"x", "-1",
					"y", "3"),
				nx(54), // break
				nx(57),
				nx(58), // result = append(result, x)
				nx(59),

				nx(48), // for _, x := range... (x == -2)
				notAtEntryPoint(t),
				nx(49), // if x == -4
				assertEval(t,
					"result", "[]int len: 3, cap: 4, [1,2,-1]",
					"x", "-2"),
				nx(52), // for _, y := range... (y == 1)
				nx(53), // if y == 3
				nx(56), // result = append(result, y)
				nx(57),
				nx(52), // for _, y := range... (y == 2)
				notAtEntryPoint(t),
				nx(53), // if y == 3
				nx(56), // result = append(result, y)
				nx(57),
				nx(52), // for _, y := range... (y == 3)
				nx(53), // if y == 3
				nx(54), // break
				nx(57),
				nx(58), // result = append(result, x)
				nx(59),

				nx(48), // for _, x := range... (x == -4)
				assertEval(t,
					"result", "[]int len: 6, cap: 8, [1,2,-1,1,2,-2]",
					"x", "-4"),
				nx(49), // if x == -4
				nx(50), // break
				nx(59),
				nx(60),
				nx(61),
			})
		})

		t.Run("TestBreak2", func(t *testing.T) {
			testseq2intl(t, fixture, grp, p, nil, []seqTest{
				funcBreak(t, "main.TestBreak2"),

				{contContinue, 63}, // TestBreak2
				nx(64),
				nx(65),

				nx(66), // for _, x := range (x == -1)
				nx(67), // for _, y := range (y == 1)
				nx(68), // if y == 3
				nx(71), // if x == -4
				nx(74), // result = append(result, y)
				nx(75),

				nx(67), // for _, y := range (y == 2)
				nx(68), // if y == 3
				nx(71), // if x == -4
				nx(74), // result = append(result, y)
				nx(75),

				nx(67), // for _, y := range (y == 3)
				nx(68), // if y == 3
				nx(69), // break
				nx(75),
				nx(76), // result = append(result, x)
				nx(77),

				nx(66), // for _, x := range (x == -2)
				nx(67), // for _, y := range (y == 1)
				nx(68), // if y == 3
				nx(71), // if x == -4
				nx(74), // result = append(result, y)
				nx(75),

				nx(67), // for _, y := range (y == 2)
				nx(68), // if y == 3
				nx(71), // if x == -4
				nx(74), // result = append(result, y)
				nx(75),

				nx(67), // for _, y := range (y == 3)
				nx(68), // if y == 3
				nx(69), // break
				nx(75),
				nx(76), // result = append(result, x)
				nx(77),

				nx(66), // for _, x := range (x == -4)
				nx(67), // for _, y := range (y == 1)
				nx(68), // if y == 3
				nx(71), // if x == -4
				nx(72), // break outer
				nx(75),
				nx(77),
				nx(78),
				nx(79),
			})
		})

		t.Run("TestMultiCont0", func(t *testing.T) {
			testseq2intl(t, fixture, grp, p, nil, []seqTest{
				funcBreak(t, "main.TestMultiCont0"),
				{contContinue, 81},
				nx(82),
				nx(84),
				nx(85), // for _, w := range (w == 1000)
				nx(86), // result = append(result, w)
				assertEval(t,
					"w", "1000",
					"result", "[]int len: 0, cap: 10, []"),
				nx(87), // if w == 2000
				assertLocals(t, "result", "w"),
				assertEval(t, "result", "[]int len: 1, cap: 10, [1000]"),
				nx(90), // for _, x := range (x == 100)
				nx(91), // for _, y := range (y == 10)
				nx(92), // result = append(result, y)
				assertLocals(t, "result", "w", "x", "y"),
				assertEval(t,
					"w", "1000",
					"x", "100",
					"y", "10"),

				nx(93), // for _, z := range (z == 1)
				nx(94), // if z&1 == 1
				assertLocals(t, "result", "w", "x", "y", "z"),
				assertEval(t,
					"w", "1000",
					"x", "100",
					"y", "10",
					"z", "1"),
				nx(95), // continue

				nx(93), // for _, z := range (z == 2)
				nx(94), // if z&1 == 1
				assertEval(t, "z", "2"),
				nx(97), // result = append(result, z)
				nx(98), // if z >= 4 {
				nx(101),

				nx(93), // for _, z := range (z == 3)
				nx(94), // if z&1 == 1
				assertEval(t, "z", "3"),
				nx(95), // continue

				nx(93), // for _, z := range (z == 4)
				nx(94), // if z&1 == 1
				assertEval(t, "z", "4"),
				nx(97), // result = append(result, z)
				assertEval(t, "result", "[]int len: 3, cap: 10, [1000,10,2]"),
				nx(98), // if z >= 4 {
				nx(99), // continue W
				nx(101),
				nx(103),
				nx(105),

				nx(85), // for _, w := range (w == 2000)
				nx(86), // result = append(result, w)
				nx(87), // if w == 2000
				assertEval(t,
					"w", "2000",
					"result", "[]int len: 5, cap: 10, [1000,10,2,4,2000]"),
				nx(88), // break
				nx(106),
				nx(107), // fmt.Println
			})
		})

		t.Run("TestPanickyIterator1", func(t *testing.T) {
			testseq2intl(t, fixture, grp, p, nil, []seqTest{
				funcBreak(t, "main.TestPanickyIterator1"),
				{contContinue, 110},
				nx(111),
				nx(112),
				nx(116), // for _, z := range (z == 1)
				nx(117), // result = append(result, z)
				nx(118), // if z == 4
				nx(121),

				nx(116), // for _, z := range (z == 2)
				nx(117), // result = append(result, z)
				nx(118), // if z == 4
				nx(121),

				nx(116), // for _, z := range (z == 3)
				nx(117), // result = append(result, z)
				nx(118), // if z == 4
				nx(121),

				nx(116), // for _, z := range (z == 4)
				nx(117), // result = append(result, z)
				nx(118), // if z == 4
				nx(119), // break

				nx(112), // defer func()
				nx(113), // r := recover()
				nx(114), // fmt.Println
			})
		})

		t.Run("TestPanickyIterator2", func(t *testing.T) {
			testseq2intl(t, fixture, grp, p, nil, []seqTest{
				funcBreak(t, "main.TestPanickyIterator2"),
				{contContinue, 125},
				nx(126),
				nx(127),
				nx(131), // for _, x := range (x == 100)
				nx(132),
				nx(133),
				nx(135), // for _, y := range (y == 10)
				nx(136), // result = append(result, y)
				nx(139), // for k, z := range (k == 0, z == 1)
				nx(140), // result = append(result, z)
				nx(141), // if k == 1
				nx(144),

				nx(139), // for k, z := range (k == 1, z == 2)
				nx(140), // result = append(result, z)
				nx(141), // if k == 1
				nx(142), // break Y
				nx(135),
				nx(145),
				nx(127), // defer func()
				nx(128), // r := recover()
				nx(129), // fmt.Println
			})
		})

		t.Run("TestPanickyIteratorWithNewDefer", func(t *testing.T) {
			testseq2intl(t, fixture, grp, p, nil, []seqTest{
				funcBreak(t, "main.TestPanickyIteratorWithNewDefer"),
				{contContinue, 149},
				nx(150),
				nx(151),
				nx(155), // for _, x := range (x == 100)
				nx(156),
				nx(157),
				nx(159), // for _, y := range (y == 10)
				nx(160),
				nx(163), // result = append(result, y)
				nx(166), // for k, z := range (k == 0, z == 1)
				nx(167), // result = append(result, z)
				nx(168), // if k == 1
				nx(171),

				nx(166), // for k, z := range (k == 0, z == 1)
				nx(167), // result = append(result, z)
				nx(168), // if k == 1
				nx(169), // break Y
				nx(159),
				nx(172),
				nx(160), // defer func()
				nx(161), // fmt.Println
			})
		})

		t.Run("TestLongReturn", func(t *testing.T) {
			testseq2intl(t, fixture, grp, p, nil, []seqTest{
				funcBreak(t, "main.TestLongReturn"),
				{contContinue, 181},
				nx(182), // for _, x := range (x == 1)
				nx(183), // for _, y := range (y == 10)
				nx(184), // if y == 10
				nx(185), // return
				nx(187),
				nx(189),
				nx(178), // into TestLongReturnWrapper, fmt.Println
			})
		})

		t.Run("TestGotoA1", func(t *testing.T) {
			testseq2intl(t, fixture, grp, p, nil, []seqTest{
				funcBreak(t, "main.TestGotoA1"),
				{contContinue, 192},
				nx(193),
				nx(194), // for _, x := range (x == -1)
				nx(195), // result = append(result, x)
				nx(196), // if x == -4
				nx(199), // for _, y := range (y == 1)
				nx(200), // if y == 3
				nx(203), // result = append(result, y)
				nx(204),

				nx(199), // for _, y := range (y == 2)
				nx(200), // if y == 3
				nx(203), // result = append(result, y)
				nx(204),

				nx(199), // for _, y := range (y == 3)
				nx(200), // if y == 3
				nx(201), // goto A
				nx(204),
				nx(206), // result = append(result, x)
				nx(207),

				nx(194), // for _, x := range (x == -4)
				nx(195), // result = append(result, x)
				nx(196), // if x == -4
				nx(197), // break
				nx(207),
				nx(208), // fmt.Println
			})
		})

		t.Run("TestGotoB1", func(t *testing.T) {
			testseq2intl(t, fixture, grp, p, nil, []seqTest{
				funcBreak(t, "main.TestGotoB1"),
				{contContinue, 211},
				nx(212),
				nx(213), // for _, x := range (x == -1)
				nx(214), // result = append(result, x)
				nx(215), // if x == -4
				nx(218), // for _, y := range (y == 1)
				nx(219), // if y == 3
				nx(222), // result = append(result, y)
				nx(223),

				nx(218), // for _, y := range (y == 2)
				nx(219), // if y == 3
				nx(222), // result = append(result, y)
				nx(223),

				nx(218), // for _, y := range (y == 3)
				nx(219), // if y == 3
				nx(220), // goto B
				nx(223),
				nx(225),
				nx(227), // result = append(result, 999)
				nx(228), // fmt.Println
			})
		})

		t.Run("TestRecur", func(t *testing.T) {
			testseq2intl(t, fixture, grp, p, nil, []seqTest{
				funcBreak(t, "main.TestRecur"),
				{contContinue, 231},
				clearBreak(t),
				nx(232), // result := []int{}
				assertEval(t, "n", "3"),
				nx(233), // if n > 0 {
				nx(234), // TestRecur

				nx(236), // for _, x := range (x == 10)
				assertFunc(t, "main.TestRecur"),
				nx(237), // result = ...
				assertEval(t, "n", "3"),
				assertFunc(t, "main.TestRecur-range1"),
				assertEval(t, "x", "10", "n", "3"),
				nx(238), // if n == 3
				nx(239), // TestRecur(0)
				nx(241),

				nx(236), // for _, x := range (x == 20)
				nx(237), // result = ...
				assertEval(t, "x", "20", "n", "3"),
			})
		})
	})
}

func TestRangeOverFuncStepOut(t *testing.T) {
	if !goversion.VersionAfterOrEqual(runtime.Version(), 1, 23) {
		t.Skip("N/A")
	}

	testseq2(t, "rangeoverfunc", "", []seqTest{
		{contContinue, 97},
		{contStepout, 251},
	})
}

func TestRangeOverFuncNextInlined(t *testing.T) {
	if !goversion.VersionAfterOrEqual(runtime.Version(), 1, 23) {
		t.Skip("N/A")
	}

	var bp *proc.Breakpoint

	funcBreak := func(t *testing.T, fnname string) seqTest {
		return seqTest{
			contNothing,
			func(p *proc.Target) {
				bp = setFunctionBreakpoint(p, t, fnname)
			}}
	}

	clearBreak := func(t *testing.T) seqTest {
		return seqTest{
			contNothing,
			func(p *proc.Target) {
				assertNoError(p.ClearBreakpoint(bp.Addr), t, "ClearBreakpoint")
			}}
	}

	nx := func(n int) seqTest {
		return seqTest{contNext, n}
	}

	assertLocals := func(t *testing.T, varnames ...string) seqTest {
		return seqTest{
			contNothing,
			func(p *proc.Target) {
				scope, err := proc.GoroutineScope(p, p.CurrentThread())
				assertNoError(err, t, "GoroutineScope")
				vars, err := scope.Locals(0, "")
				assertNoError(err, t, "Locals")

				gotnames := make([]string, len(vars))
				for i := range vars {
					gotnames[i] = vars[i].Name
				}

				ok := true
				if len(vars) != len(varnames) {
					ok = false
				} else {
					for i := range vars {
						if vars[i].Name != varnames[i] {
							ok = false
							break
						}
					}
				}
				if !ok {
					t.Errorf("Wrong variable names, expected %q, got %q", varnames, gotnames)
				}
			},
		}
	}

	assertEval := func(t *testing.T, exprvals ...string) seqTest {
		return seqTest{
			contNothing,
			func(p *proc.Target) {
				scope, err := proc.GoroutineScope(p, p.CurrentThread())
				assertNoError(err, t, "GoroutineScope")
				for i := 0; i < len(exprvals); i += 2 {
					expr, tgt := exprvals[i], exprvals[i+1]
					v, err := scope.EvalExpression(expr, normalLoadConfig)
					if err != nil {
						t.Errorf("Could not evaluate %q: %v", expr, err)
					} else {
						out := api.ConvertVar(v).SinglelineString()
						if out != tgt {
							t.Errorf("Wrong value for %q, got %q expected %q", expr, out, tgt)
						}
					}
				}
			},
		}
	}

	assertFunc := func(t *testing.T, fname string) seqTest {
		return seqTest{
			contNothing,
			func(p *proc.Target) {
				pc := currentPC(p, t)
				fn := p.BinInfo().PCToFunc(pc)
				if fn.Name != fname {
					t.Errorf("Wrong function name, expected %s got %s", fname, fn.Name)
				}
			},
		}
	}

	withTestProcessArgs("rangeoverfunc", t, ".", []string{}, protest.EnableInlining, func(p *proc.Target, grp *proc.TargetGroup, fixture protest.Fixture) {
		t.Run("TestTrickyIterAll1", func(t *testing.T) {
			testseq2intl(t, fixture, grp, p, nil, []seqTest{
				funcBreak(t, "main.TestTrickyIterAll"),
				{contContinue, 24}, // TestTrickyIterAll
				nx(25),
				nx(26),
				nx(27), // for _, x := range ...
				assertLocals(t, "trickItAll", "i"),
				assertEval(t, "i", "0"),
				nx(28), // i += x
				assertLocals(t, "trickItAll", "i", "x"),
				assertEval(t,
					"i", "0",
					"x", "30"),
				nx(29), // if i >= 36 {
				nx(32),
				nx(27), // for _, x := range ...
				nx(28), // i += x
				assertEval(t,
					"i", "30",
					"x", "7"),
				nx(29), // if i >= 36 {
				nx(30), // break
				nx(27),
				nx(32),
				nx(34), // fmt.Println
			})
		})

		t.Run("TestTrickyIterAll2", func(t *testing.T) {
			testseq2intl(t, fixture, grp, p, nil, []seqTest{
				funcBreak(t, "main.TestTrickyIterAll2"),
				{contContinue, 37}, // TestTrickyIterAll2
				nx(38),
				nx(39),
				nx(40), // for _, x := range...
				nx(41),
				nx(42),
				nx(40),
				nx(41),
				nx(42),
				nx(40),
				nx(42),
				nx(43),
			})
		})

		t.Run("TestBreak1", func(t *testing.T) {
			testseq2intl(t, fixture, grp, p, nil, []seqTest{
				funcBreak(t, "main.TestBreak1"),
				{contContinue, 46}, // TestBreak1
				nx(47),
				nx(48), // for _, x := range... (x == -1)
				nx(49), // if x == -4
				assertLocals(t, "result", "x"),
				assertEval(t,
					"result", "[]int len: 0, cap: 0, nil",
					"x", "-1"),

				nx(52), // for _, y := range... (y == 1)
				nx(53), // if y == 3
				assertLocals(t, "result", "x", "y"),
				assertEval(t,
					"result", "[]int len: 0, cap: 0, nil",
					"x", "-1",
					"y", "1"),
				nx(56), // result = append(result, y)
				nx(57),
				nx(52), // for _, y := range... (y == 2)
				nx(53), // if y == 3
				assertEval(t,
					"x", "-1",
					"y", "2"),
				nx(56), // result = append(result, y)
				nx(57),
				nx(52), // for _, y := range... (y == 3)
				nx(53), // if y == 3
				assertEval(t,
					"x", "-1",
					"y", "3"),
				nx(54), // break
				nx(52),
				nx(57),
				nx(58), // result = append(result, x)
				nx(59),

				nx(48), // for _, x := range... (x == -2)
				nx(49), // if x == -4
				assertEval(t,
					"result", "[]int len: 3, cap: 4, [1,2,-1]",
					"x", "-2"),
				nx(52), // for _, y := range... (y == 1)
				nx(53), // if y == 3
				nx(56), // result = append(result, y)
				nx(57),
				nx(52), // for _, y := range... (y == 2)
				nx(53), // if y == 3
				nx(56), // result = append(result, y)
				nx(57),
				nx(52), // for _, y := range... (y == 3)
				nx(53), // if y == 3
				nx(54), // break
				nx(52),
				nx(57),
				nx(58), // result = append(result, x)
				nx(59),

				nx(48), // for _, x := range... (x == -4)
				assertEval(t,
					"result", "[]int len: 6, cap: 8, [1,2,-1,1,2,-2]",
					"x", "-4"),
				nx(49), // if x == -4
				nx(50), // break
				nx(48),
				nx(59),
				nx(60),
				nx(61),
			})
		})

		t.Run("TestBreak2", func(t *testing.T) {
			testseq2intl(t, fixture, grp, p, nil, []seqTest{
				funcBreak(t, "main.TestBreak2"),

				{contContinue, 63}, // TestBreak2
				nx(64),
				nx(65),

				nx(66), // for _, x := range (x == -1)
				nx(67), // for _, y := range (y == 1)
				nx(68), // if y == 3
				nx(71), // if x == -4
				nx(74), // result = append(result, y)
				nx(75),

				nx(67), // for _, y := range (y == 2)
				nx(68), // if y == 3
				nx(71), // if x == -4
				nx(74), // result = append(result, y)
				nx(75),

				nx(67), // for _, y := range (y == 3)
				nx(68), // if y == 3
				nx(69), // break
				nx(67),
				nx(75),
				nx(76), // result = append(result, x)
				nx(77),

				nx(66), // for _, x := range (x == -2)
				nx(67), // for _, y := range (y == 1)
				nx(68), // if y == 3
				nx(71), // if x == -4
				nx(74), // result = append(result, y)
				nx(75),

				nx(67), // for _, y := range (y == 2)
				nx(68), // if y == 3
				nx(71), // if x == -4
				nx(74), // result = append(result, y)
				nx(75),

				nx(67), // for _, y := range (y == 3)
				nx(68), // if y == 3
				nx(69), // break
				nx(67),
				nx(75),
				nx(76), // result = append(result, x)
				nx(77),

				nx(66), // for _, x := range (x == -4)
				nx(67), // for _, y := range (y == 1)
				nx(68), // if y == 3
				nx(71), // if x == -4
				nx(72), // break outer
				nx(67),
				nx(75),
				nx(66),
				nx(77),
				nx(78),
				nx(79),
			})
		})

		t.Run("TestMultiCont0", func(t *testing.T) {
			testseq2intl(t, fixture, grp, p, nil, []seqTest{
				funcBreak(t, "main.TestMultiCont0"),
				{contContinue, 81},
				nx(82),
				nx(84),
				nx(85), // for _, w := range (w == 1000)
				nx(86), // result = append(result, w)
				assertEval(t,
					"w", "1000",
					"result", "[]int len: 0, cap: 10, []"),
				nx(87), // if w == 2000
				assertLocals(t, "result", "w"),
				assertEval(t, "result", "[]int len: 1, cap: 10, [1000]"),
				nx(90), // for _, x := range (x == 100)
				nx(91), // for _, y := range (y == 10)
				nx(92), // result = append(result, y)
				assertLocals(t, "result", "w", "x", "y"),
				assertEval(t,
					"w", "1000",
					"x", "100",
					"y", "10"),

				nx(93), // for _, z := range (z == 1)
				nx(94), // if z&1 == 1
				assertLocals(t, "result", "w", "x", "y", "z"),
				assertEval(t,
					"w", "1000",
					"x", "100",
					"y", "10",
					"z", "1"),
				nx(95), // continue

				nx(93), // for _, z := range (z == 2)
				nx(94), // if z&1 == 1
				assertEval(t, "z", "2"),
				nx(97), // result = append(result, z)
				nx(98), // if z >= 4 {
				nx(101),

				nx(93), // for _, z := range (z == 3)
				nx(94), // if z&1 == 1
				assertEval(t, "z", "3"),
				nx(95), // continue

				nx(93), // for _, z := range (z == 4)
				nx(94), // if z&1 == 1
				assertEval(t, "z", "4"),
				nx(97), // result = append(result, z)
				assertEval(t, "result", "[]int len: 3, cap: 10, [1000,10,2]"),
				nx(98), // if z >= 4 {
				nx(99), // continue W
				nx(93),
				nx(101),
				nx(91),
				nx(103),
				nx(90),
				nx(105),

				nx(85), // for _, w := range (w == 2000)
				nx(86), // result = append(result, w)
				nx(87), // if w == 2000
				assertEval(t,
					"w", "2000",
					"result", "[]int len: 5, cap: 10, [1000,10,2,4,2000]"),
				nx(88), // break
				nx(85),
				nx(106),
				nx(107), // fmt.Println
			})
		})

		t.Run("TestPanickyIterator1", func(t *testing.T) {
			testseq2intl(t, fixture, grp, p, nil, []seqTest{
				funcBreak(t, "main.TestPanickyIterator1"),
				{contContinue, 110},
				nx(111),
				nx(112),
				nx(116), // for _, z := range (z == 1)
				nx(117), // result = append(result, z)
				nx(118), // if z == 4
				nx(121),

				nx(116), // for _, z := range (z == 2)
				nx(117), // result = append(result, z)
				nx(118), // if z == 4
				nx(121),

				nx(116), // for _, z := range (z == 3)
				nx(117), // result = append(result, z)
				nx(118), // if z == 4
				nx(121),

				nx(116), // for _, z := range (z == 4)
				nx(117), // result = append(result, z)
				nx(118), // if z == 4
				nx(119), // break

				nx(112), // defer func()
				nx(113), // r := recover()
				nx(114), // fmt.Println
			})
		})

		t.Run("TestPanickyIterator2", func(t *testing.T) {
			testseq2intl(t, fixture, grp, p, nil, []seqTest{
				funcBreak(t, "main.TestPanickyIterator2"),
				{contContinue, 125},
				nx(126),
				nx(127),
				nx(131), // for _, x := range (x == 100)
				nx(132),
				nx(133),
				nx(135), // for _, y := range (y == 10)
				nx(136), // result = append(result, y)
				nx(139), // for k, z := range (k == 0, z == 1)
				nx(140), // result = append(result, z)
				nx(141), // if k == 1
				nx(144),

				nx(139), // for k, z := range (k == 1, z == 2)
				nx(140), // result = append(result, z)
				nx(141), // if k == 1
				nx(142), // break Y
				nx(135),
				nx(145),
				nx(127), // defer func()
				nx(128), // r := recover()
				nx(129), // fmt.Println
			})
		})

		t.Run("TestPanickyIteratorWithNewDefer", func(t *testing.T) {
			testseq2intl(t, fixture, grp, p, nil, []seqTest{
				funcBreak(t, "main.TestPanickyIteratorWithNewDefer"),
				{contContinue, 149},
				nx(150),
				nx(151),
				nx(155), // for _, x := range (x == 100)
				nx(156),
				nx(157),
				nx(159), // for _, y := range (y == 10)
				nx(160),
				nx(163), // result = append(result, y)
				nx(166), // for k, z := range (k == 0, z == 1)
				nx(167), // result = append(result, z)
				nx(168), // if k == 1
				nx(171),

				nx(166), // for k, z := range (k == 0, z == 1)
				nx(167), // result = append(result, z)
				nx(168), // if k == 1
				nx(169), // break Y
				nx(159),
				nx(172),
				nx(160), // defer func()
				nx(161), // fmt.Println
			})
		})

		t.Run("TestLongReturn", func(t *testing.T) {
			testseq2intl(t, fixture, grp, p, nil, []seqTest{
				funcBreak(t, "main.TestLongReturn"),
				{contContinue, 181},
				nx(182), // for _, x := range (x == 1)
				nx(183), // for _, y := range (y == 10)
				nx(184), // if y == 10
				nx(185), // return
				nx(183),
				nx(187),
				nx(182),
				nx(189),
				nx(178), // into TestLongReturnWrapper, fmt.Println
			})
		})

		t.Run("TestGotoA1", func(t *testing.T) {
			testseq2intl(t, fixture, grp, p, nil, []seqTest{
				funcBreak(t, "main.TestGotoA1"),
				{contContinue, 192},
				nx(193),
				nx(194), // for _, x := range (x == -1)
				nx(195), // result = append(result, x)
				nx(196), // if x == -4
				nx(199), // for _, y := range (y == 1)
				nx(200), // if y == 3
				nx(203), // result = append(result, y)
				nx(204),

				nx(199), // for _, y := range (y == 2)
				nx(200), // if y == 3
				nx(203), // result = append(result, y)
				nx(204),

				nx(199), // for _, y := range (y == 3)
				nx(200), // if y == 3
				nx(201), // goto A
				nx(199),
				nx(204),
				nx(206), // result = append(result, x)
				nx(207),

				nx(194), // for _, x := range (x == -4)
				nx(195), // result = append(result, x)
				nx(196), // if x == -4
				nx(197), // break
				nx(194),
				nx(207),
				nx(208), // fmt.Println
			})
		})

		t.Run("TestGotoB1", func(t *testing.T) {
			testseq2intl(t, fixture, grp, p, nil, []seqTest{
				funcBreak(t, "main.TestGotoB1"),
				{contContinue, 211},
				nx(212),
				nx(213), // for _, x := range (x == -1)
				nx(214), // result = append(result, x)
				nx(215), // if x == -4
				nx(218), // for _, y := range (y == 1)
				nx(219), // if y == 3
				nx(222), // result = append(result, y)
				nx(223),

				nx(218), // for _, y := range (y == 2)
				nx(219), // if y == 3
				nx(222), // result = append(result, y)
				nx(223),

				nx(218), // for _, y := range (y == 3)
				nx(219), // if y == 3
				nx(220), // goto B
				nx(218),
				nx(223),
				nx(213),
				nx(225),
				nx(227), // result = append(result, 999)
				nx(228), // fmt.Println
			})
		})

		t.Run("TestRecur", func(t *testing.T) {
			testseq2intl(t, fixture, grp, p, nil, []seqTest{
				funcBreak(t, "main.TestRecur"),
				{contContinue, 231},
				clearBreak(t),
				nx(232), // result := []int{}
				assertEval(t, "n", "3"),
				nx(233), // if n > 0 {
				nx(234), // TestRecur

				nx(236), // for _, x := range (x == 10)
				assertFunc(t, "main.TestRecur"),
				assertEval(t, "n", "3"),
				nx(237), // result = ...
				assertFunc(t, "main.TestRecur-range1"),
				assertEval(t, "x", "10", "n", "3"),
				nx(238), // if n == 3
				nx(239), // TestRecur(0)
				nx(241),

				nx(236), // for _, x := range (x == 20)
				nx(237), // result = ...
				assertEval(t, "x", "20", "n", "3"),
			})
		})
	})
}
