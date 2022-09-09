// This script checks that the Go runtime hasn't changed in ways that Delve
// doesn't understand. It accomplishes this task by parsing the pkg/proc
// package and extracting rules from all the comments starting with the
// magic string '+rtype'.
//
// COMMAND LINE
//
// go run _scripts/rtype.go (report [output-file]|check)
//
// Invoked with the command 'report' it will extract rules from pkg/proc and
// print them to stdout.
// Invoked with the command 'check' it will actually check that the runtime
// conforms to the rules in pkg/proc.
//
// RTYPE RULES
//
// // +rtype -var V T
//
// 	checks that variable runtime.V exists and has type T
//
// // +rtype -field S.F T
//
// 	checks that struct runtime.S has a field called F of type T
//
// const C1 = V // +rtype C2
//
// 	checks that constant runtime.C2 exists and has value V
//
// case "F": // +rtype -fieldof S T
//
// 	checks that struct runtime.S has a field called F of type T
//
// v := ... // +rtype T
//
// 	if v is declared as *proc.Variable it will assume that it has type
// 	runtime.T and it will then parse the enclosing function, searching for
// 	all calls to:
//		v.loadFieldNamed
//		v.fieldVariable
//		v.structMember
//	and check that type T has the specified fields.
//
// v.loadFieldNamed("F") // +rtype T
// v.loadFieldNamed("F") // +rtype -opt T
//
// 	checks that field F of the struct type declared for v has type T. Can
// 	also be used for fieldVariable, structMember and, inside parseG,
// 	loadInt64Maybe.
// 	The -opt flag specifies that the field can be missing (but if it exists
// 	it must have type T).
//
//
// Anywhere a type is required the following expressions can be used:
//
//  - any builtin type
//  - a type defined in the runtime package, without the 'runtime.' prefix
//  - anytype to match all possible types
//  - an expression of the form T1|T2 where both T1 and T2 can be arbitrary type expressions

package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/constant"
	"go/printer"
	"go/token"
	"go/types"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

const magicCommentPrefix = "+rtype"

var fset = &token.FileSet{}
var checkVarTypeRules = []*checkVarType{}
var checkFieldTypeRules = map[string][]*checkFieldType{}
var checkConstValRules = map[string][]*checkConstVal{}
var showRuleOrigin = false

// rtypeCmnt represents a +rtype comment
type rtypeCmnt struct {
	slash    token.Pos
	txt      string
	node     ast.Node // associated node
	toplevel ast.Decl // toplevel declaration that contains the Slash of the comment
	stmt     ast.Stmt
}

type checkVarType struct {
	V, T string // V must have type T
	pos  token.Pos
}

func (c *checkVarType) String() string {
	if showRuleOrigin {
		pos := fset.Position(c.pos)
		return fmt.Sprintf("var %s %s // %s:%d", c.V, c.T, relative(pos.Filename), pos.Line)
	}
	return fmt.Sprintf("var %s %s", c.V, c.T)
}

type checkFieldType struct {
	S, F, T string // S.F must have type T
	opt     bool
	pos     token.Pos
}

func (c *checkFieldType) String() string {
	pos := fset.Position(c.pos)
	return fmt.Sprintf("field %s.%s %s // %s:%d", c.S, c.F, c.T, relative(pos.Filename), pos.Line)
}

type checkConstVal struct {
	C   string // const C = V
	V   constant.Value
	pos token.Pos
}

func (c *checkConstVal) String() string {
	if showRuleOrigin {
		pos := fset.Position(c.pos)
		return fmt.Sprintf("const %s = %s // %s:%d", c.C, c.V, relative(pos.Filename), pos.Line)
	}
	return fmt.Sprintf("const %s = %s", c.C, c.V)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Wrong number of arguments.\n\trtype (report [output-file]|check)\n")
		os.Exit(1)
	}

	command := os.Args[1]

	setup()

	switch command {
	case "report":
		if len(os.Args) > 2 {
			fh, err := os.Create(os.Args[2])
			if err != nil {
				log.Fatalf("error creating output file: %v", err)
			}
			defer fh.Close()
			os.Stdout = fh
		}
		report()
	case "check":
		check()
	default:
		fmt.Fprintf(os.Stderr, "Wrong argument %s\n", command)
		os.Exit(1)
	}
}

