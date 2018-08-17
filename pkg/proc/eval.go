package proc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/printer"
	"go/token"
	"reflect"
	"strconv"
	"strings"

	"github.com/derekparker/delve/pkg/dwarf/godwarf"
	"github.com/derekparker/delve/pkg/dwarf/reader"
	"github.com/derekparker/delve/pkg/goversion"
)

var OperationOnSpecialFloatError = errors.New("operations on non-finite floats not implemented")

// EvalExpression returns the value of the given expression.
func (scope *EvalScope) EvalExpression(expr string, cfg LoadConfig) (*Variable, error) {
	t, err := parser.ParseExpr(expr)
	if err != nil {
		return nil, err
	}

	ev, err := scope.evalToplevelTypeCast(t, cfg)
	if ev == nil && err == nil {
		ev, err = scope.evalAST(t)
	}
	if err != nil {
		return nil, err
	}
	ev.loadValue(cfg)
	if ev.Name == "" {
		ev.Name = expr
	}
	return ev, nil
}

// evalToplevelTypeCast implements certain type casts that we only support
// at the outermost levels of an expression.
func (scope *EvalScope) evalToplevelTypeCast(t ast.Expr, cfg LoadConfig) (*Variable, error) {
	call, _ := t.(*ast.CallExpr)
	if call == nil || len(call.Args) != 1 {
		return nil, nil
	}
	targetTypeStr := exprToString(removeParen(call.Fun))
	var targetType godwarf.Type
	switch targetTypeStr {
	case "[]byte", "[]uint8":
		targetType = fakeSliceType(&godwarf.IntType{BasicType: godwarf.BasicType{CommonType: godwarf.CommonType{ByteSize: 1, Name: "uint8"}, BitSize: 8, BitOffset: 0}})
	case "[]int32", "[]rune":
		targetType = fakeSliceType(&godwarf.IntType{BasicType: godwarf.BasicType{CommonType: godwarf.CommonType{ByteSize: 1, Name: "int32"}, BitSize: 32, BitOffset: 0}})
	case "string":
		var err error
		targetType, err = scope.BinInfo.findType("string")
		if err != nil {
			return nil, err
		}
	default:
		return nil, nil
	}

	argv, err := scope.evalToplevelTypeCast(call.Args[0], cfg)
	if argv == nil && err == nil {
		argv, err = scope.evalAST(call.Args[0])
	}
	if err != nil {
		return nil, err
	}
	argv.loadValue(cfg)
	if argv.Unreadable != nil {
		return nil, argv.Unreadable
	}

	v := newVariable("", 0, targetType, scope.BinInfo, scope.Mem)
	v.loaded = true

	converr := fmt.Errorf("can not convert %q to %s", exprToString(call.Args[0]), targetTypeStr)

	switch targetTypeStr {
	case "[]byte", "[]uint8":
		if argv.Kind != reflect.String {
			return nil, converr
		}
		for i, ch := range []byte(constant.StringVal(argv.Value)) {
			e := scope.newVariable("", argv.Addr+uintptr(i), targetType.(*godwarf.SliceType).ElemType, argv.mem)
			e.loaded = true
			e.Value = constant.MakeInt64(int64(ch))
			v.Children = append(v.Children, *e)
		}
		v.Len = int64(len(v.Children))
		v.Cap = v.Len
		return v, nil

	case "[]int32", "[]rune":
		if argv.Kind != reflect.String {
			return nil, converr
		}
		for i, ch := range constant.StringVal(argv.Value) {
			e := scope.newVariable("", argv.Addr+uintptr(i), targetType.(*godwarf.SliceType).ElemType, argv.mem)
			e.loaded = true
			e.Value = constant.MakeInt64(int64(ch))
			v.Children = append(v.Children, *e)
		}
		v.Len = int64(len(v.Children))
		v.Cap = v.Len
		return v, nil

	case "string":
		if argv.Kind != reflect.Slice {
			return nil, nil
		}
		switch elemType := argv.RealType.(*godwarf.SliceType).ElemType.(type) {
		case *godwarf.UintType:
			if elemType.Name != "uint8" && elemType.Name != "byte" {
				return nil, nil
			}
			bytes := make([]byte, len(argv.Children))
			for i := range argv.Children {
				n, _ := constant.Int64Val(argv.Children[i].Value)
				bytes[i] = byte(n)
			}
			v.Value = constant.MakeString(string(bytes))

		case *godwarf.IntType:
			if elemType.Name != "int32" && elemType.Name != "rune" {
				return nil, nil
			}
			runes := make([]rune, len(argv.Children))
			for i := range argv.Children {
				n, _ := constant.Int64Val(argv.Children[i].Value)
				runes[i] = rune(n)
			}
			v.Value = constant.MakeString(string(runes))

		default:
			return nil, nil
		}
		v.Len = int64(len(constant.StringVal(v.Value)))
		return v, nil
	}

	return nil, nil
}

