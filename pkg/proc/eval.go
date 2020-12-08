package proc

import (
	"bytes"
	"debug/dwarf"
	"encoding/binary"
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/printer"
	"go/scanner"
	"go/token"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/go-delve/delve/pkg/dwarf/godwarf"
	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/reader"
	"github.com/go-delve/delve/pkg/goversion"
)

var errOperationOnSpecialFloat = errors.New("operations on non-finite floats not implemented")

// EvalScope is the scope for variable evaluation. Contains the thread,
// current location (PC), and canonical frame address.
type EvalScope struct {
	Location
	Regs    op.DwarfRegisters
	Mem     MemoryReadWriter // Target's memory
	g       *G
	BinInfo *BinaryInfo

	frameOffset int64

	aordr *dwarf.Reader // extra reader to load DW_AT_abstract_origin entries, do not initialize

	// When the following pointer is not nil this EvalScope was created
	// by CallFunction and the expression evaluation is executing on a
	// different goroutine from the debugger's main goroutine.
	// Under this circumstance the expression evaluator can make function
	// calls by setting up the runtime.debugCallV1 call and then writing a
	// value to the continueRequest channel.
	// When a value is written to continueRequest the debugger's main goroutine
	// will call Continue, when the runtime in the target process sends us a
	// request in the function call protocol the debugger's main goroutine will
	// write a value to the continueCompleted channel.
	// The goroutine executing the expression evaluation shall signal that the
	// evaluation is complete by closing the continueRequest channel.
	callCtx *callContext

	// If trustArgOrder is true function arguments that don't have an address
	// will have one assigned by looking at their position in the argument
	// list.
	trustArgOrder bool
}

// ConvertEvalScope returns a new EvalScope in the context of the
// specified goroutine ID and stack frame.
// If deferCall is > 0 the eval scope will be relative to the specified deferred call.
func ConvertEvalScope(dbp *Target, gid, frame, deferCall int) (*EvalScope, error) {
	if _, err := dbp.Valid(); err != nil {
		return nil, err
	}
	ct := dbp.CurrentThread()
	g, err := FindGoroutine(dbp, gid)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return ThreadScope(ct)
	}

	var opts StacktraceOptions
	if deferCall > 0 {
		opts = StacktraceReadDefers
	}

	locs, err := g.Stacktrace(frame+1, opts)
	if err != nil {
		return nil, err
	}

	if frame >= len(locs) {
		return nil, fmt.Errorf("Frame %d does not exist in goroutine %d", frame, gid)
	}

	if deferCall > 0 {
		if deferCall-1 >= len(locs[frame].Defers) {
			return nil, fmt.Errorf("Frame %d only has %d deferred calls", frame, len(locs[frame].Defers))
		}

		d := locs[frame].Defers[deferCall-1]
		if d.Unreadable != nil {
			return nil, d.Unreadable
		}

		return d.EvalScope(ct)
	}

	return FrameToScope(dbp.BinInfo(), dbp.Memory(), g, locs[frame:]...), nil
}

// FrameToScope returns a new EvalScope for frames[0].
// If frames has at least two elements all memory between
// frames[0].Regs.SP() and frames[1].Regs.CFA will be cached.
// Otherwise all memory between frames[0].Regs.SP() and frames[0].Regs.CFA
// will be cached.
func FrameToScope(bi *BinaryInfo, thread MemoryReadWriter, g *G, frames ...Stackframe) *EvalScope {
	// Creates a cacheMem that will preload the entire stack frame the first
	// time any local variable is read.
	// Remember that the stack grows downward in memory.
	minaddr := frames[0].Regs.SP()
	var maxaddr uint64
	if len(frames) > 1 && frames[0].SystemStack == frames[1].SystemStack {
		maxaddr = uint64(frames[1].Regs.CFA)
	} else {
		maxaddr = uint64(frames[0].Regs.CFA)
	}
	if maxaddr > minaddr && maxaddr-minaddr < maxFramePrefetchSize {
		thread = cacheMemory(thread, minaddr, int(maxaddr-minaddr))
	}

	s := &EvalScope{Location: frames[0].Call, Regs: frames[0].Regs, Mem: thread, g: g, BinInfo: bi, frameOffset: frames[0].FrameOffset()}
	s.PC = frames[0].lastpc
	return s
}

