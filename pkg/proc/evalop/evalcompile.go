package evalop

import (
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/scanner"
	"go/token"
	"strconv"
	"strings"

	"github.com/go-delve/delve/pkg/astutil"
	"github.com/go-delve/delve/pkg/dwarf/godwarf"
	"github.com/go-delve/delve/pkg/dwarf/reader"
)

var (
	ErrFuncCallNotAllowed = errors.New("function calls not allowed without using 'call'")
	errFuncCallNotAllowedLitAlloc = errors.New("literal can not be allocated because function calls are not allowed without using 'call'")
)

const (
	BreakpointHitCountVarNamePackage   = "delve"
	BreakpointHitCountVarName          = "bphitcount"
	BreakpointHitCountVarNameQualified = BreakpointHitCountVarNamePackage + "." + BreakpointHitCountVarName
	DebugPinnerFunctionName            = "runtime.debugPinnerV1"
)

type compileCtx struct {
	evalLookup
	ops        []Op
	allowCalls bool
	curCall    int
	flags      Flags
	pinnerUsed bool
	hasCalls   bool
}

type evalLookup interface {
	FindTypeExpr(ast.Expr) (godwarf.Type, error)
	HasBuiltin(string) bool
	PtrSize() int
}

// Flags describes flags used to control Compile and CompileAST
type Flags uint8

const (
	CanSet         Flags = 1 << iota // Assignment is allowed
	HasDebugPinner                   // runtime.debugPinner is available
)

// CompileAST compiles the expression t into a list of instructions.
func CompileAST(lookup evalLookup, t ast.Expr, flags Flags) ([]Op, error) {
	ctx := &compileCtx{evalLookup: lookup, allowCalls: true, flags: flags}
	err := ctx.compileAST(t, true)
	if err != nil {
		return nil, err
	}

	ctx.compileDebugPinnerSetupTeardown()

	err = ctx.depthCheck(1)
	if err != nil {
		return ctx.ops, err
	}
	return ctx.ops, nil
}

