package proctl

import (
	"errors"
	"path/filepath"
	"sort"
	"testing"
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
		t.Fatalf("Expected %s got %s\n", expected.varType, variable.Type)
	}

	if variable.Value != expected.value {
		t.Fatalf("Expected %#v got %#v\n", expected.value, variable.Value)
	}
}

func TestVariableEvaluation(t *testing.T) {
	executablePath := "../_fixtures/testvariables"

	fp, err := filepath.Abs(executablePath + ".go")
	if err != nil {
		t.Fatal(err)
	}

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
		{"a6.Baz", "8", "int", nil},
		{"a7.Baz", "5", "int", nil},
		{"a8.Baz", "feh", "struct string", nil},
		{"a9.Baz", "nil", "int", errors.New("a9 is nil")},
		{"a9.NonExistent", "nil", "int", errors.New("a9 has no member NonExistent")},
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
		{"NonExistent", "", "", errors.New("could not find symbol value for NonExistent")},
	}

	withTestProcess(executablePath, t, func(p *DebuggedProcess) {
		pc, _, _ := p.goSymTable.LineToPC(fp, 57)

		_, err := p.Break(pc)
		assertNoError(err, t, "Break() returned an error")

		err = p.Continue()
		assertNoError(err, t, "Continue() returned an error")

		for _, tc := range testcases {
			variable, err := p.EvalSymbol(tc.name)
			if tc.err == nil {
				assertNoError(err, t, "EvalSymbol() returned an error")
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
	executablePath := "../_fixtures/testvariables"

	fp, err := filepath.Abs(executablePath + ".go")
	if err != nil {
		t.Fatal(err)
	}

	withTestProcess(executablePath, t, func(p *DebuggedProcess) {
		pc, _, _ := p.goSymTable.LineToPC(fp, 57)

		_, err := p.Break(pc)
		assertNoError(err, t, "Break() returned an error")

		err = p.Continue()
		assertNoError(err, t, "Continue() returned an error")

		_, err = p.EvalSymbol("a1")
		assertNoError(err, t, "Unable to find variable a1")

		_, err = p.EvalSymbol("a2")
		assertNoError(err, t, "Unable to find variable a1")

		// Move scopes, a1 exists here by a2 does not
		pc, _, _ = p.goSymTable.LineToPC(fp, 23)

		_, err = p.Break(pc)
		assertNoError(err, t, "Break() returned an error")

		err = p.Continue()
		assertNoError(err, t, "Continue() returned an error")

		_, err = p.EvalSymbol("a1")
		assertNoError(err, t, "Unable to find variable a1")

		_, err = p.EvalSymbol("a2")
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
	executablePath := "../_fixtures/testvariables"

	fp, err := filepath.Abs(executablePath + ".go")
	if err != nil {
		t.Fatal(err)
	}

	testcases := []struct {
		fn     func(*ThreadContext) ([]*Variable, error)
		output []varTest
	}{
		{(*ThreadContext).LocalVariables,
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
		{(*ThreadContext).FunctionArguments,
			[]varTest{
				{"bar", "main.FooBar {Baz: 10, Bur: lorem}", "main.FooBar", nil},
				{"baz", "bazburzum", "struct string", nil}}},
	}

	withTestProcess(executablePath, t, func(p *DebuggedProcess) {
		pc, _, _ := p.goSymTable.LineToPC(fp, 57)

		_, err := p.Break(pc)
		assertNoError(err, t, "Break() returned an error")

		err = p.Continue()
		assertNoError(err, t, "Continue() returned an error")

		for _, tc := range testcases {
			vars, err := tc.fn(p.CurrentThread)
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