// ThreadScope returns an EvalScope for the given thread.
func ThreadScope(thread Thread) (*EvalScope, error) {
	locations, err := ThreadStacktrace(thread, 1)
	if err != nil {
		return nil, err
	}
	if len(locations) < 1 {
		return nil, errors.New("could not decode first frame")
	}
	return FrameToScope(thread.BinInfo(), thread.ProcessMemory(), nil, locations...), nil
}

// GoroutineScope returns an EvalScope for the goroutine running on the given thread.
func GoroutineScope(thread Thread) (*EvalScope, error) {
	locations, err := ThreadStacktrace(thread, 1)
	if err != nil {
		return nil, err
	}
	if len(locations) < 1 {
		return nil, errors.New("could not decode first frame")
	}
	g, err := GetG(thread)
	if err != nil {
		return nil, err
	}
	return FrameToScope(thread.BinInfo(), thread.ProcessMemory(), g, locations...), nil
}

// EvalExpression returns the value of the given expression.
func (scope *EvalScope) EvalExpression(expr string, cfg LoadConfig) (*Variable, error) {
	if scope.callCtx != nil {
		// makes sure that the other goroutine won't wait forever if we make a mistake
		defer close(scope.callCtx.continueRequest)
	}
	t, err := parser.ParseExpr(expr)
	if eqOff, isAs := isAssignment(err); scope.callCtx != nil && isAs {
		lexpr := expr[:eqOff]
		rexpr := expr[eqOff+1:]
		err := scope.SetVariable(lexpr, rexpr)
		scope.callCtx.doReturn(nil, err)
		return nil, err
	}
	if err != nil {
		scope.callCtx.doReturn(nil, err)
		return nil, err
	}

	ev, err := scope.evalToplevelTypeCast(t, cfg)
	if ev == nil && err == nil {
		ev, err = scope.evalAST(t)
	}
	if err != nil {
		scope.callCtx.doReturn(nil, err)
		return nil, err
	}
	ev.loadValue(cfg)
	if ev.Name == "" {
		ev.Name = expr
	}
	scope.callCtx.doReturn(ev, nil)
	return ev, nil
}

func isAssignment(err error) (int, bool) {
	el, isScannerErr := err.(scanner.ErrorList)
	if isScannerErr && el[0].Msg == "expected '==', found '='" {
		return el[0].Pos.Offset, true
	}
	return 0, false
}

