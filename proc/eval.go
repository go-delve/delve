package proc

import (
	"bytes"
	"debug/dwarf"
	"encoding/binary"
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/printer"
	"go/token"
	"reflect"

	"github.com/derekparker/delve/dwarf/reader"
)

// EvalExpression returns the value of the given expression.
func (scope *EvalScope) EvalExpression(expr string) (*Variable, error) {
	t, err := parser.ParseExpr(expr)
	if err != nil {
		return nil, err
	}

	ev, err := scope.evalAST(t)
	if err != nil {
		return nil, err
	}
	ev.loadValue()
	return ev, nil
}

func (scope *EvalScope) evalAST(t ast.Expr) (*Variable, error) {
	switch node := t.(type) {
	case *ast.CallExpr:
		if len(node.Args) == 1 {
			v, err := scope.evalTypeCast(node)
			if err == nil {
				return v, nil
			}
			if err != reader.TypeNotFoundErr {
				return v, err
			}
		}
		return scope.evalBuiltinCall(node)

	case *ast.Ident:
		return scope.evalIdent(node)

	case *ast.ParenExpr:
		// otherwise just eval recursively
		return scope.evalAST(node.X)

	case *ast.SelectorExpr: // <expression>.<identifier>
		// try to interpret the selector as a package variable
		if maybePkg, ok := node.X.(*ast.Ident); ok {
			if v, err := scope.packageVarAddr(maybePkg.Name + "." + node.Sel.Name); err == nil {
				return v, nil
			}
		}
		// if it's not a package variable then it must be a struct member access
		return scope.evalStructSelector(node)

	case *ast.TypeAssertExpr: // <expression>.(<type>)
		return scope.evalTypeAssert(node)

	case *ast.IndexExpr:
		return scope.evalIndex(node)

	case *ast.SliceExpr:
		if node.Slice3 {
			return nil, fmt.Errorf("3-index slice expressions not supported")
		}
		return scope.evalReslice(node)

	case *ast.StarExpr:
		// pointer dereferencing *<expression>
		return scope.evalPointerDeref(node)

	case *ast.UnaryExpr:
		// The unary operators we support are +, - and & (note that unary * is parsed as ast.StarExpr)
		switch node.Op {
		case token.AND:
			return scope.evalAddrOf(node)

		default:
			return scope.evalUnary(node)
		}

	case *ast.BinaryExpr:
		return scope.evalBinary(node)

	case *ast.BasicLit:
		return newConstant(constant.MakeFromLiteral(node.Value, node.Kind, 0), scope.Thread), nil

	default:
		return nil, fmt.Errorf("expression %T not implemented", t)

	}
}

func exprToString(t ast.Expr) string {
	var buf bytes.Buffer
	printer.Fprint(&buf, token.NewFileSet(), t)
	return buf.String()
}

