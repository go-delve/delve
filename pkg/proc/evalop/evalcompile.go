package evalop

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/printer"
	"go/scanner"
	"go/token"
	"reflect"
	"strconv"
	"strings"

	"github.com/go-delve/delve/pkg/dwarf/godwarf"
	"github.com/go-delve/delve/pkg/dwarf/reader"
)

var (
	ErrFuncCallNotAllowed = errors.New("function calls not allowed without using 'call'")
)

type compileCtx struct {
	evalLookup
	ops        []Op
	allowCalls bool
	curCall    int
}

type evalLookup interface {
	FindTypeExpr(ast.Expr) (godwarf.Type, error)
	HasLocal(string) bool
	HasGlobal(string, string) bool
	HasBuiltin(string) bool
	LookupRegisterName(string) (int, bool)
}

// CompileAST compiles the expression t into a list of instructions.
func CompileAST(lookup evalLookup, t ast.Expr) ([]Op, error) {
	ctx := &compileCtx{evalLookup: lookup, allowCalls: true}
	err := ctx.compileAST(t)
	if err != nil {
		return nil, err
	}

	err = ctx.depthCheck(1)
	if err != nil {
		return ctx.ops, err
	}
	return ctx.ops, nil
}

// Compile compiles the expression expr into a list of instructions.
// If canSet is true expressions like "x = y" are also accepted.
func Compile(lookup evalLookup, expr string, canSet bool) ([]Op, error) {
	t, err := parser.ParseExpr(expr)
	if err != nil {
		if canSet {
			eqOff, isAs := isAssignment(err)
			if isAs {
				return CompileSet(lookup, expr[:eqOff], expr[eqOff+1:])
			}
		}
		return nil, err
	}
	return CompileAST(lookup, t)
}

func isAssignment(err error) (int, bool) {
	el, isScannerErr := err.(scanner.ErrorList)
	if isScannerErr && el[0].Msg == "expected '==', found '='" {
		return el[0].Pos.Offset, true
	}
	return 0, false
}

// CompileSet compiles the expression setting lhexpr to rhexpr into a list of
// instructions.
func CompileSet(lookup evalLookup, lhexpr, rhexpr string) ([]Op, error) {
	lhe, err := parser.ParseExpr(lhexpr)
	if err != nil {
		return nil, err
	}
	rhe, err := parser.ParseExpr(rhexpr)
	if err != nil {
		return nil, err
	}

	ctx := &compileCtx{evalLookup: lookup, allowCalls: true}
	err = ctx.compileAST(rhe)
	if err != nil {
		return nil, err
	}

	if isStringLiteral(rhe) {
		ctx.compileAllocLiteralString()
	}

	err = ctx.compileAST(lhe)
	if err != nil {
		return nil, err
	}

	ctx.pushOp(&SetValue{lhe: lhe, Rhe: rhe})

	err = ctx.depthCheck(0)
	if err != nil {
		return ctx.ops, err
	}
	return ctx.ops, nil
}

func (ctx *compileCtx) compileAllocLiteralString() {
	ctx.pushOp(&CallInjectionAllocString{Phase: 0})
	ctx.pushOp(&CallInjectionAllocString{Phase: 1})
	ctx.pushOp(&CallInjectionAllocString{Phase: 2})
}

func (ctx *compileCtx) pushOp(op Op) {
	ctx.ops = append(ctx.ops, op)
}

// depthCheck validates the list of instructions produced by Compile and
// CompileSet by performing a stack depth check.
// It calculates the depth of the stack at every instruction in ctx.ops and
// checks that they have enough arguments to execute. For instructions that
// can be reached through multiple paths (because of a jump) it checks that
// all paths reach the instruction with the same stack depth.
// Finally it checks that the stack depth after all instructions have
// executed is equal to endDepth.
func (ctx *compileCtx) depthCheck(endDepth int) error {
	depth := make([]int, len(ctx.ops)+1) // depth[i] is the depth of the stack before i-th instruction
	for i := range depth {
		depth[i] = -1
	}
	depth[0] = 0

	var err error
	checkAndSet := func(j, d int) { // sets depth[j] to d after checking that we can
		if depth[j] < 0 {
			depth[j] = d
		}
		if d != depth[j] {
			err = fmt.Errorf("internal debugger error: depth check error at instruction %d: expected depth %d have %d (jump target)\n%s", j, d, depth[j], Listing(depth, ctx.ops))
		}
	}

	for i, op := range ctx.ops {
		npop, npush := op.depthCheck()
		if depth[i] < npop {
			return fmt.Errorf("internal debugger error: depth check error at instruction %d: expected at least %d have %d\n%s", i, npop, depth[i], Listing(depth, ctx.ops))
		}
		d := depth[i] - npop + npush
		checkAndSet(i+1, d)
		if jmp, _ := op.(*Jump); jmp != nil {
			checkAndSet(jmp.Target, d)
		}
		if err != nil {
			return err
		}
	}

	if depth[len(ctx.ops)] != endDepth {
		return fmt.Errorf("internal debugger error: depth check failed: depth at the end is not %d (got %d)\n%s", depth[len(ctx.ops)], endDepth, Listing(depth, ctx.ops))
	}
	return nil
}

