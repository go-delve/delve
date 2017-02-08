package proc

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"github.com/derekparker/delve/pkg/dwarf/reader"

	"golang.org/x/debug/dwarf"
)

// The kind field in runtime._type is a reflect.Kind value plus
// some extra flags defined here.
// See equivalent declaration in $GOROOT/src/reflect/type.go
const (
	kindDirectIface = 1 << 5
	kindGCProg      = 1 << 6 // Type.gc points to GC program
	kindNoPointers  = 1 << 7
	kindMask        = (1 << 5) - 1
)

// Value of tflag field in runtime._type.
// See $GOROOT/reflect/type.go for a description of these flags.
const (
	tflagUncommon  = 1 << 0
	tflagExtraStar = 1 << 1
	tflagNamed     = 1 << 2
)

// Do not call this function directly it isn't able to deal correctly with package paths
func (dbp *Process) findType(name string) (dwarf.Type, error) {
	off, found := dbp.types[name]
	if !found {
		return nil, reader.TypeNotFoundErr
	}
	return dbp.dwarf.Type(off)
}

func (dbp *Process) pointerTo(typ dwarf.Type) dwarf.Type {
	return &dwarf.PtrType{dwarf.CommonType{int64(dbp.arch.PtrSize()), "*" + typ.Common().Name, reflect.Ptr, 0}, typ}
}