// Locals returns all variables in 'scope'.
func (scope *EvalScope) Locals() ([]*Variable, error) {
	if scope.Fn == nil {
		return nil, errors.New("unable to find function context")
	}

	trustArgOrder := scope.trustArgOrder && scope.BinInfo.Producer() != "" && goversion.ProducerAfterOrEqual(scope.BinInfo.Producer(), 1, 12) && scope.Fn != nil && (scope.PC == scope.Fn.Entry)

	dwarfTree, err := scope.image().getDwarfTree(scope.Fn.offset)
	if err != nil {
		return nil, err
	}

	variablesFlags := reader.VariablesOnlyVisible
	if scope.BinInfo.Producer() != "" && goversion.ProducerAfterOrEqual(scope.BinInfo.Producer(), 1, 15) {
		variablesFlags |= reader.VariablesTrustDeclLine
	}

	varEntries := reader.Variables(dwarfTree, scope.PC, scope.Line, variablesFlags)
	vars := make([]*Variable, 0, len(varEntries))
	depths := make([]int, 0, len(varEntries))
	for _, entry := range varEntries {
		val, err := extractVarInfoFromEntry(scope.BinInfo, scope.image(), scope.Regs, scope.Mem, entry.Tree)
		if err != nil {
			// skip variables that we can't parse yet
			continue
		}
		if trustArgOrder && ((val.Unreadable != nil && val.Addr == 0) || val.Flags&VariableFakeAddress != 0) && entry.Tag == dwarf.TagFormalParameter {
			addr := afterLastArgAddr(vars)
			if addr == 0 {
				addr = uint64(scope.Regs.CFA)
			}
			addr = uint64(alignAddr(int64(addr), val.DwarfType.Align()))
			val = newVariable(val.Name, addr, val.DwarfType, scope.BinInfo, scope.Mem)
		}
		vars = append(vars, val)
		depth := entry.Depth
		if entry.Tag == dwarf.TagFormalParameter {
			if depth <= 1 {
				depth = 0
			}
			isret, _ := entry.Val(dwarf.AttrVarParam).(bool)
			if isret {
				val.Flags |= VariableReturnArgument
			} else {
				val.Flags |= VariableArgument
			}
		}
		depths = append(depths, depth)
	}

	if len(vars) <= 0 {
		return vars, nil
	}

	sort.Stable(&variablesByDepthAndDeclLine{vars, depths})

	lvn := map[string]*Variable{} // lvn[n] is the last variable we saw named n

	for i, v := range vars {
		if name := v.Name; len(name) > 1 && name[0] == '&' {
			locationExpr := v.LocationExpr
			declLine := v.DeclLine
			v = v.maybeDereference()
			if v.Addr == 0 && v.Unreadable == nil {
				v.Unreadable = fmt.Errorf("no address for escaped variable")
			}
			v.Name = name[1:]
			v.Flags |= VariableEscaped
			// See https://github.com/go-delve/delve/issues/2049 for details
			if locationExpr != nil {
				locationExpr.isEscaped = true
				v.LocationExpr = locationExpr
			}
			v.DeclLine = declLine
			vars[i] = v
		}
		if otherv := lvn[v.Name]; otherv != nil {
			otherv.Flags |= VariableShadowed
		}
		lvn[v.Name] = v
	}

	return vars, nil
}

func afterLastArgAddr(vars []*Variable) uint64 {
	for i := len(vars) - 1; i >= 0; i-- {
		v := vars[i]
		if (v.Flags&VariableArgument != 0) || (v.Flags&VariableReturnArgument != 0) {
			return v.Addr + uint64(v.DwarfType.Size())
		}
	}
	return 0
}

// setValue writes the value of srcv to dstv.
// * If srcv is a numerical literal constant and srcv is of a compatible type
//   the necessary type conversion is performed.
// * If srcv is nil and dstv is of a nil'able type then dstv is nilled.
// * If srcv is the empty string and dstv is a string then dstv is set to the
//   empty string.
// * If dstv is an "interface {}" and srcv is either an interface (possibly
//   non-empty) or a pointer shaped type (map, channel, pointer or struct
//   containing a single pointer field) the type conversion to "interface {}"
//   is performed.
// * If srcv and dstv have the same type and are both addressable then the
//   contents of srcv are copied byte-by-byte into dstv
func (scope *EvalScope) setValue(dstv, srcv *Variable, srcExpr string) error {
	srcv.loadValue(loadSingleValue)

	typerr := srcv.isType(dstv.RealType, dstv.Kind)
	if _, isTypeConvErr := typerr.(*typeConvErr); isTypeConvErr {
		// attempt iface -> eface and ptr-shaped -> eface conversions.
		return convertToEface(srcv, dstv)
	}
	if typerr != nil {
		return typerr
	}

	if srcv.Unreadable != nil {
		return fmt.Errorf("Expression \"%s\" is unreadable: %v", srcExpr, srcv.Unreadable)
	}

	// Numerical types
	switch dstv.Kind {
	case reflect.Float32, reflect.Float64:
		f, _ := constant.Float64Val(srcv.Value)
		return dstv.writeFloatRaw(f, dstv.RealType.Size())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, _ := constant.Int64Val(srcv.Value)
		return dstv.writeUint(uint64(n), dstv.RealType.Size())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, _ := constant.Uint64Val(srcv.Value)
		return dstv.writeUint(n, dstv.RealType.Size())
	case reflect.Bool:
		return dstv.writeBool(constant.BoolVal(srcv.Value))
	case reflect.Complex64, reflect.Complex128:
		real, _ := constant.Float64Val(constant.Real(srcv.Value))
		imag, _ := constant.Float64Val(constant.Imag(srcv.Value))
		return dstv.writeComplex(real, imag, dstv.RealType.Size())
	}

	// nilling nillable variables
	if srcv == nilVariable {
		return dstv.writeZero()
	}

	if srcv.Kind == reflect.String {
		if err := allocString(scope, srcv); err != nil {
			return err
		}
		return dstv.writeString(uint64(srcv.Len), uint64(srcv.Base))
	}

	// slice assignment (this is not handled by the writeCopy below so that
	// results of a reslice operation can be used here).
	if srcv.Kind == reflect.Slice {
		return dstv.writeSlice(srcv.Len, srcv.Cap, srcv.Base)
	}

	// allow any integer to be converted to any pointer
	if t, isptr := dstv.RealType.(*godwarf.PtrType); isptr {
		return dstv.writeUint(uint64(srcv.Children[0].Addr), int64(t.ByteSize))
	}

	// byte-by-byte copying for everything else, but the source must be addressable
	if srcv.Addr != 0 {
		return dstv.writeCopy(srcv)
	}

	return fmt.Errorf("can not set variables of type %s (not implemented)", dstv.Kind.String())
}