func (ctx *compileCtx) compileAST(t ast.Expr) error {
	switch node := t.(type) {
	case *ast.CallExpr:
		return ctx.compileTypeCastOrFuncCall(node)

	case *ast.Ident:
		return ctx.compileIdent(node)

	case *ast.ParenExpr:
		// otherwise just eval recursively
		return ctx.compileAST(node.X)

	case *ast.SelectorExpr: // <expression>.<identifier>
		switch x := node.X.(type) {
		case *ast.Ident:
			switch {
			case x.Name == "runtime" && node.Sel.Name == "curg":
				ctx.pushOp(&PushCurg{})

			case x.Name == "runtime" && node.Sel.Name == "frameoff":
				ctx.pushOp(&PushFrameoff{})

			case x.Name == "runtime" && node.Sel.Name == "threadid":
				ctx.pushOp(&PushThreadID{})

			case ctx.HasLocal(x.Name):
				ctx.pushOp(&PushLocal{Name: x.Name})
				ctx.pushOp(&Select{node.Sel.Name})

			case ctx.HasGlobal(x.Name, node.Sel.Name):
				ctx.pushOp(&PushPackageVar{x.Name, node.Sel.Name})

			default:
				return ctx.compileUnary(node.X, &Select{node.Sel.Name})
			}

		case *ast.CallExpr:
			ident, ok := x.Fun.(*ast.SelectorExpr)
			if ok {
				f, ok := ident.X.(*ast.Ident)
				if ok && f.Name == "runtime" && ident.Sel.Name == "frame" {
					switch arg := x.Args[0].(type) {
					case *ast.BasicLit:
						fr, err := strconv.ParseInt(arg.Value, 10, 8)
						if err != nil {
							return err
						}
						// Push local onto the stack to be evaluated in the new frame context.
						ctx.pushOp(&PushLocal{Name: node.Sel.Name, Frame: fr})
						return nil
					default:
						return fmt.Errorf("expected integer value for frame, got %v", arg)
					}
				}
			}
			return ctx.compileUnary(node.X, &Select{node.Sel.Name})

		case *ast.BasicLit: // try to accept "package/path".varname syntax for package variables
			s, err := strconv.Unquote(x.Value)
			if err != nil {
				return err
			}
			if ctx.HasGlobal(s, node.Sel.Name) {
				ctx.pushOp(&PushPackageVar{s, node.Sel.Name})
				return nil
			}
			return ctx.compileUnary(node.X, &Select{node.Sel.Name})

		default:
			return ctx.compileUnary(node.X, &Select{node.Sel.Name})

		}

	case *ast.TypeAssertExpr: // <expression>.(<type>)
		return ctx.compileTypeAssert(node)

	case *ast.IndexExpr:
		return ctx.compileBinary(node.X, node.Index, nil, &Index{node})

	case *ast.SliceExpr:
		if node.Slice3 {
			return fmt.Errorf("3-index slice expressions not supported")
		}
		return ctx.compileReslice(node)

	case *ast.StarExpr:
		// pointer dereferencing *<expression>
		return ctx.compileUnary(node.X, &PointerDeref{node})

	case *ast.UnaryExpr:
		// The unary operators we support are +, - and & (note that unary * is parsed as ast.StarExpr)
		switch node.Op {
		case token.AND:
			return ctx.compileUnary(node.X, &AddrOf{node})
		default:
			return ctx.compileUnary(node.X, &Unary{node})
		}

	case *ast.BinaryExpr:
		switch node.Op {
		case token.INC, token.DEC, token.ARROW:
			return fmt.Errorf("operator %s not supported", node.Op.String())
		}
		// short circuits logical operators
		var sop *Jump
		switch node.Op {
		case token.LAND:
			sop = &Jump{When: JumpIfFalse, Node: node.X}
		case token.LOR:
			sop = &Jump{When: JumpIfTrue, Node: node.X}
		}
		err := ctx.compileBinary(node.X, node.Y, sop, &Binary{node})
		if err != nil {
			return err
		}
		if sop != nil {
			sop.Target = len(ctx.ops)
			ctx.pushOp(&BoolToConst{})
		}

	case *ast.BasicLit:
		ctx.pushOp(&PushConst{constant.MakeFromLiteral(node.Value, node.Kind, 0)})

	case *ast.CompositeLit:
		return ctx.compileCompositeLit(node)

	default:
		return fmt.Errorf("expression %T not implemented", t)
	}
	return nil
}