// setup parses the proc package, extracting all +rtype comments and
// converting them into rules.
func setup() {
	pkgs, err := packages.Load(&packages.Config{Mode: packages.LoadSyntax, Fset: fset}, "github.com/go-delve/delve/pkg/proc")
	if err != nil {
		log.Fatalf("could not load proc package: %v", err)
	}

	for _, file := range pkgs[0].Syntax {
		cmntmap := ast.NewCommentMap(fset, file, file.Comments)
		rtypeCmnts := getRtypeCmnts(file, cmntmap)
		for _, rtcmnt := range rtypeCmnts {
			if rtcmnt == nil {
				continue
			}
			process(pkgs[0], rtcmnt, cmntmap, rtypeCmnts)
		}
	}
}

// getRtypeCmnts returns all +rtype comments inside 'file'. It also
// decorates them with the toplevel declaration that contains them as well
// as the statement they are associated with (where applicable).
func getRtypeCmnts(file *ast.File, cmntmap ast.CommentMap) []*rtypeCmnt {
	r := []*rtypeCmnt{}

	for n, cmntgrps := range cmntmap {
		for _, cmntgrp := range cmntgrps {
			if len(cmntgrp.List) == 0 {
				continue
			}

			for _, cmnt := range cmntgrp.List {
				txt := cleanupCommentText(cmnt.Text)
				if !strings.HasPrefix(txt, magicCommentPrefix) {
					continue
				}

				r = append(r, &rtypeCmnt{slash: cmnt.Slash, txt: txt, node: n})
			}

		}
	}

	sort.Slice(r, func(i, j int) bool { return r[i].slash < r[j].slash })

	// assign each comment to the toplevel declaration that contains it
	for i, j := 0, 0; i < len(r) && j < len(file.Decls); {
		decl := file.Decls[j]
		if decl.Pos() <= r[i].slash && r[i].slash < decl.End() {
			r[i].toplevel = decl
			i++
		} else {
			j++
		}
	}

	// for comments declared inside a function also find the statement that contains them.
	for i := range r {
		fndecl, ok := r[i].toplevel.(*ast.FuncDecl)
		if !ok {
			continue
		}

		var lastStmt ast.Stmt
		ast.Inspect(fndecl, func(n ast.Node) bool {
			if stmt, _ := n.(ast.Stmt); stmt != nil {
				lastStmt = stmt
			}
			if n == r[i].node {
				r[i].stmt = lastStmt
			}
			return true
		})
	}

	return r
}

func cleanupCommentText(txt string) string {
	if strings.HasPrefix(txt, "/*") || strings.HasPrefix(txt, "//") {
		txt = txt[2:]
	}
	return strings.TrimSpace(strings.TrimSuffix(txt, "*/"))
}

// process processes a single +rtype comment, turning it into a rule.
// If the +rtype comment is associated with a *proc.Variable declaration
// then it also checks the containing function for all uses of that
// variable.
func process(pkg *packages.Package, rtcmnt *rtypeCmnt, cmntmap ast.CommentMap, rtcmnts []*rtypeCmnt) {
	tinfo := pkg.TypesInfo
	fields := strings.Split(rtcmnt.txt, " ")

	switch fields[1] {
	case "-var":
		// -var V T
		// requests that variable V is of type T
		addCheckVarType(fields[2], fields[3], rtcmnt.slash)
	case "-field":
		// -field S.F T
		// requests that field F of type S is of type T
		v := strings.Split(fields[2], ".")
		addCheckFieldType(v[0], v[1], fields[3], false, rtcmnt.slash)
	default:
		ok := false
		if ident := isProcVariableDecl(rtcmnt.stmt, tinfo); ident != nil {
			if len(fields) == 2 {
				processProcVariableUses(rtcmnt.toplevel, tinfo, ident, cmntmap, rtcmnts, fields[1])
				ok = true
			} else if len(fields) == 3 && fields[1] == "-opt" {
				processProcVariableUses(rtcmnt.toplevel, tinfo, ident, cmntmap, rtcmnts, fields[2])
				ok = true
			}
		} else if ident := isConstDecl(rtcmnt.toplevel, rtcmnt.node); len(fields) == 2 && ident != nil {
			addCheckConstVal(fields[1], constValue(tinfo.Defs[ident]), rtcmnt.slash)
			ok = true
		} else if F := isStringCaseClause(rtcmnt.stmt); F != "" && len(fields) == 4 && fields[1] == "-fieldof" {
			addCheckFieldType(fields[2], F, fields[3], false, rtcmnt.slash)
			ok = true
		}
		if !ok {
			pos := fset.Position(rtcmnt.slash)
			log.Fatalf("%s:%d: unrecognized +rtype comment\n", pos.Filename, pos.Line)
		}
	}
}

