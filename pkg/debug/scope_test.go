package debug_test

import (
	"fmt"
	"go/constant"
	"go/parser"
	"go/token"
	"math"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/go-delve/delve/pkg/debug"
	"github.com/go-delve/delve/pkg/goversion"
	"github.com/go-delve/delve/pkg/proc"
	protest "github.com/go-delve/delve/pkg/proc/test"
)

func TestScopeWithEscapedVariable(t *testing.T) {
	if ver, _ := goversion.Parse(runtime.Version()); ver.Major >= 0 && !ver.AfterOrEqual(goversion.GoVersion{1, 9, -1, 3, 0, ""}) {
		return
	}

	withTestTarget("scopeescapevareval", t, func(tgt *debug.Target, fixture protest.Fixture) {
		assertNoError(tgt.Continue(), t, "Continue")

		// On the breakpoint there are two 'a' variables in scope, the one that
		// isn't shadowed is a variable that escapes to the heap and figures in
		// debug_info as '&a'. Evaluating 'a' should yield the escaped variable.

		avar := evalVariable(tgt, t, "a")
		if aval, _ := constant.Int64Val(avar.Value); aval != 3 {
			t.Errorf("wrong value for variable a: %d", aval)
		}

		if avar.Flags&proc.VariableEscaped == 0 {
			t.Errorf("variable a isn't escaped to the heap")
		}
	})
}

// TestScope will:
// - run _fixtures/scopetest.go
// - set a breakpoint on all lines containing a comment
// - continue until the program ends
// - every time a breakpoint is hit it will check that
//   scope.FunctionArguments+scope.LocalVariables and scope.EvalExpression
//   return what the corresponding comment describes they should return and
//   removes the breakpoint.
//
// Each comment is a comma separated list of variable declarations, with
// each variable declaration having the following format:
//
//  name type = initialvalue
//
// the = and the initial value are optional and can only be specified if the
// type is an integer type, float32, float64 or bool.
//
// If multiple variables with the same name are specified:
// 1. LocalVariables+FunctionArguments should return them in the same order and
//    every variable except the last one should be marked as shadowed
// 2. EvalExpression should return the last one.
func TestScope(t *testing.T) {
	if ver, _ := goversion.Parse(runtime.Version()); ver.Major >= 0 && !ver.AfterOrEqual(goversion.GoVersion{1, 9, -1, 0, 0, ""}) {
		return
	}

	fixturesDir := protest.FindFixturesDir()
	scopetestPath := filepath.Join(fixturesDir, "scopetest.go")

	scopeChecks := getScopeChecks(scopetestPath, t)

	withTestTarget("scopetest", t, func(tgt *debug.Target, fixture protest.Fixture) {
		for i := range scopeChecks {
			setFileBreakpoint(tgt, t, fixture.Source, scopeChecks[i].line)
		}

		t.Logf("%d breakpoints set", len(scopeChecks))

		for {
			if err := tgt.Continue(); err != nil {
				if _, exited := err.(proc.ErrProcessExited); exited {
					break
				}
				assertNoError(err, t, "Continue()")
			}
			bp := tgt.BreakpointStateForThread(tgt.CurrentThread().ThreadID())

			scopeCheck := findScopeCheck(scopeChecks, bp.Line)
			if scopeCheck == nil {
				t.Errorf("unknown stop position %s:%d %#x", bp.File, bp.Line, bp.Addr)
			}

			scope, _ := scopeCheck.checkLocalsAndArgs(tgt, t)

			for i := range scopeCheck.varChecks {
				vc := &scopeCheck.varChecks[i]
				if vc.shdw {
					continue
				}
				vc.checkInScope(scopeCheck.line, scope, t)
			}

			scopeCheck.ok = true
			_, err := tgt.ClearBreakpoint(bp.Addr)
			assertNoError(err, t, "ClearBreakpoint")
		}
	})

	for i := range scopeChecks {
		if !scopeChecks[i].ok {
			t.Errorf("breakpoint at line %d not hit", scopeChecks[i].line)
		}
	}

}