func (ctx *compileCtx) compileTypeCastOrFuncCall(node *ast.CallExpr) error {
	if len(node.Args) != 1 {
		// Things that have more or less than one argument are always function calls.
		return ctx.compileFunctionCall(node)
	}

	ambiguous := func() error {
		// Ambiguous, could be a function call or a type cast, if node.Fun can be
		// evaluated then try to treat it as a function call, otherwise try the
		// type cast.
		ctx2 := &compileCtx{evalLookup: ctx.evalLookup}
		err0 := ctx2.compileAST(node.Fun)
		if err0 == nil {
			return ctx.compileFunctionCall(node)
		}
		return ctx.compileTypeCast(node, err0)
	}

	fnnode := node.Fun
	for {
		fnnode = removeParen(fnnode)
		n, _ := fnnode.(*ast.StarExpr)
		if n == nil {
			break
		}
		fnnode = n.X
	}

	switch n := fnnode.(type) {
	case *ast.BasicLit:
		// It can only be a ("type string")(x) type cast
		return ctx.compileTypeCast(node, nil)
	case *ast.ArrayType, *ast.StructType, *ast.FuncType, *ast.InterfaceType, *ast.MapType, *ast.ChanType:
		return ctx.compileTypeCast(node, nil)
	case *ast.SelectorExpr:
		if _, isident := n.X.(*ast.Ident); isident {
			if typ, _ := ctx.FindTypeExpr(n); typ != nil {
				return ctx.compileTypeCast(node, nil)
			}
			return ambiguous()
		}
		return ctx.compileFunctionCall(node)
	case *ast.Ident:
		if ctx.HasBuiltin(n.Name) {
			return ctx.compileFunctionCall(node)
		}
		if ctx.HasGlobal("", n.Name) || ctx.HasLocal(n.Name) {
			return ctx.compileFunctionCall(node)
		}
		return ctx.compileTypeCast(node, fmt.Errorf("could not find symbol value for %s", n.Name))
	case *ast.IndexExpr:
		// Ambiguous, could be a parametric type
		switch n.X.(type) {
		case *ast.Ident, *ast.SelectorExpr:
			// Do the type-cast first since evaluating node.Fun could be expensive.
			err := ctx.compileTypeCast(node, nil)
			if err == nil || err != reader.ErrTypeNotFound {
				return err
			}
			return ctx.compileFunctionCall(node)
		default:
			return ctx.compileFunctionCall(node)
		}
	case *ast.IndexListExpr:
		return ctx.compileTypeCast(node, nil)
	default:
		// All other expressions must be function calls
		return ctx.compileFunctionCall(node)
	}
}

func (ctx *compileCtx) compileTypeCast(node *ast.CallExpr, ambiguousErr error) error {
	err := ctx.compileAST(node.Args[0])
	if err != nil {
		return err
	}

	fnnode := node.Fun

	// remove all enclosing parenthesis from the type name
	fnnode = removeParen(fnnode)

	targetTypeStr := exprToString(removeParen(node.Fun))
	styp, err := ctx.FindTypeExpr(fnnode)
	if err != nil {
		switch targetTypeStr {
		case "[]byte", "[]uint8":
			styp = godwarf.FakeSliceType(godwarf.FakeBasicType("uint", 8))
		case "[]int32", "[]rune":
			styp = godwarf.FakeSliceType(godwarf.FakeBasicType("int", 32))
		default:
			if ambiguousErr != nil && err == reader.ErrTypeNotFound {
				return fmt.Errorf("could not evaluate function or type %s: %v", exprToString(node.Fun), ambiguousErr)
			}
			return err
		}
	}

	ctx.pushOp(&TypeCast{DwarfType: styp, Node: node})
	return nil
}

