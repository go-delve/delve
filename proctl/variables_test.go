package proctl

import (
	"path/filepath"
	"sort"
	"testing"
)

type varTest struct {
	name    string
	value   string
	varType string
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
		{"a1", "foo", "struct string"},
		{"a2", "6", "int"},
		{"a3", "7.23", "float64"},
		{"a4", "[2]int [1 2]", "[2]int"},
		{"a5", "len: 5 cap: 5 [1 2 3 4 5]", "struct []int"},
		{"a6", "main.FooBar {Baz: 8, Bur: word}", "main.FooBar"},
		{"a7", "*main.FooBar {Baz: 5, Bur: strum}", "*main.FooBar"},
		{"baz", "bazburzum", "struct string"},
		{"neg", "-1", "int"},
		{"i8", "1", "int8"},
		{"f32", "1.2", "float32"},
		{"a6.Baz", "8", "int"},
		{"i32", "[2]int32 [1 2]", "[2]int32"},
	}

	withTestProcess(executablePath, t, func(p *DebuggedProcess) {
		pc, _, _ := p.GoSymTable.LineToPC(fp, 30)

		_, err := p.Break(uintptr(pc))
		assertNoError(err, t, "Break() returned an error")

		err = p.Continue()
		assertNoError(err, t, "Continue() returned an error")

		for _, tc := range testcases {
			variable, err := p.EvalSymbol(tc.name)
			assertNoError(err, t, "EvalSymbol() returned an error")
			assertVariable(t, variable, tc)
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
		pc, _, _ := p.GoSymTable.LineToPC(fp, 30)

		_, err := p.Break(uintptr(pc))
		assertNoError(err, t, "Break() returned an error")

		err = p.Continue()
		assertNoError(err, t, "Continue() returned an error")

		_, err = p.EvalSymbol("a1")
		assertNoError(err, t, "Unable to find variable a1")

		_, err = p.EvalSymbol("a2")
		assertNoError(err, t, "Unable to find variable a1")

		// Move scopes, a1 exists here by a2 does not
		pc, _, _ = p.GoSymTable.LineToPC(fp, 12)

		_, err = p.Break(uintptr(pc))
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
				{"a1", "foo", "struct string"},
				{"a2", "6", "int"},
				{"a3", "7.23", "float64"},
				{"a4", "[2]int [1 2]", "[2]int"},
				{"a5", "len: 5 cap: 5 [1 2 3 4 5]", "struct []int"},
				{"a6", "main.FooBar {Baz: 8, Bur: word}", "main.FooBar"},
				{"a7", "*main.FooBar {Baz: 5, Bur: strum}", "*main.FooBar"},
				{"f32", "1.2", "float32"},
				{"i32", "[2]int32 [1 2]", "[2]int32"},
				{"i8", "1", "int8"},
				{"neg", "-1", "int"}}},
		{(*ThreadContext).FunctionArguments,
			[]varTest{
				{"bar", "main.FooBar {Baz: 10, Bur: lorem}", "main.FooBar"},
				{"baz", "bazburzum", "struct string"}}},
	}

	withTestProcess(executablePath, t, func(p *DebuggedProcess) {
		pc, _, _ := p.GoSymTable.LineToPC(fp, 30)

		_, err := p.Break(uintptr(pc))
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