// EvalVariable returns the value of the given expression (backwards compatibility).
func (scope *EvalScope) EvalVariable(name string, cfg LoadConfig) (*Variable, error) {
	return scope.EvalExpression(name, cfg)
}

// SetVariable sets the value of the named variable
func (scope *EvalScope) SetVariable(name, value string) error {
	t, err := parser.ParseExpr(name)
	if err != nil {
		return err
	}

	xv, err := scope.evalAST(t)
	if err != nil {
		return err
	}

	if xv.Addr == 0 {
		return fmt.Errorf("Can not assign to \"%s\"", name)
	}

	if xv.Unreadable != nil {
		return fmt.Errorf("Expression \"%s\" is unreadable: %v", name, xv.Unreadable)
	}

	t, err = parser.ParseExpr(value)
	if err != nil {
		return err
	}

	yv, err := scope.evalAST(t)
	if err != nil {
		return err
	}

	return scope.setValue(xv, yv, value)
}

// LocalVariables returns all local variables from the current function scope.
func (scope *EvalScope) LocalVariables(cfg LoadConfig) ([]*Variable, error) {
	vars, err := scope.Locals()
	if err != nil {
		return nil, err
	}
	vars = filterVariables(vars, func(v *Variable) bool {
		return (v.Flags & (VariableArgument | VariableReturnArgument)) == 0
	})
	cfg.MaxMapBuckets = maxMapBucketsFactor * cfg.MaxArrayValues
	loadValues(vars, cfg)
	return vars, nil
}

// FunctionArguments returns the name, value, and type of all current function arguments.
func (scope *EvalScope) FunctionArguments(cfg LoadConfig) ([]*Variable, error) {
	vars, err := scope.Locals()
	if err != nil {
		return nil, err
	}
	vars = filterVariables(vars, func(v *Variable) bool {
		return (v.Flags & (VariableArgument | VariableReturnArgument)) != 0
	})
	cfg.MaxMapBuckets = maxMapBucketsFactor * cfg.MaxArrayValues
	loadValues(vars, cfg)
	return vars, nil
}

func filterVariables(vars []*Variable, pred func(v *Variable) bool) []*Variable {
	r := make([]*Variable, 0, len(vars))
	for i := range vars {
		if pred(vars[i]) {
			r = append(r, vars[i])
		}
	}
	return r
}

func regsReplaceStaticBase(regs op.DwarfRegisters, image *Image) op.DwarfRegisters {
	regs.StaticBase = image.StaticBase
	return regs
}