// Eval type cast expressions
func (scope *EvalScope) evalTypeCast(node *ast.CallExpr) (*Variable, error) {
	argv, err := scope.evalAST(node.Args[0])
	if err != nil {
		return nil, err
	}
	argv.loadValue()
	if argv.Unreadable != nil {
		return nil, argv.Unreadable
	}

	fnnode := node.Fun

	// remove all enclosing parenthesis from the type name
	for {
		p, ok := fnnode.(*ast.ParenExpr)
		if !ok {
			break
		}
		fnnode = p.X
	}

	styp, err := scope.Thread.dbp.findTypeExpr(fnnode)
	if err != nil {
		return nil, err
	}
	typ := resolveTypedef(styp)

	converr := fmt.Errorf("can not convert %q to %s", exprToString(node.Args[0]), typ.String())

	v := newVariable("", 0, styp, scope.Thread.dbp, scope.Thread)
	v.loaded = true

	switch ttyp := typ.(type) {
	case *dwarf.PtrType:
		if ptrTypeKind(ttyp) != reflect.Ptr {
			return nil, converr
		}
		switch argv.Kind {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			// ok
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			// ok
		default:
			return nil, converr
		}

		n, _ := constant.Int64Val(argv.Value)

		v.Children = []Variable{*(scope.newVariable("", uintptr(n), ttyp.Type))}
		return v, nil

	case *dwarf.UintType:
		switch argv.Kind {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			n, _ := constant.Int64Val(argv.Value)
			v.Value = constant.MakeUint64(convertInt(uint64(n), false, ttyp.Size()))
			return v, nil
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			n, _ := constant.Uint64Val(argv.Value)
			v.Value = constant.MakeUint64(convertInt(n, false, ttyp.Size()))
			return v, nil
		case reflect.Float32, reflect.Float64:
			x, _ := constant.Float64Val(argv.Value)
			v.Value = constant.MakeUint64(uint64(x))
			return v, nil
		}
	case *dwarf.IntType:
		switch argv.Kind {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			n, _ := constant.Int64Val(argv.Value)
			v.Value = constant.MakeInt64(int64(convertInt(uint64(n), true, ttyp.Size())))
			return v, nil
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			n, _ := constant.Uint64Val(argv.Value)
			v.Value = constant.MakeInt64(int64(convertInt(n, true, ttyp.Size())))
			return v, nil
		case reflect.Float32, reflect.Float64:
			x, _ := constant.Float64Val(argv.Value)
			v.Value = constant.MakeInt64(int64(x))
			return v, nil
		}
	case *dwarf.FloatType:
		switch argv.Kind {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			fallthrough
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			fallthrough
		case reflect.Float32, reflect.Float64:
			v.Value = argv.Value
			return v, nil
		}
	case *dwarf.ComplexType:
		switch argv.Kind {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			fallthrough
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			fallthrough
		case reflect.Float32, reflect.Float64:
			v.Value = argv.Value
			return v, nil
		}
	}

	return nil, converr
}

func convertInt(n uint64, signed bool, size int64) uint64 {
	buf := make([]byte, 64/8)
	binary.BigEndian.PutUint64(buf, n)
	m := 64/8 - int(size)
	s := byte(0)
	if signed && (buf[m]&0x80 > 0) {
		s = 0xff
	}
	for i := 0; i < m; i++ {
		buf[i] = s
	}
	return uint64(binary.BigEndian.Uint64(buf))
}

func (scope *EvalScope) evalBuiltinCall(node *ast.CallExpr) (*Variable, error) {
	fnnode, ok := node.Fun.(*ast.Ident)
	if !ok {
		return nil, fmt.Errorf("function calls are not supported")
	}

	args := make([]*Variable, len(node.Args))

	for i := range node.Args {
		v, err := scope.evalAST(node.Args[i])
		if err != nil {
			return nil, err
		}
		args[i] = v
	}

	switch fnnode.Name {
	case "cap":
		return capBuiltin(args, node.Args)
	case "len":
		return lenBuiltin(args, node.Args)
	case "complex":
		return complexBuiltin(args, node.Args)
	case "imag":
		return imagBuiltin(args, node.Args)
	case "real":
		return realBuiltin(args, node.Args)
	}

	return nil, fmt.Errorf("function calls are not supported")
}

func capBuiltin(args []*Variable, nodeargs []ast.Expr) (*Variable, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("wrong number of arguments to cap: %d", len(args))
	}

	arg := args[0]
	invalidArgErr := fmt.Errorf("invalid argument %s (type %s) for cap", exprToString(nodeargs[0]), arg.TypeString())

	switch arg.Kind {
	case reflect.Ptr:
		arg = arg.maybeDereference()
		if arg.Kind != reflect.Array {
			return nil, invalidArgErr
		}
		fallthrough
	case reflect.Array:
		return newConstant(constant.MakeInt64(arg.Len), arg.mem), nil
	case reflect.Slice:
		return newConstant(constant.MakeInt64(arg.Cap), arg.mem), nil
	case reflect.Chan:
		arg.loadValue()
		if arg.Unreadable != nil {
			return nil, arg.Unreadable
		}
		if arg.base == 0 {
			return newConstant(constant.MakeInt64(0), arg.mem), nil
		}
		return newConstant(arg.Children[1].Value, arg.mem), nil
	default:
		return nil, invalidArgErr
	}
}

