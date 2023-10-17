//go:build !go1.18

package evalop

import "go/ast"

type astIndexListExpr struct {
	ast.Expr
}