func TestInlinedStacktraceAndVariables(t *testing.T) {
	if runtime.GOARCH == "arm64" {
		t.Skip("arm64 does not support Stacktrace for now")
	}
	if ver, _ := goversion.Parse(runtime.Version()); ver.Major >= 0 && !ver.AfterOrEqual(goversion.GoVersion{1, 10, -1, 0, 0, ""}) {
		// Versions of go before 1.10 do not have DWARF information for inlined calls
		t.Skip("inlining not supported")
	}

	firstCallCheck := &scopeCheck{
		line: 7,
		ok:   false,
		varChecks: []varCheck{
			varCheck{
				name:   "a",
				typ:    "int",
				kind:   reflect.Int,
				hasVal: true,
				intVal: 3,
			},
			varCheck{
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
			varCheck{
				name:   "a",
				typ:    "int",
				kind:   reflect.Int,
				hasVal: true,
				intVal: 4,
			},
			varCheck{
				name:   "z",
				typ:    "int",
				kind:   reflect.Int,
				hasVal: true,
				intVal: 16,
			},
		},
	}

	withTestTargetArgs("testinline", t, ".", []string{}, protest.EnableInlining, func(tgt *debug.Target, fixture protest.Fixture) {
		pcs, err := tgt.BinInfo().LineToPC(fixture.Source, 7)
		assertNoError(err, t, "LineToPC")
		if len(pcs) < 2 {
			t.Fatalf("expected at least two locations for %s:%d (got %d: %#x)", fixture.Source, 7, len(pcs), pcs)
		}
		for _, pc := range pcs {
			t.Logf("setting breakpoint at %#x\n", pc)
			_, err := tgt.SetBreakpoint(pc, proc.UserBreakpoint, nil)
			assertNoError(err, t, fmt.Sprintf("SetBreakpoint(%#x)", pc))
		}

		// first inlined call
		assertNoError(tgt.Continue(), t, "Continue")
		frames, err := proc.ThreadStacktrace(tgt.CurrentThread(), tgt.BinInfo(), 20)
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

		if avar, _ := constant.Int64Val(evalVariable(tgt, t, "a").Value); avar != 3 {
			t.Fatalf("value of 'a' variable is not 3 (%d)", avar)
		}
		if zvar, _ := constant.Int64Val(evalVariable(tgt, t, "z").Value); zvar != 9 {
			t.Fatalf("value of 'z' variable is not 9 (%d)", zvar)
		}

		if _, ok := firstCallCheck.checkLocalsAndArgs(tgt, t); !ok {
			t.Fatalf("exiting for past errors")
		}

		// second inlined call
		assertNoError(tgt.Continue(), t, "Continue")
		frames, err = proc.ThreadStacktrace(tgt.CurrentThread(), tgt.BinInfo(), 20)
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

		if avar, _ := constant.Int64Val(evalVariable(tgt, t, "a").Value); avar != 4 {
			t.Fatalf("value of 'a' variable is not 3 (%d)", avar)
		}
		if zvar, _ := constant.Int64Val(evalVariable(tgt, t, "z").Value); zvar != 16 {
			t.Fatalf("value of 'z' variable is not 9 (%d)", zvar)
		}
		if bvar, err := evalVariableOrError(tgt, "b"); err == nil {
			t.Fatalf("expected error evaluating 'b', but it succeeded instead: %v", bvar)
		}

		if _, ok := secondCallCheck.checkLocalsAndArgs(tgt, t); !ok {
			t.Fatalf("exiting for past errors")
		}
	})
}

type scopeCheck struct {
	line      int
	varChecks []varCheck
	ok        bool // this scope check was passed
}

type varCheck struct {
	name     string
	typ      string
	kind     reflect.Kind
	shdw     bool // this variable should be shadowed
	hasVal   bool
	intVal   int64
	uintVal  uint64
	floatVal float64
	boolVal  bool

	ok bool // this variable check was passed
}

func getScopeChecks(path string, t *testing.T) []scopeCheck {
	var fset token.FileSet
	root, err := parser.ParseFile(&fset, path, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("could not parse %s: %v", path, err)
	}

	scopeChecks := []scopeCheck{}

	for _, cmtg := range root.Comments {
		for _, cmt := range cmtg.List {
			pos := fset.Position(cmt.Slash)

			scopeChecks = append(scopeChecks, scopeCheck{line: pos.Line})
			scopeChecks[len(scopeChecks)-1].Parse(cmt.Text[2:], t)
		}
	}

	return scopeChecks
}

func findScopeCheck(scopeChecks []scopeCheck, line int) *scopeCheck {
	for i := range scopeChecks {
		if scopeChecks[i].line == line {
			return &scopeChecks[i]
		}
	}
	return nil
}

func (check *scopeCheck) Parse(descr string, t *testing.T) {
	decls := strings.Split(descr, ",")
	check.varChecks = make([]varCheck, len(decls))
	for i, decl := range decls {
		varcheck := &check.varChecks[i]
		value := ""
		if equal := strings.Index(decl, "="); equal >= 0 {
			value = strings.TrimSpace(decl[equal+1:])
			decl = strings.TrimSpace(decl[:equal])
			varcheck.hasVal = true
		} else {
			decl = strings.TrimSpace(decl)
		}

		space := strings.Index(decl, " ")
		if space < 0 {
			t.Fatalf("could not parse scope comment %q (%q)", descr, decl)
		}
		varcheck.name = strings.TrimSpace(decl[:space])
		varcheck.typ = strings.TrimSpace(decl[space+1:])
		if strings.Index(varcheck.typ, " ") >= 0 {
			t.Fatalf("could not parse scope comment %q (%q)", descr, decl)
		}

		if !varcheck.hasVal {
			continue
		}

		switch varcheck.typ {
		case "int", "int8", "int16", "int32", "int64":
			var err error
			varcheck.kind = reflect.Int
			varcheck.intVal, err = strconv.ParseInt(value, 10, 64)
			if err != nil {
				t.Fatalf("could not parse scope comment %q: %v", descr, err)
			}

		case "uint", "uint8", "uint16", "uint32", "uint64", "uintptr":
			var err error
			varcheck.kind = reflect.Uint
			varcheck.uintVal, err = strconv.ParseUint(value, 10, 64)
			if err != nil {
				t.Fatalf("could not parse scope comment %q: %v", descr, err)
			}

		case "float32", "float64":
			var err error
			varcheck.kind = reflect.Float64
			varcheck.floatVal, err = strconv.ParseFloat(value, 64)
			if err != nil {
				t.Fatalf("could not parse scope comment %q: %v", descr, err)
			}

		case "bool":
			var err error
			varcheck.kind = reflect.Bool
			varcheck.boolVal, err = strconv.ParseBool(value)
			if err != nil {
				t.Fatalf("could not parse scope comment %q: %v", descr, err)
			}
		}
	}

	for i := 1; i < len(check.varChecks); i++ {
		if check.varChecks[i-1].name == check.varChecks[i].name {
			check.varChecks[i-1].shdw = true
		}
	}
}

func (scopeCheck *scopeCheck) checkLocalsAndArgs(tgt *debug.Target, t *testing.T) (*proc.EvalScope, bool) {
	scope, err := proc.GoroutineScope(tgt.CurrentThread(), tgt.BinInfo())
	assertNoError(err, t, "GoroutineScope()")

	ok := true

	args, err := scope.FunctionArguments(normalLoadConfig)
	assertNoError(err, t, "FunctionArguments()")
	locals, err := scope.LocalVariables(normalLoadConfig)
	assertNoError(err, t, "LocalVariables()")

	for _, arg := range args {
		scopeCheck.checkVar(arg, t)
	}

	for _, local := range locals {
		scopeCheck.checkVar(local, t)
	}

	for i := range scopeCheck.varChecks {
		if !scopeCheck.varChecks[i].ok {
			t.Errorf("%d: variable %s not found", scopeCheck.line, scopeCheck.varChecks[i].name)
			ok = false
		}
	}

	return scope, ok
}

func (check *scopeCheck) checkVar(v *proc.Variable, t *testing.T) {
	var varCheck *varCheck
	for i := range check.varChecks {
		if !check.varChecks[i].ok && (check.varChecks[i].name == v.Name) {
			varCheck = &check.varChecks[i]
			break
		}
	}

	if varCheck == nil {
		t.Errorf("%d: unexpected variable %s", check.line, v.Name)
		return
	}

	varCheck.check(check.line, v, t, "FunctionArguments+LocalVariables")
	varCheck.ok = true
}

func (varCheck *varCheck) checkInScope(line int, scope *proc.EvalScope, t *testing.T) {
	v, err := scope.EvalVariable(varCheck.name, normalLoadConfig)
	assertNoError(err, t, fmt.Sprintf("EvalVariable(%s)", varCheck.name))
	varCheck.check(line, v, t, "EvalExpression")

}

func (varCheck *varCheck) check(line int, v *proc.Variable, t *testing.T, ctxt string) {
	typ := v.DwarfType.String()
	typ = strings.Replace(typ, " ", "", -1)
	if typ != varCheck.typ {
		t.Errorf("%d: wrong type for %s (%s), got %s, expected %s", line, v.Name, ctxt, typ, varCheck.typ)
	}

	if varCheck.shdw && v.Flags&proc.VariableShadowed == 0 {
		t.Errorf("%d: expected shadowed %s variable", line, v.Name)
	}

	if !varCheck.hasVal {
		return
	}

	switch varCheck.kind {
	case reflect.Int:
		if vv, _ := constant.Int64Val(v.Value); vv != varCheck.intVal {
			t.Errorf("%d: wrong value for %s (%s), got %d expected %d", line, v.Name, ctxt, vv, varCheck.intVal)
		}
	case reflect.Uint:
		if vv, _ := constant.Uint64Val(v.Value); vv != varCheck.uintVal {
			t.Errorf("%d: wrong value for %s (%s), got %d expected %d", line, v.Name, ctxt, vv, varCheck.uintVal)
		}
	case reflect.Float64:
		if vv, _ := constant.Float64Val(v.Value); math.Abs(vv-varCheck.floatVal) > 0.001 {
			t.Errorf("%d: wrong value for %s (%s), got %g expected %g", line, v.Name, ctxt, vv, varCheck.floatVal)
		}
	case reflect.Bool:
		if vv := constant.BoolVal(v.Value); vv != varCheck.boolVal {
			t.Errorf("%d: wrong value for %s (%s), got %v expected %v", line, v.Name, ctxt, vv, varCheck.boolVal)
		}
	}
}
