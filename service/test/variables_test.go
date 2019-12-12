package service_test

import (
	"errors"
	"fmt"
	"go/constant"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/go-delve/delve/pkg/debug"
	"github.com/go-delve/delve/pkg/goversion"
	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/service/api"

	protest "github.com/go-delve/delve/pkg/proc/test"
)

var pnormalLoadConfig = proc.LoadConfig{true, 1, 64, 64, -1, 0}
var pshortLoadConfig = proc.LoadConfig{false, 0, 64, 0, 3, 0}

type varTest struct {
	name         string
	preserveName bool
	value        string
	alternate    string
	varType      string
	err          error
}

func matchStringOrPrefix(output, target string) bool {
	if strings.HasSuffix(target, "…") {
		prefix := target[:len(target)-len("…")]
		b := strings.HasPrefix(output, prefix)
		return b
	} else {
		return output == target
	}
}

func assertVariable(t *testing.T, variable *proc.Variable, expected varTest) {
	if expected.preserveName {
		if variable.Name != expected.name {
			t.Fatalf("Expected %s got %s\n", expected.name, variable.Name)
		}
	}

	cv := api.ConvertVar(variable)

	if cv.Type != expected.varType {
		t.Fatalf("Expected %s got %s (for variable %s)\n", expected.varType, cv.Type, expected.name)
	}

	if ss := cv.SinglelineString(); !matchStringOrPrefix(ss, expected.value) {
		t.Fatalf("Expected %#v got %#v (for variable %s)\n", expected.value, ss, expected.name)
	}
}

