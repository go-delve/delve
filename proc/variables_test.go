package proc

import (
	"fmt"
	"sort"
	"strconv"
	"testing"

	protest "github.com/derekparker/delve/proc/test"
)

type varTest struct {
	name    string
	value   string
	varType string
	err     error
}

func assertVariable(t *testing.T, variable *Variable, expected varTest) {
	if variable.Name != expected.name {
		t.Fatalf("Expected %s got %s\n", expected.name, variable.Name)
	}

	if variable.Type != expected.varType {
		t.Fatalf("Expected %s got %s (for variable %s)\n", expected.varType, variable.Type, expected.name)
	}

	if variable.Value != expected.value {
		t.Fatalf("Expected %#v got %#v (for variable %s)\n", expected.value, variable.Value, expected.name)
	}
}

func evalVariable(p *Process, symbol string) (*Variable, error) {
	scope, err := p.CurrentThread.Scope()
	if err != nil {
		return nil, err
	}
	return scope.EvalVariable(symbol)
}

const varTestBreakpointLineNumber = 59

func TestVariableEvaluation(t *testing.T) {
	testcases := []varTest{
		{"a1", "foofoofoofoofoofoo", "struct string", nil},
		{"a10", "ofo", "struct string", nil},
		{"a11", "[3]main.FooBar [{Baz: 1, Bur: a},{Baz: 2, Bur: b},{Baz: 3, Bur: c}]", "[3]main.FooBar", nil},
		{"a12", "[]main.FooBar len: 2, cap: 2, [{Baz: 4, Bur: d},{Baz: 5, Bur: e}]", "struct []main.FooBar", nil},
		{"a13", "[]*main.FooBar len: 3, cap: 3, [*{Baz: 6, Bur: f},*{Baz: 7, Bur: g},*{Baz: 8, Bur: h}]", "struct []*main.FooBar", nil},
		{"a2", "6", "int", nil},
		{"a3", "7.23", "float64", nil},
		{"a4", "[2]int [1,2]", "[2]int", nil},
		{"a5", "[]int len: 5, cap: 5, [1,2,3,4,5]", "struct []int", nil},
		{"a6", "main.FooBar {Baz: 8, Bur: word}", "main.FooBar", nil},
		{"a7", "*main.FooBar {Baz: 5, Bur: strum}", "*main.FooBar", nil},
		{"a8", "main.FooBar2 {Bur: 10, Baz: feh}", "main.FooBar2", nil},
		{"a9", "*main.FooBar nil", "*main.FooBar", nil},
		{"baz", "bazburzum", "struct string", nil},
		{"neg", "-1", "int", nil},
		{"f32", "1.2", "float32", nil},
		{"c64", "(1 + 2i)", "complex64", nil},
		{"c128", "(2 + 3i)", "complex128", nil},
		{"a6.Baz", "8", "int", nil},
		{"a7.Baz", "5", "int", nil},
		{"a8.Baz", "feh", "struct string", nil},
		{"a9.Baz", "nil", "int", fmt.Errorf("a9 is nil")},
		{"a9.NonExistent", "nil", "int", fmt.Errorf("a9 has no member NonExistent")},
		{"a8", "main.FooBar2 {Bur: 10, Baz: feh}", "main.FooBar2", nil}, // reread variable after member
		{"i32", "[2]int32 [1,2]", "[2]int32", nil},
		{"b1", "true", "bool", nil},
		{"b2", "false", "bool", nil}, {"i8", "1", "int8", nil},
		{"u16", "65535", "uint16", nil},
		{"u32", "4294967295", "uint32", nil},
		{"u64", "18446744073709551615", "uint64", nil},
		{"u8", "255", "uint8", nil},
		{"up", "5", "uintptr", nil},
		{"f", "main.barfoo", "func()", nil},
		{"ba", "[]int len: 200, cap: 200, [0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,...+136 more]", "struct []int", nil},
		{"ms", "main.Nest {Level: 0, Nest: *main.Nest {Level: 1, Nest: *main.Nest {...}}}", "main.Nest", nil},
		{"main.p1", "10", "int", nil},
		{"p1", "10", "int", nil},
		{"NonExistent", "", "", fmt.Errorf("could not find symbol value for NonExistent")},
	}

	withTestProcess("testvariables", t, func(p *Process, fixture protest.Fixture) {
		pc, _, _ := p.goSymTable.LineToPC(fixture.Source, varTestBreakpointLineNumber)

		_, err := p.SetBreakpoint(pc)
		assertNoError(err, t, "SetBreakpoint() returned an error")

		err = p.Continue()
		assertNoError(err, t, "Continue() returned an error")

		for _, tc := range testcases {
			variable, err := evalVariable(p, tc.name)
			if tc.err == nil {
				assertNoError(err, t, "EvalVariable() returned an error")
				assertVariable(t, variable, tc)
			} else {
				if tc.err.Error() != err.Error() {
					t.Fatalf("Unexpected error. Expected %s got %s", tc.err.Error(), err.Error())
				}
			}
		}
	})
}