// isProcVariableDecl returns true if stmt is a declaration of a
// *proc.Variable variable.
func isProcVariableDecl(stmt ast.Stmt, tinfo *types.Info) *ast.Ident {
	ass, _ := stmt.(*ast.AssignStmt)
	if ass == nil {
		return nil
	}
	if len(ass.Lhs) == 0 {
		return nil
	}
	ident, _ := ass.Lhs[0].(*ast.Ident)
	if ident == nil {
		return nil
	}
	var typ types.Type
	if def := tinfo.Defs[ident]; def != nil {
		typ = def.Type()
	}
	if tv, ok := tinfo.Types[ident]; ok {
		typ = tv.Type
	}
	if typ == nil {
		return nil
	}
	if typ == nil || typ.String() != "*github.com/go-delve/delve/pkg/proc.Variable" {
		return nil
	}
	return ident
}

func isConstDecl(toplevel ast.Decl, node ast.Node) *ast.Ident {
	gendecl, _ := toplevel.(*ast.GenDecl)
	if gendecl == nil {
		return nil
	}
	if gendecl.Tok != token.CONST {
		return nil
	}
	valspec, _ := node.(*ast.ValueSpec)
	if valspec == nil {
		return nil
	}
	if len(valspec.Names) != 1 {
		return nil
	}
	return valspec.Names[0]
}

func isStringCaseClause(stmt ast.Stmt) string {
	c, _ := stmt.(*ast.CaseClause)
	if c == nil {
		return ""
	}
	if len(c.List) != 1 {
		return ""
	}
	lit := c.List[0].(*ast.BasicLit)
	if lit == nil {
		return ""
	}
	if lit.Kind != token.STRING {
		return ""
	}
	r, _ := strconv.Unquote(lit.Value)
	return r
}

// processProcVariableUses scans the body of the function declaration 'decl'
// looking for uses of 'procVarIdent' which is assumed to be an identifier
// for a *proc.Variable variable.
func processProcVariableUses(decl ast.Node, tinfo *types.Info, procVarIdent *ast.Ident, cmntmap ast.CommentMap, rtcmnts []*rtypeCmnt, S string) {
	if len(S) > 0 && S[0] == '*' {
		S = S[1:]
	}
	isParseG := false
	if fndecl, _ := decl.(*ast.FuncDecl); fndecl != nil {
		if fndecl.Name.Name == "parseG" {
			if procVarIdent.Name == "v" {
				isParseG = true
			}
		}
	}
	var lastStmt ast.Stmt
	ast.Inspect(decl, func(n ast.Node) bool {
		if stmt, _ := n.(ast.Stmt); stmt != nil {
			lastStmt = stmt
		}

		fncall, _ := n.(*ast.CallExpr)
		if fncall == nil {
			return true
		}
		var methodName string
		if isParseG {
			if xident, _ := fncall.Fun.(*ast.Ident); xident != nil && (xident.Name == "loadInt64Maybe" || xident.Name == "loadUint64Maybe") {
				methodName = "loadInt64Maybe"
			}
		}
		if methodName == "" {
			sel, _ := fncall.Fun.(*ast.SelectorExpr)
			if sel == nil {
				return true
			}
			methodName = sel.Sel.Name
			xident, _ := sel.X.(*ast.Ident)
			if xident == nil {
				return true
			}
			if xident.Obj != procVarIdent.Obj {
				return true
			}
		}
		if len(fncall.Args) < 1 {
			return true
		}
		arg0, _ := fncall.Args[0].(*ast.BasicLit)
		if arg0 == nil {
			return true
		}
		if arg0.Kind != token.STRING {
			return true
		}

		switch methodName {
		case "loadFieldNamed", "fieldVariable", "loadInt64Maybe", "structMember":
			rtcmntIdx := -1
			if cmntgrps := cmntmap[lastStmt]; len(cmntgrps) > 0 && len(cmntgrps[0].List) > 0 {
				rtcmntIdx = findComment(cmntgrps[0].List[0].Slash, rtcmnts)
			}
			typ := "anytype"
			opt := false

			if rtcmntIdx >= 0 {
				fields := strings.Split(rtcmnts[rtcmntIdx].txt, " ")
				if len(fields) == 2 {
					typ = fields[1]
				} else if len(fields) == 3 && fields[1] == "-opt" {
					opt = true
					typ = fields[2]
				}
				if isProcVariableDecl(lastStmt, tinfo) == nil {
					// remove it because we have already processed it
					rtcmnts[rtcmntIdx] = nil
				}
			}
			F, _ := strconv.Unquote(arg0.Value)
			addCheckFieldType(S, F, typ, opt, fncall.Pos())
			//printNode(fset, fncall)
		default:
			pos := fset.Position(n.Pos())
			log.Fatalf("unknown node at %s:%d", pos.Filename, pos.Line)
		}
		return true
	})
}