func (dbp *Process) findTypeExpr(expr ast.Expr) (dwarf.Type, error) {
	dbp.loadPackageMap()
	if lit, islit := expr.(*ast.BasicLit); islit && lit.Kind == token.STRING {
		// Allow users to specify type names verbatim as quoted
		// string. Useful as a catch-all workaround for cases where we don't
		// parse/serialize types correctly or can not resolve package paths.
		typn, _ := strconv.Unquote(lit.Value)
		return dbp.findType(typn)
	}
	dbp.expandPackagesInType(expr)
	if snode, ok := expr.(*ast.StarExpr); ok {
		// Pointer types only appear in the dwarf informations when
		// a pointer to the type is used in the target program, here
		// we create a pointer type on the fly so that the user can
		// specify a pointer to any variable used in the target program
		ptyp, err := dbp.findTypeExpr(snode.X)
		if err != nil {
			return nil, err
		}
		return dbp.pointerTo(ptyp), nil
	}
	return dbp.findType(exprToString(expr))
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

func (dbp *Process) loadPackageMap() error {
	if dbp.packageMap != nil {
		return nil
	}
	dbp.packageMap = map[string]string{}
	reader := dbp.DwarfReader()
	for entry, err := reader.Next(); entry != nil; entry, err = reader.Next() {
		if err != nil {
			return err
		}

		if entry.Tag != dwarf.TagTypedef && entry.Tag != dwarf.TagBaseType && entry.Tag != dwarf.TagClassType && entry.Tag != dwarf.TagStructType {
			continue
		}

		typename, ok := entry.Val(dwarf.AttrName).(string)
		if !ok || complexType(typename) {
			continue
		}

		dot := strings.LastIndex(typename, ".")
		if dot < 0 {
			continue
		}
		path := typename[:dot]
		slash := strings.LastIndex(path, "/")
		if slash < 0 || slash+1 >= len(path) {
			continue
		}
		name := path[slash+1:]
		dbp.packageMap[name] = path
	}
	return nil
}

type sortFunctionsDebugInfoByLowpc []functionDebugInfo

func (v sortFunctionsDebugInfoByLowpc) Len() int           { return len(v) }
func (v sortFunctionsDebugInfoByLowpc) Less(i, j int) bool { return v[i].lowpc < v[j].lowpc }
func (v sortFunctionsDebugInfoByLowpc) Swap(i, j int) {
	temp := v[i]
	v[i] = v[j]
	v[j] = temp
}

func (dbp *Process) loadDebugInfoMaps(wg *sync.WaitGroup) {
	defer wg.Done()
	dbp.types = make(map[string]dwarf.Offset)
	dbp.functions = []functionDebugInfo{}
	reader := dbp.DwarfReader()
	for entry, err := reader.Next(); entry != nil; entry, err = reader.Next() {
		if err != nil {
			break
		}
		switch entry.Tag {
		case dwarf.TagArrayType, dwarf.TagBaseType, dwarf.TagClassType, dwarf.TagStructType, dwarf.TagUnionType, dwarf.TagConstType, dwarf.TagVolatileType, dwarf.TagRestrictType, dwarf.TagEnumerationType, dwarf.TagPointerType, dwarf.TagSubroutineType, dwarf.TagTypedef, dwarf.TagUnspecifiedType:
			name, ok := entry.Val(dwarf.AttrName).(string)
			if !ok {
				continue
			}
			if _, exists := dbp.types[name]; !exists {
				dbp.types[name] = entry.Offset
			}
		case dwarf.TagSubprogram:
			lowpc, ok := entry.Val(dwarf.AttrLowpc).(uint64)
			if !ok {
				continue
			}
			highpc, ok := entry.Val(dwarf.AttrHighpc).(uint64)
			if !ok {
				continue
			}
			dbp.functions = append(dbp.functions, functionDebugInfo{lowpc, highpc, entry.Offset})
		}
	}
	sort.Sort(sortFunctionsDebugInfoByLowpc(dbp.functions))
}

func (dbp *Process) findFunctionDebugInfo(pc uint64) (dwarf.Offset, error) {
	i := sort.Search(len(dbp.functions), func(i int) bool {
		fn := dbp.functions[i]
		return pc <= fn.lowpc || (fn.lowpc <= pc && pc < fn.highpc)
	})
	if i != len(dbp.functions) {
		fn := dbp.functions[i]
		if fn.lowpc <= pc && pc < fn.highpc {
			return fn.offset, nil
		}
	}
	return 0, errors.New("unable to find function context")
}

func (dbp *Process) expandPackagesInType(expr ast.Expr) {
	switch e := expr.(type) {
	case *ast.ArrayType:
		dbp.expandPackagesInType(e.Elt)
	case *ast.ChanType:
		dbp.expandPackagesInType(e.Value)
	case *ast.FuncType:
		for i := range e.Params.List {
			dbp.expandPackagesInType(e.Params.List[i].Type)
		}
		if e.Results != nil {
			for i := range e.Results.List {
				dbp.expandPackagesInType(e.Results.List[i].Type)
			}
		}
	case *ast.MapType:
		dbp.expandPackagesInType(e.Key)
		dbp.expandPackagesInType(e.Value)
	case *ast.ParenExpr:
		dbp.expandPackagesInType(e.X)
	case *ast.SelectorExpr:
		switch x := e.X.(type) {
		case *ast.Ident:
			if path, ok := dbp.packageMap[x.Name]; ok {
				x.Name = path
			}
		default:
			dbp.expandPackagesInType(e.X)
		}
	case *ast.StarExpr:
		dbp.expandPackagesInType(e.X)
	default:
		// nothing to do
	}
}

type nameOfRuntimeTypeEntry struct {
	typename string
	kind     int64
}

// Returns the type name of the type described in _type.
// _type is a non-loaded Variable pointing to runtime._type struct in the target.
// The returned string is in the format that's used in DWARF data
func nameOfRuntimeType(_type *Variable) (typename string, kind int64, err error) {
	if e, ok := _type.dbp.nameOfRuntimeType[_type.Addr]; ok {
		return e.typename, e.kind, nil
	}

	var tflag int64

	if tflagField := _type.loadFieldNamed("tflag"); tflagField != nil && tflagField.Value != nil {
		tflag, _ = constant.Int64Val(tflagField.Value)
	}
	if kindField := _type.loadFieldNamed("kind"); kindField != nil && kindField.Value != nil {
		kind, _ = constant.Int64Val(kindField.Value)
	}

	// Named types are defined by a 'type' expression, everything else
	// (for example pointers to named types) are not considered named.
	if tflag&tflagNamed != 0 {
		typename, err = nameOfNamedRuntimeType(_type, kind, tflag)
		return typename, kind, err
	} else {
		typename, err = nameOfUnnamedRuntimeType(_type, kind, tflag)
		return typename, kind, err
	}

	_type.dbp.nameOfRuntimeType[_type.Addr] = nameOfRuntimeTypeEntry{typename, kind}

	return typename, kind, nil
}

// The layout of a runtime._type struct is as follows:
//
// <runtime._type><kind specific struct fields><runtime.uncommontype>
//
// with the 'uncommon type struct' being optional
//
// For named types first we extract the type name from the 'str'
// field in the runtime._type struct.
// Then we prepend the package path from the runtime.uncommontype
// struct, when it exists.
//
// To find out the memory address of the runtime.uncommontype struct
// we first cast the Variable pointing to the runtime._type struct
// to a struct specific to the type's kind (for example, if the type
// being described is a slice type the variable will be specialized
// to a runtime.slicetype).
func nameOfNamedRuntimeType(_type *Variable, kind, tflag int64) (typename string, err error) {
	var strOff int64
	if strField := _type.loadFieldNamed("str"); strField != nil && strField.Value != nil {
		strOff, _ = constant.Int64Val(strField.Value)
	} else {
		return "", errors.New("could not find str field")
	}

	// The following code is adapted from reflect.(*rtype).Name.
	// For a description of how memory is organized for type names read
	// the comment to 'type name struct' in $GOROOT/src/reflect/type.go

	typename, _, _, err = _type.dbp.resolveNameOff(_type.Addr, uintptr(strOff))
	if err != nil {
		return "", err
	}

	if tflag&tflagExtraStar != 0 {
		typename = typename[1:]
	}

	if i := strings.Index(typename, "."); i >= 0 {
		typename = typename[i+1:]
	} else {
		return typename, nil
	}

	// The following code is adapted from reflect.(*rtype).PkgPath in
	// $GOROOT/src/reflect/type.go

	_type, err = specificRuntimeType(_type, kind)
	if err != nil {
		return "", err
	}

	if ut := uncommon(_type, tflag); ut != nil {
		if pkgPathField := ut.loadFieldNamed("pkgpath"); pkgPathField != nil && pkgPathField.Value != nil {
			pkgPathOff, _ := constant.Int64Val(pkgPathField.Value)
			pkgPath, _, _, err := _type.dbp.resolveNameOff(_type.Addr, uintptr(pkgPathOff))
			if err != nil {
				return "", err
			}
			typename = pkgPath + "." + typename
		}
	}

	return typename, nil
}

func nameOfUnnamedRuntimeType(_type *Variable, kind, tflag int64) (string, error) {
	_type, err := specificRuntimeType(_type, kind)
	if err != nil {
		return "", err
	}

	// The types referred to here are defined in $GOROOT/src/runtime/type.go
	switch reflect.Kind(kind & kindMask) {
	case reflect.Array:
		var len int64
		if lenField := _type.loadFieldNamed("len"); lenField != nil && lenField.Value != nil {
			len, _ = constant.Int64Val(lenField.Value)
		}
		elemname, err := fieldToType(_type, "elem")
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("[%d]%s", len, elemname), nil
	case reflect.Chan:
		elemname, err := fieldToType(_type, "elem")
		if err != nil {
			return "", err
		}
		return "chan " + elemname, nil
	case reflect.Func:
		return nameOfFuncRuntimeType(_type, tflag, true)
	case reflect.Interface:
		return nameOfInterfaceRuntimeType(_type, kind, tflag)
	case reflect.Map:
		keyname, err := fieldToType(_type, "key")
		if err != nil {
			return "", err
		}
		elemname, err := fieldToType(_type, "elem")
		if err != nil {
			return "", err
		}
		return "map[" + keyname + "]" + elemname, nil
	case reflect.Ptr:
		elemname, err := fieldToType(_type, "elem")
		if err != nil {
			return "", err
		}
		return "*" + elemname, nil
	case reflect.Slice:
		elemname, err := fieldToType(_type, "elem")
		if err != nil {
			return "", err
		}
		return "[]" + elemname, nil
	case reflect.Struct:
		return nameOfStructRuntimeType(_type, kind, tflag)
	default:
		return nameOfNamedRuntimeType(_type, kind, tflag)
	}
}

// Returns the expression describing an anonymous function type.
// A runtime.functype is followed by a runtime.uncommontype
// (optional) and then by an array of pointers to runtime._type,
// one for each input and output argument.
func nameOfFuncRuntimeType(_type *Variable, tflag int64, anonymous bool) (string, error) {
	rtyp, err := _type.dbp.findType("runtime._type")
	if err != nil {
		return "", err
	}
	prtyp := _type.dbp.pointerTo(rtyp)

	uadd := _type.RealType.Common().ByteSize
	if ut := uncommon(_type, tflag); ut != nil {
		uadd += ut.RealType.Common().ByteSize
	}

	var inCount, outCount int64
	if inCountField := _type.loadFieldNamed("inCount"); inCountField != nil && inCountField.Value != nil {
		inCount, _ = constant.Int64Val(inCountField.Value)
	}
	if outCountField := _type.loadFieldNamed("outCount"); outCountField != nil && outCountField.Value != nil {
		outCount, _ = constant.Int64Val(outCountField.Value)
		// only the lowest 15 bits of outCount are used, rest are flags
		outCount = outCount & (1<<15 - 1)
	}

	cursortyp := _type.newVariable("", _type.Addr+uintptr(uadd), prtyp)
	var buf bytes.Buffer
	if anonymous {
		buf.WriteString("func(")
	} else {
		buf.WriteString("(")
	}

	for i := int64(0); i < inCount; i++ {
		argtype := cursortyp.maybeDereference()
		cursortyp.Addr += uintptr(_type.dbp.arch.PtrSize())
		argtypename, _, err := nameOfRuntimeType(argtype)
		if err != nil {
			return "", err
		}
		buf.WriteString(argtypename)
		if i != inCount-1 {
			buf.WriteString(", ")
		}
	}
	buf.WriteString(")")

	switch outCount {
	case 0:
		// nothing to do
	case 1:
		buf.WriteString(" ")
		argtype := cursortyp.maybeDereference()
		argtypename, _, err := nameOfRuntimeType(argtype)
		if err != nil {
			return "", err
		}
		buf.WriteString(argtypename)
	default:
		buf.WriteString(" (")
		for i := int64(0); i < outCount; i++ {
			argtype := cursortyp.maybeDereference()
			cursortyp.Addr += uintptr(_type.dbp.arch.PtrSize())
			argtypename, _, err := nameOfRuntimeType(argtype)
			if err != nil {
				return "", err
			}
			buf.WriteString(argtypename)
			if i != inCount-1 {
				buf.WriteString(", ")
			}
		}
		buf.WriteString(")")
	}
	return buf.String(), nil
}

func nameOfInterfaceRuntimeType(_type *Variable, kind, tflag int64) (string, error) {
	var buf bytes.Buffer
	buf.WriteString("interface {")

	methods, _ := _type.structMember("methods")
	methods.loadArrayValues(0, LoadConfig{false, 1, 0, 4096, -1})
	if methods.Unreadable != nil {
		return "", nil
	}

	if len(methods.Children) == 0 {
		buf.WriteString("}")
		return buf.String(), nil
	} else {
		buf.WriteString(" ")
	}

	for i, im := range methods.Children {
		var methodname, methodtype string
		for i := range im.Children {
			switch im.Children[i].Name {
			case "name":
				nameoff, _ := constant.Int64Val(im.Children[i].Value)
				var err error
				methodname, _, _, err = _type.dbp.resolveNameOff(_type.Addr, uintptr(nameoff))
				if err != nil {
					return "", err
				}

			case "typ":
				typeoff, _ := constant.Int64Val(im.Children[i].Value)
				typ, err := _type.dbp.resolveTypeOff(_type.Addr, uintptr(typeoff))
				if err != nil {
					return "", err
				}
				typ, err = specificRuntimeType(typ, int64(reflect.Func))
				if err != nil {
					return "", err
				}
				var tflag int64
				if tflagField := typ.loadFieldNamed("tflag"); tflagField != nil && tflagField.Value != nil {
					tflag, _ = constant.Int64Val(tflagField.Value)
				}
				methodtype, err = nameOfFuncRuntimeType(typ, tflag, false)
				if err != nil {
					return "", err
				}
			}
		}

		buf.WriteString(methodname)
		buf.WriteString(methodtype)

		if i != len(methods.Children)-1 {
			buf.WriteString("; ")
		} else {
			buf.WriteString(" }")
		}
	}
	return buf.String(), nil
}

func nameOfStructRuntimeType(_type *Variable, kind, tflag int64) (string, error) {
	var buf bytes.Buffer
	buf.WriteString("struct {")

	fields, _ := _type.structMember("fields")
	fields.loadArrayValues(0, LoadConfig{false, 1, 0, 4096, -1})
	if fields.Unreadable != nil {
		return "", fields.Unreadable
	}

	if len(fields.Children) == 0 {
		buf.WriteString("}")
		return buf.String(), nil
	} else {
		buf.WriteString(" ")
	}

	for i, field := range fields.Children {
		var fieldname, fieldtypename string
		var typeField *Variable
		for i := range field.Children {
			switch field.Children[i].Name {
			case "name":
				nameoff, _ := constant.Int64Val(field.Children[i].Value)
				var err error
				fieldname, _, _, err = _type.dbp.loadName(uintptr(nameoff))
				if err != nil {
					return "", err
				}

			case "typ":
				typeField = field.Children[i].maybeDereference()
				var err error
				fieldtypename, _, err = nameOfRuntimeType(typeField)
				if err != nil {
					return "", err
				}
			}
		}

		// fieldname will be the empty string for anonymous fields
		if fieldname != "" {
			buf.WriteString(fieldname)
			buf.WriteString(" ")
		}
		buf.WriteString(fieldtypename)
		if i != len(fields.Children)-1 {
			buf.WriteString("; ")
		} else {
			buf.WriteString(" }")
		}
	}

	return buf.String(), nil
}

func fieldToType(_type *Variable, fieldName string) (string, error) {
	typeField, err := _type.structMember(fieldName)
	if err != nil {
		return "", err
	}
	typeField = typeField.maybeDereference()
	typename, _, err := nameOfRuntimeType(typeField)
	return typename, err
}

func specificRuntimeType(_type *Variable, kind int64) (*Variable, error) {
	rtyp, err := _type.dbp.findType("runtime._type")
	if err != nil {
		return nil, err
	}
	prtyp := _type.dbp.pointerTo(rtyp)

	uintptrtyp, err := _type.dbp.findType("uintptr")
	if err != nil {
		return nil, err
	}

	uint32typ := &dwarf.UintType{dwarf.BasicType{CommonType: dwarf.CommonType{ByteSize: 4, Name: "uint32"}}}
	uint16typ := &dwarf.UintType{dwarf.BasicType{CommonType: dwarf.CommonType{ByteSize: 2, Name: "uint16"}}}

	newStructType := func(name string, sz uintptr) *dwarf.StructType {
		return &dwarf.StructType{dwarf.CommonType{Name: name, ByteSize: int64(sz)}, name, "struct", nil, false}
	}

	appendField := func(typ *dwarf.StructType, name string, fieldtype dwarf.Type, off uintptr) {
		typ.Field = append(typ.Field, &dwarf.StructField{Name: name, ByteOffset: int64(off), Type: fieldtype})
	}

	newSliceType := func(elemtype dwarf.Type) *dwarf.SliceType {
		r := newStructType("[]"+elemtype.Common().Name, uintptr(3*uintptrtyp.Size()))
		appendField(r, "array", _type.dbp.pointerTo(elemtype), 0)
		appendField(r, "len", uintptrtyp, uintptr(uintptrtyp.Size()))
		appendField(r, "cap", uintptrtyp, uintptr(2*uintptrtyp.Size()))
		return &dwarf.SliceType{StructType: *r, ElemType: elemtype}
	}

	var typ *dwarf.StructType

	type rtype struct {
		size       uintptr
		ptrdata    uintptr
		hash       uint32 // hash of type; avoids computation in hash tables
		tflag      uint8  // extra type information flags
		align      uint8  // alignment of variable with this type
		fieldAlign uint8  // alignment of struct field with this type
		kind       uint8  // enumeration for C
		alg        *byte  // algorithm table
		gcdata     *byte  // garbage collection data
		str        int32  // string form
		ptrToThis  int32  // type for pointer to this type, may be zero
	}

	switch reflect.Kind(kind & kindMask) {
	case reflect.Array:
		// runtime.arraytype
		var a struct {
			rtype
			elem  *rtype // array element type
			slice *rtype // slice type
			len   uintptr
		}
		typ = newStructType("runtime.arraytype", unsafe.Sizeof(a))
		appendField(typ, "elem", prtyp, unsafe.Offsetof(a.elem))
		appendField(typ, "len", uintptrtyp, unsafe.Offsetof(a.len))
	case reflect.Chan:
		// runtime.chantype
		var a struct {
			rtype
			elem *rtype  // channel element type
			dir  uintptr // channel direction (ChanDir)
		}
		typ = newStructType("runtime.chantype", unsafe.Sizeof(a))
		appendField(typ, "elem", prtyp, unsafe.Offsetof(a.elem))
	case reflect.Func:
		// runtime.functype
		var a struct {
			rtype    `reflect:"func"`
			inCount  uint16
			outCount uint16 // top bit is set if last input parameter is ...
		}
		typ = newStructType("runtime.functype", unsafe.Sizeof(a))
		appendField(typ, "inCount", uint16typ, unsafe.Offsetof(a.inCount))
		appendField(typ, "outCount", uint16typ, unsafe.Offsetof(a.outCount))
	case reflect.Interface:
		// runtime.imethod
		type imethod struct {
			name uint32 // name of method
			typ  uint32 // .(*FuncType) underneath
		}

		var im imethod

		// runtime.interfacetype
		var a struct {
			rtype   `reflect:"interface"`
			pkgPath *byte     // import path
			methods []imethod // sorted by hash
		}

		imethodtype := newStructType("runtime.imethod", unsafe.Sizeof(im))
		appendField(imethodtype, "name", uint32typ, unsafe.Offsetof(im.name))
		appendField(imethodtype, "typ", uint32typ, unsafe.Offsetof(im.typ))
		typ = newStructType("runtime.interfacetype", unsafe.Sizeof(a))
		appendField(typ, "methods", newSliceType(imethodtype), unsafe.Offsetof(a.methods))
	case reflect.Map:
		// runtime.maptype
		var a struct {
			rtype         `reflect:"map"`
			key           *rtype // map key type
			elem          *rtype // map element (value) type
			bucket        *rtype // internal bucket structure
			hmap          *rtype // internal map header
			keysize       uint8  // size of key slot
			indirectkey   uint8  // store ptr to key instead of key itself
			valuesize     uint8  // size of value slot
			indirectvalue uint8  // store ptr to value instead of value itself
			bucketsize    uint16 // size of bucket
			reflexivekey  bool   // true if k==k for all keys
			needkeyupdate bool   // true if we need to update key on an overwrite
		}
		typ = newStructType("runtime.maptype", unsafe.Sizeof(a))
		appendField(typ, "key", prtyp, unsafe.Offsetof(a.key))
		appendField(typ, "elem", prtyp, unsafe.Offsetof(a.elem))
	case reflect.Ptr:
		// runtime.ptrtype
		var a struct {
			rtype `reflect:"ptr"`
			elem  *rtype // pointer element (pointed at) type
		}
		typ = newStructType("runtime.ptrtype", unsafe.Sizeof(a))
		appendField(typ, "elem", prtyp, unsafe.Offsetof(a.elem))
	case reflect.Slice:
		// runtime.slicetype
		var a struct {
			rtype `reflect:"slice"`
			elem  *rtype // slice element type
		}

		typ = newStructType("runtime.slicetype", unsafe.Sizeof(a))
		appendField(typ, "elem", prtyp, unsafe.Offsetof(a.elem))
	case reflect.Struct:
		// runtime.structtype
		type structField struct {
			name   *byte   // name is empty for embedded fields
			typ    *rtype  // type of field
			offset uintptr // byte offset of field within struct
		}

		var sf structField

		var a struct {
			rtype   `reflect:"struct"`
			pkgPath *byte
			fields  []structField // sorted by offset
		}

		fieldtype := newStructType("runtime.structtype", unsafe.Sizeof(sf))
		appendField(fieldtype, "name", uintptrtyp, unsafe.Offsetof(sf.name))
		appendField(fieldtype, "typ", prtyp, unsafe.Offsetof(sf.typ))
		typ = newStructType("runtime.structtype", unsafe.Sizeof(a))
		appendField(typ, "fields", newSliceType(fieldtype), unsafe.Offsetof(a.fields))
	default:
		return _type, nil
	}

	return _type.newVariable(_type.Name, _type.Addr, typ), nil
}

// See reflect.(*rtype).uncommon in $GOROOT/src/reflect/type.go
func uncommon(_type *Variable, tflag int64) *Variable {
	if tflag&tflagUncommon == 0 {
		return nil
	}

	typ, err := _type.dbp.findType("runtime.uncommontype")
	if err != nil {
		return nil
	}

	return _type.newVariable(_type.Name, _type.Addr+uintptr(_type.RealType.Size()), typ)
}
