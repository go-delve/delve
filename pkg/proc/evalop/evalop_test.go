package evalop

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

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
