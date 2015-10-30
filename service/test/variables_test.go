package servicetest

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/derekparker/delve/proc"
	"github.com/derekparker/delve/service/api"

	protest "github.com/derekparker/delve/proc/test"
)

type varTest struct {
	name         string
	preserveName bool
	value        string
	setTo        string
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

func evalVariable(p *proc.Process, symbol string) (*proc.Variable, error) {
	scope, err := p.CurrentThread.Scope()
	if err != nil {
		return nil, err
	}
	return scope.EvalVariable(symbol)
}

func (tc *varTest) settable() bool {
	return tc.setTo != ""
}

func (tc *varTest) afterSet() varTest {
	r := *tc
	r.value = r.setTo
	return r
}

func setVariable(p *proc.Process, symbol, value string) error {
	scope, err := p.CurrentThread.Scope()
	if err != nil {
		return err
	}
	return scope.SetVariable(symbol, value)
}

const varTestBreakpointLineNumber = 59

func withTestProcess(name string, t *testing.T, fn func(p *proc.Process, fixture protest.Fixture)) {
	fixture := protest.BuildFixture(name)
	p, err := proc.Launch([]string{fixture.Path})
	if err != nil {
		t.Fatal("Launch():", err)
	}

	defer func() {
		p.Halt()
		p.Kill()
	}()

	fn(p, fixture)
}

func TestVariableEvaluation(t *testing.T) {
	testcases := []varTest{
		{"a1", true, "\"foofoofoofoofoofoo\"", "", "struct string", nil},
		{"a11", true, "[3]main.FooBar [{Baz: 1, Bur: \"a\"},{Baz: 2, Bur: \"b\"},{Baz: 3, Bur: \"c\"}]", "", "[3]main.FooBar", nil},
		{"a12", true, "[]main.FooBar len: 2, cap: 2, [{Baz: 4, Bur: \"d\"},{Baz: 5, Bur: \"e\"}]", "", "struct []main.FooBar", nil},
		{"a13", true, "[]*main.FooBar len: 3, cap: 3, [*{Baz: 6, Bur: \"f\"},*{Baz: 7, Bur: \"g\"},*{Baz: 8, Bur: \"h\"}]", "", "struct []*main.FooBar", nil},
		{"a2", true, "6", "10", "int", nil},
		{"a3", true, "7.23", "3.1", "float64", nil},
		{"a4", true, "[2]int [1,2]", "", "[2]int", nil},
		{"a5", true, "[]int len: 5, cap: 5, [1,2,3,4,5]", "", "struct []int", nil},
		{"a6", true, "main.FooBar {Baz: 8, Bur: \"word\"}", "", "main.FooBar", nil},
		{"a7", true, "*main.FooBar {Baz: 5, Bur: \"strum\"}", "", "*main.FooBar", nil},
		{"a8", true, "main.FooBar2 {Bur: 10, Baz: \"feh\"}", "", "main.FooBar2", nil},
		{"a9", true, "*main.FooBar nil", "", "*main.FooBar", nil},
		{"baz", true, "\"bazburzum\"", "", "struct string", nil},
		{"neg", true, "-1", "-20", "int", nil},
		{"f32", true, "1.2", "1.1", "float32", nil},
		{"c64", true, "(1 + 2i)", "(4 + 5i)", "complex64", nil},
		{"c128", true, "(2 + 3i)", "(6.3 + 7i)", "complex128", nil},
		{"a6.Baz", true, "8", "20", "int", nil},
		{"a7.Baz", true, "5", "25", "int", nil},
		{"a8.Baz", true, "\"feh\"", "", "struct string", nil},
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
		{"ba", true, "[]int len: 200, cap: 200, [0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,...+136 more]", "", "struct []int", nil},
		{"ms", true, "main.Nest {Level: 0, Nest: *main.Nest {Level: 1, Nest: *(*main.Nest)(…", "", "main.Nest", nil},
		{"ms.Nest.Nest", true, "*main.Nest {Level: 2, Nest: *main.Nest {Level: 3, Nest: *(*main.Nest)(…", "", "*main.Nest", nil},
		{"ms.Nest.Nest.Nest.Nest.Nest", true, "*main.Nest nil", "", "*main.Nest", nil},
		{"ms.Nest.Nest.Nest.Nest.Nest.Nest", true, "", "", "*main.Nest", fmt.Errorf("ms.Nest.Nest.Nest.Nest.Nest is nil")},
		{"main.p1", true, "10", "12", "int", nil},
		{"p1", true, "10", "13", "int", nil},
		{"NonExistent", true, "", "", "", fmt.Errorf("could not find symbol value for NonExistent")},
	}

	withTestProcess("testvariables", t, func(p *proc.Process, fixture protest.Fixture) {
		err := p.Continue()
		assertNoError(err, t, "Continue() returned an error")

		for _, tc := range testcases {
			variable, err := evalVariable(p, tc.name)
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

			if tc.settable() {
				assertNoError(setVariable(p, tc.name, tc.setTo), t, "SetVariable()")
				variable, err = evalVariable(p, tc.name)
				assertNoError(err, t, "EvalVariable()")
				assertVariable(t, variable, tc.afterSet())

				assertNoError(setVariable(p, tc.name, tc.value), t, "SetVariable()")
				variable, err := evalVariable(p, tc.name)
				assertNoError(err, t, "EvalVariable()")
				assertVariable(t, variable, tc)
			}
		}
	})
}

func TestMultilineVariableEvaluation(t *testing.T) {
	testcases := []varTest{
		{"a1", true, "\"foofoofoofoofoofoo\"", "", "struct string", nil},
		{"a11", true, `[3]main.FooBar [
	{Baz: 1, Bur: "a"},
	{Baz: 2, Bur: "b"},
	{Baz: 3, Bur: "c"},
]`, "", "[3]main.FooBar", nil},
		{"a12", true, `[]main.FooBar len: 2, cap: 2, [
	{Baz: 4, Bur: "d"},
	{Baz: 5, Bur: "e"},
]`, "", "struct []main.FooBar", nil},
		{"a13", true, `[]*main.FooBar len: 3, cap: 3, [
	*{Baz: 6, Bur: "f"},
	*{Baz: 7, Bur: "g"},
	*{Baz: 8, Bur: "h"},
]`, "", "struct []*main.FooBar", nil},
		{"a2", true, "6", "10", "int", nil},
		{"a4", true, "[2]int [1,2]", "", "[2]int", nil},
		{"a5", true, "[]int len: 5, cap: 5, [1,2,3,4,5]", "", "struct []int", nil},
		{"a6", true, "main.FooBar {Baz: 8, Bur: \"word\"}", "", "main.FooBar", nil},
		{"a7", true, "*main.FooBar {Baz: 5, Bur: \"strum\"}", "", "*main.FooBar", nil},
		{"a8", true, "main.FooBar2 {Bur: 10, Baz: \"feh\"}", "", "main.FooBar2", nil},
		{"a9", true, "*main.FooBar nil", "", "*main.FooBar", nil},
		{"a8", true, "main.FooBar2 {Bur: 10, Baz: \"feh\"}", "", "main.FooBar2", nil}, // reread variable after member
		{"i32", true, "[2]int32 [1,2]", "", "[2]int32", nil},
		{"ba", true, "[]int len: 200, cap: 200, [0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,...+136 more]", "", "struct []int", nil},
		{"ms", true, `main.Nest {
	Level: 0,
	Nest: *main.Nest {
		Level: 1,
		Nest: *(*main.Nest)(…`, "", "main.Nest", nil},
	}

	withTestProcess("testvariables", t, func(p *proc.Process, fixture protest.Fixture) {
		err := p.Continue()
		assertNoError(err, t, "Continue() returned an error")

		for _, tc := range testcases {
			variable, err := evalVariable(p, tc.name)
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
		fn     func(*proc.EvalScope) ([]*proc.Variable, error)
		output []varTest
	}{
		{(*proc.EvalScope).LocalVariables,
			[]varTest{
				{"a1", true, "\"foofoofoofoofoofoo\"", "", "struct string", nil},
				{"a10", true, "\"ofo\"", "", "struct string", nil},
				{"a11", true, "[3]main.FooBar [{Baz: 1, Bur: \"a\"},{Baz: 2, Bur: \"b\"},{Baz: 3, Bur: \"c\"}]", "", "[3]main.FooBar", nil},
				{"a12", true, "[]main.FooBar len: 2, cap: 2, [{Baz: 4, Bur: \"d\"},{Baz: 5, Bur: \"e\"}]", "", "struct []main.FooBar", nil},
				{"a13", true, "[]*main.FooBar len: 3, cap: 3, [*{Baz: 6, Bur: \"f\"},*{Baz: 7, Bur: \"g\"},*{Baz: 8, Bur: \"h\"}]", "", "struct []*main.FooBar", nil},
				{"a2", true, "6", "", "int", nil},
				{"a3", true, "7.23", "", "float64", nil},
				{"a4", true, "[2]int [1,2]", "", "[2]int", nil},
				{"a5", true, "[]int len: 5, cap: 5, [1,2,3,4,5]", "", "struct []int", nil},
				{"a6", true, "main.FooBar {Baz: 8, Bur: \"word\"}", "", "main.FooBar", nil},
				{"a7", true, "*main.FooBar {Baz: 5, Bur: \"strum\"}", "", "*main.FooBar", nil},
				{"a8", true, "main.FooBar2 {Bur: 10, Baz: \"feh\"}", "", "main.FooBar2", nil},
				{"a9", true, "*main.FooBar nil", "", "*main.FooBar", nil},
				{"b1", true, "true", "", "bool", nil},
				{"b2", true, "false", "", "bool", nil},
				{"ba", true, "[]int len: 200, cap: 200, [0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,...+136 more]", "", "struct []int", nil},
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
				{"baz", true, "\"bazburzum\"", "", "struct string", nil}}},
	}

	withTestProcess("testvariables", t, func(p *proc.Process, fixture protest.Fixture) {
		err := p.Continue()
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

func TestEmbeddedStruct(t *testing.T) {
	withTestProcess("testvariables4", t, func(p *proc.Process, fixture protest.Fixture) {
		testcases := []varTest{
			{"b.val", true, "-314", "", "int", nil},
			{"b.A.val", true, "-314", "", "int", nil},
			{"b.a.val", true, "42", "", "int", nil},
			{"b.ptr.val", true, "1337", "", "int", nil},
			{"b.C.s", true, "\"hello\"", "", "struct string", nil},
			{"b.s", true, "\"hello\"", "", "struct string", nil},
			{"b2", true, "main.B {main.A: struct main.A {val: 42}, *main.C: *struct main.C nil, a: main.A {val: 47}, ptr: *main.A nil}", "", "main.B", nil},
		}
		assertNoError(p.Continue(), t, "Continue()")

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

func TestComplexSetting(t *testing.T) {
	withTestProcess("testvariables", t, func(p *proc.Process, fixture protest.Fixture) {
		err := p.Continue()
		assertNoError(err, t, "Continue() returned an error")

		h := func(setExpr, value string) {
			assertNoError(setVariable(p, "c128", setExpr), t, "SetVariable()")
			variable, err := evalVariable(p, "c128")
			assertNoError(err, t, "EvalVariable()")
			if s := api.ConvertVar(variable).SinglelineString(); s != value {
				t.Fatalf("Wrong value of c128: \"%s\", expected \"%s\" after setting it to \"%s\"", s, value, setExpr)
			}
		}

		h("3.2i", "(0 + 3.2i)")
		h("1.1", "(1.1 + 0i)")
		h("1 + 3.3i", "(1 + 3.3i)")
		h("complex128(1.2, 3.4)", "(1.2 + 3.4i)")
	})
}

func TestEvalExpression(t *testing.T) {
	testcases := []varTest{
		// slice/array/string subscript
		{"s1[0]", false, "\"one\"", "", "struct string", nil},
		{"s1[1]", false, "\"two\"", "", "struct string", nil},
		{"s1[2]", false, "\"three\"", "", "struct string", nil},
		{"s1[3]", false, "\"four\"", "", "struct string", nil},
		{"s1[4]", false, "\"five\"", "", "struct string", nil},
		{"s1[5]", false, "", "", "struct string", fmt.Errorf("index out of bounds")},
		{"a1[0]", false, "\"one\"", "", "struct string", nil},
		{"a1[1]", false, "\"two\"", "", "struct string", nil},
		{"a1[2]", false, "\"three\"", "", "struct string", nil},
		{"a1[3]", false, "\"four\"", "", "struct string", nil},
		{"a1[4]", false, "\"five\"", "", "struct string", nil},
		{"a1[5]", false, "", "", "struct string", fmt.Errorf("index out of bounds")},
		{"str1[0]", false, "48", "", "byte", nil},
		{"str1[1]", false, "49", "", "byte", nil},
		{"str1[2]", false, "50", "", "byte", nil},
		{"str1[10]", false, "48", "", "byte", nil},
		{"str1[11]", false, "", "", "byte", fmt.Errorf("index out of bounds")},

		// slice/array/string reslicing
		{"a1[2:4]", false, "[]struct string len: 2, cap: 2, [\"three\",\"four\"]", "", "struct []struct string", nil},
		{"s1[2:4]", false, "[]string len: 2, cap: 2, [\"three\",\"four\"]", "", "struct []string", nil},
		{"str1[2:4]", false, "\"23\"", "", "struct string", nil},
		{"str1[0:11]", false, "\"01234567890\"", "", "struct string", nil},
		{"str1[:3]", false, "\"012\"", "", "struct string", nil},
		{"str1[3:]", false, "\"34567890\"", "", "struct string", nil},
		{"str1[0:12]", false, "", "", "struct string", fmt.Errorf("index out of bounds")},
		{"str1[5:3]", false, "", "", "struct string", fmt.Errorf("index out of bounds")},

		// pointers
		{"*p2", false, "5", "", "int", nil},
		{"p2", true, "*5", "", "*int", nil},
		{"p3", true, "*int nil", "", "*int", nil},
		{"*p3", false, "", "", "int", fmt.Errorf("nil pointer dereference")},

		// combined expressions
		{"c1.pb.a.A", true, "1", "", "int", nil},
		{"c1.sa[1].B", false, "3", "", "int", nil},
		{"s2[5].B", false, "12", "", "int", nil},
		{"s2[c1.sa[2].B].A", false, "11", "", "int", nil},
		{"s2[*p2].B", false, "12", "", "int", nil},

		// constants
		{"1.1", false, "1.1", "", "", nil},
		{"10", false, "10", "", "", nil},
		{"1 + 2i", false, "(1 + 2i)", "", "", nil},
		{"true", false, "true", "", "", nil},
		{"\"test\"", false, "\"test\"", "", "", nil},

		// binary operators
		{"i2 + i3", false, "5", "", "int", nil},
		{"i2 - i3", false, "-1", "", "int", nil},
		{"i3 - i2", false, "1", "", "int", nil},
		{"i2 * i3", false, "6", "", "int", nil},
		{"i2/i3", false, "0", "", "int", nil},
		{"f1/2.0", false, "1.5", "", "float64", nil},
		{"i2 << 2", false, "8", "", "int", nil},

		// unary operators
		{"-i2", false, "-2", "", "int", nil},
		{"+i2", false, "2", "", "int", nil},
		{"^i2", false, "-3", "", "int", nil},

		// comparison operators
		{"i2 == i3", false, "false", "", "", nil},
		{"i2 == 2", false, "true", "", "", nil},
		{"i2 == 2.0", false, "true", "", "", nil},
		{"i2 == 3", false, "false", "", "", nil},
		{"i2 != i3", false, "true", "", "", nil},
		{"i2 < i3", false, "true", "", "", nil},
		{"i2 <= i3", false, "true", "", "", nil},
		{"i2 > i3", false, "false", "", "", nil},
		{"i2 >= i3", false, "false", "", "", nil},
		{"i2 >= 2", false, "true", "", "", nil},
		{"str1 == \"01234567890\"", false, "true", "", "", nil},
		{"str1 < \"01234567890\"", false, "false", "", "", nil},
		{"str1 < \"11234567890\"", false, "true", "", "", nil},
		{"str1 > \"00234567890\"", false, "true", "", "", nil},
		{"str1 == str1", false, "true", "", "", nil},
		{"c1.pb.a == *(c1.sa[0])", false, "true", "", "", nil},
		{"c1.pb.a != *(c1.sa[0])", false, "false", "", "", nil},
		{"c1.pb.a == *(c1.sa[1])", false, "false", "", "", nil},
		{"c1.pb.a != *(c1.sa[1])", false, "true", "", "", nil},

		// nil
		{"nil", false, "nil", "", "", nil},
		{"nil+1", false, "", "", "", fmt.Errorf("operator + can not be applied to \"nil\"")},
		{"fn1", false, "main.afunc", "", "main.functype", nil},
		{"fn2", false, "nil", "", "main.functype", nil},
		{"nilslice", false, "[]int len: 0, cap: 0, []", "", "struct []int", nil},
		{"fn1 == fn2", false, "", "", "", fmt.Errorf("can not compare func variables")},
		{"fn1 == nil", false, "false", "", "", nil},
		{"fn1 != nil", false, "true", "", "", nil},
		{"fn2 == nil", false, "true", "", "", nil},
		{"fn2 != nil", false, "false", "", "", nil},
		{"c1.sa == nil", false, "false", "", "", nil},
		{"c1.sa != nil", false, "true", "", "", nil},
		{"c1.sa[0] == nil", false, "false", "", "", nil},
		{"c1.sa[0] != nil", false, "true", "", "", nil},
		{"nilslice == nil", false, "true", "", "", nil},
		{"nilslice != nil", false, "false", "", "", nil},
		{"nilptr == nil", false, "true", "", "", nil},
		{"nilptr != nil", false, "false", "", "", nil},
		{"p1 == nil", false, "false", "", "", nil},
		{"p1 != nil", false, "true", "", "", nil},

		// errors
		{"&3", false, "", "", "", fmt.Errorf("can not take address of \"3\"")},
		{"*3", false, "", "", "", fmt.Errorf("expression \"3\" can not be dereferenced")},
		{"&(i2 + i3)", false, "", "", "", fmt.Errorf("can not take address of \"(i2 + i3)\"")},
		{"i2 + p1", false, "", "", "", fmt.Errorf("mismatched types \"int\" and \"*int\"")},
		{"i2 + f1", false, "", "", "", fmt.Errorf("mismatched types \"int\" and \"float64\"")},
		{"i2 << f1", false, "", "", "", fmt.Errorf("shift count type float64, must be unsigned integer")},
		{"i2 << -1", false, "", "", "", fmt.Errorf("shift count type int, must be unsigned integer")},
		{"i2 << i3", false, "", "", "int", fmt.Errorf("shift count type int, must be unsigned integer")},
		{"*(i2 + i3)", false, "", "", "", fmt.Errorf("expression \"(i2 + i3)\" (int) can not be dereferenced")},
		{"i2.member", false, "", "", "", fmt.Errorf("i2 (type int) is not a struct")},
		{"fmt.Println(\"hello\")", false, "", "", "", fmt.Errorf("no type entry found")},
	}

	withTestProcess("testvariables3", t, func(p *proc.Process, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue() returned an error")
		for _, tc := range testcases {
			variable, err := evalVariable(p, tc.name)
			if tc.err == nil {
				assertNoError(err, t, fmt.Sprintf("EvalExpression(%s) returned an error", tc.name))
				assertVariable(t, variable, tc)
			} else {
				if err == nil {
					t.Fatalf("Expected error %s, got non (%s)", tc.err.Error(), tc.name)
				}
				if tc.err.Error() != err.Error() {
					t.Fatalf("Unexpected error. Expected %s got %s", tc.err.Error(), err.Error())
				}
			}
		}
	})
}

func TestEvalAddrAndCast(t *testing.T) {
	withTestProcess("testvariables3", t, func(p *proc.Process, fixture protest.Fixture) {
		assertNoError(p.Continue(), t, "Continue() returned an error")
		c1addr, err := evalVariable(p, "&c1")
		assertNoError(err, t, "EvalExpression(&c1)")
		c1addrstr := api.ConvertVar(c1addr).SinglelineString()
		t.Logf("&c1 → %s", c1addrstr)
		if !strings.HasPrefix(c1addrstr, "(*main.cstruct)(0x") {
			t.Fatalf("Invalid value of EvalExpression(&c1) \"%s\"", c1addrstr)
		}

		aaddr, err := evalVariable(p, "&(c1.pb.a)")
		assertNoError(err, t, "EvalExpression(&(c1.pb.a))")
		aaddrstr := api.ConvertVar(aaddr).SinglelineString()
		t.Logf("&(c1.pb.a) → %s", aaddrstr)
		if !strings.HasPrefix(aaddrstr, "(*main.astruct)(0x") {
			t.Fatalf("invalid value of EvalExpression(&(c1.pb.a)) \"%s\"", aaddrstr)
		}

		a, err := evalVariable(p, "*"+aaddrstr)
		assertNoError(err, t, fmt.Sprintf("EvalExpression(*%s)", aaddrstr))
		t.Logf("*%s → %s", aaddrstr, api.ConvertVar(a).SinglelineString())
		assertVariable(t, a, varTest{aaddrstr, false, "struct main.astruct {A: 1, B: 2}", "", "struct main.astruct", nil})
	})
}