func TestVariableFunctionScoping(t *testing.T) {
	withTestProcess("testvariables", t, func(p *Process, fixture protest.Fixture) {
		pc, _, _ := p.goSymTable.LineToPC(fixture.Source, varTestBreakpointLineNumber)

		_, err := p.SetBreakpoint(pc)
		assertNoError(err, t, "SetBreakpoint() returned an error")

		err = p.Continue()
		assertNoError(err, t, "Continue() returned an error")
		p.ClearBreakpoint(pc)

		_, err = evalVariable(p, "a1")
		assertNoError(err, t, "Unable to find variable a1")

		_, err = evalVariable(p, "a2")
		assertNoError(err, t, "Unable to find variable a1")

		// Move scopes, a1 exists here by a2 does not
		pc, _, _ = p.goSymTable.LineToPC(fixture.Source, 23)

		_, err = p.SetBreakpoint(pc)
		assertNoError(err, t, "SetBreakpoint() returned an error")

		err = p.Continue()
		assertNoError(err, t, "Continue() returned an error")

		_, err = evalVariable(p, "a1")
		assertNoError(err, t, "Unable to find variable a1")

		_, err = evalVariable(p, "a2")
		if err == nil {
			t.Fatalf("Can eval out of scope variable a2")
		}
	})
}