// Compile compiles the expression expr into a list of instructions.
// If canSet is true expressions like "x = y" are also accepted.
func Compile(lookup evalLookup, expr string, flags Flags) ([]Op, error) {
	t, err := parser.ParseExpr(expr)
	if err != nil {
		if flags&CanSet != 0 {
			eqOff, isAs := isAssignment(err)
			if isAs {
				return CompileSet(lookup, expr[:eqOff], expr[eqOff+1:], flags)
			}
		}
		return nil, err
	}
	return CompileAST(lookup, t, flags)
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
func CompileSet(lookup evalLookup, lhexpr, rhexpr string, flags Flags) ([]Op, error) {
	lhe, err := parser.ParseExpr(lhexpr)
	if err != nil {
		return nil, err
	}
	rhe, err := parser.ParseExpr(rhexpr)
	if err != nil {
		return nil, err
	}

	ctx := &compileCtx{evalLookup: lookup, allowCalls: true, flags: flags}
	err = ctx.compileAST(rhe, false)
	if err != nil {
		return nil, err
	}

	err = ctx.maybeMaterialize(rhe)
	if err != nil {
		return nil, err
	}

	err = ctx.compileAST(lhe, false)
	if err != nil {
		return nil, err
	}

	ctx.pushOp(&SetValue{lhe: lhe, Rhe: rhe})

	ctx.compileDebugPinnerSetupTeardown()

	err = ctx.depthCheck(0)
	if err != nil {
		return ctx.ops, err
	}
	return ctx.ops, nil
}

func (ctx *compileCtx) compileAllocLiteralString() {
	jmp := &Jump{When: JumpIfAllocStringChecksFail}
	ctx.pushOp(jmp)

	ctx.compileSpecialCall("runtime.mallocgc", []ast.Expr{
		&ast.BasicLit{Kind: token.INT, Value: "0"},
		&ast.Ident{Name: "nil"},
		&ast.Ident{Name: "false"},
	}, []Op{
		&PushLen{},
		&PushNil{},
		&PushConst{constant.MakeBool(false)},
	}, specialCallDoPinning|specialCallIsStringAlloc)

	ctx.pushOp(&ConvertAllocToString{})
	jmp.Target = len(ctx.ops)
}

type specialCallFlags uint8

const (
	specialCallDoPinning specialCallFlags = 1 << iota
	specialCallIsStringAlloc
	specialCallComplainAboutStringAlloc
)

func (ctx *compileCtx) compileSpecialCall(fnname string, argAst []ast.Expr, args []Op, flags specialCallFlags) {
	doPinning := flags&specialCallDoPinning != 0

	id := ctx.curCall
	ctx.curCall++
	ctx.pushOp(&CallInjectionStartSpecial{
		id:     id,
		FnName: fnname,
		ArgAst: argAst,

		ComplainAboutStringAlloc: flags&specialCallComplainAboutStringAlloc != 0})
	ctx.pushOp(&CallInjectionSetTarget{id: id})

	for i := range args {
		if args[i] != nil {
			ctx.pushOp(args[i])
		}
		ctx.pushOp(&CallInjectionCopyArg{id: id, ArgNum: i})
	}

	doPinning = doPinning && (ctx.flags&HasDebugPinner != 0)

	if doPinning {
		ctx.pinnerUsed = true
		if flags&specialCallIsStringAlloc == 0 {
			ctx.hasCalls = true
		}
	}

	ctx.pushOp(&CallInjectionComplete{id: id, DoPinning: doPinning})

	if doPinning {
		ctx.compilePinningLoop(id)
	}
}

func (ctx *compileCtx) compileDebugPinnerSetupTeardown() {
	if !ctx.pinnerUsed {
		return
	}

	// Prepend debug pinner allocation
	mainops := ctx.ops
	ctx.ops = []Op{}
	flags := specialCallFlags(0)
	if !ctx.hasCalls {
		flags = specialCallComplainAboutStringAlloc
	}
	ctx.compileSpecialCall(DebugPinnerFunctionName, []ast.Expr{}, []Op{}, flags)
	ctx.pushOp(&SetDebugPinner{})

	// Adjust jump destinations
	for _, op := range mainops {
		switch op := op.(type) {
		case *Jump:
			op.Target += len(ctx.ops)
		}
	}

	ctx.ops = append(ctx.ops, mainops...)

	// Append debug pinner deallocation
	ctx.compileSpecialCall("runtime.(*Pinner).Unpin", []ast.Expr{
		&ast.Ident{Name: "debugPinner"},
	}, []Op{
		&PushDebugPinner{},
	}, 0)
	ctx.pushOp(&Pop{})
	ctx.pushOp(&PushNil{})
	ctx.pushOp(&SetDebugPinner{})
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

	debugPinnerSeen := false

	for i, op := range ctx.ops {
		npop, npush := op.depthCheck()
		if depth[i] < npop {
			return fmt.Errorf("internal debugger error: depth check error at instruction %d: expected at least %d have %d\n%s", i, npop, depth[i], Listing(depth, ctx.ops))
		}
		d := depth[i] - npop + npush
		checkAndSet(i+1, d)
		switch op := op.(type) {
		case *Jump:
			checkAndSet(op.Target, d)
		case *CallInjectionStartSpecial:
			debugPinnerSeen = true
		case *CallInjectionComplete:
			if op.DoPinning && !debugPinnerSeen {
				err = fmt.Errorf("internal debugger error: pinning call injection seen before call to %s at instruction %d", DebugPinnerFunctionName, i)
			}
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

func (ctx *compileCtx) compileAST(t ast.Expr, toplevel bool) error {
	switch node := t.(type) {
	case *ast.CallExpr:
		return ctx.compileTypeCastOrFuncCall(node, toplevel)

	case *ast.Ident:
		return ctx.compileIdent(node)

	case *ast.ParenExpr:
		// otherwise just eval recursively
		return ctx.compileAST(node.X, false)

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

			case x.Name == "runtime" && node.Sel.Name == "rangeParentOffset":
				ctx.pushOp(&PushRangeParentOffset{})

			case x.Name == BreakpointHitCountVarNamePackage && node.Sel.Name == BreakpointHitCountVarName:
				ctx.pushOp(&PushBreakpointHitCount{})

			default:
				ctx.pushOp(&PushPackageVarOrSelect{Name: x.Name, Sel: node.Sel.Name})
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
			ctx.pushOp(&PushPackageVarOrSelect{Name: s, Sel: node.Sel.Name, NameIsString: true})

		default:
			return ctx.compileUnary(node.X, &Select{node.Sel.Name})
		}

	case *ast.TypeAssertExpr: // <expression>.(<type>)
		return ctx.compileTypeAssert(node)

	case *ast.IndexExpr:
		return ctx.compileBinary(node.X, node.Index, nil, &Index{node})

	case *ast.SliceExpr:
		if node.Slice3 {
			return errors.New("3-index slice expressions not supported")
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
		notimplerr := fmt.Errorf("expression %T not implemented", t)
		if ctx.flags&HasDebugPinner == 0 {
			return notimplerr
		}
		dtyp, err := ctx.FindTypeExpr(node.Type)
		if err != nil {
			return err
		}
		typ := godwarf.ResolveTypedef(dtyp)
		switch typ := typ.(type) {
		case *godwarf.StructType:
			if !ctx.allowCalls {
				return errFuncCallNotAllowedLitAlloc
			}

			ctx.pushOp(&PushNewFakeVariable{Type: dtyp})

			for i, elt := range node.Elts {
				ctx.pushOp(&Dup{})

				var rhe ast.Expr
				switch elt := elt.(type) {
				case *ast.KeyValueExpr:
					ctx.pushOp(&Select{Name: elt.Key.(*ast.Ident).Name})
					rhe = elt.Value
					err := ctx.compileAST(elt.Value, false)
					if err != nil {
						return err
					}
					err = ctx.maybeMaterialize(elt.Value)
					if err != nil {
						return err
					}
				default:
					ctx.pushOp(&Select{Name: typ.Field[i].Name})
					rhe = elt
					err := ctx.compileAST(elt, false)
					if err != nil {
						return err
					}
					err = ctx.maybeMaterialize(elt)
					if err != nil {
						return err
					}
				}
				ctx.pushOp(&Roll{1})
				ctx.pushOp(&SetValue{Rhe: rhe})
			}

		case *godwarf.SliceType:
			return notimplerr

		case *godwarf.MapType:
			return notimplerr

		case *godwarf.ArrayType:
			return notimplerr

		default:
			return notimplerr
		}

	default:
		return fmt.Errorf("expression %T not implemented", t)
	}
	return nil
}

func (ctx *compileCtx) compileTypeCastOrFuncCall(node *ast.CallExpr, toplevel bool) error {
	if len(node.Args) != 1 {
		// Things that have more or less than one argument are always function calls.
		return ctx.compileFunctionCall(node, toplevel)
	}

	ambiguous := func() error {
		// Ambiguous, could be a function call or a type cast, if node.Fun can be
		// evaluated then try to treat it as a function call, otherwise try the
		// type cast.
		ctx2 := &compileCtx{evalLookup: ctx.evalLookup}
		err0 := ctx2.compileAST(node.Fun, false)
		if err0 == nil {
			return ctx.compileFunctionCall(node, toplevel)
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
		return ctx.compileFunctionCall(node, toplevel)
	case *ast.Ident:
		if typ, _ := ctx.FindTypeExpr(n); typ != nil {
			return ctx.compileTypeCast(node, fmt.Errorf("could not find symbol value for %s", n.Name))
		}
		return ctx.compileFunctionCall(node, toplevel)
	case *ast.IndexExpr:
		// Ambiguous, could be a parametric type
		switch n.X.(type) {
		case *ast.Ident, *ast.SelectorExpr:
			// Do the type-cast first since evaluating node.Fun could be expensive.
			err := ctx.compileTypeCast(node, nil)
			if err == nil || err != reader.ErrTypeNotFound {
				return err
			}
			return ctx.compileFunctionCall(node, toplevel)
		default:
			return ctx.compileFunctionCall(node, toplevel)
		}
	case *ast.IndexListExpr:
		return ctx.compileTypeCast(node, nil)
	default:
		// All other expressions must be function calls
		return ctx.compileFunctionCall(node, toplevel)
	}
}

func (ctx *compileCtx) compileTypeCast(node *ast.CallExpr, ambiguousErr error) error {
	err := ctx.compileAST(node.Args[0], false)
	if err != nil {
		return err
	}

	fnnode := node.Fun

	// remove all enclosing parenthesis from the type name
	fnnode = removeParen(fnnode)

	targetTypeStr := astutil.ExprToString(removeParen(node.Fun))
	styp, err := ctx.FindTypeExpr(fnnode)
	if err != nil {
		switch targetTypeStr {
		case "[]byte", "[]uint8":
			styp = godwarf.FakeSliceType(godwarf.FakeBasicType("uint", 8))
		case "[]int32", "[]rune":
			styp = godwarf.FakeSliceType(godwarf.FakeBasicType("int", 32))
		default:
			if ambiguousErr != nil && err == reader.ErrTypeNotFound {
				return fmt.Errorf("could not evaluate function or type %s: %v", astutil.ExprToString(node.Fun), ambiguousErr)
			}
			return err
		}
	}

	ctx.pushOp(&TypeCast{DwarfType: styp, Node: node})
	return nil
}

func (ctx *compileCtx) compileBuiltinCall(builtin string, args []ast.Expr) error {
	for _, arg := range args {
		err := ctx.compileAST(arg, false)
		if err != nil {
			return err
		}
	}
	ctx.pushOp(&BuiltinCall{builtin, args})
	return nil
}

func (ctx *compileCtx) compileIdent(node *ast.Ident) error {
	ctx.pushOp(&PushIdent{node.Name})
	return nil
}

func (ctx *compileCtx) compileUnary(expr ast.Expr, op Op) error {
	err := ctx.compileAST(expr, false)
	if err != nil {
		return err
	}
	ctx.pushOp(op)
	return nil
}

func (ctx *compileCtx) compileTypeAssert(node *ast.TypeAssertExpr) error {
	err := ctx.compileAST(node.X, false)
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
	err := ctx.compileAST(a, false)
	if err != nil {
		return err
	}
	if sop != nil {
		ctx.pushOp(sop)
	}
	err = ctx.compileAST(b, false)
	if err != nil {
		return err
	}
	ctx.pushOp(op)
	return nil
}

func (ctx *compileCtx) compileReslice(node *ast.SliceExpr) error {
	err := ctx.compileAST(node.X, false)
	if err != nil {
		return err
	}

	trustLen := true
	hasHigh := false
	if node.High != nil {
		hasHigh = true
		err = ctx.compileAST(node.High, false)
		if err != nil {
			return err
		}
		_, isbasiclit := node.High.(*ast.BasicLit)
		trustLen = trustLen && isbasiclit
	} else {
		trustLen = false
	}

	if node.Low != nil {
		err = ctx.compileAST(node.Low, false)
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

func (ctx *compileCtx) compileFunctionCall(node *ast.CallExpr, toplevel bool) error {
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

	if ctx.flags&HasDebugPinner != 0 {
		return ctx.compileFunctionCallWithPinning(node, id, toplevel)
	}

	return ctx.compileFunctionCallNoPinning(node, id)
}

// compileFunctionCallNoPinning compiles a function call when runtime.debugPinner is
// not available in the target.
func (ctx *compileCtx) compileFunctionCallNoPinning(node *ast.CallExpr, id int) error {
	oldAllowCalls := ctx.allowCalls
	oldOps := ctx.ops
	ctx.allowCalls = false
	err := ctx.compileAST(node.Fun, false)
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
	err = ctx.compileAST(node.Fun, false)
	if err != nil {
		return err
	}
	if jmpif != nil {
		jmpif.Target = len(ctx.ops)
	}

	ctx.pushOp(&CallInjectionSetTarget{id: id})

	for i, arg := range node.Args {
		err := ctx.compileAST(arg, false)
		if err != nil {
			return fmt.Errorf("error evaluating %q as argument %d in function %s: %v", astutil.ExprToString(arg), i+1, astutil.ExprToString(node.Fun), err)
		}
		err = ctx.maybeMaterialize(arg)
		if err != nil {
			return err
		}
		ctx.pushOp(&CallInjectionCopyArg{id: id, ArgNum: i, ArgExpr: arg})
	}

	ctx.pushOp(&CallInjectionComplete{id: id})

	return nil
}

// compileFunctionCallWithPinning compiles a function call when runtime.debugPinner
// is available in the target.
func (ctx *compileCtx) compileFunctionCallWithPinning(node *ast.CallExpr, id int, toplevel bool) error {
	if !toplevel {
		ctx.pinnerUsed = true
	}
	ctx.hasCalls = true

	err := ctx.compileAST(node.Fun, false)
	if err != nil {
		return err
	}

	for i, arg := range node.Args {
		err := ctx.compileAST(arg, false)
		if err != nil {
			return fmt.Errorf("error evaluating %q as argument %d in function %s: %v", astutil.ExprToString(arg), i+1, astutil.ExprToString(node.Fun), err)
		}
		err = ctx.maybeMaterialize(arg)
		if err != nil {
			return err
		}
	}

	ctx.pushOp(&Roll{len(node.Args)})
	ctx.pushOp(&CallInjectionStart{HasFunc: true, id: id, Node: node})
	ctx.pushOp(&Pop{})
	ctx.pushOp(&CallInjectionSetTarget{id: id})

	for i := len(node.Args) - 1; i >= 0; i-- {
		arg := node.Args[i]
		ctx.pushOp(&CallInjectionCopyArg{id: id, ArgNum: i, ArgExpr: arg})
	}

	ctx.pushOp(&CallInjectionComplete{id: id, DoPinning: !toplevel})

	if !toplevel {
		ctx.compilePinningLoop(id)
	}

	return nil
}

func (ctx *compileCtx) compilePinningLoop(id int) {
	loopStart := len(ctx.ops)
	jmp := &Jump{When: JumpIfPinningDone}
	ctx.pushOp(jmp)
	ctx.pushOp(&PushPinAddress{})
	ctx.compileSpecialCall("runtime.(*Pinner).Pin", []ast.Expr{
		&ast.Ident{Name: "debugPinner"},
		&ast.Ident{Name: "pinAddress"},
	}, []Op{
		&PushDebugPinner{},
		nil,
	}, 0)
	ctx.pushOp(&Pop{})
	ctx.pushOp(&Jump{When: JumpAlways, Target: loopStart})
	jmp.Target = len(ctx.ops)
	ctx.pushOp(&CallInjectionComplete2{id: id})
}

// If expr will produce a string literal or a pointer to a literal allocate
// it into the target program's address space.
func (ctx *compileCtx) maybeMaterialize(expr ast.Expr) error {
	if isStringLiteralThatNeedsAlloc(expr) {
		ctx.compileAllocLiteralString()
		return nil
	}

	expr = removeParen(expr)
	addrof, isunary := expr.(*ast.UnaryExpr)
	if !isunary || addrof.Op != token.AND {
		return nil
	}
	lit, iscomplit := addrof.X.(*ast.CompositeLit)
	if !iscomplit {
		return nil
	}

	dtyp, err := ctx.FindTypeExpr(lit.Type)
	if err != nil {
		return err
	}

	switch godwarf.ResolveTypedef(dtyp).(type) {
	case *godwarf.StructType, *godwarf.ArrayType:
		if _, isaddrof := ctx.ops[len(ctx.ops)-1].(*AddrOf); isaddrof {
			ctx.ops = ctx.ops[:len(ctx.ops)-1]
		} else {
			ctx.pushOp(&PointerDeref{&ast.StarExpr{X: &ast.Ident{Name: "unallocated-literal"}}})
		}

		ctx.compileSpecialCall("runtime.mallocgc", []ast.Expr{
			&ast.BasicLit{Kind: token.INT, Value: "1"},
			lit.Type,
			&ast.Ident{Name: "true"},
		}, []Op{
			&PushConst{Value: constant.MakeInt64(1)},
			&PushRuntimeType{dtyp},
			&PushConst{Value: constant.MakeBool(true)},
		}, specialCallDoPinning)
		ctx.pushOp(&TypeCast{
			DwarfType: godwarf.FakePointerType(dtyp, int64(ctx.PtrSize())),
			Node: &ast.CallExpr{
				Fun:  lit.Type,
				Args: []ast.Expr{&ast.Ident{Name: "new allocation"}}}})

		xderef := &ast.StarExpr{X: &ast.Ident{Name: "new-allocation"}}
		xset := &ast.Ident{Name: "literal-allocation"}

		ctx.pushOp(&Dup{})                // stack after: [ ptrToRealLiteral, ptrToRealLiteral, fakeLiteral ]
		ctx.pushOp(&PointerDeref{xderef}) // stack after: [ realLiteral, ptrToRealLiteral, fakeLiteral ]
		ctx.pushOp(&Roll{2})              // stack after: [ fakeLiteral, realLiteral, ptrToRealLiteral ]
		ctx.pushOp(&Roll{1})              // stack after: [ realLiteral, fakeLiteral, ptrToRealLiteral ]
		ctx.pushOp(&SetValue{Rhe: xset})  // stack after: [ ptrToRealLiteral ]
		return nil

	default:
		// either *godwarf.MapType, *godwarf.SliceType or *godwarf.FuncType
		return fmt.Errorf("allocating a literal of type %s not implemented", dtyp.String())
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

func isStringLiteralThatNeedsAlloc(expr ast.Expr) bool {
	switch expr := expr.(type) {
	case *ast.BasicLit:
		return expr.Kind == token.STRING && expr.Value != `""`
	case *ast.BinaryExpr:
		if expr.Op == token.ADD {
			return isStringLiteralThatNeedsAlloc(expr.X) && isStringLiteralThatNeedsAlloc(expr.Y)
		}
	case *ast.ParenExpr:
		return isStringLiteralThatNeedsAlloc(expr.X)
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
