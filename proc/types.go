package proc

import (
	"go/ast"
	"go/token"
	"reflect"
	"strconv"

	"github.com/derekparker/delve/pkg/dwarf/reader"
	"golang.org/x/debug/dwarf"
)

// Do not call this function directly it isn't able to deal correctly with package paths
func (p *Process) findType(name string) (dwarf.Type, error) {
	off, found := p.Dwarf.Types[name]
	if !found {
		return nil, reader.TypeNotFoundErr
	}
	return p.Dwarf.Type(off)
}

func (p *Process) pointerTo(typ dwarf.Type) dwarf.Type {
	return &dwarf.PtrType{dwarf.CommonType{int64(p.arch.PtrSize()), "", reflect.Ptr, 0}, typ}
}

func (p *Process) findTypeExpr(expr ast.Expr) (dwarf.Type, error) {
	if lit, islit := expr.(*ast.BasicLit); islit && lit.Kind == token.STRING {
		// Allow users to specify type names verbatim as quoted
		// string. Useful as a catch-all workaround for cases where we don't
		// parse/serialize types correctly or can not resolve package paths.
		typn, _ := strconv.Unquote(lit.Value)
		return p.findType(typn)
	}
	p.expandPackagesInType(expr)
	if snode, ok := expr.(*ast.StarExpr); ok {
		// Pointer types only appear in the dwarf informations when
		// a pointer to the type is used in the target program, here
		// we create a pointer type on the fly so that the user can
		// specify a pointer to any variable used in the target program
		ptyp, err := p.findTypeExpr(snode.X)
		if err != nil {
			return nil, err
		}
		return p.pointerTo(ptyp), nil
	}
	return p.findType(exprToString(expr))
}

func complexType(typename string) bool {
	for _, ch := range typename {
		switch ch {
		case '*', '[', '<', '{', '(', ' ':
			return true
		}
	}
	return false
}

func (p *Process) expandPackagesInType(expr ast.Expr) {
	switch e := expr.(type) {
	case *ast.ArrayType:
		p.expandPackagesInType(e.Elt)
	case *ast.ChanType:
		p.expandPackagesInType(e.Value)
	case *ast.FuncType:
		for i := range e.Params.List {
			p.expandPackagesInType(e.Params.List[i].Type)
		}
		if e.Results != nil {
			for i := range e.Results.List {
				p.expandPackagesInType(e.Results.List[i].Type)
			}
		}
	case *ast.MapType:
		p.expandPackagesInType(e.Key)
		p.expandPackagesInType(e.Value)
	case *ast.ParenExpr:
		p.expandPackagesInType(e.X)
	case *ast.SelectorExpr:
		switch x := e.X.(type) {
		case *ast.Ident:
			if path, ok := p.Dwarf.Packages[x.Name]; ok {
				x.Name = path
			}
		default:
			p.expandPackagesInType(e.X)
		}
	case *ast.StarExpr:
		p.expandPackagesInType(e.X)
	default:
		// nothing to do
	}
}