func (scope *EvalScope) evalAST(t ast.Expr) (*Variable, error) {
	switch node := t.(type) {
	case *ast.CallExpr:
		if len(node.Args) == 1 {
			v, err := scope.evalTypeCast(node)
			if err == nil {
				return v, nil
			}
			_, isident := node.Fun.(*ast.Ident)
			// we don't support function calls at the moment except for a few
			// builtin functions so just return the type error here if the function
			// isn't an identifier.
			// More sophisticated logic will be required when function calls
			// are implemented.
			if err != reader.TypeNotFoundErr || !isident {
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
			if maybePkg.Name == "runtime" && node.Sel.Name == "curg" {
				if scope.Gvar == nil {
					return nilVariable, nil
				}
				return scope.Gvar.clone(), nil
			} else if maybePkg.Name == "runtime" && node.Sel.Name == "frameoff" {
				return newConstant(constant.MakeInt64(scope.frameOffset), scope.Mem), nil
			} else if v, err := scope.findGlobal(maybePkg.Name + "." + node.Sel.Name); err == nil {
				return v, nil
			}
		}
		// try to accept "package/path".varname syntax for package variables
		if maybePkg, ok := node.X.(*ast.BasicLit); ok && maybePkg.Kind == token.STRING {
			pkgpath, err := strconv.Unquote(maybePkg.Value)
			if err == nil {
				if v, err := scope.findGlobal(pkgpath + "." + node.Sel.Name); err == nil {
					return v, nil
				}
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
		return newConstant(constant.MakeFromLiteral(node.Value, node.Kind, 0), scope.Mem), nil

	default:
		return nil, fmt.Errorf("expression %T not implemented", t)

	}
}

func exprToString(t ast.Expr) string {
	var buf bytes.Buffer
	printer.Fprint(&buf, token.NewFileSet(), t)
	return buf.String()
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

// Eval type cast expressions
func (scope *EvalScope) evalTypeCast(node *ast.CallExpr) (*Variable, error) {
	argv, err := scope.evalAST(node.Args[0])
	if err != nil {
		return nil, err
	}
	argv.loadValue(loadSingleValue)
	if argv.Unreadable != nil {
		return nil, argv.Unreadable
	}

	fnnode := node.Fun

	// remove all enclosing parenthesis from the type name
	fnnode = removeParen(fnnode)

	styp, err := scope.BinInfo.findTypeExpr(fnnode)
	if err != nil {
		return nil, err
	}
	typ := resolveTypedef(styp)

	converr := fmt.Errorf("can not convert %q to %s", exprToString(node.Args[0]), typ.String())

	v := newVariable("", 0, styp, scope.BinInfo, scope.Mem)
	v.loaded = true

	switch ttyp := typ.(type) {
	case *godwarf.PtrType:
		switch argv.Kind {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			// ok
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			// ok
		default:
			return nil, converr
		}

		n, _ := constant.Int64Val(argv.Value)

		v.Children = []Variable{*(scope.newVariable("", uintptr(n), ttyp.Type, scope.Mem))}
		return v, nil

	case *godwarf.UintType:
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
		case reflect.Ptr:
			v.Value = constant.MakeUint64(uint64(argv.Children[0].Addr))
			return v, nil
		}
	case *godwarf.IntType:
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
	case *godwarf.FloatType:
		switch argv.Kind {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			fallthrough
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			fallthrough
		case reflect.Float32, reflect.Float64:
			v.Value = argv.Value
			return v, nil
		}
	case *godwarf.ComplexType:
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
		arg.loadValue(loadFullValue)
		if arg.Unreadable != nil {
			return nil, arg.Unreadable
		}
		if arg.Base == 0 {
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
		arg.loadValue(loadFullValue)
		if arg.Unreadable != nil {
			return nil, arg.Unreadable
		}
		if arg.Base == 0 {
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

	realev.loadValue(loadSingleValue)
	imagev.loadValue(loadSingleValue)

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
		sz = realev.RealType.(*godwarf.FloatType).Size()
	}
	if imagev.RealType != nil {
		isz := imagev.RealType.(*godwarf.FloatType).Size()
		if isz > sz {
			sz = isz
		}
	}

	if sz == 0 {
		sz = 128
	}

	typ := &godwarf.ComplexType{BasicType: godwarf.BasicType{CommonType: godwarf.CommonType{ByteSize: int64(sz / 8), Name: fmt.Sprintf("complex%d", sz)}, BitSize: sz, BitOffset: 0}}

	r := realev.newVariable("", 0, typ, nil)
	r.Value = constant.BinaryOp(realev.Value, token.ADD, constant.MakeImag(imagev.Value))
	return r, nil
}

func imagBuiltin(args []*Variable, nodeargs []ast.Expr) (*Variable, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("wrong number of arguments to imag: %d", len(args))
	}

	arg := args[0]
	arg.loadValue(loadSingleValue)

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
	arg.loadValue(loadSingleValue)

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
		return newConstant(constant.MakeBool(node.Name == "true"), scope.Mem), nil
	case "nil":
		return nilVariable, nil
	}

	vars, err := scope.Locals()
	if err != nil {
		return nil, err
	}
	for i := range vars {
		if vars[i].Name == node.Name && vars[i].Flags&VariableShadowed == 0 {
			return vars[i], nil
		}
	}

	// if it's not a local variable then it could be a package variable w/o explicit package name
	if scope.Fn != nil {
		if v, err := scope.findGlobal(scope.Fn.PackageName() + "." + node.Name); err == nil {
			v.Name = node.Name
			return v, nil
		}
	}
	return nil, fmt.Errorf("could not find symbol value for %s", node.Name)
}

// Evaluates expressions <subexpr>.<field name> where subexpr is not a package name
func (scope *EvalScope) evalStructSelector(node *ast.SelectorExpr) (*Variable, error) {
	xv, err := scope.evalAST(node.X)
	if err != nil {
		return nil, err
	}
	rv, err := xv.findMethod(node.Sel.Name)
	if err != nil {
		return nil, err
	}
	if rv != nil {
		return rv, nil
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
	xv.loadInterface(0, false, loadFullValue)
	if xv.Unreadable != nil {
		return nil, xv.Unreadable
	}
	if xv.Children[0].Unreadable != nil {
		return nil, xv.Children[0].Unreadable
	}
	if xv.Children[0].Addr == 0 {
		return nil, fmt.Errorf("interface conversion: %s is nil, not %s", xv.DwarfType.String(), exprToString(node.Type))
	}
	typ, err := scope.BinInfo.findTypeExpr(node.Type)
	if err != nil {
		return nil, err
	}
	if xv.Children[0].DwarfType.Common().Name != typ.Common().Name {
		return nil, fmt.Errorf("interface conversion: %s is %s, not %s", xv.DwarfType.Common().Name, xv.Children[0].TypeString(), typ.Common().Name)
	}
	// loadInterface will set OnlyAddr for the data member since here we are
	// passing false to loadData, however returning the variable with OnlyAddr
	// set here would be wrong since, once the expression evaluation
	// terminates, the value of this variable will be loaded.
	xv.Children[0].OnlyAddr = false
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

	xev = xev.maybeDereference()

	idxev, err := scope.evalAST(node.Index)
	if err != nil {
		return nil, err
	}

	cantindex := fmt.Errorf("expression \"%s\" (%s) does not support indexing", exprToString(node.X), xev.TypeString())

	switch xev.Kind {
	case reflect.Ptr:
		if xev == nilVariable {
			return nil, cantindex
		}
		_, isarrptr := xev.RealType.(*godwarf.PtrType).Type.(*godwarf.ArrayType)
		if !isarrptr {
			return nil, cantindex
		}
		xev = xev.maybeDereference()
		fallthrough

	case reflect.Slice, reflect.Array, reflect.String:
		if xev.Base == 0 {
			return nil, fmt.Errorf("can not index \"%s\"", exprToString(node.X))
		}
		n, err := idxev.asInt()
		if err != nil {
			return nil, err
		}
		return xev.sliceAccess(int(n))

	case reflect.Map:
		idxev.loadValue(loadFullValue)
		if idxev.Unreadable != nil {
			return nil, idxev.Unreadable
		}
		return xev.mapAccess(idxev)
	default:
		return nil, cantindex
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
		if xev.Base == 0 {
			return nil, fmt.Errorf("can not slice \"%s\"", exprToString(node.X))
		}
		return xev.reslice(low, high)
	case reflect.Map:
		if node.High != nil {
			return nil, fmt.Errorf("second slice argument must be empty for maps")
		}
		xev.mapSkip += int(low)
		xev.mapIterator() // reads map length
		if int64(xev.mapSkip) >= xev.Len {
			return nil, fmt.Errorf("map index out of bounds")
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

	return xev.pointerToVariable(), nil
}

func (v *Variable) pointerToVariable() *Variable {
	v.OnlyAddr = true

	typename := "*" + v.DwarfType.Common().Name
	rv := v.newVariable("", 0, &godwarf.PtrType{CommonType: godwarf.CommonType{ByteSize: int64(v.bi.Arch.PtrSize()), Name: typename}, Type: v.DwarfType}, v.mem)
	rv.Children = []Variable{*v}
	rv.loaded = true

	return rv
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

	xv.loadValue(loadSingleValue)
	if xv.Unreadable != nil {
		return nil, xv.Unreadable
	}
	if xv.FloatSpecial != 0 {
		return nil, OperationOnSpecialFloatError
	}
	if xv.Value == nil {
		return nil, fmt.Errorf("operator %s can not be applied to \"%s\"", node.Op.String(), exprToString(node.X))
	}
	rc, err := constantUnaryOp(node.Op, xv.Value)
	if err != nil {
		return nil, err
	}
	if xv.DwarfType != nil {
		r := xv.newVariable("", 0, xv.DwarfType, scope.Mem)
		r.Value = rc
		return r, nil
	}
	return newConstant(rc, xv.mem), nil
}

func negotiateType(op token.Token, xv, yv *Variable) (godwarf.Type, error) {
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
	xv.loadValue(loadFullValue)
	if xv.Unreadable != nil {
		return nil, xv.Unreadable
	}

	// short circuits logical operators
	switch node.Op {
	case token.LAND:
		if !constant.BoolVal(xv.Value) {
			return newConstant(xv.Value, xv.mem), nil
		}
	case token.LOR:
		if constant.BoolVal(xv.Value) {
			return newConstant(xv.Value, xv.mem), nil
		}
	}

	yv, err := scope.evalAST(node.Y)
	if err != nil {
		return nil, err
	}
	yv.loadValue(loadFullValue)
	if yv.Unreadable != nil {
		return nil, yv.Unreadable
	}

	if xv.FloatSpecial != 0 || yv.FloatSpecial != 0 {
		return nil, OperationOnSpecialFloatError
	}

	typ, err := negotiateType(node.Op, xv, yv)
	if err != nil {
		return nil, err
	}

	op := node.Op
	if typ != nil && (op == token.QUO) {
		_, isint := typ.(*godwarf.IntType)
		_, isuint := typ.(*godwarf.UintType)
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

		r := xv.newVariable("", 0, typ, scope.Mem)
		r.Value = rc
		if r.Kind == reflect.String {
			r.Len = xv.Len + yv.Len
		}
		return r, nil
	}
}

// Compares xv to yv using operator op
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
		if xv.Len != yv.Len {
			switch op {
			case token.EQL:
				return false, nil
			case token.NEQ:
				return true, nil
			}
		}
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
			return false, fmt.Errorf("structure too deep for comparison")
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
		return v.Children[0].Addr == 0 && v.Children[0].Kind == reflect.Invalid
	case reflect.Slice, reflect.Map, reflect.Func, reflect.Chan:
		return v.Base == 0
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
		v.loadValue(loadSingleValue)
		if v.Unreadable != nil {
			return 0, v.Unreadable
		}
		if _, ok := v.DwarfType.(*godwarf.IntType); !ok {
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
		v.loadValue(loadSingleValue)
		if v.Unreadable != nil {
			return 0, v.Unreadable
		}
		if _, ok := v.DwarfType.(*godwarf.UintType); !ok {
			return 0, fmt.Errorf("can not convert value of type %s to uint", v.DwarfType.String())
		}
	}
	n, _ := constant.Uint64Val(v.Value)
	return n, nil
}

type typeConvErr struct {
	srcType, dstType godwarf.Type
}

func (err *typeConvErr) Error() string {
	return fmt.Sprintf("can not convert value of type %s to %s", err.srcType.String(), err.dstType.String())
}

func (v *Variable) isType(typ godwarf.Type, kind reflect.Kind) error {
	if v.DwarfType != nil {
		if typ != nil && typ.String() != v.RealType.String() {
			return &typeConvErr{v.DwarfType, typ}
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

	switch typ.(type) {
	case *godwarf.IntType:
		if v.Value.Kind() != constant.Int {
			return converr
		}
	case *godwarf.UintType:
		if v.Value.Kind() != constant.Int {
			return converr
		}
	case *godwarf.FloatType:
		if (v.Value.Kind() != constant.Int) && (v.Value.Kind() != constant.Float) {
			return converr
		}
	case *godwarf.BoolType:
		if v.Value.Kind() != constant.Bool {
			return converr
		}
	case *godwarf.StringType:
		if v.Value.Kind() != constant.String {
			return converr
		}
	case *godwarf.ComplexType:
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
	mem := v.mem
	if v.Kind != reflect.Array {
		mem = DereferenceMemory(mem)
	}
	return v.newVariable("", v.Base+uintptr(int64(idx)*v.stride), v.fieldType, mem), nil
}

func (v *Variable) mapAccess(idx *Variable) (*Variable, error) {
	it := v.mapIterator()
	if it == nil {
		return nil, fmt.Errorf("can not access unreadable map: %v", v.Unreadable)
	}

	first := true
	for it.next() {
		key := it.key()
		key.loadValue(loadFullValue)
		if key.Unreadable != nil {
			return nil, fmt.Errorf("can not access unreadable map: %v", key.Unreadable)
		}
		if first {
			first = false
			if err := idx.isType(key.RealType, key.Kind); err != nil {
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

	base := v.Base + uintptr(int64(low)*v.stride)
	len := high - low

	if high-low < 0 {
		return nil, fmt.Errorf("index out of bounds")
	}

	typ := v.DwarfType
	if _, isarr := v.DwarfType.(*godwarf.ArrayType); isarr {
		typ = fakeSliceType(v.fieldType)
	}

	mem := v.mem
	if v.Kind != reflect.Array {
		mem = DereferenceMemory(mem)
	}

	r := v.newVariable("", 0, typ, mem)
	r.Cap = len
	r.Len = len
	r.Base = base
	r.stride = v.stride
	r.fieldType = v.fieldType

	return r, nil
}

// findMethod finds method mname in the type of variable v
func (v *Variable) findMethod(mname string) (*Variable, error) {
	if _, isiface := v.RealType.(*godwarf.InterfaceType); isiface {
		v.loadInterface(0, false, loadFullValue)
		if v.Unreadable != nil {
			return nil, v.Unreadable
		}
		return v.Children[0].findMethod(mname)
	}

	typ := v.DwarfType
	ptyp, isptr := typ.(*godwarf.PtrType)
	if isptr {
		typ = ptyp.Type
	}

	if _, istypedef := typ.(*godwarf.TypedefType); !istypedef {
		return nil, nil
	}

	typePath := typ.Common().Name
	dot := strings.LastIndex(typePath, ".")
	if dot < 0 {
		// probably just a C type
		return nil, nil
	}

	pkg := typePath[:dot]
	receiver := typePath[dot+1:]

	if fn, ok := v.bi.LookupFunc[fmt.Sprintf("%s.%s.%s", pkg, receiver, mname)]; ok {
		r, err := functionToVariable(fn, v.bi, v.mem)
		if err != nil {
			return nil, err
		}
		if isptr {
			r.Children = append(r.Children, *(v.maybeDereference()))
		} else {
			r.Children = append(r.Children, *v)
		}
		return r, nil
	}

	if fn, ok := v.bi.LookupFunc[fmt.Sprintf("%s.(*%s).%s", pkg, receiver, mname)]; ok {
		r, err := functionToVariable(fn, v.bi, v.mem)
		if err != nil {
			return nil, err
		}
		if isptr {
			r.Children = append(r.Children, *v)
		} else {
			r.Children = append(r.Children, *(v.pointerToVariable()))
		}
		return r, nil
	}

	return nil, nil
}

func functionToVariable(fn *Function, bi *BinaryInfo, mem MemoryReadWriter) (*Variable, error) {
	typ, err := fn.fakeType(bi, true)
	if err != nil {
		return nil, err
	}
	v := newVariable(fn.Name, 0, typ, bi, mem)
	v.Value = constant.MakeString(fn.Name)
	v.loaded = true
	v.Base = uintptr(fn.Entry)
	return v, nil
}

func fakeSliceType(fieldType godwarf.Type) godwarf.Type {
	return &godwarf.SliceType{
		StructType: godwarf.StructType{
			CommonType: godwarf.CommonType{
				ByteSize: 24,
				Name:     "",
			},
			StructName: fmt.Sprintf("[]%s", fieldType.Common().Name),
			Kind:       "struct",
			Field:      nil,
		},
		ElemType: fieldType,
	}
}

var errMethodEvalUnsupported = errors.New("evaluating methods not supported on this version of Go")

func (fn *Function) fakeType(bi *BinaryInfo, removeReceiver bool) (*godwarf.FuncType, error) {
	if producer := bi.Producer(); producer == "" || !goversion.ProducerAfterOrEqual(producer, 1, 10) {
		// versions of Go prior to 1.10 do not distinguish between parameters and
		// return values, therefore we can't use a subprogram DIE to derive a
		// function type.
		return nil, errMethodEvalUnsupported
	}
	_, formalArgs, err := funcCallArgs(fn, bi, true)
	if err != nil {
		return nil, err
	}

	if removeReceiver {
		formalArgs = formalArgs[1:]
	}

	args := make([]string, 0, len(formalArgs))
	rets := make([]string, 0, len(formalArgs))

	for _, formalArg := range formalArgs {
		var s string
		if strings.HasPrefix(formalArg.name, "~") {
			s = formalArg.typ.String()
		} else {
			s = fmt.Sprintf("%s %s", formalArg.name, formalArg.typ.String())
		}
		if formalArg.isret {
			rets = append(rets, s)
		} else {
			args = append(args, s)
		}
	}

	argstr := strings.Join(args, ", ")
	var retstr string
	switch len(rets) {
	case 0:
		retstr = ""
	case 1:
		retstr = " " + rets[0]
	default:
		retstr = " (" + strings.Join(rets, ", ") + ")"
	}
	return &godwarf.FuncType{
		CommonType: godwarf.CommonType{
			Name:        "func(" + argstr + ")" + retstr,
			ReflectKind: reflect.Func,
		},
		//TODO(aarzilli): at the moment we aren't using the ParamType and
		// ReturnType fields of FuncType anywhere (when this is returned to the
		// client it's first converted to a string and the function calling code
		// reads the subroutine entry because it needs to know the stack offsets).
		// If we start using them they should be filled here.
	}, nil
}