func (ctx *compileCtx) compileBuiltinCall(builtin string, args []ast.Expr) error {
	for _, arg := range args {
		err := ctx.compileAST(arg)
		if err != nil {
			return err
		}
	}
	ctx.pushOp(&BuiltinCall{builtin, args})
	return nil
}

func (ctx *compileCtx) compileIdent(node *ast.Ident) error {
	switch {
	case ctx.HasLocal(node.Name):
		ctx.pushOp(&PushLocal{Name: node.Name})
	case ctx.HasGlobal("", node.Name):
		ctx.pushOp(&PushPackageVar{"", node.Name})
	case node.Name == "true" || node.Name == "false":
		ctx.pushOp(&PushConst{constant.MakeBool(node.Name == "true")})
	case node.Name == "nil":
		ctx.pushOp(&PushNil{})
	default:
		found := false
		if regnum, ok := ctx.LookupRegisterName(node.Name); ok {
			ctx.pushOp(&PushRegister{regnum, node.Name})
			found = true
		}
		if !found {
			return fmt.Errorf("could not find symbol value for %s", node.Name)
		}
	}
	return nil
}

func (ctx *compileCtx) compileUnary(expr ast.Expr, op Op) error {
	err := ctx.compileAST(expr)
	if err != nil {
		return err
	}
	ctx.pushOp(op)
	return nil
}

func (ctx *compileCtx) compileTypeAssert(node *ast.TypeAssertExpr) error {
	err := ctx.compileAST(node.X)
	if err != nil {
		return err
	}
	// Accept .(data) as a type assertion that always succeeds, so that users
	// can access the data field of an interface without actually having to
	// type the concrete type.
	if idtyp, isident := node.Type.(*ast.Ident); !isident || idtyp.Name != "data" {
		typ, err := ctx.FindTypeExpr(node.Type)
		if err != nil {
			return err
		}
		ctx.pushOp(&TypeAssert{typ, node})
		return nil
	}
	ctx.pushOp(&TypeAssert{nil, node})
	return nil
}

func (ctx *compileCtx) compileBinary(a, b ast.Expr, sop *Jump, op Op) error {
	err := ctx.compileAST(a)
	if err != nil {
		return err
	}
	if sop != nil {
		ctx.pushOp(sop)
	}
	err = ctx.compileAST(b)
	if err != nil {
		return err
	}
	ctx.pushOp(op)
	return nil
}

func (ctx *compileCtx) compileReslice(node *ast.SliceExpr) error {
	err := ctx.compileAST(node.X)
	if err != nil {
		return err
	}

	trustLen := true
	hasHigh := false
	if node.High != nil {
		hasHigh = true
		err = ctx.compileAST(node.High)
		if err != nil {
			return err
		}
		_, isbasiclit := node.High.(*ast.BasicLit)
		trustLen = trustLen && isbasiclit
	} else {
		trustLen = false
	}

	if node.Low != nil {
		err = ctx.compileAST(node.Low)
		if err != nil {
			return err
		}
		_, isbasiclit := node.Low.(*ast.BasicLit)
		trustLen = trustLen && isbasiclit
	} else {
		ctx.pushOp(&PushConst{constant.MakeInt64(0)})
	}

	ctx.pushOp(&Reslice{Node: node, HasHigh: hasHigh, TrustLen: trustLen})
	return nil
}