// PackageVariables returns the name, value, and type of all package variables in the application.
func (scope *EvalScope) PackageVariables(cfg LoadConfig) ([]*Variable, error) {
	pkgvars := make([]packageVar, len(scope.BinInfo.packageVars))
	copy(pkgvars, scope.BinInfo.packageVars)
	sort.Slice(pkgvars, func(i, j int) bool {
		if pkgvars[i].cu.image.addr == pkgvars[j].cu.image.addr {
			return pkgvars[i].offset < pkgvars[j].offset
		}
		return pkgvars[i].cu.image.addr < pkgvars[j].cu.image.addr
	})
	vars := make([]*Variable, 0, len(scope.BinInfo.packageVars))
	for _, pkgvar := range pkgvars {
		reader := pkgvar.cu.image.dwarfReader
		reader.Seek(pkgvar.offset)
		entry, err := reader.Next()
		if err != nil {
			return nil, err
		}

		// Ignore errors trying to extract values
		val, err := extractVarInfoFromEntry(scope.BinInfo, pkgvar.cu.image, regsReplaceStaticBase(scope.Regs, pkgvar.cu.image), scope.Mem, godwarf.EntryToTree(entry))
		if val != nil && val.Kind == reflect.Invalid {
			continue
		}
		if err != nil {
			continue
		}
		val.loadValue(cfg)
		vars = append(vars, val)
	}

	return vars, nil
}

func (scope *EvalScope) findGlobal(pkgName, varName string) (*Variable, error) {
	for _, pkgPath := range scope.BinInfo.PackageMap[pkgName] {
		v, err := scope.findGlobalInternal(pkgPath + "." + varName)
		if err != nil || v != nil {
			return v, err
		}
	}
	v, err := scope.findGlobalInternal(pkgName + "." + varName)
	if err != nil || v != nil {
		return v, err
	}
	return nil, fmt.Errorf("could not find symbol value for %s.%s", pkgName, varName)
}

func (scope *EvalScope) findGlobalInternal(name string) (*Variable, error) {
	for _, pkgvar := range scope.BinInfo.packageVars {
		if pkgvar.name == name || strings.HasSuffix(pkgvar.name, "/"+name) {
			reader := pkgvar.cu.image.dwarfReader
			reader.Seek(pkgvar.offset)
			entry, err := reader.Next()
			if err != nil {
				return nil, err
			}
			return extractVarInfoFromEntry(scope.BinInfo, pkgvar.cu.image, regsReplaceStaticBase(scope.Regs, pkgvar.cu.image), scope.Mem, godwarf.EntryToTree(entry))
		}
	}
	for _, fn := range scope.BinInfo.Functions {
		if fn.Name == name || strings.HasSuffix(fn.Name, "/"+name) {
			//TODO(aarzilli): convert function entry into a function type?
			r := newVariable(fn.Name, fn.Entry, &godwarf.FuncType{}, scope.BinInfo, scope.Mem)
			r.Value = constant.MakeString(fn.Name)
			r.Base = fn.Entry
			r.loaded = true
			if fn.Entry == 0 {
				r.Unreadable = fmt.Errorf("function %s is inlined", fn.Name)
			}
			return r, nil
		}
	}
	for dwref, ctyp := range scope.BinInfo.consts {
		for _, cval := range ctyp.values {
			if cval.fullName == name || strings.HasSuffix(cval.fullName, "/"+name) {
				t, err := scope.BinInfo.Images[dwref.imageIndex].Type(dwref.offset)
				if err != nil {
					return nil, err
				}
				v := newVariable(name, 0x0, t, scope.BinInfo, scope.Mem)
				switch v.Kind {
				case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
					v.Value = constant.MakeInt64(cval.value)
				case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
					v.Value = constant.MakeUint64(uint64(cval.value))
				default:
					return nil, fmt.Errorf("unsupported constant kind %v", v.Kind)
				}
				v.Flags |= VariableConstant
				v.loaded = true
				return v, nil
			}
		}
	}
	return nil, nil
}

// image returns the image containing the current function.
func (scope *EvalScope) image() *Image {
	return scope.BinInfo.funcToImage(scope.Fn)
}

// globalFor returns a global scope for 'image' with the register values of 'scope'.
func (scope *EvalScope) globalFor(image *Image) *EvalScope {
	r := *scope
	r.Regs.StaticBase = image.StaticBase
	r.Fn = &Function{cu: &compileUnit{image: image}}
	return &r
}