func lenBuiltin(args []*Variable, nodeargs []ast.Expr) (*Variable, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("wrong number of arguments to len: %d", len(args))
	}
	arg := args[0]
	invalidArgErr := fmt.Errorf("invalid argument %s (type %s) for len", exprToString(nodeargs[0]), arg.TypeString())

	switch arg.Kind {
	case reflect.Ptr:
		arg = arg.maybeDereference()
		if arg.Kind != reflect.Array {
			return nil, invalidArgErr
		}
		fallthrough
	case reflect.Array, reflect.Slice, reflect.String:
		if arg.Unreadable != nil {
			return nil, arg.Unreadable
		}
		return newConstant(constant.MakeInt64(arg.Len), arg.mem), nil
	case reflect.Chan:
		arg.loadValue()
		if arg.Unreadable != nil {
			return nil, arg.Unreadable
		}
		if arg.base == 0 {
			return newConstant(constant.MakeInt64(0), arg.mem), nil
		}
		return newConstant(arg.Children[0].Value, arg.mem), nil
	case reflect.Map:
		it := arg.mapIterator()
		if arg.Unreadable != nil {
			return nil, arg.Unreadable
		}
		if it == nil {
			return newConstant(constant.MakeInt64(0), arg.mem), nil
		}
		return newConstant(constant.MakeInt64(arg.Len), arg.mem), nil
	default:
		return nil, invalidArgErr
	}
}

func complexBuiltin(args []*Variable, nodeargs []ast.Expr) (*Variable, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("wrong number of arguments to complex: %d", len(args))
	}

	realev := args[0]
	imagev := args[1]

	realev.loadValue()
	imagev.loadValue()

	if realev.Unreadable != nil {
		return nil, realev.Unreadable
	}

	if imagev.Unreadable != nil {
		return nil, imagev.Unreadable
	}

	if realev.Value == nil || ((realev.Value.Kind() != constant.Int) && (realev.Value.Kind() != constant.Float)) {
		return nil, fmt.Errorf("invalid argument 1 %s (type %s) to complex", exprToString(nodeargs[0]), realev.TypeString())
	}

	if imagev.Value == nil || ((imagev.Value.Kind() != constant.Int) && (imagev.Value.Kind() != constant.Float)) {
		return nil, fmt.Errorf("invalid argument 2 %s (type %s) to complex", exprToString(nodeargs[1]), imagev.TypeString())
	}

	sz := int64(0)
	if realev.RealType != nil {
		sz = realev.RealType.(*dwarf.FloatType).Size()
	}
	if imagev.RealType != nil {
		isz := imagev.RealType.(*dwarf.FloatType).Size()
		if isz > sz {
			sz = isz
		}
	}

	if sz == 0 {
		sz = 128
	}

	typ := &dwarf.ComplexType{BasicType: dwarf.BasicType{CommonType: dwarf.CommonType{ByteSize: int64(sz / 8), Name: fmt.Sprintf("complex%d", sz)}, BitSize: sz, BitOffset: 0}}

	r := realev.newVariable("", 0, typ)
	r.Value = constant.BinaryOp(realev.Value, token.ADD, constant.MakeImag(imagev.Value))
	return r, nil
}

func imagBuiltin(args []*Variable, nodeargs []ast.Expr) (*Variable, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("wrong number of arguments to imag: %d", len(args))
	}

	arg := args[0]
	arg.loadValue()

	if arg.Unreadable != nil {
		return nil, arg.Unreadable
	}

	if arg.Kind != reflect.Complex64 && arg.Kind != reflect.Complex128 {
		return nil, fmt.Errorf("invalid argument %s (type %s) to imag", exprToString(nodeargs[0]), arg.TypeString())
	}

	return newConstant(constant.Imag(arg.Value), arg.mem), nil
}

func realBuiltin(args []*Variable, nodeargs []ast.Expr) (*Variable, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("wrong number of arguments to real: %d", len(args))
	}

	arg := args[0]
	arg.loadValue()

	if arg.Unreadable != nil {
		return nil, arg.Unreadable
	}

	if arg.Value == nil || ((arg.Value.Kind() != constant.Int) && (arg.Value.Kind() != constant.Float) && (arg.Value.Kind() != constant.Complex)) {
		return nil, fmt.Errorf("invalid argument %s (type %s) to real", exprToString(nodeargs[0]), arg.TypeString())
	}

	return newConstant(constant.Real(arg.Value), arg.mem), nil
}