func (ctx *compileCtx) compileFunctionCall(node *ast.CallExpr) error {
	if fnnode, ok := node.Fun.(*ast.Ident); ok {
		if ctx.HasBuiltin(fnnode.Name) {
			return ctx.compileBuiltinCall(fnnode.Name, node.Args)
		}
	}
	if !ctx.allowCalls {
		return ErrFuncCallNotAllowed
	}

	id := ctx.curCall
	ctx.curCall++

	oldAllowCalls := ctx.allowCalls
	oldOps := ctx.ops
	ctx.allowCalls = false
	err := ctx.compileAST(node.Fun)
	ctx.allowCalls = oldAllowCalls
	hasFunc := false
	if err != nil {
		ctx.ops = oldOps
		if err != ErrFuncCallNotAllowed {
			return err
		}
	} else {
		hasFunc = true
	}
	ctx.pushOp(&CallInjectionStart{HasFunc: hasFunc, id: id, Node: node})

	// CallInjectionStart pushes true on the stack if it needs the function argument re-evaluated
	var jmpif *Jump
	if hasFunc {
		jmpif = &Jump{When: JumpIfFalse, Pop: true}
		ctx.pushOp(jmpif)
	}
	ctx.pushOp(&Pop{})
	err = ctx.compileAST(node.Fun)
	if err != nil {
		return err
	}
	if jmpif != nil {
		jmpif.Target = len(ctx.ops)
	}

	ctx.pushOp(&CallInjectionSetTarget{id: id})

	for i, arg := range node.Args {
		err := ctx.compileAST(arg)
		if err != nil {
			return fmt.Errorf("error evaluating %q as argument %d in function %s: %v", exprToString(arg), i+1, exprToString(node.Fun), err)
		}
		if isStringLiteral(arg) {
			ctx.compileAllocLiteralString()
		}
		ctx.pushOp(&CallInjectionCopyArg{id: id, ArgNum: i, ArgExpr: arg})
	}

	ctx.pushOp(&CallInjectionComplete{id: id})

	return nil
}

func (ctx *compileCtx) compileCompositeLit(node *ast.CompositeLit) error {
	typ, err := ctx.FindTypeExpr(node.Type)
	if err != nil {
		return err
	}

	switch typ := typ.(type) {
	case *godwarf.StructType:
		return ctx.compileStructLit(typ, node.Elts)
	case *godwarf.ArrayType:
		return ctx.compileArrayOrSliceLit(typ, typ.Type, typ.Count, node.Elts)
	case *godwarf.SliceType:
		return ctx.compileArrayOrSliceLit(typ, typ.ElemType, -1, node.Elts)
	case *godwarf.MapType:
		return ctx.compileMapLit(typ, node.Elts)
	default:
		return fmt.Errorf("composite literals of %v not supported", typ)
	}
}

func (ctx *compileCtx) compileStructLit(typ *godwarf.StructType, elements []ast.Expr) error {
	fields := map[string]int{}
	for i, f := range typ.Field {
		fields[f.Name] = i
	}

	var isNamed, isPos bool
	values := make([]ast.Expr, len(typ.Field))
	for i, el := range elements {
		kv, ok := el.(*ast.KeyValueExpr)
		if ok {
			isNamed = true
		} else {
			isPos = true
		}
		if isNamed && isPos {
			return errors.New("cannot mix positional and named values in a composite literal")
		}

		if isPos {
			if i >= len(typ.Field) {
				return fmt.Errorf("too many values for %v: want %v, got %v", typ, len(typ.Field), len(elements))
			}
			values[i] = el
			continue
		}

		ident, ok := kv.Key.(*ast.Ident)
		if !ok {
			return fmt.Errorf("expression %T not supported as a composite literal key for a struct type", kv.Key)
		}

		i, ok := fields[ident.Name]
		if !ok {
			return fmt.Errorf("%s is not a field of %v", ident.Name, typ)
		}
		if values[i] != nil {
			return fmt.Errorf("duplicate field %s in struct literal", ident.Name)
		}
		values[i] = kv.Value
	}
	if isPos && len(elements) < len(typ.Field) {
		return fmt.Errorf("too few values for %v: want %v, got %v", typ, len(typ.Field), len(elements))
	}

	// push values
	for i, v := range values {
		if v != nil {
			err := ctx.compileAST(v)
			if err != nil {
				return err
			}
			continue
		}

		// add the default value for the field
		err := ctx.pushZero(typ.Field[i].Type)
		if err != nil {
			return err
		}
	}

	ctx.pushOp(&CompositeLit{typ, len(values)})

	return nil
}

