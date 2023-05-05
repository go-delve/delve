package main

import (
	"bytes"
	"fmt"
	"go/format"
	"go/token"
	"go/types"
	"io"
	"log"
	"os"
	"strings"

	"golang.org/x/tools/go/packages"
)

func must(err error, fmtstr string, args ...interface{}) {
	if err != nil {
		log.Fatalf(fmtstr, args...)
	}
}

func main() {
	buf := bytes.NewBuffer([]byte{})
	fmt.Fprintf(buf, "// DO NOT EDIT: auto-generated using _scripts/gen-suitablemethods.go\n\n")
	fmt.Fprintf(buf, "package rpccommon\n\n")
	fmt.Fprintf(buf, "import ( \"reflect\"; \"github.com/go-delve/delve/service/rpc2\" )\n")

	dorpc(buf, "rpc2.RPCServer", "rpc2", "2")
	dorpc(buf, "RPCServer", "rpccommon", "Common")

	src, err := format.Source(buf.Bytes())
	must(err, "error formatting source: %v", err)

	out := os.Stdout
	if os.Args[1] != "-" {
		if !strings.HasSuffix(os.Args[1], ".go") {
			os.Args[1] += ".go"
		}
		out, err = os.Create(os.Args[1])
		must(err, "error creating %s: %v", os.Args[1], err)
	}
	_, err = out.Write(src)
	must(err, "error writing output: %v", err)
	if out != os.Stdout {
		must(out.Close(), "error closing %s: %v", os.Args[1], err)
	}
}

func dorpc(buf io.Writer, typename, pkgname, outname string) {
	fmt.Fprintf(buf, "func suitableMethods%s(s *%s, methods map[string]*methodType) {\n", outname, typename)

	fset := &token.FileSet{}
	cfg := &packages.Config{
		Mode: packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedName | packages.NeedCompiledGoFiles | packages.NeedTypes,
		Fset: fset,
	}
	pkgs, err := packages.Load(cfg, fmt.Sprintf("github.com/go-delve/delve/service/%s", pkgname))
	must(err, "error loading packages: %v", err)
	packages.Visit(pkgs, func(pkg *packages.Package) bool {
		if pkg.PkgPath != fmt.Sprintf("github.com/go-delve/delve/service/%s", pkgname) {
			return true
		}
		mset := types.NewMethodSet(types.NewPointer(pkg.Types.Scope().Lookup("RPCServer").Type()))
		for i := 0; i < mset.Len(); i++ {
			fn := mset.At(i).Obj().(*types.Func)
			name := fn.Name()
			if name[0] >= 'a' && name[0] <= 'z' {
				continue
			}
			sig := fn.Type().(*types.Signature)
			if sig.Params().Len() != 2 {
				continue
			}
			fmt.Fprintf(buf, "methods[\"RPCServer.%s\"] = &methodType{ method: reflect.ValueOf(s.%s) }\n", name, name)

		}
		return true
	}, nil)
	fmt.Fprintf(buf, "}\n\n")
}