// DwarfReader returns the DwarfReader containing the
// Dwarf information for the target process.
func (scope *EvalScope) DwarfReader() *reader.Reader {
	return scope.image().DwarfReader()
}

// PtrSize returns the size of a pointer.
func (scope *EvalScope) PtrSize() int {
	return scope.BinInfo.Arch.PtrSize()
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
			e := newVariable("", argv.Addr+uint64(i), targetType.(*godwarf.SliceType).ElemType, scope.BinInfo, argv.mem)
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
			e := newVariable("", argv.Addr+uint64(i), targetType.(*godwarf.SliceType).ElemType, scope.BinInfo, argv.mem)
			e.loaded = true
			e.Value = constant.MakeInt64(int64(ch))
			v.Children = append(v.Children, *e)
		}
		v.Len = int64(len(v.Children))
		v.Cap = v.Len
		return v, nil

	case "string":
		switch argv.Kind {
		case reflect.String:
			s := constant.StringVal(argv.Value)
			v.Value = constant.MakeString(s)
			v.Len = int64(len(s))
			return v, nil
		case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Int, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uint, reflect.Uintptr:
			b, _ := constant.Int64Val(argv.Value)
			s := string(rune(b))
			v.Value = constant.MakeString(s)
			v.Len = int64(len(s))
			return v, nil
		case reflect.Slice, reflect.Array:
			var elem godwarf.Type
			if argv.Kind == reflect.Slice {
				elem = argv.RealType.(*godwarf.SliceType).ElemType
			} else {
				elem = argv.RealType.(*godwarf.ArrayType).Type
			}
			switch elemType := elem.(type) {
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

		default:
			return nil, nil
		}
	}

	return nil, nil
}