func (ctx *compileCtx) compileArrayOrSliceLit(typ, elTyp godwarf.Type, count int64, elements []ast.Expr) error {
	var values []ast.Expr
	if count >= 0 {
		values = make([]ast.Expr, count)
	}

	i := -1
	for _, el := range elements {
		i++
		if kv, ok := el.(*ast.KeyValueExpr); ok {
			lit, ok := kv.Key.(*ast.BasicLit)
			if !ok {
				return fmt.Errorf("unsupported non-constant index for array or slice literal")
			}
			cv := constant.MakeFromLiteral(lit.Value, lit.Kind, 0)
			j, ok := constant.Int64Val(cv)
			if !ok {
				return fmt.Errorf("cannot use a %v as an index for a array or slice literal", cv.Kind())
			}
			i = int(j)
			el = kv.Value
		}

		if count >= 0 && i >= int(count) {
			return fmt.Errorf("index %d out of bounds for array or slice literal", i)
		} else if len(values) <= i {
			values = append(values, make([]ast.Expr, i-len(values)+1)...)
		}

		if values[i] != nil {
			return fmt.Errorf("duplicate index %d in array or slice literal", i)
		}
		values[i] = el
	}

	// push values
	for _, v := range values {
		if v != nil {
			err := ctx.compileAST(v)
			if err != nil {
				return err
			}
			continue
		}

		// add the default value for the field
		err := ctx.pushZero(elTyp)
		if err != nil {
			return err
		}
	}

	ctx.pushOp(&CompositeLit{typ, len(values)})

	return nil
}

func (ctx *compileCtx) compileMapLit(typ *godwarf.MapType, elements []ast.Expr) error {
	for _, el := range elements {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			// should not happen
			panic(fmt.Errorf("map literal contains a %T!?", el))
		}

		// parse the key and value
		err := ctx.compileAST(kv.Key)
		if err != nil {
			return err
		}
		err = ctx.compileAST(kv.Value)
		if err != nil {
			return err
		}
	}

	ctx.pushOp(&CompositeLit{typ, len(elements) * 2})

	return nil
}

func (ctx *compileCtx) pushZero(typ godwarf.Type) error {
	// add the default value for the field
	switch typ.Common().ReflectKind {
	case reflect.Bool:
		ctx.pushOp(&PushConst{constant.MakeBool(false)})
		return nil

	case reflect.Int,
		reflect.Int8,
		reflect.Int16,
		reflect.Int32,
		reflect.Int64,
		reflect.Uint,
		reflect.Uint8,
		reflect.Uint16,
		reflect.Uint32,
		reflect.Uint64,
		reflect.Uintptr,
		reflect.Float32,
		reflect.Float64,
		reflect.Complex64,
		reflect.Complex128:
		ctx.pushOp(&PushConst{constant.MakeInt64(0)})
		return nil

	case reflect.Chan,
		reflect.Func,
		reflect.Interface,
		reflect.Map,
		reflect.Pointer,
		reflect.Slice:
		ctx.pushOp(&PushNil{})
		return nil

	case reflect.String:
		ctx.pushOp(&PushConst{constant.MakeString("")})
		return nil

	case reflect.Struct:
		return ctx.compileStructLit(typ.(*godwarf.StructType), nil)

	default:
		// TODO reflect.UnsafePointer, reflect.Array
		return fmt.Errorf("unsupported struct literal field type %v", typ)
	}
}

func Listing(depth []int, ops []Op) string {
	if depth == nil {
		depth = make([]int, len(ops)+1)
	}
	buf := new(strings.Builder)
	for i, op := range ops {
		fmt.Fprintf(buf, " %3d  (%2d->%2d) %#v\n", i, depth[i], depth[i+1], op)
	}
	return buf.String()
}

func isStringLiteral(expr ast.Expr) bool {
	switch expr := expr.(type) {
	case *ast.BasicLit:
		return expr.Kind == token.STRING
	case *ast.BinaryExpr:
		if expr.Op == token.ADD {
			return isStringLiteral(expr.X) && isStringLiteral(expr.Y)
		}
	case *ast.ParenExpr:
		return isStringLiteral(expr.X)
	}
	return false
}

func removeParen(n ast.Expr) ast.Expr {
	for {
		p, ok := n.(*ast.ParenExpr)
		if !ok {
			break
		}
		n = p.X
	}
	return n
}

func exprToString(t ast.Expr) string {
	var buf bytes.Buffer
	printer.Fprint(&buf, token.NewFileSet(), t)
	return buf.String()
}