// Evaluates identifier expressions
func (scope *EvalScope) evalIdent(node *ast.Ident) (*Variable, error) {
	switch node.Name {
	case "true", "false":
		return newConstant(constant.MakeBool(node.Name == "true"), scope.Thread), nil
	case "nil":
		return nilVariable, nil
	}

	// try to interpret this as a local variable
	v, err := scope.extractVarInfo(node.Name)
	if err != nil {
		origErr := err
		// workaround: sometimes go inserts an entry for '&varname' instead of varname
		v, err = scope.extractVarInfo("&" + node.Name)
		if err != nil {
			// if it's not a local variable then it could be a package variable w/o explicit package name
			_, _, fn := scope.Thread.dbp.PCToLine(scope.PC)
			if fn != nil {
				if v, err := scope.packageVarAddr(fn.PackageName() + "." + node.Name); err == nil {
					v.Name = node.Name
					return v, nil
				}
			}
			return nil, origErr
		}
		v = v.maybeDereference()
		v.Name = node.Name
	}
	return v, nil
}

// Evaluates expressions <subexpr>.<field name> where subexpr is not a package name
func (scope *EvalScope) evalStructSelector(node *ast.SelectorExpr) (*Variable, error) {
	xv, err := scope.evalAST(node.X)
	if err != nil {
		return nil, err
	}
	return xv.structMember(node.Sel.Name)
}

// Evaluates expressions <subexpr>.(<type>)
func (scope *EvalScope) evalTypeAssert(node *ast.TypeAssertExpr) (*Variable, error) {
	xv, err := scope.evalAST(node.X)
	if err != nil {
		return nil, err
	}
	if xv.Kind != reflect.Interface {
		return nil, fmt.Errorf("expression \"%s\" not an interface", exprToString(node.X))
	}
	xv.loadInterface(0, false)
	if xv.Unreadable != nil {
		return nil, xv.Unreadable
	}
	if xv.Children[0].Unreadable != nil {
		return nil, xv.Children[0].Unreadable
	}
	if xv.Children[0].Addr == 0 {
		return nil, fmt.Errorf("interface conversion: %s is nil, not %s", xv.DwarfType.String(), exprToString(node.Type))
	}
	typ, err := scope.Thread.dbp.findTypeExpr(node.Type)
	if err != nil {
		return nil, err
	}
	if xv.Children[0].DwarfType.String() != typ.String() {
		return nil, fmt.Errorf("interface conversion: %s is %s, not %s", xv.DwarfType.String(), xv.Children[0].TypeString(), typ)
	}
	return &xv.Children[0], nil
}

// Evaluates expressions <subexpr>[<subexpr>] (subscript access to arrays, slices and maps)
func (scope *EvalScope) evalIndex(node *ast.IndexExpr) (*Variable, error) {
	xev, err := scope.evalAST(node.X)
	if err != nil {
		return nil, err
	}
	if xev.Unreadable != nil {
		return nil, xev.Unreadable
	}

	idxev, err := scope.evalAST(node.Index)
	if err != nil {
		return nil, err
	}

	switch xev.Kind {
	case reflect.Slice, reflect.Array, reflect.String:
		if xev.base == 0 {
			return nil, fmt.Errorf("can not index \"%s\"", exprToString(node.X))
		}
		n, err := idxev.asInt()
		if err != nil {
			return nil, err
		}
		return xev.sliceAccess(int(n))

	case reflect.Map:
		idxev.loadValue()
		if idxev.Unreadable != nil {
			return nil, idxev.Unreadable
		}
		return xev.mapAccess(idxev)
	default:
		return nil, fmt.Errorf("expression \"%s\" (%s) does not support indexing", exprToString(node.X), xev.TypeString())

	}
}

