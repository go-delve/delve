package terminal

import (
	"fmt"
	"strings"
	"testing"
)

func TestStarlarkExamples(t *testing.T) {
	withTestTerminal("testvariables2", t, func(term *FakeTerminal) {
		term.MustExec("continue")
		t.Run("goroutine_start_line", func(t *testing.T) { testStarlarkExampleGoroutineStartLine(t, term) })
		t.Run("create_breakpoint_main", func(t *testing.T) { testStarlarkExampleCreateBreakpointmain(t, term) })
		t.Run("switch_to_main_goroutine", func(t *testing.T) { testStarlarkExampleSwitchToMainGoroutine(t, term) })
		t.Run("linked_list", func(t *testing.T) { testStarlarkExampleLinkedList(t, term) })
		t.Run("echo_expr", func(t *testing.T) { testStarlarkEchoExpr(t, term) })
		t.Run("find_array", func(t *testing.T) { testStarlarkFindArray(t, term) })
		t.Run("map_iteration", func(t *testing.T) { testStarlarkMapIteration(t, term) })
	})
}

func testStarlarkExampleGoroutineStartLine(t *testing.T, term *FakeTerminal) {
	term.MustExec("source " + findStarFile("goroutine_start_line"))
	out1 := term.MustExec("gsl")
	t.Logf("gsl: %q", out1)
	if !strings.Contains(out1, "func main() {") {
		t.Fatalf("goroutine_start_line example failed")
	}
}

func testStarlarkExampleCreateBreakpointmain(t *testing.T, term *FakeTerminal) {
	out2p1 := term.MustExec("source " + findStarFile("create_breakpoint_main"))
	t.Logf("create_breakpoint_main: %s", out2p1)
	out2p2 := term.MustExec("breakpoints")
	t.Logf("breakpoints: %q", out2p2)
	if !strings.Contains(out2p2, "main.afunc1") {
		t.Fatalf("create_breakpoint_runtime_func example failed")
	}
}