func findFirstNonRuntimeFrame(tgt *debug.Target) (proc.Stackframe, error) {
	frames, err := proc.ThreadStacktrace(tgt.CurrentThread(), 10)
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

func evalScope(tgt *debug.Target) (*proc.EvalScope, error) {
	if testBackend != "rr" {
		return proc.GoroutineScope(tgt.CurrentThread())
	}
	frame, err := findFirstNonRuntimeFrame(tgt)
	if err != nil {
		return nil, err
	}
	return proc.FrameToScope(tgt.BinInfo(), tgt.CurrentThread(), nil, frame), nil
}

func evalVariable(tgt *debug.Target, symbol string, cfg proc.LoadConfig) (*proc.Variable, error) {
	scope, err := evalScope(tgt)
	if err != nil {
		return nil, err
	}

	return scope.EvalVariable(symbol, cfg)
}

func (tc *varTest) alternateVarTest() varTest {
	r := *tc
	r.value = r.alternate
	return r
}

func setVariable(tgt proc.Process, symbol, value string) error {
	scope, err := proc.GoroutineScope(tgt.CurrentThread())
	if err != nil {
		return err
	}
	return scope.SetVariable(symbol, value)
}

func withTestTarget(name string, t *testing.T, fn func(tgt *debug.Target, fixture protest.Fixture)) {
	withTestTargetArgs(name, t, ".", []string{}, 0, fn)
}

func withTestTargetArgs(name string, t *testing.T, wd string, args []string, buildFlags protest.BuildFlags, fn func(tgt *debug.Target, fixture protest.Fixture)) {
	if testBackend == "rr" {
		protest.MustHaveRecordingAllowed(t)
	}
	if buildMode == "pie" {
		buildFlags |= protest.BuildModePIE
	}
	fixture := protest.BuildFixture(name, buildFlags)
	tgt, err := debug.Launch(append([]string{fixture.Path}, args...), wd, false, testBackend, []string{})
	if err != nil {
		t.Fatal("Launch():", err)
	}

	defer func() {
		tgt.Detach(true)
	}()

	fn(tgt, fixture)
}

func TestVariableEvaluation(t *testing.T) {
	testcases := []varTest{
		{"a1", true, "\"foofoofoofoofoofoo\"", "", "string", nil},
		{"a11", true, "[3]main.FooBar [{Baz: 1, Bur: \"a\"},{Baz: 2, Bur: \"b\"},{Baz: 3, Bur: \"c\"}]", "", "[3]main.FooBar", nil},
		{"a12", true, "[]main.FooBar len: 2, cap: 2, [{Baz: 4, Bur: \"d\"},{Baz: 5, Bur: \"e\"}]", "", "[]main.FooBar", nil},
		{"a13", true, "[]*main.FooBar len: 3, cap: 3, [*{Baz: 6, Bur: \"f\"},*{Baz: 7, Bur: \"g\"},*{Baz: 8, Bur: \"h\"}]", "", "[]*main.FooBar", nil},
		{"a2", true, "6", "10", "int", nil},
		{"a3", true, "7.23", "3.1", "float64", nil},
		{"a4", true, "[2]int [1,2]", "", "[2]int", nil},
		{"a5", true, "[]int len: 5, cap: 5, [1,2,3,4,5]", "", "[]int", nil},
		{"a6", true, "main.FooBar {Baz: 8, Bur: \"word\"}", "", "main.FooBar", nil},
		{"a7", true, "*main.FooBar {Baz: 5, Bur: \"strum\"}", "", "*main.FooBar", nil},
		{"a8", true, "main.FooBar2 {Bur: 10, Baz: \"feh\"}", "", "main.FooBar2", nil},
		{"a9", true, "*main.FooBar nil", "", "*main.FooBar", nil},
		{"baz", true, "\"bazburzum\"", "", "string", nil},
		{"neg", true, "-1", "-20", "int", nil},
		{"f32", true, "1.2", "1.1", "float32", nil},
		{"c64", true, "(1 + 2i)", "(4 + 5i)", "complex64", nil},
		{"c128", true, "(2 + 3i)", "(6.3 + 7i)", "complex128", nil},
		{"a6.Baz", true, "8", "20", "int", nil},
		{"a7.Baz", true, "5", "25", "int", nil},
		{"a8.Baz", true, "\"feh\"", "", "string", nil},
		{"a9.Baz", true, "nil", "", "int", fmt.Errorf("a9 is nil")},
		{"a9.NonExistent", true, "nil", "", "int", fmt.Errorf("a9 has no member NonExistent")},
		{"a8", true, "main.FooBar2 {Bur: 10, Baz: \"feh\"}", "", "main.FooBar2", nil}, // reread variable after member
		{"i32", true, "[2]int32 [1,2]", "", "[2]int32", nil},
		{"b1", true, "true", "false", "bool", nil},
		{"b2", true, "false", "true", "bool", nil},
		{"i8", true, "1", "2", "int8", nil},
		{"u16", true, "65535", "0", "uint16", nil},
		{"u32", true, "4294967295", "1", "uint32", nil},
		{"u64", true, "18446744073709551615", "2", "uint64", nil},
		{"u8", true, "255", "3", "uint8", nil},
		{"up", true, "5", "4", "uintptr", nil},
		{"f", true, "main.barfoo", "", "func()", nil},
		{"ba", true, "[]int len: 200, cap: 200, [0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,...+136 more]", "", "[]int", nil},
		{"ms", true, "main.Nest {Level: 0, Nest: *main.Nest {Level: 1, Nest: *(*main.Nest)(…", "", "main.Nest", nil},
		{"ms.Nest.Nest", true, "*main.Nest {Level: 2, Nest: *main.Nest {Level: 3, Nest: *(*main.Nest)(…", "", "*main.Nest", nil},
		{"ms.Nest.Nest.Nest.Nest.Nest", true, "*main.Nest nil", "", "*main.Nest", nil},
		{"ms.Nest.Nest.Nest.Nest.Nest.Nest", true, "", "", "*main.Nest", fmt.Errorf("ms.Nest.Nest.Nest.Nest.Nest is nil")},
		{"main.p1", true, "10", "12", "int", nil},
		{"p1", true, "10", "13", "int", nil},
		{"NonExistent", true, "", "", "", fmt.Errorf("could not find symbol value for NonExistent")},
	}

	protest.AllowRecording(t)
	withTestTarget("testvariables", t, func(tgt *debug.Target, fixture protest.Fixture) {
		err := tgt.Continue()
		assertNoError(err, t, "Continue() returned an error")

		for _, tc := range testcases {
			variable, err := evalVariable(tgt, tc.name, pnormalLoadConfig)
			if tc.err == nil {
				assertNoError(err, t, "EvalVariable() returned an error")
				assertVariable(t, variable, tc)
			} else {
				if err == nil {
					t.Fatalf("Expected error %s, got no error: %s\n", tc.err.Error(), api.ConvertVar(variable).SinglelineString())
				}
				if tc.err.Error() != err.Error() {
					t.Fatalf("Unexpected error. Expected %s got %s", tc.err.Error(), err.Error())
				}
			}

			if tc.alternate != "" && testBackend != "rr" {
				assertNoError(setVariable(tgt, tc.name, tc.alternate), t, "SetVariable()")
				variable, err = evalVariable(tgt, tc.name, pnormalLoadConfig)
				assertNoError(err, t, "EvalVariable()")
				assertVariable(t, variable, tc.alternateVarTest())

				assertNoError(setVariable(tgt, tc.name, tc.value), t, "SetVariable()")
				variable, err := evalVariable(tgt, tc.name, pnormalLoadConfig)
				assertNoError(err, t, "EvalVariable()")
				assertVariable(t, variable, tc)
			}
		}
	})
}

func TestSetVariable(t *testing.T) {
	var testcases = []struct {
		name     string
		typ      string // type of <name>
		startVal string // original value of <name>
		expr     string
		finalVal string // new value of <name> after executing <name> = <expr>
	}{
		{"b.ptr", "*main.A", "*main.A {val: 1337}", "nil", "*main.A nil"},
		{"m2", "map[int]*main.astruct", "map[int]*main.astruct [1: *{A: 10, B: 11}, ]", "nil", "map[int]*main.astruct nil"},
		{"fn1", "main.functype", "main.afunc", "nil", "nil"},
		{"ch1", "chan int", "chan int 4/11", "nil", "chan int nil"},
		{"s2", "[]main.astruct", "[]main.astruct len: 8, cap: 8, [{A: 1, B: 2},{A: 3, B: 4},{A: 5, B: 6},{A: 7, B: 8},{A: 9, B: 10},{A: 11, B: 12},{A: 13, B: 14},{A: 15, B: 16}]", "nil", "[]main.astruct len: 0, cap: 0, nil"},
		{"err1", "error", "error(*main.astruct) *{A: 1, B: 2}", "nil", "error nil"},
		{"s1[0]", "string", `"one"`, `""`, `""`},
		{"as1", "main.astruct", "main.astruct {A: 1, B: 1}", `m1["Malone"]`, "main.astruct {A: 2, B: 3}"},

		{"iface1", "interface {}", "interface {}(*main.astruct) *{A: 1, B: 2}", "nil", "interface {} nil"},
		{"iface1", "interface {}", "interface {} nil", "iface2", "interface {}(string) \"test\""},
		{"iface1", "interface {}", "interface {}(string) \"test\"", "parr", "interface {}(*[4]int) *[0,1,2,3]"},

		{"s3", "[]int", `[]int len: 0, cap: 6, []`, "s4[2:5]", "[]int len: 3, cap: 3, [3,4,5]"},
		{"s3", "[]int", "[]int len: 3, cap: 3, [3,4,5]", "arr1[:]", "[]int len: 4, cap: 4, [0,1,2,3]"},
	}

	withTestTarget("testvariables2", t, func(tgt *debug.Target, fixture protest.Fixture) {
		assertNoError(tgt.Continue(), t, "Continue()")

		for _, tc := range testcases {
			if tc.name == "iface1" && tc.expr == "parr" {
				if !goversion.VersionAfterOrEqual(runtime.Version(), 1, 11) {
					// conversion pointer -> eface not supported prior to Go 1.11
					continue
				}
			}
			variable, err := evalVariable(tgt, tc.name, pnormalLoadConfig)
			assertNoError(err, t, "EvalVariable()")
			assertVariable(t, variable, varTest{tc.name, true, tc.startVal, "", tc.typ, nil})

			assertNoError(setVariable(tgt, tc.name, tc.expr), t, "SetVariable()")

			variable, err = evalVariable(tgt, tc.name, pnormalLoadConfig)
			assertNoError(err, t, "EvalVariable()")
			assertVariable(t, variable, varTest{tc.name, true, tc.finalVal, "", tc.typ, nil})
		}
	})
}

func TestVariableEvaluationShort(t *testing.T) {
	testcases := []varTest{
		{"a1", true, "\"foofoofoofoofoofoo\"", "", "string", nil},
		{"a11", true, "[3]main.FooBar [...]", "", "[3]main.FooBar", nil},
		{"a12", true, "[]main.FooBar len: 2, cap: 2, [...]", "", "[]main.FooBar", nil},
		{"a13", true, "[]*main.FooBar len: 3, cap: 3, [...]", "", "[]*main.FooBar", nil},
		{"a2", true, "6", "", "int", nil},
		{"a3", true, "7.23", "", "float64", nil},
		{"a4", true, "[2]int [...]", "", "[2]int", nil},
		{"a5", true, "[]int len: 5, cap: 5, [...]", "", "[]int", nil},
		{"a6", true, "main.FooBar {Baz: 8, Bur: \"word\"}", "", "main.FooBar", nil},
		{"a7", true, "(*main.FooBar)(0x…", "", "*main.FooBar", nil},
		{"a8", true, "main.FooBar2 {Bur: 10, Baz: \"feh\"}", "", "main.FooBar2", nil},
		{"a9", true, "*main.FooBar nil", "", "*main.FooBar", nil},
		{"baz", true, "\"bazburzum\"", "", "string", nil},
		{"neg", true, "-1", "", "int", nil},
		{"f32", true, "1.2", "", "float32", nil},
		{"c64", true, "(1 + 2i)", "", "complex64", nil},
		{"c128", true, "(2 + 3i)", "", "complex128", nil},
		{"a6.Baz", true, "8", "", "int", nil},
		{"a7.Baz", true, "5", "", "int", nil},
		{"a8.Baz", true, "\"feh\"", "", "string", nil},
		{"a9.Baz", true, "nil", "", "int", fmt.Errorf("a9 is nil")},
		{"a9.NonExistent", true, "nil", "", "int", fmt.Errorf("a9 has no member NonExistent")},
		{"a8", true, "main.FooBar2 {Bur: 10, Baz: \"feh\"}", "", "main.FooBar2", nil}, // reread variable after member
		{"i32", true, "[2]int32 [...]", "", "[2]int32", nil},
		{"b1", true, "true", "false", "bool", nil},
		{"b2", true, "false", "true", "bool", nil},
		{"i8", true, "1", "2", "int8", nil},
		{"u16", true, "65535", "0", "uint16", nil},
		{"u32", true, "4294967295", "1", "uint32", nil},
		{"u64", true, "18446744073709551615", "2", "uint64", nil},
		{"u8", true, "255", "3", "uint8", nil},
		{"up", true, "5", "4", "uintptr", nil},
		{"f", true, "main.barfoo", "", "func()", nil},
		{"ba", true, "[]int len: 200, cap: 200, [...]", "", "[]int", nil},
		{"ms", true, "main.Nest {Level: 0, Nest: (*main.Nest)(0x…", "", "main.Nest", nil},
		{"ms.Nest.Nest", true, "(*main.Nest)(0x…", "", "*main.Nest", nil},
		{"ms.Nest.Nest.Nest.Nest.Nest", true, "*main.Nest nil", "", "*main.Nest", nil},
		{"ms.Nest.Nest.Nest.Nest.Nest.Nest", true, "", "", "*main.Nest", fmt.Errorf("ms.Nest.Nest.Nest.Nest.Nest is nil")},
		{"main.p1", true, "10", "", "int", nil},
		{"p1", true, "10", "", "int", nil},
		{"NonExistent", true, "", "", "", fmt.Errorf("could not find symbol value for NonExistent")},
	}

	protest.AllowRecording(t)
	withTestTarget("testvariables", t, func(tgt *debug.Target, fixture protest.Fixture) {
		err := tgt.Continue()
		assertNoError(err, t, "Continue() returned an error")

		for _, tc := range testcases {
			variable, err := evalVariable(tgt, tc.name, pshortLoadConfig)
			if tc.err == nil {
				assertNoError(err, t, "EvalVariable() returned an error")
				assertVariable(t, variable, tc)
			} else {
				if err == nil {
					t.Fatalf("Expected error %s, got no error: %s\n", tc.err.Error(), api.ConvertVar(variable).SinglelineString())
				}
				if tc.err.Error() != err.Error() {
					t.Fatalf("Unexpected error. Expected %s got %s", tc.err.Error(), err.Error())
				}
			}
		}
	})
}

func TestMultilineVariableEvaluation(t *testing.T) {
	testcases := []varTest{
		{"a1", true, "\"foofoofoofoofoofoo\"", "", "string", nil},
		{"a11", true, `[3]main.FooBar [
	{Baz: 1, Bur: "a"},
	{Baz: 2, Bur: "b"},
	{Baz: 3, Bur: "c"},
]`, "", "[3]main.FooBar", nil},
		{"a12", true, `[]main.FooBar len: 2, cap: 2, [
	{Baz: 4, Bur: "d"},
	{Baz: 5, Bur: "e"},
]`, "", "[]main.FooBar", nil},
		{"a13", true, `[]*main.FooBar len: 3, cap: 3, [
	*{Baz: 6, Bur: "f"},
	*{Baz: 7, Bur: "g"},
	*{Baz: 8, Bur: "h"},
]`, "", "[]*main.FooBar", nil},
		{"a2", true, "6", "10", "int", nil},
		{"a4", true, "[2]int [1,2]", "", "[2]int", nil},
		{"a5", true, "[]int len: 5, cap: 5, [1,2,3,4,5]", "", "[]int", nil},
		{"a6", true, "main.FooBar {Baz: 8, Bur: \"word\"}", "", "main.FooBar", nil},
		{"a7", true, "*main.FooBar {Baz: 5, Bur: \"strum\"}", "", "*main.FooBar", nil},
		{"a8", true, "main.FooBar2 {Bur: 10, Baz: \"feh\"}", "", "main.FooBar2", nil},
		{"a9", true, "*main.FooBar nil", "", "*main.FooBar", nil},
		{"a8", true, "main.FooBar2 {Bur: 10, Baz: \"feh\"}", "", "main.FooBar2", nil}, // reread variable after member
		{"i32", true, "[2]int32 [1,2]", "", "[2]int32", nil},
		{"ba", true, "[]int len: 200, cap: 200, [0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,...+136 more]", "", "[]int", nil},
		{"ms", true, `main.Nest {
	Level: 0,
	Nest: *main.Nest {
		Level: 1,
		Nest: *(*main.Nest)(…`, "", "main.Nest", nil},
	}

	protest.AllowRecording(t)
	withTestTarget("testvariables", t, func(tgt *debug.Target, fixture protest.Fixture) {
		err := tgt.Continue()
		assertNoError(err, t, "Continue() returned an error")

		for _, tc := range testcases {
			variable, err := evalVariable(tgt, tc.name, pnormalLoadConfig)
			assertNoError(err, t, "EvalVariable() returned an error")
			if ms := api.ConvertVar(variable).MultilineString(""); !matchStringOrPrefix(ms, tc.value) {
				t.Fatalf("Expected %s got %s (variable %s)\n", tc.value, ms, variable.Name)
			}
		}
	})
}

type varArray []*proc.Variable

// Len is part of sort.Interface.
func (s varArray) Len() int {
	return len(s)
}

// Swap is part of sort.Interface.
func (s varArray) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

// Less is part of sort.Interface. It is implemented by calling the "by" closure in the sorter.
func (s varArray) Less(i, j int) bool {
	return s[i].Name < s[j].Name
}

func TestLocalVariables(t *testing.T) {
	testcases := []struct {
		fn     func(*proc.EvalScope, proc.LoadConfig) ([]*proc.Variable, error)
		output []varTest
	}{
		{(*proc.EvalScope).LocalVariables,
			[]varTest{
				{"a1", true, "\"foofoofoofoofoofoo\"", "", "string", nil},
				{"a10", true, "\"ofo\"", "", "string", nil},
				{"a11", true, "[3]main.FooBar [{Baz: 1, Bur: \"a\"},{Baz: 2, Bur: \"b\"},{Baz: 3, Bur: \"c\"}]", "", "[3]main.FooBar", nil},
				{"a12", true, "[]main.FooBar len: 2, cap: 2, [{Baz: 4, Bur: \"d\"},{Baz: 5, Bur: \"e\"}]", "", "[]main.FooBar", nil},
				{"a13", true, "[]*main.FooBar len: 3, cap: 3, [*{Baz: 6, Bur: \"f\"},*{Baz: 7, Bur: \"g\"},*{Baz: 8, Bur: \"h\"}]", "", "[]*main.FooBar", nil},
				{"a2", true, "6", "", "int", nil},
				{"a3", true, "7.23", "", "float64", nil},
				{"a4", true, "[2]int [1,2]", "", "[2]int", nil},
				{"a5", true, "[]int len: 5, cap: 5, [1,2,3,4,5]", "", "[]int", nil},
				{"a6", true, "main.FooBar {Baz: 8, Bur: \"word\"}", "", "main.FooBar", nil},
				{"a7", true, "*main.FooBar {Baz: 5, Bur: \"strum\"}", "", "*main.FooBar", nil},
				{"a8", true, "main.FooBar2 {Bur: 10, Baz: \"feh\"}", "", "main.FooBar2", nil},
				{"a9", true, "*main.FooBar nil", "", "*main.FooBar", nil},
				{"b1", true, "true", "", "bool", nil},
				{"b2", true, "false", "", "bool", nil},
				{"ba", true, "[]int len: 200, cap: 200, [0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,...+136 more]", "", "[]int", nil},
				{"c128", true, "(2 + 3i)", "", "complex128", nil},
				{"c64", true, "(1 + 2i)", "", "complex64", nil},
				{"f", true, "main.barfoo", "", "func()", nil},
				{"f32", true, "1.2", "", "float32", nil},
				{"i32", true, "[2]int32 [1,2]", "", "[2]int32", nil},
				{"i8", true, "1", "", "int8", nil},
				{"ms", true, "main.Nest {Level: 0, Nest: *main.Nest {Level: 1, Nest: *(*main.Nest)…", "", "main.Nest", nil},
				{"neg", true, "-1", "", "int", nil},
				{"u16", true, "65535", "", "uint16", nil},
				{"u32", true, "4294967295", "", "uint32", nil},
				{"u64", true, "18446744073709551615", "", "uint64", nil},
				{"u8", true, "255", "", "uint8", nil},
				{"up", true, "5", "", "uintptr", nil}}},
		{(*proc.EvalScope).FunctionArguments,
			[]varTest{
				{"bar", true, "main.FooBar {Baz: 10, Bur: \"lorem\"}", "", "main.FooBar", nil},
				{"baz", true, "\"bazburzum\"", "", "string", nil}}},
	}

	protest.AllowRecording(t)
	withTestTarget("testvariables", t, func(tgt *debug.Target, fixture protest.Fixture) {
		err := tgt.Continue()
		assertNoError(err, t, "Continue() returned an error")

		for _, tc := range testcases {
			var scope *proc.EvalScope
			var err error

			if testBackend == "rr" {
				var frame proc.Stackframe
				frame, err = findFirstNonRuntimeFrame(tgt)
				if err == nil {
					scope = proc.FrameToScope(tgt.BinInfo(), tgt.CurrentThread(), nil, frame)
				}
			} else {
				scope, err = proc.GoroutineScope(tgt.CurrentThread())
			}

			assertNoError(err, t, "scope")
			vars, err := tc.fn(scope, pnormalLoadConfig)
			assertNoError(err, t, "LocalVariables() returned an error")

			sort.Sort(varArray(vars))

			if len(tc.output) != len(vars) {
				t.Fatalf("Invalid variable count. Expected %d got %d.", len(tc.output), len(vars))
			}

			for i, variable := range vars {
				assertVariable(t, variable, tc.output[i])
			}
		}
	})
}

func TestEmbeddedStruct(t *testing.T) {
	protest.AllowRecording(t)
	withTestTarget("testvariables2", t, func(tgt *debug.Target, fixture protest.Fixture) {
		testcases := []varTest{
			{"b.val", true, "-314", "-314", "int", nil},
			{"b.A.val", true, "-314", "-314", "int", nil},
			{"b.a.val", true, "42", "42", "int", nil},
			{"b.ptr.val", true, "1337", "1337", "int", nil},
			{"b.C.s", true, "\"hello\"", "\"hello\"", "string", nil},
			{"b.s", true, "\"hello\"", "\"hello\"", "string", nil},
			{"b2", true, "main.B {A: main.A {val: 42}, C: *main.C nil, a: main.A {val: 47}, ptr: *main.A nil}", "main.B {A: (*main.A)(0x…", "main.B", nil},
		}
		assertNoError(tgt.Continue(), t, "Continue()")

		ver, _ := goversion.Parse(runtime.Version())
		if ver.Major >= 0 && !ver.AfterOrEqual(goversion.GoVersion{1, 9, -1, 0, 0, ""}) {
			// on go < 1.9 embedded fields had different names
			for i := range testcases {
				if testcases[i].name == "b2" {
					testcases[i].value = "main.B {main.A: main.A {val: 42}, *main.C: *main.C nil, a: main.A {val: 47}, ptr: *main.A nil}"
					testcases[i].alternate = "main.B {main.A: (*main.A)(0x…"
				}
			}
		}

		for _, tc := range testcases {
			variable, err := evalVariable(tgt, tc.name, pnormalLoadConfig)
			if tc.err == nil {
				assertNoError(err, t, fmt.Sprintf("EvalVariable(%s) returned an error", tc.name))
				assertVariable(t, variable, tc)
				variable, err = evalVariable(tgt, tc.name, pshortLoadConfig)
				assertNoError(err, t, fmt.Sprintf("EvalVariable(%s, pshortLoadConfig) returned an error", tc.name))
				assertVariable(t, variable, tc.alternateVarTest())
			} else {
				if tc.err.Error() != err.Error() {
					t.Fatalf("Unexpected error. Expected %s got %s", tc.err.Error(), err.Error())
				}
			}
		}
	})
}

func TestComplexSetting(t *testing.T) {
	withTestTarget("testvariables", t, func(tgt *debug.Target, fixture protest.Fixture) {
		err := tgt.Continue()
		assertNoError(err, t, "Continue() returned an error")

		h := func(setExpr, value string) {
			assertNoError(setVariable(tgt, "c128", setExpr), t, "SetVariable()")
			variable, err := evalVariable(tgt, "c128", pnormalLoadConfig)
			assertNoError(err, t, "EvalVariable()")
			if s := api.ConvertVar(variable).SinglelineString(); s != value {
				t.Fatalf("Wrong value of c128: \"%s\", expected \"%s\" after setting it to \"%s\"", s, value, setExpr)
			}
		}

		h("3.2i", "(0 + 3.2i)")
		h("1.1", "(1.1 + 0i)")
		h("1 + 3.3i", "(1 + 3.3i)")
		h("complex(1.2, 3.4)", "(1.2 + 3.4i)")
	})
}

func TestEvalExpression(t *testing.T) {
	testcases := []varTest{
		// slice/array/string subscript
		{"s1[0]", false, "\"one\"", "\"one\"", "string", nil},
		{"s1[1]", false, "\"two\"", "\"two\"", "string", nil},
		{"s1[2]", false, "\"three\"", "\"three\"", "string", nil},
		{"s1[3]", false, "\"four\"", "\"four\"", "string", nil},
		{"s1[4]", false, "\"five\"", "\"five\"", "string", nil},
		{"s1[5]", false, "", "", "string", fmt.Errorf("index out of bounds")},
		{"a1[0]", false, "\"one\"", "\"one\"", "string", nil},
		{"a1[1]", false, "\"two\"", "\"two\"", "string", nil},
		{"a1[2]", false, "\"three\"", "\"three\"", "string", nil},
		{"a1[3]", false, "\"four\"", "\"four\"", "string", nil},
		{"a1[4]", false, "\"five\"", "\"five\"", "string", nil},
		{"a1[5]", false, "", "", "string", fmt.Errorf("index out of bounds")},
		{"str1[0]", false, "48", "48", "byte", nil},
		{"str1[1]", false, "49", "49", "byte", nil},
		{"str1[2]", false, "50", "50", "byte", nil},
		{"str1[10]", false, "48", "48", "byte", nil},
		{"str1[11]", false, "", "", "byte", fmt.Errorf("index out of bounds")},

		// slice/array/string reslicing
		{"a1[2:4]", false, "[]string len: 2, cap: 2, [\"three\",\"four\"]", "[]string len: 2, cap: 2, [...]", "[]string", nil},
		{"s1[2:4]", false, "[]string len: 2, cap: 2, [\"three\",\"four\"]", "[]string len: 2, cap: 2, [...]", "[]string", nil},
		{"str1[2:4]", false, "\"23\"", "\"23\"", "string", nil},
		{"str1[0:11]", false, "\"01234567890\"", "\"01234567890\"", "string", nil},
		{"str1[:3]", false, "\"012\"", "\"012\"", "string", nil},
		{"str1[3:]", false, "\"34567890\"", "\"34567890\"", "string", nil},
		{"str1[0:12]", false, "", "", "string", fmt.Errorf("index out of bounds")},
		{"str1[5:3]", false, "", "", "string", fmt.Errorf("index out of bounds")},

		// NaN and Inf floats
		{"pinf", false, "+Inf", "+Inf", "float64", nil},
		{"ninf", false, "-Inf", "-Inf", "float64", nil},
		{"nan", false, "NaN", "NaN", "float64", nil},

		// pointers
		{"*p2", false, "5", "5", "int", nil},
		{"p2", true, "*5", "(*int)(0x…", "*int", nil},
		{"p3", true, "*int nil", "*int nil", "*int", nil},
		{"*p3", false, "", "", "int", fmt.Errorf("nil pointer dereference")},

		// channels
		{"ch1", true, "chan int 4/11", "chan int 4/11", "chan int", nil},
		{"chnil", true, "chan int nil", "chan int nil", "chan int", nil},
		{"ch1+1", false, "", "", "", fmt.Errorf("can not convert 1 constant to chan int")},

		// maps
		{"m1[\"Malone\"]", false, "main.astruct {A: 2, B: 3}", "main.astruct {A: 2, B: 3}", "main.astruct", nil},
		{"m2[1].B", false, "11", "11", "int", nil},
		{"m2[c1.sa[2].B-4].A", false, "10", "10", "int", nil},
		{"m2[*p1].B", false, "11", "11", "int", nil},
		{"m3[as1]", false, "42", "42", "int", nil},
		{"mnil[\"Malone\"]", false, "", "", "", fmt.Errorf("key not found")},
		{"m1[80:]", false, "", "", "", fmt.Errorf("map index out of bounds")},

		// interfaces
		{"err1", true, "error(*main.astruct) *{A: 1, B: 2}", "error(*main.astruct) 0x…", "error", nil},
		{"err2", true, "error(*main.bstruct) *{a: main.astruct {A: 1, B: 2}}", "error(*main.bstruct) 0x…", "error", nil},
		{"errnil", true, "error nil", "error nil", "error", nil},
		{"iface1", true, "interface {}(*main.astruct) *{A: 1, B: 2}", "interface {}(*main.astruct) 0x…", "interface {}", nil},
		{"iface1.A", false, "1", "1", "int", nil},
		{"iface1.B", false, "2", "2", "int", nil},
		{"iface2", true, "interface {}(string) \"test\"", "interface {}(string) \"test\"", "interface {}", nil},
		{"iface3", true, "interface {}(map[string]go/constant.Value) []", "interface {}(map[string]go/constant.Value) []", "interface {}", nil},
		{"iface4", true, "interface {}([]go/constant.Value) [4]", "interface {}([]go/constant.Value) [...]", "interface {}", nil},
		{"ifacenil", true, "interface {} nil", "interface {} nil", "interface {}", nil},
		{"err1 == err2", false, "false", "false", "", nil},
		{"err1 == iface1", false, "", "", "", fmt.Errorf("mismatched types \"error\" and \"interface {}\"")},
		{"errnil == nil", false, "true", "true", "", nil},
		{"errtypednil == nil", false, "false", "false", "", nil},
		{"nil == errnil", false, "true", "true", "", nil},
		{"err1.(*main.astruct)", false, "*main.astruct {A: 1, B: 2}", "(*main.astruct)(0x…", "*main.astruct", nil},
		{"err1.(*main.bstruct)", false, "", "", "", fmt.Errorf("interface conversion: error is *main.astruct, not *main.bstruct")},
		{"errnil.(*main.astruct)", false, "", "", "", fmt.Errorf("interface conversion: error is nil, not *main.astruct")},
		{"const1", true, "go/constant.Value(go/constant.int64Val) 3", "go/constant.Value(go/constant.int64Val) 3", "go/constant.Value", nil},

		// combined expressions
		{"c1.pb.a.A", true, "1", "1", "int", nil},
		{"c1.sa[1].B", false, "3", "3", "int", nil},
		{"s2[5].B", false, "12", "12", "int", nil},
		{"s2[c1.sa[2].B].A", false, "11", "11", "int", nil},
		{"s2[*p2].B", false, "12", "12", "int", nil},

		// constants
		{"1.1", false, "1.1", "1.1", "", nil},
		{"10", false, "10", "10", "", nil},
		{"1 + 2i", false, "(1 + 2i)", "(1 + 2i)", "", nil},
		{"true", false, "true", "true", "", nil},
		{"\"test\"", false, "\"test\"", "\"test\"", "", nil},

		// binary operators
		{"i2 + i3", false, "5", "5", "int", nil},
		{"i2 - i3", false, "-1", "-1", "int", nil},
		{"i3 - i2", false, "1", "1", "int", nil},
		{"i2 * i3", false, "6", "6", "int", nil},
		{"i2/i3", false, "0", "0", "int", nil},
		{"f1/2.0", false, "1.5", "1.5", "float64", nil},
		{"i2 << 2", false, "8", "8", "int", nil},

		// unary operators
		{"-i2", false, "-2", "-2", "int", nil},
		{"+i2", false, "2", "2", "int", nil},
		{"^i2", false, "-3", "-3", "int", nil},

		// comparison operators
		{"i2 == i3", false, "false", "false", "", nil},
		{"i2 == 2", false, "true", "true", "", nil},
		{"i2 == 2", false, "true", "true", "", nil},
		{"i2 == 3", false, "false", "false", "", nil},
		{"i2 != i3", false, "true", "true", "", nil},
		{"i2 < i3", false, "true", "true", "", nil},
		{"i2 <= i3", false, "true", "true", "", nil},
		{"i2 > i3", false, "false", "false", "", nil},
		{"i2 >= i3", false, "false", "false", "", nil},
		{"i2 >= 2", false, "true", "true", "", nil},
		{"str1 == \"01234567890\"", false, "true", "true", "", nil},
		{"str1 < \"01234567890\"", false, "false", "false", "", nil},
		{"str1 < \"11234567890\"", false, "true", "true", "", nil},
		{"str1 > \"00234567890\"", false, "true", "true", "", nil},
		{"str1 == str1", false, "true", "true", "", nil},
		{"c1.pb.a == *(c1.sa[0])", false, "true", "true", "", nil},
		{"c1.pb.a != *(c1.sa[0])", false, "false", "false", "", nil},
		{"c1.pb.a == *(c1.sa[1])", false, "false", "false", "", nil},
		{"c1.pb.a != *(c1.sa[1])", false, "true", "true", "", nil},
		{`longstr == "not this"`, false, "false", "false", "", nil},

		// builtins
		{"cap(parr)", false, "4", "4", "", nil},
		{"len(parr)", false, "4", "4", "", nil},
		{"cap(p1)", false, "", "", "", fmt.Errorf("invalid argument p1 (type *int) for cap")},
		{"len(p1)", false, "", "", "", fmt.Errorf("invalid argument p1 (type *int) for len")},
		{"cap(a1)", false, "5", "5", "", nil},
		{"len(a1)", false, "5", "5", "", nil},
		{"cap(s3)", false, "6", "6", "", nil},
		{"len(s3)", false, "0", "0", "", nil},
		{"cap(nilslice)", false, "0", "0", "", nil},
		{"len(nilslice)", false, "0", "0", "", nil},
		{"cap(ch1)", false, "11", "11", "", nil},
		{"len(ch1)", false, "4", "4", "", nil},
		{"cap(chnil)", false, "0", "0", "", nil},
		{"len(chnil)", false, "0", "0", "", nil},
		{"len(m1)", false, "66", "66", "", nil},
		{"len(mnil)", false, "0", "0", "", nil},
		{"imag(cpx1)", false, "2", "2", "", nil},
		{"real(cpx1)", false, "1", "1", "", nil},
		{"imag(3i)", false, "3", "3", "", nil},
		{"real(4)", false, "4", "4", "", nil},

		// nil
		{"nil", false, "nil", "nil", "", nil},
		{"nil+1", false, "", "", "", fmt.Errorf("operator + can not be applied to \"nil\"")},
		{"fn1", false, "main.afunc", "main.afunc", "main.functype", nil},
		{"fn2", false, "nil", "nil", "main.functype", nil},
		{"nilslice", false, "[]int len: 0, cap: 0, nil", "[]int len: 0, cap: 0, nil", "[]int", nil},
		{"fn1 == fn2", false, "", "", "", fmt.Errorf("can not compare func variables")},
		{"fn1 == nil", false, "false", "false", "", nil},
		{"fn1 != nil", false, "true", "true", "", nil},
		{"fn2 == nil", false, "true", "true", "", nil},
		{"fn2 != nil", false, "false", "false", "", nil},
		{"c1.sa == nil", false, "false", "false", "", nil},
		{"c1.sa != nil", false, "true", "true", "", nil},
		{"c1.sa[0] == nil", false, "false", "false", "", nil},
		{"c1.sa[0] != nil", false, "true", "true", "", nil},
		{"nilslice == nil", false, "true", "true", "", nil},
		{"nil == nilslice", false, "true", "true", "", nil},
		{"nilslice != nil", false, "false", "false", "", nil},
		{"nilptr == nil", false, "true", "true", "", nil},
		{"nilptr != nil", false, "false", "false", "", nil},
		{"p1 == nil", false, "false", "false", "", nil},
		{"p1 != nil", false, "true", "true", "", nil},
		{"ch1 == nil", false, "false", "false", "", nil},
		{"chnil == nil", false, "true", "true", "", nil},
		{"ch1 == chnil", false, "", "", "", fmt.Errorf("can not compare chan variables")},
		{"m1 == nil", false, "false", "false", "", nil},
		{"mnil == m1", false, "", "", "", fmt.Errorf("can not compare map variables")},
		{"mnil == nil", false, "true", "true", "", nil},
		{"nil == 2", false, "", "", "", fmt.Errorf("can not compare int to nil")},
		{"2 == nil", false, "", "", "", fmt.Errorf("can not compare int to nil")},

		// errors
		{"&3", false, "", "", "", fmt.Errorf("can not take address of \"3\"")},
		{"*3", false, "", "", "", fmt.Errorf("expression \"3\" (int) can not be dereferenced")},
		{"&(i2 + i3)", false, "", "", "", fmt.Errorf("can not take address of \"(i2 + i3)\"")},
		{"i2 + p1", false, "", "", "", fmt.Errorf("mismatched types \"int\" and \"*int\"")},
		{"i2 + f1", false, "", "", "", fmt.Errorf("mismatched types \"int\" and \"float64\"")},
		{"i2 << f1", false, "", "", "", fmt.Errorf("shift count type float64, must be unsigned integer")},
		{"i2 << -1", false, "", "", "", fmt.Errorf("shift count must not be negative")},
		{"*(i2 + i3)", false, "", "", "", fmt.Errorf("expression \"(i2 + i3)\" (int) can not be dereferenced")},
		{"i2.member", false, "", "", "", fmt.Errorf("i2 (type int) is not a struct")},
		{"fmt.Println(\"hello\")", false, "", "", "", fmt.Errorf("function calls not allowed without using 'call'")},
		{"*nil", false, "", "", "", fmt.Errorf("nil can not be dereferenced")},
		{"!nil", false, "", "", "", fmt.Errorf("operator ! can not be applied to \"nil\"")},
		{"&nil", false, "", "", "", fmt.Errorf("can not take address of \"nil\"")},
		{"nil[0]", false, "", "", "", fmt.Errorf("expression \"nil\" (nil) does not support indexing")},
		{"nil[2:10]", false, "", "", "", fmt.Errorf("can not slice \"nil\" (type nil)")},
		{"nil.member", false, "", "", "", fmt.Errorf("nil (type nil) is not a struct")},
		{"(map[string]main.astruct)(0x4000)", false, "", "", "", fmt.Errorf("can not convert \"0x4000\" to map[string]main.astruct")},

		// typecasts
		{"uint(i2)", false, "2", "2", "uint", nil},
		{"int8(i2)", false, "2", "2", "int8", nil},
		{"int(f1)", false, "3", "3", "int", nil},
		{"complex128(f1)", false, "(3 + 0i)", "(3 + 0i)", "complex128", nil},
		{"uint8(i4)", false, "32", "32", "uint8", nil},
		{"uint8(i5)", false, "253", "253", "uint8", nil},
		{"int8(i5)", false, "-3", "-3", "int8", nil},
		{"int8(i6)", false, "12", "12", "int8", nil},
		{"string(byteslice[0])", false, `"t"`, `"t"`, "string", nil},
		{"string(runeslice[0])", false, `"t"`, `"t"`, "string", nil},

		// misc
		{"i1", true, "1", "1", "int", nil},
		{"mainMenu", true, `main.Menu len: 3, cap: 3, [{Name: "home", Route: "/", Active: 1},{Name: "About", Route: "/about", Active: 1},{Name: "Login", Route: "/login", Active: 1}]`, `main.Menu len: 3, cap: 3, [...]`, "main.Menu", nil},
		{"mainMenu[0]", false, `main.Item {Name: "home", Route: "/", Active: 1}`, `main.Item {Name: "home", Route: "/", Active: 1}`, "main.Item", nil},
		{"sd", false, "main.D {u1: 0, u2: 0, u3: 0, u4: 0, u5: 0, u6: 0}", "main.D {u1: 0, u2: 0, u3: 0,...+3 more}", "main.D", nil},

		{"ifacearr", false, "[]error len: 2, cap: 2, [*main.astruct {A: 0, B: 0},nil]", "[]error len: 2, cap: 2, [...]", "[]error", nil},
		{"efacearr", false, `[]interface {} len: 3, cap: 3, [*main.astruct {A: 0, B: 0},"test",nil]`, "[]interface {} len: 3, cap: 3, [...]", "[]interface {}", nil},

		{"zsslice", false, `[]struct {} len: 3, cap: 3, [{},{},{}]`, `[]struct {} len: 3, cap: 3, [...]`, "[]struct {}", nil},
		{"zsvmap", false, `map[string]struct {} ["testkey": {}, ]`, `map[string]struct {} [...]`, "map[string]struct {}", nil},
		{"tm", false, "main.truncatedMap {v: []map[string]main.astruct len: 1, cap: 1, [[...]]}", "main.truncatedMap {v: []map[string]main.astruct len: 1, cap: 1, [...]}", "main.truncatedMap", nil},

		{"emptyslice", false, `[]string len: 0, cap: 0, []`, `[]string len: 0, cap: 0, []`, "[]string", nil},
		{"emptymap", false, `map[string]string []`, `map[string]string []`, "map[string]string", nil},
		{"mnil", false, `map[string]main.astruct nil`, `map[string]main.astruct nil`, "map[string]main.astruct", nil},

		// conversions between string/[]byte/[]rune (issue #548)
		{"runeslice", true, `[]int32 len: 4, cap: 4, [116,232,115,116]`, `[]int32 len: 4, cap: 4, [...]`, "[]int32", nil},
		{"byteslice", true, `[]uint8 len: 5, cap: 5, [116,195,168,115,116]`, `[]uint8 len: 5, cap: 5, [...]`, "[]uint8", nil},
		{"[]byte(str1)", false, `[]uint8 len: 11, cap: 11, [48,49,50,51,52,53,54,55,56,57,48]`, `[]uint8 len: 11, cap: 11, [48,49,50,51,52,53,54,55,56,57,48]`, "[]uint8", nil},
		{"[]uint8(str1)", false, `[]uint8 len: 11, cap: 11, [48,49,50,51,52,53,54,55,56,57,48]`, `[]uint8 len: 11, cap: 11, [48,49,50,51,52,53,54,55,56,57,48]`, "[]uint8", nil},
		{"[]rune(str1)", false, `[]int32 len: 11, cap: 11, [48,49,50,51,52,53,54,55,56,57,48]`, `[]int32 len: 11, cap: 11, [48,49,50,51,52,53,54,55,56,57,48]`, "[]int32", nil},
		{"[]int32(str1)", false, `[]int32 len: 11, cap: 11, [48,49,50,51,52,53,54,55,56,57,48]`, `[]int32 len: 11, cap: 11, [48,49,50,51,52,53,54,55,56,57,48]`, "[]int32", nil},
		{"string(byteslice)", false, `"tèst"`, `""`, "string", nil},
		{"[]int32(string(byteslice))", false, `[]int32 len: 4, cap: 4, [116,232,115,116]`, `[]int32 len: 0, cap: 0, nil`, "[]int32", nil},
		{"string(runeslice)", false, `"tèst"`, `""`, "string", nil},
		{"[]byte(string(runeslice))", false, `[]uint8 len: 5, cap: 5, [116,195,168,115,116]`, `[]uint8 len: 0, cap: 0, nil`, "[]uint8", nil},
		{"*(*[5]byte)(uintptr(&byteslice[0]))", false, `[5]uint8 [116,195,168,115,116]`, `[5]uint8 [...]`, "[5]uint8", nil},
		{"string(bytearray)", false, `"tèst"`, `""`, "string", nil},
		{"string(runearray)", false, `"tèst"`, `""`, "string", nil},
		{"string(str1)", false, `"01234567890"`, `"01234567890"`, "string", nil},

		// access to channel field members
		{"ch1.qcount", false, "4", "4", "uint", nil},
		{"ch1.dataqsiz", false, "11", "11", "uint", nil},
		{"ch1.buf", false, `*[11]int [1,4,3,2,0,0,0,0,0,0,0]`, `(*[11]int)(…`, "*[11]int", nil},
		{"ch1.buf[0]", false, "1", "1", "int", nil},

		// shortcircuited logical operators
		{"nilstruct != nil && nilstruct.A == 1", false, "false", "false", "", nil},
		{"nilstruct == nil || nilstruct.A == 1", false, "true", "true", "", nil},

		{"afunc", true, `main.afunc`, `main.afunc`, `func()`, nil},
		{"main.afunc2", true, `main.afunc2`, `main.afunc2`, `func()`, nil},

		{"s2[0].Error", false, "main.(*astruct).Error", "main.(*astruct).Error", "func() string", nil},
		{"s2[0].NonPointerRecieverMethod", false, "main.astruct.NonPointerRecieverMethod", "main.astruct.NonPointerRecieverMethod", "func()", nil},
		{"as2.Error", false, "main.(*astruct).Error", "main.(*astruct).Error", "func() string", nil},
		{"as2.NonPointerRecieverMethod", false, "main.astruct.NonPointerRecieverMethod", "main.astruct.NonPointerRecieverMethod", "func()", nil},

		{`iface2map.(data)`, false, "…", "…", "map[string]interface {}", nil},

		{"issue1578", false, "main.Block {cache: *main.Cache nil}", "main.Block {cache: *main.Cache nil}", "main.Block", nil},
	}

	ver, _ := goversion.Parse(runtime.Version())
	if ver.Major >= 0 && !ver.AfterOrEqual(goversion.GoVersion{1, 7, -1, 0, 0, ""}) {
		for i := range testcases {
			if testcases[i].name == "iface3" {
				testcases[i].value = "interface {}(*map[string]go/constant.Value) *[]"
				testcases[i].alternate = "interface {}(*map[string]go/constant.Value) 0x…"
			}
		}
	}

	protest.AllowRecording(t)
	withTestTarget("testvariables2", t, func(tgt *debug.Target, fixture protest.Fixture) {
		assertNoError(tgt.Continue(), t, "Continue() returned an error")
		for _, tc := range testcases {
			variable, err := evalVariable(tgt, tc.name, pnormalLoadConfig)
			if err != nil && err.Error() == "evaluating methods not supported on this version of Go" {
				// this type of eval is unsupported with the current version of Go.
				continue
			}
			if tc.err == nil {
				assertNoError(err, t, fmt.Sprintf("EvalExpression(%s) returned an error", tc.name))
				assertVariable(t, variable, tc)
				variable, err := evalVariable(tgt, tc.name, pshortLoadConfig)
				assertNoError(err, t, fmt.Sprintf("EvalExpression(%s, pshortLoadConfig) returned an error", tc.name))
				assertVariable(t, variable, tc.alternateVarTest())
			} else {
				if err == nil {
					t.Fatalf("Expected error %s, got no error (%s)", tc.err.Error(), tc.name)
				}
				if tc.err.Error() != err.Error() {
					t.Fatalf("Unexpected error. Expected %s got %s", tc.err.Error(), err.Error())
				}
			}

		}
	})
}

func TestEvalAddrAndCast(t *testing.T) {
	protest.AllowRecording(t)
	withTestTarget("testvariables2", t, func(tgt *debug.Target, fixture protest.Fixture) {
		assertNoError(tgt.Continue(), t, "Continue() returned an error")
		c1addr, err := evalVariable(tgt, "&c1", pnormalLoadConfig)
		assertNoError(err, t, "EvalExpression(&c1)")
		c1addrstr := api.ConvertVar(c1addr).SinglelineString()
		t.Logf("&c1 → %s", c1addrstr)
		if !strings.HasPrefix(c1addrstr, "(*main.cstruct)(0x") {
			t.Fatalf("Invalid value of EvalExpression(&c1) \"%s\"", c1addrstr)
		}

		aaddr, err := evalVariable(tgt, "&(c1.pb.a)", pnormalLoadConfig)
		assertNoError(err, t, "EvalExpression(&(c1.pb.a))")
		aaddrstr := api.ConvertVar(aaddr).SinglelineString()
		t.Logf("&(c1.pb.a) → %s", aaddrstr)
		if !strings.HasPrefix(aaddrstr, "(*main.astruct)(0x") {
			t.Fatalf("invalid value of EvalExpression(&(c1.pb.a)) \"%s\"", aaddrstr)
		}

		a, err := evalVariable(tgt, "*"+aaddrstr, pnormalLoadConfig)
		assertNoError(err, t, fmt.Sprintf("EvalExpression(*%s)", aaddrstr))
		t.Logf("*%s → %s", aaddrstr, api.ConvertVar(a).SinglelineString())
		assertVariable(t, a, varTest{aaddrstr, false, "main.astruct {A: 1, B: 2}", "", "main.astruct", nil})
	})
}

func TestMapEvaluation(t *testing.T) {
	protest.AllowRecording(t)
	withTestTarget("testvariables2", t, func(tgt *debug.Target, fixture protest.Fixture) {
		assertNoError(tgt.Continue(), t, "Continue() returned an error")
		m1v, err := evalVariable(tgt, "m1", pnormalLoadConfig)
		assertNoError(err, t, "EvalVariable()")
		m1 := api.ConvertVar(m1v)
		t.Logf("m1 = %v", m1.MultilineString(""))

		if m1.Type != "map[string]main.astruct" {
			t.Fatalf("Wrong type: %s", m1.Type)
		}

		if len(m1.Children)/2 != 64 {
			t.Fatalf("Wrong number of children: %d", len(m1.Children)/2)
		}

		m1sliced, err := evalVariable(tgt, "m1[64:]", pnormalLoadConfig)
		assertNoError(err, t, "EvalVariable(m1[64:])")
		if len(m1sliced.Children)/2 != int(m1.Len-64) {
			t.Fatalf("Wrong number of children (after slicing): %d", len(m1sliced.Children)/2)
		}

		countMalone := func(m *api.Variable) int {
			found := 0
			for i := range m.Children {
				if i%2 == 0 && m.Children[i].Value == "Malone" {
					found++
				}
			}
			return found
		}

		found := countMalone(m1)
		found += countMalone(api.ConvertVar(m1sliced))

		if found != 1 {
			t.Fatalf("Could not find Malone exactly 1 time: found %d", found)
		}
	})
}

func TestUnsafePointer(t *testing.T) {
	protest.AllowRecording(t)
	withTestTarget("testvariables2", t, func(tgt *debug.Target, fixture protest.Fixture) {
		assertNoError(tgt.Continue(), t, "Continue() returned an error")
		up1v, err := evalVariable(tgt, "up1", pnormalLoadConfig)
		assertNoError(err, t, "EvalVariable(up1)")
		up1 := api.ConvertVar(up1v)
		if ss := up1.SinglelineString(); !strings.HasPrefix(ss, "unsafe.Pointer(") {
			t.Fatalf("wrong value for up1: %s", ss)
		}
	})
}

type issue426TestCase struct {
	name string
	typ  string
}

func TestIssue426(t *testing.T) {
	// type casts using quoted type names
	testcases := []issue426TestCase{
		{"iface1", `interface {}`},
		{"mapanonstruct1", `map[string]struct {}`},
		{"anonstruct1", `struct { val go/constant.Value }`},
		{"anonfunc", `func(struct { i int }, interface {}, struct { val go/constant.Value })`},
		{"anonstruct2", `struct { i int; j int }`},
		{"anoniface1", `interface { OtherFunction(int, int); SomeFunction(struct { val go/constant.Value }) }`},
	}

	ver, _ := goversion.Parse(runtime.Version())
	if ver.Major < 0 || ver.AfterOrEqual(goversion.GoVersion{1, 8, -1, 0, 0, ""}) {
		testcases[2].typ = `struct { main.val go/constant.Value }`
		testcases[3].typ = `func(struct { main.i int }, interface {}, struct { main.val go/constant.Value })`
		testcases[4].typ = `struct { main.i int; main.j int }`
		testcases[5].typ = `interface { OtherFunction(int, int); SomeFunction(struct { main.val go/constant.Value }) }`
	}

	// Serialization of type expressions (go/ast.Expr) containing anonymous structs or interfaces
	// differs from the serialization used by the linker to produce DWARF type information
	protest.AllowRecording(t)
	withTestTarget("testvariables2", t, func(tgt *debug.Target, fixture protest.Fixture) {
		assertNoError(tgt.Continue(), t, "Continue() returned an error")
		for _, testcase := range testcases {
			v, err := evalVariable(tgt, testcase.name, pnormalLoadConfig)
			assertNoError(err, t, fmt.Sprintf("EvalVariable(%s)", testcase.name))
			t.Logf("%s → %s", testcase.name, v.RealType.String())
			expr := fmt.Sprintf("(*%q)(%d)", testcase.typ, v.Addr)
			_, err = evalVariable(tgt, expr, pnormalLoadConfig)
			assertNoError(err, t, fmt.Sprintf("EvalVariable(%s)", expr))
		}
	})
}

func testPackageRenamesHelper(t *testing.T, tgt *debug.Target, testcases []varTest) {
	for _, tc := range testcases {
		variable, err := evalVariable(tgt, tc.name, pnormalLoadConfig)
		if tc.err == nil {
			assertNoError(err, t, fmt.Sprintf("EvalExpression(%s) returned an error", tc.name))
			assertVariable(t, variable, tc)
		} else {
			if err == nil {
				t.Fatalf("Expected error %s, got no error (%s)", tc.err.Error(), tc.name)
			}
			if tc.err.Error() != err.Error() {
				t.Fatalf("Unexpected error. Expected %s got %s", tc.err.Error(), err.Error())
			}
		}
	}
}

func TestPackageRenames(t *testing.T) {
	// Tests that the concrete type of an interface variable is resolved
	// correctly in a few edge cases, in particular:
	// - in the presence of renamed imports
	// - when two packages with the same name are imported
	// - when a package has a canonical name that's different from its
	// path (for example the last element of the path contains a '.' or a
	// '-' or because the package name is different)
	// all of those edge cases are tested within composite types
	testcases := []varTest{
		// Renamed imports
		{"badexpr", true, `interface {}(*go/ast.BadExpr) *{From: 1, To: 2}`, "", "interface {}", nil},
		{"req", true, `interface {}(*net/http.Request) *{Method: "amethod", …`, "", "interface {}", nil},
		{"amap", true, "interface {}(map[go/ast.BadExpr]net/http.Request) [{From: 2, To: 3}: *{Method: \"othermethod\", …", "", "interface {}", nil},

		// Package name that doesn't match import path
		{"iface3", true, `interface {}(*github.com/go-delve/delve/_fixtures/internal/dir0/renamedpackage.SomeType) *{A: true}`, "", "interface {}", nil},

		// Interfaces to anonymous types
		{"amap2", true, "interface {}(*map[go/ast.BadExpr]net/http.Request) *[{From: 2, To: 3}: *{Method: \"othermethod\", …", "", "interface {}", nil},
		{"dir0someType", true, "interface {}(*github.com/go-delve/delve/_fixtures/internal/dir0/pkg.SomeType) *{X: 3}", "", "interface {}", nil},
		{"dir1someType", true, "interface {}(github.com/go-delve/delve/_fixtures/internal/dir1/pkg.SomeType) {X: 1, Y: 2}", "", "interface {}", nil},
		{"amap3", true, "interface {}(map[github.com/go-delve/delve/_fixtures/internal/dir0/pkg.SomeType]github.com/go-delve/delve/_fixtures/internal/dir1/pkg.SomeType) [{X: 4}: {X: 5, Y: 6}, ]", "", "interface {}", nil},
		{"anarray", true, `interface {}([2]github.com/go-delve/delve/_fixtures/internal/dir0/pkg.SomeType) [{X: 1},{X: 2}]`, "", "interface {}", nil},
		{"achan", true, `interface {}(chan github.com/go-delve/delve/_fixtures/internal/dir0/pkg.SomeType) chan github.com/go-delve/delve/_fixtures/internal/dir0/pkg.SomeType 0/0`, "", "interface {}", nil},
		{"aslice", true, `interface {}([]github.com/go-delve/delve/_fixtures/internal/dir0/pkg.SomeType) [{X: 3},{X: 4}]`, "", "interface {}", nil},
		{"afunc", true, `interface {}(func(github.com/go-delve/delve/_fixtures/internal/dir0/pkg.SomeType, github.com/go-delve/delve/_fixtures/internal/dir1/pkg.SomeType)) main.main.func1`, "", "interface {}", nil},
		{"astruct", true, `interface {}(*struct { A github.com/go-delve/delve/_fixtures/internal/dir1/pkg.SomeType; B github.com/go-delve/delve/_fixtures/internal/dir0/pkg.SomeType }) *{A: github.com/go-delve/delve/_fixtures/internal/dir1/pkg.SomeType {X: 1, Y: 2}, B: github.com/go-delve/delve/_fixtures/internal/dir0/pkg.SomeType {X: 3}}`, "", "interface {}", nil},
		{"iface2iface", true, `interface {}(*interface { AMethod(int) int; AnotherMethod(int) int }) **github.com/go-delve/delve/_fixtures/internal/dir0/pkg.SomeType {X: 4}`, "", "interface {}", nil},

		{`"dir0/pkg".A`, false, "0", "", "int", nil},
		{`"dir1/pkg".A`, false, "1", "", "int", nil},
	}

	testcases1_8 := []varTest{
		// before 1.9 embedded struct fields have fieldname == type
		{"astruct2", true, `interface {}(*struct { github.com/go-delve/delve/_fixtures/internal/dir1/pkg.SomeType; X int }) *{github.com/go-delve/delve/_fixtures/internal/dir1/pkg.SomeType: github.com/go-delve/delve/_fixtures/internal/dir1/pkg.SomeType {X: 1, Y: 2}, X: 10}`, "", "interface {}", nil},
	}

	testcases1_9 := []varTest{
		{"astruct2", true, `interface {}(*struct { github.com/go-delve/delve/_fixtures/internal/dir1/pkg.SomeType; X int }) *{SomeType: github.com/go-delve/delve/_fixtures/internal/dir1/pkg.SomeType {X: 1, Y: 2}, X: 10}`, "", "interface {}", nil},
	}

	testcases1_13 := []varTest{
		// needs DW_AT_go_package_name attribute added to Go1.13
		{`dirio.A`, false, `"something"`, "", "string", nil},
	}

	if !goversion.VersionAfterOrEqual(runtime.Version(), 1, 7) {
		return
	}

	protest.AllowRecording(t)
	withTestTarget("pkgrenames", t, func(tgt *debug.Target, fixture protest.Fixture) {
		assertNoError(tgt.Continue(), t, "Continue() returned an error")
		testPackageRenamesHelper(t, tgt, testcases)

		if goversion.VersionAfterOrEqual(runtime.Version(), 1, 9) {
			testPackageRenamesHelper(t, tgt, testcases1_9)
		} else {
			testPackageRenamesHelper(t, tgt, testcases1_8)
		}

		if goversion.VersionAfterOrEqual(runtime.Version(), 1, 13) {
			testPackageRenamesHelper(t, tgt, testcases1_13)
		}
	})
}

func TestConstants(t *testing.T) {
	testcases := []varTest{
		{"a", true, "constTwo (2)", "", "main.ConstType", nil},
		{"b", true, "constThree (3)", "", "main.ConstType", nil},
		{"c", true, "bitZero|bitOne (3)", "", "main.BitFieldType", nil},
		{"d", true, "33", "", "main.BitFieldType", nil},
		{"e", true, "10", "", "main.ConstType", nil},
		{"f", true, "0", "", "main.BitFieldType", nil},
		{"bitZero", true, "1", "", "main.BitFieldType", nil},
		{"bitOne", true, "2", "", "main.BitFieldType", nil},
		{"constTwo", true, "2", "", "main.ConstType", nil},
		{"pkg.SomeConst", false, "2", "", "int", nil},
	}
	ver, _ := goversion.Parse(runtime.Version())
	if ver.Major > 0 && !ver.AfterOrEqual(goversion.GoVersion{1, 10, -1, 0, 0, ""}) {
		// Not supported on 1.9 or earlier
		t.Skip("constants added in go 1.10")
	}
	withTestTarget("consts", t, func(tgt *debug.Target, fixture protest.Fixture) {
		assertNoError(tgt.Continue(), t, "Continue")
		for _, testcase := range testcases {
			variable, err := evalVariable(tgt, testcase.name, pnormalLoadConfig)
			assertNoError(err, t, fmt.Sprintf("EvalVariable(%s)", testcase.name))
			assertVariable(t, variable, testcase)
		}
	})
}

func setFunctionBreakpoint(tgt *debug.Target, t testing.TB, fname string) *proc.Breakpoint {
	_, f, l, _ := runtime.Caller(1)
	f = filepath.Base(f)

	addrs, err := tgt.BinInfo().FindFunctionLocation(tgt, tgt.Breakpoints(), fname, 0)
	if err != nil {
		t.Fatalf("%s:%d: FindFunctionLocation(%s): %v", f, l, fname, err)
	}
	if len(addrs) != 1 {
		t.Fatalf("%s:%d: setFunctionBreakpoint(%s): too many results %v", f, l, fname, addrs)
	}
	bp, err := tgt.SetBreakpoint(addrs[0], proc.UserBreakpoint, nil)
	if err != nil {
		t.Fatalf("%s:%d: SetBreakpoint(%#v, %v): %v", f, l, addrs[0], proc.UserBreakpoint, err)
	}
	return bp
}

func TestIssue1075(t *testing.T) {
	withTestTarget("clientdo", t, func(tgt *debug.Target, fixture protest.Fixture) {
		setFunctionBreakpoint(tgt, t, "net/http.(*Client).Do")
		assertNoError(tgt.Continue(), t, "Continue()")
		for i := 0; i < 10; i++ {
			scope, err := proc.GoroutineScope(tgt.CurrentThread())
			assertNoError(err, t, fmt.Sprintf("GoroutineScope (%d)", i))
			vars, err := scope.LocalVariables(pnormalLoadConfig)
			assertNoError(err, t, fmt.Sprintf("LocalVariables (%d)", i))
			for _, v := range vars {
				api.ConvertVar(v).SinglelineString()
			}
		}
	})
}

type testCaseCallFunction struct {
	expr string   // call expression to evaluate
	outs []string // list of return parameters in this format: <param name>:<param type>:<param value>
	err  error    // if not nil should return an error
}

func TestCallFunction(t *testing.T) {
	if runtime.GOARCH == "arm64" {
		t.Skip("arm64 does not support CallFunction for now")
	}
	protest.MustSupportFunctionCalls(t, testBackend)

	var testcases = []testCaseCallFunction{
		// Basic function call injection tests

		{"call1(one, two)", []string{":int:3"}, nil},
		{"call1(one+two, 4)", []string{":int:7"}, nil},
		{"callpanic()", []string{`~panic:interface {}:interface {}(string) "callpanic panicked"`}, nil},
		{`stringsJoin(nil, "")`, []string{`:string:""`}, nil},
		{`stringsJoin(stringslice, comma)`, []string{`:string:"one,two,three"`}, nil},
		{`stringsJoin(s1, comma)`, nil, errors.New(`error evaluating "s1" as argument v in function main.stringsJoin: could not find symbol value for s1`)},
		{`stringsJoin(intslice, comma)`, nil, errors.New("can not convert value of type []int to []string")},
		{`noreturncall(2)`, nil, nil},

		// Expression tests
		{`square(2) + 1`, []string{":int:5"}, nil},
		{`intcallpanic(1) + 1`, []string{":int:2"}, nil},
		{`intcallpanic(0) + 1`, []string{`~panic:interface {}:interface {}(string) "panic requested"`}, nil},
		{`onetwothree(5)[1] + 2`, []string{":int:9"}, nil},

		// Call types tests (methods, function pointers, etc.)
		// The following set of calls was constructed using https://docs.google.com/document/d/1bMwCey-gmqZVTpRax-ESeVuZGmjwbocYs1iHplK-cjo/pub as a reference

		{`a.VRcvr(1)`, []string{`:string:"1 + 3 = 4"`}, nil}, // direct call of a method with value receiver / on a value

		{`a.PRcvr(2)`, []string{`:string:"2 - 3 = -1"`}, nil},  // direct call of a method with pointer receiver / on a value
		{`pa.VRcvr(3)`, []string{`:string:"3 + 6 = 9"`}, nil},  // direct call of a method with value receiver / on a pointer
		{`pa.PRcvr(4)`, []string{`:string:"4 - 6 = -2"`}, nil}, // direct call of a method with pointer receiver / on a pointer

		{`vable_pa.VRcvr(6)`, []string{`:string:"6 + 6 = 12"`}, nil}, // indirect call of method on interface / containing value with value method
		{`pable_pa.PRcvr(7)`, []string{`:string:"7 - 6 = 1"`}, nil},  // indirect call of method on interface / containing pointer with value method
		{`vable_a.VRcvr(5)`, []string{`:string:"5 + 3 = 8"`}, nil},   // indirect call of method on interface / containing pointer with pointer method

		{`pa.nonexistent()`, nil, errors.New("pa has no member nonexistent")},
		{`a.nonexistent()`, nil, errors.New("a has no member nonexistent")},
		{`vable_pa.nonexistent()`, nil, errors.New("vable_pa has no member nonexistent")},
		{`vable_a.nonexistent()`, nil, errors.New("vable_a has no member nonexistent")},
		{`pable_pa.nonexistent()`, nil, errors.New("pable_pa has no member nonexistent")},

		{`fn2glob(10, 20)`, []string{":int:30"}, nil},               // indirect call of func value / set to top-level func
		{`fn2clos(11)`, []string{`:string:"1 + 6 + 11 = 18"`}, nil}, // indirect call of func value / set to func literal
		{`fn2clos(12)`, []string{`:string:"2 + 6 + 12 = 20"`}, nil},
		{`fn2valmeth(13)`, []string{`:string:"13 + 6 = 19"`}, nil}, // indirect call of func value / set to value method
		{`fn2ptrmeth(14)`, []string{`:string:"14 - 6 = 8"`}, nil},  // indirect call of func value / set to pointer method

		{"fn2nil()", nil, errors.New("nil pointer dereference")},

		{"ga.PRcvr(2)", []string{`:string:"2 - 0 = 2"`}, nil},

		// Nested function calls tests

		{`onetwothree(intcallpanic(2))`, []string{`:[]int:[]int len: 3, cap: 3, [3,4,5]`}, nil},
		{`onetwothree(intcallpanic(0))`, []string{`~panic:interface {}:interface {}(string) "panic requested"`}, nil},
		{`onetwothree(intcallpanic(2)+1)`, []string{`:[]int:[]int len: 3, cap: 3, [4,5,6]`}, nil},
		{`onetwothree(intcallpanic("not a number"))`, nil, errors.New("can not convert \"not a number\" constant to int")},

		// Variable setting tests
		{`pa2 = getAStructPtr(8); pa2`, []string{`pa2:*main.astruct:*main.astruct {X: 8}`}, nil},

		// Escape tests

		{"escapeArg(&a2)", nil, errors.New("cannot use &a2 as argument pa2 in function main.escapeArg: stack object passed to escaping pointer: pa2")},

		// Issue 1577
		{"1+2", []string{`::3`}, nil},
		{`"de"+"mo"`, []string{`::"demo"`}, nil},
	}

	var testcases112 = []testCaseCallFunction{
		// string allocation requires trusted argument order, which we don't have in Go 1.11
		{`stringsJoin(stringslice, ",")`, []string{`:string:"one,two,three"`}, nil},
		{`str = "a new string"; str`, []string{`str:string:"a new string"`}, nil},

		// support calling optimized functions
		{`strings.Join(nil, "")`, []string{`:string:""`}, nil},
		{`strings.Join(stringslice, comma)`, []string{`:string:"one,two,three"`}, nil},
		{`strings.Join(s1, comma)`, nil, errors.New(`error evaluating "s1" as argument a in function strings.Join: could not find symbol value for s1`)},
		{`strings.Join(intslice, comma)`, nil, errors.New("can not convert value of type []int to []string")},
		{`strings.Join(stringslice, ",")`, []string{`:string:"one,two,three"`}, nil},
		{`strings.LastIndexByte(stringslice[1], 'w')`, []string{":int:1"}, nil},
		{`strings.LastIndexByte(stringslice[1], 'o')`, []string{":int:2"}, nil},
		{`d.Base.Method()`, []string{`:int:4`}, nil},
		{`d.Method()`, []string{`:int:4`}, nil},
	}

	var testcases113 = []testCaseCallFunction{
		{`curriedAdd(2)(3)`, []string{`:int:5`}, nil},

		// Method calls on a value returned by a function

		{`getAStruct(3).VRcvr(1)`, []string{`:string:"1 + 3 = 4"`}, nil}, // direct call of a method with value receiver / on a value

		{`getAStruct(3).PRcvr(2)`, nil, errors.New("cannot use getAStruct(3).PRcvr as argument pa in function main.(*astruct).PRcvr: stack object passed to escaping pointer: pa")}, // direct call of a method with pointer receiver / on a value
		{`getAStructPtr(6).VRcvr(3)`, []string{`:string:"3 + 6 = 9"`}, nil},  // direct call of a method with value receiver / on a pointer
		{`getAStructPtr(6).PRcvr(4)`, []string{`:string:"4 - 6 = -2"`}, nil}, // direct call of a method with pointer receiver / on a pointer

		{`getVRcvrableFromAStruct(3).VRcvr(6)`, []string{`:string:"6 + 3 = 9"`}, nil},     // indirect call of method on interface / containing value with value method
		{`getPRcvrableFromAStructPtr(6).PRcvr(7)`, []string{`:string:"7 - 6 = 1"`}, nil},  // indirect call of method on interface / containing pointer with value method
		{`getVRcvrableFromAStructPtr(6).VRcvr(5)`, []string{`:string:"5 + 6 = 11"`}, nil}, // indirect call of method on interface / containing pointer with pointer method
	}

	withTestTarget("fncall", t, func(tgt *debug.Target, fixture protest.Fixture) {
		_, err := tgt.BinInfo().FindFunctionLocation(tgt, tgt.Breakpoints(), "runtime.debugCallV1", 0)
		if err != nil {
			t.Skip("function calls not supported on this version of go")
		}
		assertNoError(tgt.Continue(), t, "Continue()")
		for _, tc := range testcases {
			testCallFunction(t, tgt, tc)
		}

		if goversion.VersionAfterOrEqual(runtime.Version(), 1, 12) {
			for _, tc := range testcases112 {
				testCallFunction(t, tgt, tc)
			}
		}

		if goversion.VersionAfterOrEqual(runtime.Version(), 1, 13) {
			for _, tc := range testcases113 {
				testCallFunction(t, tgt, tc)
			}
		}

		// LEAVE THIS AS THE LAST ITEM, IT BREAKS THE TARGET PROCESS!!!
		testCallFunction(t, tgt, testCaseCallFunction{"-unsafe escapeArg(&a2)", nil, nil})
	})
}

func testCallFunction(t *testing.T, tgt *debug.Target, tc testCaseCallFunction) {
	const unsafePrefix = "-unsafe "

	var callExpr, varExpr string

	if semicolon := strings.Index(tc.expr, ";"); semicolon >= 0 {
		callExpr = tc.expr[:semicolon]
		varExpr = tc.expr[semicolon+1:]
	} else {
		callExpr = tc.expr
	}

	checkEscape := true
	if strings.HasPrefix(callExpr, unsafePrefix) {
		callExpr = callExpr[len(unsafePrefix):]
		checkEscape = false
	}
	t.Logf("call %q", tc.expr)
	err := proc.EvalExpressionWithCalls(tgt.Process, tgt.SelectedGoroutine(), callExpr, pnormalLoadConfig, checkEscape, tgt.Continue)
	if tc.err != nil {
		t.Logf("\terr = %v\n", err)
		if err == nil {
			t.Fatalf("call %q: expected error %q, got no error", tc.expr, tc.err.Error())
		}
		if tc.err.Error() != err.Error() {
			t.Fatalf("call %q: expected error %q, got %q", tc.expr, tc.err.Error(), err.Error())
		}
		return
	}

	if err != nil {
		t.Fatalf("call %q: error %q", tc.expr, err.Error())
	}

	retvalsVar := tgt.CurrentThread().Common().ReturnValues(pnormalLoadConfig)
	retvals := make([]*api.Variable, len(retvalsVar))

	for i := range retvals {
		retvals[i] = api.ConvertVar(retvalsVar[i])
	}

	if varExpr != "" {
		scope, err := proc.GoroutineScope(tgt.CurrentThread())
		assertNoError(err, t, "GoroutineScope")
		v, err := scope.EvalExpression(varExpr, pnormalLoadConfig)
		assertNoError(err, t, fmt.Sprintf("EvalExpression(%s)", varExpr))
		retvals = append(retvals, api.ConvertVar(v))
	}

	for i := range retvals {
		t.Logf("\t%s = %s", retvals[i].Name, retvals[i].SinglelineString())
	}

	if len(retvals) != len(tc.outs) {
		t.Fatalf("call %q: wrong number of return parameters", tc.expr)
	}

	for i := range retvals {
		outfields := strings.SplitN(tc.outs[i], ":", 3)
		tgtName, tgtType, tgtValue := outfields[0], outfields[1], outfields[2]

		if tgtName != "" && tgtName != retvals[i].Name {
			t.Fatalf("call %q output parameter %d: expected name %q, got %q", tc.expr, i, tgtName, retvals[i].Name)
		}

		if retvals[i].Type != tgtType {
			t.Fatalf("call %q, output parameter %d: expected type %q, got %q", tc.expr, i, tgtType, retvals[i].Type)
		}
		if cvs := retvals[i].SinglelineString(); cvs != tgtValue {
			t.Fatalf("call %q, output parameter %d: expected value %q, got %q", tc.expr, i, tgtValue, cvs)
		}
	}
}

func TestIssue1531(t *testing.T) {
	// Go 1.12 introduced a change to the map representation where empty cells can be marked with 1 instead of just 0.
	withTestTarget("issue1531", t, func(tgt *debug.Target, fixture protest.Fixture) {
		assertNoError(tgt.Continue(), t, "Continue()")

		hasKeys := func(mv *proc.Variable, keys ...string) {
			n := 0
			for i := 0; i < len(mv.Children); i += 2 {
				cv := &mv.Children[i]
				s := constant.StringVal(cv.Value)
				found := false
				for j := range keys {
					if keys[j] == s {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("key %q not allowed", s)
					return
				}
				n++
			}
			if n != len(keys) {
				t.Fatalf("wrong number of keys found")
			}
		}

		mv, err := evalVariable(tgt, "m", pnormalLoadConfig)
		assertNoError(err, t, "EvalVariable(m)")
		cmv := api.ConvertVar(mv)
		t.Logf("m = %s", cmv.SinglelineString())
		hasKeys(mv, "s", "r", "v")

		mmv, err := evalVariable(tgt, "mm", pnormalLoadConfig)
		assertNoError(err, t, "EvalVariable(mm)")
		cmmv := api.ConvertVar(mmv)
		t.Logf("mm = %s", cmmv.SinglelineString())
		hasKeys(mmv, "r", "t", "v")
	})
}

func setFileBreakpoint(tgt *debug.Target, t *testing.T, fixture protest.Fixture, lineno int) *proc.Breakpoint {
	_, f, l, _ := runtime.Caller(1)
	f = filepath.Base(f)

	addrs, err := tgt.BinInfo().FindFileLocation(tgt, tgt.Breakpoints(), fixture.Source, lineno)
	if err != nil {
		t.Fatalf("%s:%d: FindFileLocation(%s, %d): %v", f, l, fixture.Source, lineno, err)
	}
	if len(addrs) != 1 {
		t.Fatalf("%s:%d: setFileLineBreakpoint(%s, %d): too many results %v", f, l, fixture.Source, lineno, addrs)
	}
	bp, err := tgt.SetBreakpoint(addrs[0], proc.UserBreakpoint, nil)
	if err != nil {
		t.Fatalf("%s:%d: SetBreakpoint: %v", f, l, err)
	}
	return bp
}

func currentLocation(tgt *debug.Target, t *testing.T) (pc uint64, f string, ln int, fn *proc.Function) {
	regs, err := tgt.CurrentThread().Registers(false)
	if err != nil {
		t.Fatalf("Registers error: %v", err)
	}
	f, l, fn := tgt.BinInfo().PCToLine(regs.PC())
	t.Logf("at %#x %s:%d %v", regs.PC(), f, l, fn)
	return regs.PC(), f, l, fn
}

func assertCurrentLocationFunction(tgt *debug.Target, t *testing.T, fnname string) {
	_, _, _, fn := currentLocation(tgt, t)
	if fn == nil {
		t.Fatalf("Not in a function")
	}
	if fn.Name != fnname {
		t.Fatalf("Wrong function %s %s", fn.Name, fnname)
	}
}

func TestPluginVariables(t *testing.T) {
	pluginFixtures := protest.WithPlugins(t, protest.AllNonOptimized, "plugin1/", "plugin2/")

	withTestTargetArgs("plugintest2", t, ".", []string{pluginFixtures[0].Path, pluginFixtures[1].Path}, protest.AllNonOptimized, func(tgt *debug.Target, fixture protest.Fixture) {
		setFileBreakpoint(tgt, t, fixture, 41)
		assertNoError(tgt.Continue(), t, "Continue 1")

		bp := setFunctionBreakpoint(tgt, t, "github.com/go-delve/delve/_fixtures/plugin2.TypesTest")
		t.Logf("bp.Addr = %#x", bp.Addr)
		setFunctionBreakpoint(tgt, t, "github.com/go-delve/delve/_fixtures/plugin2.aIsNotNil")

		for _, image := range tgt.BinInfo().Images {
			t.Logf("%#x %s\n", image.StaticBase, image.Path)
		}

		assertNoError(tgt.Continue(), t, "Continue 2")

		// test that PackageVariables returns variables from the executable and plugins
		scope, err := evalScope(tgt)
		assertNoError(err, t, "evalScope")
		allvars, err := scope.PackageVariables(pnormalLoadConfig)
		assertNoError(err, t, "PackageVariables")
		var plugin2AFound, mainExeGlobalFound bool
		for _, v := range allvars {
			switch v.Name {
			case "github.com/go-delve/delve/_fixtures/plugin2.A":
				plugin2AFound = true
			case "main.ExeGlobal":
				mainExeGlobalFound = true
			}
		}
		if !plugin2AFound {
			t.Fatalf("variable plugin2.A not found in the output of PackageVariables")
		}
		if !mainExeGlobalFound {
			t.Fatalf("variable main.ExeGlobal not found in the output of PackageVariables")
		}

		// read interface variable, inside plugin code, with a concrete type defined in the executable
		vs, err := evalVariable(tgt, "s", pnormalLoadConfig)
		assertNoError(err, t, "Eval(s)")
		assertVariable(t, vs, varTest{"s", true, `github.com/go-delve/delve/_fixtures/internal/pluginsupport.Something(*main.asomething) *{n: 2}`, ``, `github.com/go-delve/delve/_fixtures/internal/pluginsupport.Something`, nil})

		// test that the concrete type -> interface{} conversion works across plugins (mostly tests proc.dwarfToRuntimeType)
		assertNoError(setVariable(tgt, "plugin2.A", "main.ExeGlobal"), t, "setVariable(tgtlugin2.A = main.ExeGlobal)")
		assertNoError(tgt.Continue(), t, "Continue 3")
		assertCurrentLocationFunction(tgt, t, "github.com/go-delve/delve/_fixtures/plugin2.aIsNotNil")
		vstr, err := evalVariable(tgt, "str", pnormalLoadConfig)
		assertNoError(err, t, "Eval(str)")
		assertVariable(t, vstr, varTest{"str", true, `"success"`, ``, `string`, nil})

		assertNoError(tgt.StepOut(), t, "StepOut")
		assertNoError(tgt.StepOut(), t, "StepOut")
		assertNoError(tgt.Next(), t, "Next")

		// read interface variable, inside executable code, with a concrete type defined in a plugin
		vb, err := evalVariable(tgt, "b", pnormalLoadConfig)
		assertNoError(err, t, "Eval(b)")
		assertVariable(t, vb, varTest{"b", true, `github.com/go-delve/delve/_fixtures/internal/pluginsupport.SomethingElse(*github.com/go-delve/delve/_fixtures/plugin2.asomethingelse) *{x: 1, y: 4}`, ``, `github.com/go-delve/delve/_fixtures/internal/pluginsupport.SomethingElse`, nil})
	})
}