// Evaluates expressions <subexpr>[<subexpr>:<subexpr>]
// HACK: slicing a map expression with [0:0] will return the whole map
func (scope *EvalScope) evalReslice(node *ast.SliceExpr) (*Variable, error) {
	xev, err := scope.evalAST(node.X)
	if err != nil {
		return nil, err
	}
	if xev.Unreadable != nil {
		return nil, xev.Unreadable
	}

	var low, high int64

	if node.Low != nil {
		lowv, err := scope.evalAST(node.Low)
		if err != nil {
			return nil, err
		}
		low, err = lowv.asInt()
		if err != nil {
			return nil, fmt.Errorf("can not convert \"%s\" to int: %v", exprToString(node.Low), err)
		}
	}

	if node.High == nil {
		high = xev.Len
	} else {
		highv, err := scope.evalAST(node.High)
		if err != nil {
			return nil, err
		}
		high, err = highv.asInt()
		if err != nil {
			return nil, fmt.Errorf("can not convert \"%s\" to int: %v", exprToString(node.High), err)
		}
	}

	switch xev.Kind {
	case reflect.Slice, reflect.Array, reflect.String:
		if xev.base == 0 {
			return nil, fmt.Errorf("can not slice \"%s\"", exprToString(node.X))
		}
		return xev.reslice(low, high)
	case reflect.Map:
		if node.High != nil {
			return nil, fmt.Errorf("second slice argument must be empty for maps")
		}
		xev.mapSkip += int(low)
		xev.loadValue()
		if xev.Unreadable != nil {
			return nil, xev.Unreadable
		}
		return xev, nil
	default:
		return nil, fmt.Errorf("can not slice \"%s\" (type %s)", exprToString(node.X), xev.TypeString())
	}
}

// Evaluates a pointer dereference expression: *<subexpr>
func (scope *EvalScope) evalPointerDeref(node *ast.StarExpr) (*Variable, error) {
	xev, err := scope.evalAST(node.X)
	if err != nil {
		return nil, err
	}

	if xev.Kind != reflect.Ptr {
		return nil, fmt.Errorf("expression \"%s\" (%s) can not be dereferenced", exprToString(node.X), xev.TypeString())
	}

	if xev == nilVariable {
		return nil, fmt.Errorf("nil can not be dereferenced")
	}

	if len(xev.Children) == 1 {
		// this branch is here to support pointers constructed with typecasts from ints
		return &(xev.Children[0]), nil
	}
	rv := xev.maybeDereference()
	if rv.Addr == 0 {
		return nil, fmt.Errorf("nil pointer dereference")
	}
	return rv, nil
}

// Evaluates expressions &<subexpr>
func (scope *EvalScope) evalAddrOf(node *ast.UnaryExpr) (*Variable, error) {
	xev, err := scope.evalAST(node.X)
	if err != nil {
		return nil, err
	}
	if xev.Addr == 0 || xev.DwarfType == nil {
		return nil, fmt.Errorf("can not take address of \"%s\"", exprToString(node.X))
	}

	xev.OnlyAddr = true

	typename := "*" + xev.DwarfType.String()
	rv := scope.newVariable("", 0, &dwarf.PtrType{CommonType: dwarf.CommonType{ByteSize: int64(scope.Thread.dbp.arch.PtrSize()), Name: typename}, Type: xev.DwarfType})
	rv.Children = []Variable{*xev}
	rv.loaded = true

	return rv, nil
}

func constantUnaryOp(op token.Token, y constant.Value) (r constant.Value, err error) {
	defer func() {
		if ierr := recover(); ierr != nil {
			err = fmt.Errorf("%v", ierr)
		}
	}()
	r = constant.UnaryOp(op, y, 0)
	return
}

func constantBinaryOp(op token.Token, x, y constant.Value) (r constant.Value, err error) {
	defer func() {
		if ierr := recover(); ierr != nil {
			err = fmt.Errorf("%v", ierr)
		}
	}()
	switch op {
	case token.SHL, token.SHR:
		n, _ := constant.Uint64Val(y)
		r = constant.Shift(x, op, uint(n))
	default:
		r = constant.BinaryOp(x, op, y)
	}
	return
}

func constantCompare(op token.Token, x, y constant.Value) (r bool, err error) {
	defer func() {
		if ierr := recover(); ierr != nil {
			err = fmt.Errorf("%v", ierr)
		}
	}()
	r = constant.Compare(x, op, y)
	return
}

// Evaluates expressions: -<subexpr> and +<subexpr>
func (scope *EvalScope) evalUnary(node *ast.UnaryExpr) (*Variable, error) {
	xv, err := scope.evalAST(node.X)
	if err != nil {
		return nil, err
	}

	xv.loadValue()
	if xv.Unreadable != nil {
		return nil, xv.Unreadable
	}
	if xv.Value == nil {
		return nil, fmt.Errorf("operator %s can not be applied to \"%s\"", node.Op.String(), exprToString(node.X))
	}
	rc, err := constantUnaryOp(node.Op, xv.Value)
	if err != nil {
		return nil, err
	}
	if xv.DwarfType != nil {
		r := xv.newVariable("", 0, xv.DwarfType)
		r.Value = rc
		return r, nil
	}
	return newConstant(rc, xv.mem), nil
}