func findComment(slash token.Pos, rtcmnts []*rtypeCmnt) int {
	for i := range rtcmnts {
		if rtcmnts[i] != nil && rtcmnts[i].slash == slash {
			return i
		}
	}
	return -1
}

func addCheckVarType(V, T string, pos token.Pos) {
	checkVarTypeRules = append(checkVarTypeRules, &checkVarType{V, T, pos})
}

func addCheckFieldType(S, F, T string, opt bool, pos token.Pos) {
	checkFieldTypeRules[S] = append(checkFieldTypeRules[S], &checkFieldType{S, F, T, opt, pos})
}

func addCheckConstVal(C string, V constant.Value, pos token.Pos) {
	checkConstValRules[C] = append(checkConstValRules[C], &checkConstVal{C, V, pos})
}

// report writes a report of all rules derived from the proc package to stdout.
func report() {
	for _, rule := range checkVarTypeRules {
		fmt.Printf("%s\n\n", rule.String())
	}

	var Ss []string
	for S := range checkFieldTypeRules {
		Ss = append(Ss, S)
	}
	sort.Strings(Ss)
	for _, S := range Ss {
		rules := checkFieldTypeRules[S]
		fmt.Printf("type %s struct {\n", S)
		for _, rule := range rules {
			fmt.Printf("\t%s %s", rule.F, rule.T)
			if rule.opt {
				fmt.Printf(" (optional)")
			}
			pos := fset.Position(rule.pos)
			if showRuleOrigin {
				fmt.Printf("\t// %s:%d", relative(pos.Filename), pos.Line)
			}
			fmt.Printf("\n")
		}
		fmt.Printf("}\n\n")
	}

	var Cs []string
	for C := range checkConstValRules {
		Cs = append(Cs, C)
	}
	sort.Strings(Cs)
	for _, C := range Cs {
		rules := checkConstValRules[C]
		for i, rule := range rules {
			if i == 0 {
				fmt.Printf("%s\n", rule.String())
			} else {
				fmt.Printf("or %s\n", rule.String())
			}
		}
		fmt.Printf("\n")
	}
}

func lookupPackage(pkgmap map[string]*packages.Package, name string) *packages.Package {
	if pkgmap[name] != nil {
		return pkgmap[name]
	}

	pkgs, err := packages.Load(&packages.Config{Mode: packages.LoadSyntax, Fset: fset}, name)
	if err != nil {
		log.Fatalf("could not load runtime package: %v", err)
	}
	packages.Visit(pkgs, func(pkg *packages.Package) bool {
		if pkgmap[pkg.ID] == nil {
			pkgmap[pkg.ID] = pkg
		}
		return true
	}, nil)

	return pkgmap[name]
}

func lookupTypeDef(pkgmap map[string]*packages.Package, typ string) types.Object {
	dot := strings.Index(typ, ".")
	if dot < 0 {
		return lookupPackage(pkgmap, "runtime").Types.Scope().Lookup(typ)
	}

	return lookupPackage(pkgmap, typ[:dot]).Types.Scope().Lookup(typ[dot+1:])
}

