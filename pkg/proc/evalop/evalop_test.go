package evalop

import (
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestDepthCheck(t *testing.T) {
	t.Run("PushPopEndDepth0", func(t *testing.T) {
		ops := []Op{
			&PushConst{Value: constant.MakeInt64(1)},
			&Pop{},
		}
		if err := DepthCheck(ops, 0); err != nil {
			t.Fatalf("DepthCheck: %v", err)
		}
		if err := DepthCheck(ops, 1); err == nil {
			t.Fatal("expected end-depth mismatch for endDepth=1")
		}
	})

	t.Run("Underflow", func(t *testing.T) {
		err := DepthCheck([]Op{&Pop{}}, 0)
		if err == nil {
			t.Fatal("expected underflow error")
		}
		if !strings.Contains(err.Error(), "depth check") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("JumpAlwaysSelfRejected", func(t *testing.T) {
		ops := []Op{&Jump{When: JumpAlways, Target: 0}}
		if err := DepthCheck(ops, 0); err == nil {
			t.Fatal("expected DepthCheck to reject JumpAlways self-cycle")
		}
	})

	t.Run("JumpAlwaysCycleRejected", func(t *testing.T) {
		ops := []Op{
			&Jump{When: JumpAlways, Target: 1},
			&Jump{When: JumpAlways, Target: 0},
		}
		if err := DepthCheck(ops, 0); err == nil {
			t.Fatal("expected DepthCheck to reject JumpAlways cycle")
		}
	})

	t.Run("PinningLoopOK", func(t *testing.T) {
		ops := []Op{
			&Jump{When: JumpIfPinningDone, Target: 2},
			&Jump{When: JumpAlways, Target: 0},
		}
		if err := DepthCheck(ops, 0); err != nil {
			t.Fatalf("DepthCheck: %v", err)
		}
	})

	t.Run("OutOfRangeJumpTarget", func(t *testing.T) {
		ops := []Op{&Jump{When: JumpAlways, Target: 99}}
		if err := DepthCheck(ops, 0); err == nil {
			t.Fatal("expected out-of-range jump target error")
		}
	})

	t.Run("RollUnderflow", func(t *testing.T) {
		ops := []Op{
			&PushNil{},
			&Roll{N: 7},
		}
		if err := DepthCheck(ops, 1); err == nil {
			t.Fatal("expected depth check to reject Roll with insufficient stack")
		}
	})

	t.Run("RollOK", func(t *testing.T) {
		ops := []Op{
			&PushNil{},
			&PushNil{},
			&Roll{N: 1},
		}
		if err := DepthCheck(ops, 2); err != nil {
			t.Fatalf("DepthCheck: %v", err)
		}
	})

	t.Run("NegativeEndDepthSkipsFinalCheck", func(t *testing.T) {
		ops := []Op{
			&PushNil{},
			&PushNil{},
			&Roll{N: 1},
		}
		if err := DepthCheck(ops, -1); err != nil {
			t.Fatalf("DepthCheck(-1): %v", err)
		}
		if err := DepthCheck(ops, 1); err == nil {
			t.Fatal("expected end-depth mismatch for endDepth=1")
		}
	})

	t.Run("ConditionalJumpPopUnderflow", func(t *testing.T) {
		ops := []Op{
			&PushConst{Value: constant.MakeBool(true)},
			&Jump{When: JumpIfTrue, Pop: true, Target: 2},
			&Pop{},
		}
		if err := DepthCheck(ops, 1); err == nil {
			t.Fatal("expected underflow after conditional jump that pops")
		}
	})

	t.Run("InconsistentJoin", func(t *testing.T) {
		ops := []Op{
			&PushNil{},
			&Jump{When: JumpAlways, Target: 3},
			&PushNil{},
			&Pop{},
		}
		if err := DepthCheck(ops, 0); err == nil {
			t.Fatal("expected reject for inconsistent stack depth at join")
		}
	})
}

func assertNoError(err error, t testing.TB, s string) {
	t.Helper()
	if err != nil {
		t.Fatalf("failed assertion %s: %s\n", s, err)
	}
}

func TestEvalSwitchExhaustiveness(t *testing.T) {
	// Checks that the switch statement in (*EvalScope).executeOp of
	// pkg/proc/eval.go exhaustively covers all implementations of the
	// evalop.Op interface.

	ops := make(map[string]bool)

	var fset, fset2 token.FileSet
	f, err := parser.ParseFile(&fset, "ops.go", nil, 0)
	assertNoError(err, t, "ParseFile")
	for _, decl := range f.Decls {
		decl, _ := decl.(*ast.FuncDecl)
		if decl == nil {
			continue
		}
		if decl.Name.Name != "depthCheck" {
			continue
		}
		ops[decl.Recv.List[0].Type.(*ast.StarExpr).X.(*ast.Ident).Name] = false
	}

	f, err = parser.ParseFile(&fset2, "../eval.go", nil, 0)
	assertNoError(err, t, "ParseFile")
	for _, decl := range f.Decls {
		decl, _ := decl.(*ast.FuncDecl)
		if decl == nil {
			continue
		}
		if decl.Name.Name != "executeOp" {
			continue
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			sw, _ := n.(*ast.TypeSwitchStmt)
			if sw == nil {
				return true
			}

			for _, c := range sw.Body.List {
				if len(c.(*ast.CaseClause).List) == 0 {
					// default clause
					continue
				}
				sel := c.(*ast.CaseClause).List[0].(*ast.StarExpr).X.(*ast.SelectorExpr)
				if sel.X.(*ast.Ident).Name != "evalop" {
					t.Fatalf("wrong case statement at: %v", fset2.Position(sel.Pos()))
				}

				ops[sel.Sel.Name] = true
			}
			return false
		})
	}

	for op := range ops {
		if !ops[op] {
			t.Errorf("evalop.Op %s not used in executeOp", op)
		}
	}
}
