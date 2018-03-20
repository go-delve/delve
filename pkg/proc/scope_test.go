package proc_test

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

	"github.com/derekparker/delve/pkg/goversion"
	"github.com/derekparker/delve/pkg/proc"
	protest "github.com/derekparker/delve/pkg/proc/test"
)

func TestScopeWithEscapedVariable(t *testing.T) {
	if ver, _ := goversion.Parse(runtime.Version()); ver.Major >= 0 && !ver.AfterOrEqual(goversion.GoVersion{1, 9, -1, 3, 0, ""}) {
		return
	}

	withTestProcess("scopeescapevareval", t, func(p proc.Process, fixture protest.Fixture) {
		assertNoError(proc.Continue(p), t, "Continue")

		// On the breakpoint there are two 'a' variables in scope, the one that
		// isn't shadowed is a variable that escapes to the heap and figures in
		// debug_info as '&a'. Evaluating 'a' should yield the escaped variable.

		avar := evalVariable(p, t, "a")
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
// If multiple variables with the same name are specified
// LocalVariables+FunctionArguments should return them in the same order and
// EvalExpression should return the last one.
func TestScope(t *testing.T) {
	if ver, _ := goversion.Parse(runtime.Version()); ver.Major >= 0 && !ver.AfterOrEqual(goversion.GoVersion{1, 9, -1, 0, 0, ""}) {
		return
	}

	fixturesDir := protest.FindFixturesDir()
	scopetestPath := filepath.Join(fixturesDir, "scopetest.go")

	scopeChecks := getScopeChecks(scopetestPath, t)

	withTestProcess("scopetest", t, func(p proc.Process, fixture protest.Fixture) {
		for i := range scopeChecks {
			setFileBreakpoint(p, t, fixture, scopeChecks[i].line)
		}

		t.Logf("%d breakpoints set", len(scopeChecks))

		for {
			if err := proc.Continue(p); err != nil {
				if _, exited := err.(proc.ProcessExitedError); exited {
					break
				}
				assertNoError(err, t, "Continue()")
			}
			bp := p.CurrentThread().Breakpoint()

			scopeCheck := findScopeCheck(scopeChecks, bp.Line)
			if scopeCheck == nil {
				t.Errorf("unknown stop position %s:%d %#x", bp.File, bp.Line, bp.Addr)
			}

			scope, err := proc.GoroutineScope(p.CurrentThread())
			assertNoError(err, t, "GoroutineScope()")

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
				}
			}

			var prev *varCheck
			for i := range scopeCheck.varChecks {
				vc := &scopeCheck.varChecks[i]
				if prev != nil && prev.name != vc.name {
					prev.checkInScope(scopeCheck.line, scope, t)
				}
				prev = vc
			}
			if prev != nil {
				prev.checkInScope(scopeCheck.line, scope, t)
			}

			scopeCheck.ok = true
			_, err = p.ClearBreakpoint(bp.Addr)
			assertNoError(err, t, "ClearBreakpoint")
		}
	})

	for i := range scopeChecks {
		if !scopeChecks[i].ok {
			t.Errorf("breakpoint at line %d not hit", scopeChecks[i].line)
		}
	}

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