// check parses the runtime package and checks that all the rules retrieved
// from the 'proc' package pass.
func check() {
	pkgmap := map[string]*packages.Package{}
	allok := true

	for _, rule := range checkVarTypeRules {
		//TODO: implement
		pos := fset.Position(rule.pos)
		def := lookupPackage(pkgmap, "runtime").Types.Scope().Lookup(rule.V)
		if def == nil {
			fmt.Fprintf(os.Stderr, "%s:%d: could not find variable %s\n", pos.Filename, pos.Line, rule.V)
			allok = false
			continue
		}
		if !matchType(def.Type(), rule.T) {
			fmt.Fprintf(os.Stderr, "%s:%d: wrong type for variable %s, expected %s got %s\n", pos.Filename, pos.Line, rule.V, rule.T, typeStr(def.Type()))
			allok = false
			continue
		}
	}

	var Ss []string
	for S := range checkFieldTypeRules {
		Ss = append(Ss, S)
	}
	sort.Strings(Ss)
	for _, S := range Ss {
		rules := checkFieldTypeRules[S]
		pos := fset.Position(rules[0].pos)

		def := lookupTypeDef(pkgmap, S)
		if def == nil {
			fmt.Fprintf(os.Stderr, "%s:%d: could not find struct %s\n", pos.Filename, pos.Line, S)
			allok = false
			continue
		}

		typ := def.Type()
		if typ == nil {
			fmt.Fprintf(os.Stderr, "%s:%d: could not find struct %s\n", pos.Filename, pos.Line, S)
			allok = false
			continue
		}
		styp, _ := typ.Underlying().(*types.Struct)
		if styp == nil {
			fmt.Fprintf(os.Stderr, "%s:%d: could not find struct %s\n", pos.Filename, pos.Line, S)
			allok = false
			continue
		}

		for _, rule := range rules {
			pos := fset.Position(rule.pos)
			fieldType := fieldTypeByName(styp, rule.F)
			if fieldType == nil {
				if rule.opt {
					continue
				}
				fmt.Fprintf(os.Stderr, "%s:%d: could not find field %s.%s\n", pos.Filename, pos.Line, rule.S, rule.F)
				allok = false
				continue
			}
			if !matchType(fieldType, rule.T) {
				fmt.Fprintf(os.Stderr, "%s:%d: wrong type for field %s.%s, expected %s got %s\n", pos.Filename, pos.Line, rule.S, rule.F, rule.T, typeStr(fieldType))
				allok = false
				continue
			}
		}
	}

	var Cs []string
	for C := range checkConstValRules {
		Cs = append(Cs, C)
	}
	sort.Strings(Cs)
	for _, C := range Cs {
		rules := checkConstValRules[C]
		pos := fset.Position(rules[0].pos)
		def := lookupPackage(pkgmap, "runtime").Types.Scope().Lookup(C)
		if def == nil {
			fmt.Fprintf(os.Stderr, "%s:%d: could not find constant %s\n", pos.Filename, pos.Line, C)
			allok = false
			continue
		}

		val := constValue(def)
		found := false
		for _, rule := range rules {
			if val == rule.V {
				found = true
			}
		}
		if !found {
			fmt.Fprintf(os.Stderr, "%s:%d: wrong value for constant %s (%s)\n", pos.Filename, pos.Line, C, val.String())
			allok = false
			continue
		}
	}

	if !allok {
		os.Exit(1)
	}
}

func fieldTypeByName(typ *types.Struct, name string) types.Type {
	for i := 0; i < typ.NumFields(); i++ {
		field := typ.Field(i)
		if field.Name() == name {
			return field.Type()
		}
	}
	return nil
}

func matchType(typ types.Type, T string) bool {
	if T == "anytype" {
		return true
	}
	if strings.Index(T, "|") > 0 {
		for _, t1 := range strings.Split(T, "|") {
			if typeStr(typ) == t1 {
				return true
			}
		}
		return false
	}
	return typeStr(typ) == T
}

func typeStr(typ types.Type) string {
	return types.TypeString(typ, func(pkg *types.Package) string {
		if pkg.Path() == "runtime" {
			return ""
		}
		return pkg.Path()
	})
}

func constValue(obj types.Object) constant.Value {
	return obj.(*types.Const).Val()
}

func printNode(fset *token.FileSet, n ast.Node) {
	ast.Fprint(os.Stderr, fset, n, nil)
}

func exprToString(t ast.Expr) string {
	var buf bytes.Buffer
	printer.Fprint(&buf, token.NewFileSet(), t)
	return buf.String()
}

func relative(s string) string {
	wd, _ := os.Getwd()
	r, err := filepath.Rel(wd, s)
	if err != nil {
		return s
	}
	return r
}