func negotiateType(op token.Token, xv, yv *Variable) (dwarf.Type, error) {
	if xv == nilVariable {
		return nil, negotiateTypeNil(op, yv)
	}

	if yv == nilVariable {
		return nil, negotiateTypeNil(op, xv)
	}

	if op == token.SHR || op == token.SHL {
		if xv.Value == nil || xv.Value.Kind() != constant.Int {
			return nil, fmt.Errorf("shift of type %s", xv.Kind)
		}

		switch yv.Kind {
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			// ok
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			if yv.DwarfType != nil || constant.Sign(yv.Value) < 0 {
				return nil, fmt.Errorf("shift count type %s, must be unsigned integer", yv.Kind.String())
			}
		default:
			return nil, fmt.Errorf("shift count type %s, must be unsigned integer", yv.Kind.String())
		}

		return xv.DwarfType, nil
	}

	if xv.DwarfType == nil && yv.DwarfType == nil {
		return nil, nil
	}

	if xv.DwarfType != nil && yv.DwarfType != nil {
		if xv.DwarfType.String() != yv.DwarfType.String() {
			return nil, fmt.Errorf("mismatched types \"%s\" and \"%s\"", xv.DwarfType.String(), yv.DwarfType.String())
		}
		return xv.DwarfType, nil
	} else if xv.DwarfType != nil && yv.DwarfType == nil {
		if err := yv.isType(xv.DwarfType, xv.Kind); err != nil {
			return nil, err
		}
		return xv.DwarfType, nil
	} else if xv.DwarfType == nil && yv.DwarfType != nil {
		if err := xv.isType(yv.DwarfType, yv.Kind); err != nil {
			return nil, err
		}
		return yv.DwarfType, nil
	}

	panic("unreachable")
}

func negotiateTypeNil(op token.Token, v *Variable) error {
	if op != token.EQL && op != token.NEQ {
		return fmt.Errorf("operator %s can not be applied to \"nil\"", op.String())
	}
	switch v.Kind {
	case reflect.Ptr, reflect.UnsafePointer, reflect.Chan, reflect.Map, reflect.Interface, reflect.Slice, reflect.Func:
		return nil
	default:
		return fmt.Errorf("can not compare %s to nil", v.Kind.String())
	}
}

func (scope *EvalScope) evalBinary(node *ast.BinaryExpr) (*Variable, error) {
	switch node.Op {
	case token.INC, token.DEC, token.ARROW:
		return nil, fmt.Errorf("operator %s not supported", node.Op.String())
	}

	xv, err := scope.evalAST(node.X)
	if err != nil {
		return nil, err
	}

	yv, err := scope.evalAST(node.Y)
	if err != nil {
		return nil, err
	}

	xv.loadValue()
	yv.loadValue()

	if xv.Unreadable != nil {
		return nil, xv.Unreadable
	}

	if yv.Unreadable != nil {
		return nil, yv.Unreadable
	}

	typ, err := negotiateType(node.Op, xv, yv)
	if err != nil {
		return nil, err
	}

	op := node.Op
	if typ != nil && (op == token.QUO) {
		_, isint := typ.(*dwarf.IntType)
		_, isuint := typ.(*dwarf.UintType)
		if isint || isuint {
			// forces integer division if the result type is integer
			op = token.QUO_ASSIGN
		}
	}

	switch op {
	case token.EQL, token.LSS, token.GTR, token.NEQ, token.LEQ, token.GEQ:
		v, err := compareOp(op, xv, yv)
		if err != nil {
			return nil, err
		}
		return newConstant(constant.MakeBool(v), xv.mem), nil

	default:
		if xv.Value == nil {
			return nil, fmt.Errorf("operator %s can not be applied to \"%s\"", node.Op.String(), exprToString(node.X))
		}

		if yv.Value == nil {
			return nil, fmt.Errorf("operator %s can not be applied to \"%s\"", node.Op.String(), exprToString(node.Y))
		}

		rc, err := constantBinaryOp(op, xv.Value, yv.Value)
		if err != nil {
			return nil, err
		}

		if typ == nil {
			return newConstant(rc, xv.mem), nil
		}

		r := xv.newVariable("", 0, typ)
		r.Value = rc
		return r, nil
	}
}

