//go:build !go1.18
// +build !go1.18

package proc

import "go/ast"

type astIndexListExpr struct {
	ast.Expr
}
