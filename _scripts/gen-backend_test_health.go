package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
)

func main() {
	f, err := parser.ParseFile(new(token.FileSet), "pkg/proc/proc_test.go", nil, 0)
	if err != nil {
		log.Fatalf("could not compile proc_test.go: %v", err)
	}
	ntests := 0
	skipped := make(map[string]map[string]int)
	ast.Inspect(f, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.File:
			return true
		case *ast.FuncDecl:
			if !strings.HasPrefix(node.Name.Name, "Test") {
				return false
			}
			ntests++
		stmtLoop:
			for _, stmt := range node.Body.List {
				expr, isexpr := stmt.(*ast.ExprStmt)
				if !isexpr {
					continue
				}
				call, ok := expr.X.(*ast.CallExpr)
				if !ok {
					return false
				}
				fun, ok := call.Fun.(*ast.Ident)
				if !ok {
					return false
				}
				switch fun.Name {
				case "skipOn":
					reason, conditions := skipOnArgs(call.Args)
					if reason == "N/A" {
						ntests--
						break stmtLoop
					}
					if skipped[conditions] == nil {
						skipped[conditions] = make(map[string]int)
					}
					skipped[conditions][reason]++
				case "skipUnlessOn":
					ntests--
					break stmtLoop
				}
			}
		}
		return false
	})
	var fh io.WriteCloser
	if len(os.Args) > 1 && os.Args[1] == "-" {
		fh = os.Stdout
	} else {
		fh, err = os.Create("./Documentation/backend_test_health.md")
		if err != nil {
			log.Fatalf("could not create backend_test_health.md: %v", err)
		}
	}
	fmt.Fprintf(fh, "Tests skipped by each supported backend:\n\n")
	conds := []string{}
	for cond := range skipped {
		conds = append(conds, cond)
	}
	sort.Strings(conds)
	for _, cond := range conds {
		tot := 0
		for _, v := range skipped[cond] {
			tot += v
		}
		fmt.Fprintf(fh, "* %s skipped = %d\n", cond, tot)
		reasons := []string{}
		for reason := range skipped[cond] {
			reasons = append(reasons, reason)
		}
		sort.Strings(reasons)
		for _, reason := range reasons {
			fmt.Fprintf(fh, "\t* %d %s\n", skipped[cond][reason], reason)
		}
	}
	err = fh.Close()
	if err != nil {
		log.Fatalf("could not close output file: %v", err)
	}
}

func skipOnArgs(args []ast.Expr) (reason string, conditions string) {
	reason, _ = strconv.Unquote(args[1].(*ast.BasicLit).Value)
	conds := []string{}
	for _, arg := range args[2:] {
		cond, _ := strconv.Unquote(arg.(*ast.BasicLit).Value)
		conds = append(conds, cond)
	}
	conditions = strings.Join(conds, "/")
	return reason, conditions
}