// Comapres xv to yv using operator op
// Both xv and yv must be loaded and have a compatible type (as determined by negotiateType)
func compareOp(op token.Token, xv *Variable, yv *Variable) (bool, error) {
	switch xv.Kind {
	case reflect.Bool:
		fallthrough
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		fallthrough
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		fallthrough
	case reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128:
		return constantCompare(op, xv.Value, yv.Value)
	case reflect.String:
		if int64(len(constant.StringVal(xv.Value))) != xv.Len || int64(len(constant.StringVal(yv.Value))) != yv.Len {
			return false, fmt.Errorf("string too long for comparison")
		}
		return constantCompare(op, xv.Value, yv.Value)
	}

	if op != token.EQL && op != token.NEQ {
		return false, fmt.Errorf("operator %s not defined on %s", op.String(), xv.Kind.String())
	}

	var eql bool
	var err error

	if xv == nilVariable {
		switch op {
		case token.EQL:
			return yv.isNil(), nil
		case token.NEQ:
			return !yv.isNil(), nil
		}
	}

	if yv == nilVariable {
		switch op {
		case token.EQL:
			return xv.isNil(), nil
		case token.NEQ:
			return !xv.isNil(), nil
		}
	}

	switch xv.Kind {
	case reflect.Ptr:
		eql = xv.Children[0].Addr == yv.Children[0].Addr
	case reflect.Array:
		if int64(len(xv.Children)) != xv.Len || int64(len(yv.Children)) != yv.Len {
			return false, fmt.Errorf("array too long for comparison")
		}
		eql, err = equalChildren(xv, yv, true)
	case reflect.Struct:
		if len(xv.Children) != len(yv.Children) {
			return false, nil
		}
		if int64(len(xv.Children)) != xv.Len || int64(len(yv.Children)) != yv.Len {
			return false, fmt.Errorf("sturcture too deep for comparison")
		}
		eql, err = equalChildren(xv, yv, false)
	case reflect.Slice, reflect.Map, reflect.Func, reflect.Chan:
		return false, fmt.Errorf("can not compare %s variables", xv.Kind.String())
	case reflect.Interface:
		if xv.Children[0].RealType.String() != yv.Children[0].RealType.String() {
			eql = false
		} else {
			eql, err = compareOp(token.EQL, &xv.Children[0], &yv.Children[0])
		}
	default:
		return false, fmt.Errorf("unimplemented comparison of %s variables", xv.Kind.String())
	}

	if op == token.NEQ {
		return !eql, err
	}
	return eql, err
}

func (v *Variable) isNil() bool {
	switch v.Kind {
	case reflect.Ptr:
		return v.Children[0].Addr == 0
	case reflect.Interface:
		return false
	case reflect.Slice, reflect.Map, reflect.Func, reflect.Chan:
		return v.base == 0
	}
	return false
}

func equalChildren(xv, yv *Variable, shortcircuit bool) (bool, error) {
	r := true
	for i := range xv.Children {
		eql, err := compareOp(token.EQL, &xv.Children[i], &yv.Children[i])
		if err != nil {
			return false, err
		}
		r = r && eql
		if !r && shortcircuit {
			return false, nil
		}
	}
	return r, nil
}

func (v *Variable) asInt() (int64, error) {
	if v.DwarfType == nil {
		if v.Value.Kind() != constant.Int {
			return 0, fmt.Errorf("can not convert constant %s to int", v.Value)
		}
	} else {
		v.loadValue()
		if v.Unreadable != nil {
			return 0, v.Unreadable
		}
		if _, ok := v.DwarfType.(*dwarf.IntType); !ok {
			return 0, fmt.Errorf("can not convert value of type %s to int", v.DwarfType.String())
		}
	}
	n, _ := constant.Int64Val(v.Value)
	return n, nil
}