func (scope *EvalScope) evalAST(t ast.Expr) (*Variable, error) {
	switch node := t.(type) {
	case *ast.CallExpr:
		if len(node.Args) == 1 {
			v, err := scope.evalTypeCast(node)
			if err == nil || err != reader.TypeNotFoundErr {
				return v, err
			}
		}
		return evalFunctionCall(scope, node)

	case *ast.Ident:
		return scope.evalIdent(node)

	case *ast.ParenExpr:
		// otherwise just eval recursively
		return scope.evalAST(node.X)

	case *ast.SelectorExpr: // <expression>.<identifier>
		// try to interpret the selector as a package variable
		if maybePkg, ok := node.X.(*ast.Ident); ok {
			if maybePkg.Name == "runtime" && node.Sel.Name == "curg" {
				if scope.g == nil {
					typ, err := scope.BinInfo.findType("runtime.g")
					if err != nil {
						return nil, fmt.Errorf("blah: %v", err)
					}
					gvar := newVariable("curg", fakeAddress, typ, scope.BinInfo, scope.Mem)
					gvar.loaded = true
					gvar.Flags = VariableFakeAddress
					gvar.Children = append(gvar.Children, *newConstant(constant.MakeInt64(0), scope.Mem))
					gvar.Children[0].Name = "goid"
					return gvar, nil
				}
				return scope.g.variable.clone(), nil
			} else if maybePkg.Name == "runtime" && node.Sel.Name == "frameoff" {
				return newConstant(constant.MakeInt64(scope.frameOffset), scope.Mem), nil
			} else if v, err := scope.findGlobal(maybePkg.Name, node.Sel.Name); err == nil {
				return v, nil
			}
		}
		// try to accept "package/path".varname syntax for package variables
		if maybePkg, ok := node.X.(*ast.BasicLit); ok && maybePkg.Kind == token.STRING {
			pkgpath, err := strconv.Unquote(maybePkg.Value)
			if err == nil {
				if v, err := scope.findGlobal(pkgpath, node.Sel.Name); err == nil {
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

		v.Children = []Variable{*(newVariable("", uint64(n), ttyp.Type, scope.BinInfo, scope.Mem))}
		v.Children[0].OnlyAddr = true
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
		return nil, nil
	}

	callBuiltinWithArgs := func(builtin func([]*Variable, []ast.Expr) (*Variable, error)) (*Variable, error) {
		args := make([]*Variable, len(node.Args))

		for i := range node.Args {
			v, err := scope.evalAST(node.Args[i])
			if err != nil {
				return nil, err
			}
			args[i] = v
		}

		return builtin(args, node.Args)
	}

	switch fnnode.Name {
	case "cap":
		return callBuiltinWithArgs(capBuiltin)
	case "len":
		return callBuiltinWithArgs(lenBuiltin)
	case "complex":
		return callBuiltinWithArgs(complexBuiltin)
	case "imag":
		return callBuiltinWithArgs(imagBuiltin)
	case "real":
		return callBuiltinWithArgs(realBuiltin)
	}

	return nil, nil
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
		if v, err := scope.findGlobal(scope.Fn.PackageName(), node.Name); err == nil {
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

	// Prevent abuse, attempting to call "nil.member" directly.
	if xv.Addr == 0 && xv.Name == "nil" {
		return nil, fmt.Errorf("%s (type %s) is not a struct", xv.Name, xv.TypeString())
	}
	// Prevent abuse, attempting to call "\"fake\".member" directly.
	if xv.Addr == 0 && xv.Name == "" && xv.DwarfType == nil && xv.RealType == nil {
		return nil, fmt.Errorf("%s (type %s) is not a struct", xv.Value, xv.TypeString())
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
	// Accept .(data) as a type assertion that always succeeds, so that users
	// can access the data field of an interface without actually having to
	// type the concrete type.
	if idtyp, isident := node.Type.(*ast.Ident); !isident || idtyp.Name != "data" {
		typ, err := scope.BinInfo.findTypeExpr(node.Type)
		if err != nil {
			return nil, err
		}
		if xv.Children[0].DwarfType.Common().Name != typ.Common().Name {
			return nil, fmt.Errorf("interface conversion: %s is %s, not %s", xv.DwarfType.Common().Name, xv.Children[0].TypeString(), typ.Common().Name)
		}
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

	if xev.Flags&VariableCPtr == 0 {
		xev = xev.maybeDereference()
	}

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
		if xev.Flags&VariableCPtr == 0 {
			_, isarrptr := xev.RealType.(*godwarf.PtrType).Type.(*godwarf.ArrayType)
			if !isarrptr {
				return nil, cantindex
			}
			xev = xev.maybeDereference()
		}
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
	case reflect.Ptr:
		if xev.Flags&VariableCPtr != 0 {
			return xev.reslice(low, high)
		}
		fallthrough
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
		xev.Children[0].OnlyAddr = false
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
		return nil, errOperationOnSpecialFloat
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
			if constant.Sign(yv.Value) < 0 {
				return nil, fmt.Errorf("shift count must not be negative")
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
	if xv.Kind != reflect.String { // delay loading strings until we use them
		xv.loadValue(loadFullValue)
	}
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
	if yv.Kind != reflect.String { // delay loading strings until we use them
		yv.loadValue(loadFullValue)
	}
	if yv.Unreadable != nil {
		return nil, yv.Unreadable
	}

	if xv.FloatSpecial != 0 || yv.FloatSpecial != 0 {
		return nil, errOperationOnSpecialFloat
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
		if xv.Kind == reflect.String {
			xv.loadValue(loadFullValueLongerStrings)
		}
		if yv.Kind == reflect.String {
			yv.loadValue(loadFullValueLongerStrings)
		}
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
		if xv.Kind == reflect.String {
			xv.loadValue(loadFullValueLongerStrings)
		}
		if yv.Kind == reflect.String {
			yv.loadValue(loadFullValueLongerStrings)
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
		if typ == nil || !sameType(typ, v.RealType) {
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

func sameType(t1, t2 godwarf.Type) bool {
	// Because of a bug in the go linker a type that refers to another type
	// (for example a pointer type) will usually use the typedef but rarely use
	// the non-typedef entry directly.
	// For types that we read directly from go this is fine because it's
	// consistent, however we also synthesize some types ourselves
	// (specifically pointers and slices) and we always use a reference through
	// a typedef.
	t1 = resolveTypedef(t1)
	t2 = resolveTypedef(t2)

	if tt1, isptr1 := t1.(*godwarf.PtrType); isptr1 {
		tt2, isptr2 := t2.(*godwarf.PtrType)
		if !isptr2 {
			return false
		}
		return sameType(tt1.Type, tt2.Type)
	}
	if tt1, isslice1 := t1.(*godwarf.SliceType); isslice1 {
		tt2, isslice2 := t2.(*godwarf.SliceType)
		if !isslice2 {
			return false
		}
		return sameType(tt1.ElemType, tt2.ElemType)
	}
	return t1.String() == t2.String()
}

func (v *Variable) sliceAccess(idx int) (*Variable, error) {
	wrong := false
	if v.Flags&VariableCPtr == 0 {
		wrong = idx < 0 || int64(idx) >= v.Len
	} else {
		wrong = idx < 0
	}
	if wrong {
		return nil, fmt.Errorf("index out of bounds")
	}
	mem := v.mem
	if v.Kind != reflect.Array {
		mem = DereferenceMemory(mem)
	}
	return v.newVariable("", v.Base+uint64(int64(idx)*v.stride), v.fieldType, mem), nil
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
	wrong := false
	cptrNeedsFakeSlice := false
	if v.Flags&VariableCPtr == 0 {
		wrong = low < 0 || low >= v.Len || high < 0 || high > v.Len
	} else {
		wrong = low < 0 || high < 0
		if high == 0 {
			high = low
		}
		cptrNeedsFakeSlice = v.Kind != reflect.String
	}
	if wrong {
		return nil, fmt.Errorf("index out of bounds")
	}

	base := v.Base + uint64(int64(low)*v.stride)
	len := high - low

	if high-low < 0 {
		return nil, fmt.Errorf("index out of bounds")
	}

	typ := v.DwarfType
	if _, isarr := v.DwarfType.(*godwarf.ArrayType); isarr || cptrNeedsFakeSlice {
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
	return v.tryFindMethodInEmbeddedFields(mname)
}

func (v *Variable) tryFindMethodInEmbeddedFields(mname string) (*Variable, error) {
	structVar := v.maybeDereference()
	structVar.Name = v.Name
	if structVar.Unreadable != nil {
		return structVar, nil
	}
	switch t := structVar.RealType.(type) {
	case *godwarf.StructType:
		for _, field := range t.Field {
			if field.Embedded {
				// Recursively check for promoted fields on the embedded field
				embeddedVar, err := structVar.toField(field)
				if err != nil {
					return nil, err
				}
				if embeddedMethod, err := embeddedVar.findMethod(mname); err != nil {
					return nil, err
				} else if embeddedMethod != nil {
					return embeddedMethod, nil
				}
			}
		}
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
	v.Base = fn.Entry
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

func fakeArrayType(n uint64, fieldType godwarf.Type) godwarf.Type {
	stride := alignAddr(fieldType.Common().ByteSize, fieldType.Align())
	return &godwarf.ArrayType{
		CommonType: godwarf.CommonType{
			ReflectKind: reflect.Array,
			ByteSize:    int64(n) * stride,
			Name:        fmt.Sprintf("[%d]%s", n, fieldType.String())},
		Type:          fieldType,
		StrideBitSize: stride * 8,
		Count:         int64(n)}
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

	// Only try and remove the receiver if it is actually being passed in as a formal argument.
	// In the case of:
	//
	// func (_ X) Method() { ... }
	//
	// that would not be true, the receiver is not used and thus
	// not being passed in as a formal argument.
	//
	// TODO(derekparker) This, I think, creates a new bug where
	// if the receiver is not passed in as a formal argument but
	// there are other arguments, such as:
	//
	// func (_ X) Method(i int) { ... }
	//
	// The first argument 'i int' will be removed. We must actually detect
	// here if the receiver is being used. While this is a bug, it's not a
	// functional bug, it only affects the string representation of the fake
	// function type we create. It's not really easy to tell here if we use
	// the receiver or not. Perhaps we should not perform this manipulation at all?
	if removeReceiver && len(formalArgs) > 0 {
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