func testStarlarkExampleSwitchToMainGoroutine(t *testing.T, term *FakeTerminal) {
	gs, _, err := term.client.ListGoroutines(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range gs {
		if g.ID != 1 {
			t.Logf("switching to goroutine %d\n", g.ID)
			term.MustExec(fmt.Sprintf("goroutine %d", g.ID))
			break
		}
	}
	term.MustExec("source " + findStarFile("switch_to_main_goroutine"))
	out3p1 := term.MustExec("switch_to_main_goroutine")
	out3p2 := term.MustExec("goroutine")
	t.Logf("%q\n", out3p1)
	t.Logf("%q\n", out3p2)
	if !strings.Contains(out3p2, "Goroutine 1:\n") {
		t.Fatalf("switch_to_main_goroutine example failed")
	}
}

func testStarlarkExampleLinkedList(t *testing.T, term *FakeTerminal) {
	term.MustExec("source " + findStarFile("linked_list"))
	out := term.MustExec("linked_list ll Next 3")
	t.Logf("%q\n", out)
	if n := len(strings.Split(strings.TrimRight(out, "\n"), "\n")); n != 3 {
		t.Fatalf("wrong number of lines in output %d", n)
	}

	out = term.MustExec("linked_list ll Next 100")
	t.Logf("%q\n", out)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	for i, line := range lines {
		if i == 5 {
			if line != "5: *main.List nil" {
				t.Errorf("mismatched line %d %q", i, line)
			}
		} else {
			if !strings.HasPrefix(line, fmt.Sprintf("%d: *main.List {N: %d, Next: ", i, i)) {
				t.Errorf("mismatched line %d %q", i, line)
			}
		}
	}
	if len(lines) != 6 {
		t.Fatalf("wrong number of output lines %d", len(lines))
	}
}

func testStarlarkEchoExpr(t *testing.T, term *FakeTerminal) {
	term.MustExec("source " + findStarFile("echo_expr"))
	out := term.MustExec("echo_expr 2+2, 1-1, 2*3")
	t.Logf("echo_expr %q", out)
	if out != "a 4 b 0 c 6\n" {
		t.Error("output mismatch")
	}
}

func testStarlarkFindArray(t *testing.T, term *FakeTerminal) {
	term.MustExec("source " + findStarFile("find_array"))
	out := term.MustExec(`find_array "s2", lambda x: x.A == 5`)
	t.Logf("find_array (1) %q", out)
	if out != "found 2\n" {
		t.Error("output mismatch")
	}
	out = term.MustExec(`find_array "s2", lambda x: x.A == 20`)
	t.Logf("find_array (2) %q", out)
	if out != "not found\n" {
		t.Error("output mismatch")
	}
}

func testStarlarkMapIteration(t *testing.T, term *FakeTerminal) {
	out := term.MustExec("source " + findStarFile("starlark_map_iteration"))
	if !strings.Contains(out, "values=66") {
		t.Fatalf("testStarlarkMapIteration example failed")
	}
	t.Logf("%s", out)
}

func TestStarlarkVariable(t *testing.T) {
	withTestTerminal("testvariables2", t, func(term *FakeTerminal) {
		term.MustExec("continue")
		for _, tc := range []struct{ expr, tgt string }{
			{`v = eval(None, "i1").Variable; print(v.Value)`, "1"},
			{`v = eval(None, "f1").Variable; print(v.Value)`, "3.0"},
			{`v = eval(None, "as1").Variable; print(v.Value.A)`, "1"},
			{`v = eval(None, "as1").Variable; print(v.Value.B)`, "1"},
			{`v = eval(None, "as1").Variable; print(v.Value["A"])`, "1"},
			{`v = eval(None, "s1").Variable; print(v.Value[0])`, "one"},
			{`v = eval(None, "nilstruct").Variable; print(v.Value)`, "*main.astruct nil"},
			{`v = eval(None, "nilstruct").Variable; print(v.Value[0])`, "None"},
			{`v = eval(None, 'm1["Malone"]').Variable; print(v.Value)`, "main.astruct {A: 2, B: 3}"},
			{`v = eval(None, "m1").Variable; print(v.Value["Malone"])`, "main.astruct {A: 2, B: 3}"},
			{`v = eval(None, "m2").Variable; print(v.Value[1])`, "*main.astruct {A: 10, B: 11}"},
			{`v = eval(None, "c1.pb").Variable; print(v.Value)`, "*main.bstruct {a: main.astruct {A: 1, B: 2}}"},
			{`v = eval(None, "c1.pb").Variable; print(v.Value[0])`, "main.bstruct {a: main.astruct {A: 1, B: 2}}"},
			{`v = eval(None, "c1.pb").Variable; print(v.Value.a.B)`, "2"},
			{`v = eval(None, "iface1").Variable; print(v.Value[0])`, "*main.astruct {A: 1, B: 2}"},
			{`v = eval(None, "iface1").Variable; print(v.Value[0][0])`, "main.astruct {A: 1, B: 2}"},
			{`v = eval(None, "iface1").Variable; print(v.Value.A)`, "1"},

			{`v = eval(None, "as1", {"MaxStructFields": -1}).Variable; print(v.Value.A)`, "1"},
			{`v = eval({"GoroutineID": -1}, "as1").Variable; print(v.Value.A)`, "1"},
			{`v = eval(cur_scope(), "as1").Variable; print(v.Value.A)`, "1"},
			{`v = eval(None, "as1", default_load_config()).Variable; print(v.Value.A)`, "1"},

			// From starlark.md's examples
			{`v = eval(None, "s2").Variable; print(v.Value[0])`, "main.astruct {A: 1, B: 2}"},
			{`v = eval(None, "s2").Variable; print(v.Value[1].A)`, "3"},
			{`v = eval(None, "s2").Variable; print(v.Value[1].A + 10)`, "13"},
			{`v = eval(None, "s2").Variable; print(v.Value[1]["B"])`, "4"},
		} {
			out := strings.TrimSpace(term.MustExecStarlark(tc.expr))
			if out != tc.tgt {
				t.Errorf("for %q\nexpected %q\ngot %q", tc.expr, tc.tgt, out)
			}
		}
	})
}

// Test that pointer variables that were not loaded don't lead to crashes when
// used in Starlark scripts.
func TestStarlarkVariablePointerNotLoaded(t *testing.T) {
	withTestTerminal("testvariables_pointers_not_loaded", t, func(term *FakeTerminal) {
		term.MustExec("continue")

		// We're going to partially load some variables through the eval()
		// builtin. Then we're going to attempt to evaluate expressions which
		// try to access a field from a pointer variable that wasn't loaded
		// (i.e. a ptrVariableAsStarlarkValue with no children). The tests will
		// verify that we get an error. This is a regression test; we used to
		// panic.
		for _, tc := range []struct{ name, script, expErr string }{
			{
				// In this test, we'll load v. v will have a child (i.e.
				// v.Children[0]), but because v is nil, v.Children[0] will not
				// be loaded. We'll turn v.Children[0] into a
				// ptrVariableAsStarlarkValue, and attempt to access a field on
				// it.
				//
				// v                   -> structAsStarlarkValue<api.Variable<**int>>
				// v.Children[0]       -> structAsStarlarkValue<api.Variable<*int>>
				// v.Children[0].Value -> ptrVariableAsStarlarkValue<*int> ; this pointer variable has not been loaded
				name: "partial load because of nil ptr",
				script: `
v = eval(
		None, "ptrPtr", None
	).Variable
v.Children[0].Value.XXX
`,
				expErr: "*int has no .XXX field or method",
			},
			{
				// In this test, MaxStructFields = 10 and MaxVariableRecurse = 0
				// will cause us to load v, and v.Children[0] (in the sense that
				// v.Children[0] has a child), but _not_ load
				// v.Children[0].Children[0] (because the recursion limit is
				// exhausted by the descent into the top-level struct, so we
				// don't load what hides under the interface).
				//
				// v                               -> structAsStarlarkValue<api.Variable<StructWithInterface>>
				// v.Children[0]                   -> structAsStarlarkValue<api.Variable<interface{}>>
				// v.Children[0].Children[0]       -> structAsStarlarkValue<api.Variable<*int>>
				// v.Children[0].Children[0].Value -> ptrVariableAsStarlarkValue<*int> ; this pointer variable has not been loaded
				name: "partial load because of recursion limit",
				script: `
v = eval(
		None, "ba", {"MaxVariableRecurse": 0, "MaxStructFields": 10}
	).Variable;
v.Children[0].Children[0].Value.XXX
`,
				expErr: "*int has no .XXX field or method",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, err := term.ExecStarlark(tc.script)
				if err == nil {
					t.Fatalf("expected error %q, got success", tc.expErr)
				}
				if !strings.Contains(err.Error(), tc.expErr) {
					t.Fatalf("expected error %q, got \"%s\"", tc.expErr, err)
				}
			})
		}
	})
}