type varArray []*Variable

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
		fn     func(*EvalScope) ([]*Variable, error)
		output []varTest
	}{
		{(*EvalScope).LocalVariables,
			[]varTest{
				{"a1", "foofoofoofoofoofoo", "struct string", nil},
				{"a10", "ofo", "struct string", nil},
				{"a11", "[3]main.FooBar [{Baz: 1, Bur: a},{Baz: 2, Bur: b},{Baz: 3, Bur: c}]", "[3]main.FooBar", nil},
				{"a12", "[]main.FooBar len: 2, cap: 2, [{Baz: 4, Bur: d},{Baz: 5, Bur: e}]", "struct []main.FooBar", nil},
				{"a13", "[]*main.FooBar len: 3, cap: 3, [*{Baz: 6, Bur: f},*{Baz: 7, Bur: g},*{Baz: 8, Bur: h}]", "struct []*main.FooBar", nil},
				{"a2", "6", "int", nil},
				{"a3", "7.23", "float64", nil},
				{"a4", "[2]int [1,2]", "[2]int", nil},
				{"a5", "[]int len: 5, cap: 5, [1,2,3,4,5]", "struct []int", nil},
				{"a6", "main.FooBar {Baz: 8, Bur: word}", "main.FooBar", nil},
				{"a7", "*main.FooBar {Baz: 5, Bur: strum}", "*main.FooBar", nil},
				{"a8", "main.FooBar2 {Bur: 10, Baz: feh}", "main.FooBar2", nil},
				{"a9", "*main.FooBar nil", "*main.FooBar", nil},
				{"b1", "true", "bool", nil},
				{"b2", "false", "bool", nil},
				{"ba", "[]int len: 200, cap: 200, [0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,...+136 more]", "struct []int", nil},
				{"c128", "(2 + 3i)", "complex128", nil},
				{"c64", "(1 + 2i)", "complex64", nil},
				{"f", "main.barfoo", "func()", nil},
				{"f32", "1.2", "float32", nil},
				{"i32", "[2]int32 [1,2]", "[2]int32", nil},
				{"i8", "1", "int8", nil},
				{"ms", "main.Nest {Level: 0, Nest: *main.Nest {Level: 1, Nest: *main.Nest {...}}}", "main.Nest", nil},
				{"neg", "-1", "int", nil},
				{"u16", "65535", "uint16", nil},
				{"u32", "4294967295", "uint32", nil},
				{"u64", "18446744073709551615", "uint64", nil},
				{"u8", "255", "uint8", nil},
				{"up", "5", "uintptr", nil}}},
		{(*EvalScope).FunctionArguments,
			[]varTest{
				{"bar", "main.FooBar {Baz: 10, Bur: lorem}", "main.FooBar", nil},
				{"baz", "bazburzum", "struct string", nil}}},
	}

	withTestProcess("testvariables", t, func(p *Process, fixture protest.Fixture) {
		pc, _, _ := p.goSymTable.LineToPC(fixture.Source, varTestBreakpointLineNumber)

		_, err := p.SetBreakpoint(pc)
		assertNoError(err, t, "SetBreakpoint() returned an error")

		err = p.Continue()
		assertNoError(err, t, "Continue() returned an error")

		for _, tc := range testcases {
			scope, err := p.CurrentThread.Scope()
			assertNoError(err, t, "AsScope()")
			vars, err := tc.fn(scope)
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

func TestRecursiveStructure(t *testing.T) {
	withTestProcess("testvariables2", t, func(p *Process, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue()")
		v, err := evalVariable(p, "aas")
		assertNoError(err, t, "EvalVariable()")
		t.Logf("v: %v\n", v)
	})
}

func TestFrameEvaluation(t *testing.T) {
	withTestProcess("goroutinestackprog", t, func(p *Process, fixture protest.Fixture) {
		_, err := setFunctionBreakpoint(p, "main.stacktraceme")
		assertNoError(err, t, "setFunctionBreakpoint")
		assertNoError(p.Continue(), t, "Continue()")

		/**** Testing evaluation on goroutines ****/
		gs, err := p.GoroutinesInfo()
		assertNoError(err, t, "GoroutinesInfo")
		found := make([]bool, 10)
		for _, g := range gs {
			frame := -1
			frames, err := p.GoroutineStacktrace(g, 10)
			assertNoError(err, t, "GoroutineStacktrace()")
			for i := range frames {
				if frames[i].Call.Fn != nil && frames[i].Call.Fn.Name == "main.agoroutine" {
					frame = i
					break
				}
			}

			if frame < 0 {
				t.Logf("Goroutine %d: could not find correct frame", g.Id)
				continue
			}

			scope, err := p.ConvertEvalScope(g.Id, frame)
			assertNoError(err, t, "ConvertEvalScope()")
			t.Logf("scope = %v", scope)
			v, err := scope.EvalVariable("i")
			t.Logf("v = %v", v)
			if err != nil {
				t.Logf("Goroutine %d: %v\n", g.Id, err)
				continue
			}
			i, err := strconv.Atoi(v.Value)
			assertNoError(err, t, fmt.Sprintf("strconv.Atoi(%s)", v.Value))
			found[i] = true
		}

		for i := range found {
			if !found[i] {
				t.Fatalf("Goroutine %d not found\n", i)
			}
		}

		/**** Testing evaluation on frames ****/
		assertNoError(p.Continue(), t, "Continue() 2")
		g, err := p.CurrentThread.GetG()
		assertNoError(err, t, "GetG()")

		for i := 0; i <= 3; i++ {
			scope, err := p.ConvertEvalScope(g.Id, i+1)
			assertNoError(err, t, fmt.Sprintf("ConvertEvalScope() on frame %d", i+1))
			v, err := scope.EvalVariable("n")
			assertNoError(err, t, fmt.Sprintf("EvalVariable() on frame %d", i+1))
			n, err := strconv.Atoi(v.Value)
			assertNoError(err, t, fmt.Sprintf("strconv.Atoi(%s) on frame %d", v.Value, i+1))
			t.Logf("frame %d n %d\n", i+1, n)
			if n != 3-i {
				t.Fatalf("On frame %d value of n is %d (not %d)", i+1, n, 3-i)
			}
		}
	})
}