func (v *Variable) asUint() (uint64, error) {
	if v.DwarfType == nil {
		if v.Value.Kind() != constant.Int {
			return 0, fmt.Errorf("can not convert constant %s to uint", v.Value)
		}
	} else {
		v.loadValue()
		if v.Unreadable != nil {
			return 0, v.Unreadable
		}
		if _, ok := v.DwarfType.(*dwarf.UintType); !ok {
			return 0, fmt.Errorf("can not convert value of type %s to uint", v.DwarfType.String())
		}
	}
	n, _ := constant.Uint64Val(v.Value)
	return n, nil
}

func (v *Variable) isType(typ dwarf.Type, kind reflect.Kind) error {
	if v.DwarfType != nil {
		if typ != nil && typ.String() != v.RealType.String() {
			return fmt.Errorf("can not convert value of type %s to %s", v.DwarfType.String(), typ.String())
		}
		return nil
	}

	if typ == nil {
		return nil
	}

	if v == nilVariable {
		switch kind {
		case reflect.Slice, reflect.Map, reflect.Func, reflect.Ptr, reflect.Chan, reflect.Interface:
			return nil
		default:
			return fmt.Errorf("mismatched types nil and %s", typ.String())
		}
	}

	converr := fmt.Errorf("can not convert %s constant to %s", v.Value, typ.String())

	if v.Value == nil {
		return converr
	}

	switch t := typ.(type) {
	case *dwarf.IntType:
		if v.Value.Kind() != constant.Int {
			return converr
		}
	case *dwarf.UintType:
		if v.Value.Kind() != constant.Int {
			return converr
		}
	case *dwarf.FloatType:
		if (v.Value.Kind() != constant.Int) && (v.Value.Kind() != constant.Float) {
			return converr
		}
	case *dwarf.BoolType:
		if v.Value.Kind() != constant.Bool {
			return converr
		}
	case *dwarf.StructType:
		if t.StructName != "string" {
			return converr
		}
		if v.Value.Kind() != constant.String {
			return converr
		}
	case *dwarf.ComplexType:
		if v.Value.Kind() != constant.Complex && v.Value.Kind() != constant.Float && v.Value.Kind() != constant.Int {
			return converr
		}
	default:
		return converr
	}

	return nil
}

func (v *Variable) sliceAccess(idx int) (*Variable, error) {
	if idx < 0 || int64(idx) >= v.Len {
		return nil, fmt.Errorf("index out of bounds")
	}
	return v.newVariable("", v.base+uintptr(int64(idx)*v.stride), v.fieldType), nil
}

func (v *Variable) mapAccess(idx *Variable) (*Variable, error) {
	it := v.mapIterator()
	if it == nil {
		return nil, fmt.Errorf("can not access unreadable map: %v", v.Unreadable)
	}

	first := true
	for it.next() {
		key := it.key()
		key.loadValue()
		if key.Unreadable != nil {
			return nil, fmt.Errorf("can not access unreadable map: %v", key.Unreadable)
		}
		if first {
			first = false
			if err := idx.isType(key.DwarfType, key.Kind); err != nil {
				return nil, err
			}
		}
		eql, err := compareOp(token.EQL, key, idx)
		if err != nil {
			return nil, err
		}
		if eql {
			return it.value(), nil
		}
	}
	if v.Unreadable != nil {
		return nil, v.Unreadable
	}
	// go would return zero for the map value type here, we do not have the ability to create zeroes
	return nil, fmt.Errorf("key not found")
}

func (v *Variable) reslice(low int64, high int64) (*Variable, error) {
	if low < 0 || low >= v.Len || high < 0 || high > v.Len {
		return nil, fmt.Errorf("index out of bounds")
	}

	base := v.base + uintptr(int64(low)*v.stride)
	len := high - low

	if high-low < 0 {
		return nil, fmt.Errorf("index out of bounds")
	}

	typ := v.DwarfType
	if _, isarr := v.DwarfType.(*dwarf.ArrayType); isarr {
		typ = &dwarf.StructType{
			CommonType: dwarf.CommonType{
				ByteSize: 24,
				Name:     "",
			},
			StructName: fmt.Sprintf("[]%s", v.fieldType),
			Kind:       "struct",
			Field:      nil,
		}
	}

	r := v.newVariable("", 0, typ)
	r.Cap = len
	r.Len = len
	r.base = base
	r.stride = v.stride
	r.fieldType = v.fieldType

	return r, nil
}
