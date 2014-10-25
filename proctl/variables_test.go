package proctl_test

import (
	"path/filepath"
	"testing"

	"github.com/derekparker/delve/helper"
	"github.com/derekparker/delve/proctl"
)

func TestVariableEvaluation(t *testing.T) {
	executablePath := "../_fixtures/testvariables"

	fp, err := filepath.Abs(executablePath + ".go")
	if err != nil {
		t.Fatal(err)
	}

	testcases := []struct {
		name    string
		value   string
		varType string
	}{
		{"a1", "foo", "struct string"},
		{"a2", "6", "int"},
		{"a3", "7.23", "float64"},
		{"a4", "[2]int [1 2]", "[97]int"},
		{"a5", "len: 5 cap: 5 [1 2 3 4 5]", "struct []int"},
		{"a6", "main.FooBar {Baz: 8, Bur: word}", "main.FooBar"},
		{"a7", "*main.FooBar {Baz: 5, Bur: strum}", "*main.FooBar"},
		{"baz", "bazburzum", "struct string"},
	}

	helper.WithTestProcess(executablePath, t, func(p *proctl.DebuggedProcess) {
		pc, _, _ := p.GoSymTable.LineToPC(fp, 21)

		_, err := p.Break(uintptr(pc))
		assertNoError(err, t, "Break() returned an error")

		err = p.Continue()
		assertNoError(err, t, "Continue() returned an error")

		for _, tc := range testcases {
			variable, err := p.EvalSymbol(tc.name)
			assertNoError(err, t, "Variable() returned an error")

			if variable.Name != tc.name {
				t.Fatalf("Expected %s got %s\n", tc.name, variable.Name)
			}

			if variable.Type != tc.varType {
				t.Fatalf("Expected %s got %s\n", tc.varType, variable.Type)
			}

			if variable.Value != tc.value {
				t.Fatalf("Expected %#v got %#v\n", tc.value, variable.Value)
			}
		}
	})
}
