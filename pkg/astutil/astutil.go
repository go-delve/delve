// This package contains utility functions used by pkg/proc to generate
// ast.Expr expressions.

package astutil

import (
	"go/ast"
	"go/token"
	"strconv"
)

// Eql returns an expression evaluating 'x == y'.
func Eql(x, y ast.Expr) *ast.BinaryExpr {
	return &ast.BinaryExpr{Op: token.EQL, X: x, Y: y}
}

// Sel returns an expression evaluating 'x.sel'.
func Sel(x ast.Expr, sel string) *ast.SelectorExpr {
	return &ast.SelectorExpr{X: x, Sel: &ast.Ident{Name: sel}}
}

// PkgVar returns an expression evaluating 'pkg.v'.
func PkgVar(pkg, v string) *ast.SelectorExpr {
	return &ast.SelectorExpr{X: &ast.Ident{Name: pkg}, Sel: &ast.Ident{Name: v}}
}

// Int returns an expression representing the integer 'n'.
func Int(n int64) *ast.BasicLit {
	return &ast.BasicLit{Kind: token.INT, Value: strconv.FormatInt(n, 10)}
}

// And returns an expression evaluating 'x && y'.
func And(x, y ast.Expr) ast.Expr {
	if x == nil || y == nil {
		return nil
	}
	return &ast.BinaryExpr{Op: token.LAND, X: x, Y: y}
}

// Or returns an expression evaluating 'x || y'.
func Or(x, y ast.Expr) ast.Expr {
	if x == nil || y == nil {
		return nil
	}
	return &ast.BinaryExpr{Op: token.LOR, X: x, Y: y}
}
