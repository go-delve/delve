package main

import (
	"fmt"
	"reflect"
	"runtime"

	pkg1 "go/ast"
	pkg2 "net/http"

	"dir0/pkg"
	"dir0/renamedpackage"
	dir1pkg "dir1/pkg"
)

func main() {
	var badexpr interface{} = &pkg1.BadExpr{1, 2}
	var req interface{} = &pkg2.Request{Method: "amethod"}
	var amap interface{} = map[pkg1.BadExpr]pkg2.Request{pkg1.BadExpr{2, 3}: pkg2.Request{Method: "othermethod"}}
	var amap2 interface{} = &map[pkg1.BadExpr]pkg2.Request{pkg1.BadExpr{2, 3}: pkg2.Request{Method: "othermethod"}}
	var dir0someType interface{} = &pkg.SomeType{3}
	var dir1someType interface{} = dir1pkg.SomeType{1, 2}
	var amap3 interface{} = map[pkg.SomeType]dir1pkg.SomeType{pkg.SomeType{4}: dir1pkg.SomeType{5, 6}}
	var anarray interface{} = [2]pkg.SomeType{pkg.SomeType{1}, pkg.SomeType{2}}
	var achan interface{} = make(chan pkg.SomeType)
	var aslice interface{} = []pkg.SomeType{pkg.SomeType{3}, pkg.SomeType{4}}
	var afunc interface{} = func(a pkg.SomeType, b dir1pkg.SomeType) {}
	var astruct interface{} = &struct {
		A dir1pkg.SomeType
		B pkg.SomeType
	}{
		A: dir1pkg.SomeType{1, 2},
		B: pkg.SomeType{3},
	}
	var astruct2 interface{} = &struct {
		dir1pkg.SomeType
		X int
	}{
		SomeType: dir1pkg.SomeType{1, 2},
		X:        10,
	}
	var iface interface {
		AMethod(x int) int
		AnotherMethod(x int) int
	} = &pkg.SomeType{4}
	var iface2iface interface{} = &iface
	var iface3 interface{} = &realname.SomeType{A: true}

	runtime.Breakpoint()
	t := reflect.ValueOf(iface2iface).Elem().Type()
	m := t.Method(0)
	fmt.Println(m.Type.In(0))
	fmt.Println(m.Type.String())
	fmt.Println(badexpr, req, amap, amap2, dir0someType, dir1someType, amap3, anarray, achan, aslice, afunc, astruct, astruct2, iface2iface, iface3, pkg.SomeVar, pkg.A, dir1pkg.A)
}
