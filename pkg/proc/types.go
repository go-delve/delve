package proc

import (
	"bytes"
	"debug/dwarf"
	"errors"
	"fmt"
	"go/constant"
	"reflect"
	"strings"

	"github.com/go-delve/delve/pkg/dwarf/godwarf"
	"github.com/go-delve/delve/pkg/dwarf/reader"
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

// These constants contain the names of the fields of runtime.interfacetype
// and runtime.imethod.
// runtime.interfacetype.mhdr is a slice of runtime.imethod describing the
// methods of the interface.
const (
	imethodFieldName       = "name"
	imethodFieldItyp       = "ityp"
	interfacetypeFieldMhdr = "mhdr"
)

type runtimeTypeDIE struct {
	offset dwarf.Offset
	kind   int64
}

func pointerTo(typ godwarf.Type, arch *Arch) godwarf.Type {
	return &godwarf.PtrType{
		CommonType: godwarf.CommonType{
			ByteSize:    int64(arch.PtrSize()),
			Name:        "*" + typ.Common().Name,
			ReflectKind: reflect.Ptr,
			Offset:      0,
		},
		Type: typ,
	}
}

type functionsDebugInfoByEntry []Function

func (v functionsDebugInfoByEntry) Len() int           { return len(v) }
func (v functionsDebugInfoByEntry) Less(i, j int) bool { return v[i].Entry < v[j].Entry }
func (v functionsDebugInfoByEntry) Swap(i, j int)      { v[i], v[j] = v[j], v[i] }

type compileUnitsByOffset []*compileUnit

func (v compileUnitsByOffset) Len() int               { return len(v) }
func (v compileUnitsByOffset) Less(i int, j int) bool { return v[i].offset < v[j].offset }
func (v compileUnitsByOffset) Swap(i int, j int)      { v[i], v[j] = v[j], v[i] }

type packageVarsByAddr []packageVar

func (v packageVarsByAddr) Len() int               { return len(v) }
func (v packageVarsByAddr) Less(i int, j int) bool { return v[i].addr < v[j].addr }
func (v packageVarsByAddr) Swap(i int, j int)      { v[i], v[j] = v[j], v[i] }

type loadDebugInfoMapsContext struct {
	ardr                *reader.Reader
	abstractOriginTable map[dwarf.Offset]int
	knownPackageVars    map[string]struct{}
	offsetToVersion     map[dwarf.Offset]uint8
}

func newLoadDebugInfoMapsContext(bi *BinaryInfo, image *Image, offsetToVersion map[dwarf.Offset]uint8) *loadDebugInfoMapsContext {
	ctxt := &loadDebugInfoMapsContext{}

	ctxt.ardr = image.DwarfReader()
	ctxt.abstractOriginTable = make(map[dwarf.Offset]int)
	ctxt.offsetToVersion = offsetToVersion

	ctxt.knownPackageVars = map[string]struct{}{}
	for _, v := range bi.packageVars {
		ctxt.knownPackageVars[v.name] = struct{}{}
	}

	return ctxt
}

func (ctxt *loadDebugInfoMapsContext) lookupAbstractOrigin(bi *BinaryInfo, off dwarf.Offset) int {
	r, ok := ctxt.abstractOriginTable[off]
	if !ok {
		bi.Functions = append(bi.Functions, Function{})
		r = len(bi.Functions) - 1
		bi.Functions[r].offset = off
		ctxt.abstractOriginTable[off] = r
	}
	return r
}

// runtimeTypeToDIE returns the DIE corresponding to the runtime._type.
// This is done in three different ways depending on the version of go.
// * Before go1.7 the type name is retrieved directly from the runtime._type
//   and looked up in debug_info
// * After go1.7 the runtime._type struct is read recursively to reconstruct
//   the name of the type, and then the type's name is used to look up
//   debug_info
// * After go1.11 the runtimeTypeToDIE map is used to look up the address of
//   the type and map it drectly to a DIE.
func runtimeTypeToDIE(_type *Variable, dataAddr uint64) (typ godwarf.Type, kind int64, err error) {
	bi := _type.bi

	_type = _type.maybeDereference()

	// go 1.11 implementation: use extended attribute in debug_info

	mds, err := loadModuleData(bi, _type.mem)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading module data: %v", err)
	}

	md := findModuleDataForType(bi, mds, _type.Addr, _type.mem)
	if md != nil {
		so := bi.moduleDataToImage(md)
		if rtdie, ok := so.runtimeTypeToDIE[uint64(_type.Addr-md.types)]; ok {
			typ, err := godwarf.ReadType(so.dwarf, so.index, rtdie.offset, so.typeCache)
			if err != nil {
				return nil, 0, fmt.Errorf("invalid interface type: %v", err)
			}
			if rtdie.kind == -1 {
				if kindField := _type.loadFieldNamed("kind"); kindField != nil && kindField.Value != nil {
					rtdie.kind, _ = constant.Int64Val(kindField.Value)
				}
			}
			return typ, rtdie.kind, nil
		}
	}

	// go1.7 to go1.10 implementation: convert runtime._type structs to type names

	typename, kind, err := nameOfRuntimeType(mds, _type)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid interface type: %v", err)
	}

	typ, err = bi.findType(typename)
	if err != nil {
		return nil, 0, fmt.Errorf("interface type %q not found for %#x: %v", typename, dataAddr, err)
	}

	return typ, kind, nil
}

type nameOfRuntimeTypeEntry struct {
	typename string
	kind     int64
}

// Returns the type name of the type described in _type.
// _type is a non-loaded Variable pointing to runtime._type struct in the target.
// The returned string is in the format that's used in DWARF data
func nameOfRuntimeType(mds []moduleData, _type *Variable) (typename string, kind int64, err error) {
	if e, ok := _type.bi.nameOfRuntimeType[_type.Addr]; ok {
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
		typename, err = nameOfNamedRuntimeType(mds, _type, kind, tflag)
		if err == nil {
			_type.bi.nameOfRuntimeType[_type.Addr] = nameOfRuntimeTypeEntry{typename: typename, kind: kind}
		}
		return typename, kind, err
	}

	typename, err = nameOfUnnamedRuntimeType(mds, _type, kind, tflag)
	if err == nil {
		_type.bi.nameOfRuntimeType[_type.Addr] = nameOfRuntimeTypeEntry{typename: typename, kind: kind}
	}
	return typename, kind, err
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
func nameOfNamedRuntimeType(mds []moduleData, _type *Variable, kind, tflag int64) (typename string, err error) {
	var strOff int64
	if strField := _type.loadFieldNamed("str"); strField != nil && strField.Value != nil {
		strOff, _ = constant.Int64Val(strField.Value)
	} else {
		return "", errors.New("could not find str field")
	}

	// The following code is adapted from reflect.(*rtype).Name.
	// For a description of how memory is organized for type names read
	// the comment to 'type name struct' in $GOROOT/src/reflect/type.go

	typename, _, _, err = resolveNameOff(_type.bi, mds, _type.Addr, uint64(strOff), _type.mem)
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
			pkgPath, _, _, err := resolveNameOff(_type.bi, mds, _type.Addr, uint64(pkgPathOff), _type.mem)
			if err != nil {
				return "", err
			}
			if slash := strings.LastIndex(pkgPath, "/"); slash >= 0 {
				fixedName := strings.Replace(pkgPath[slash+1:], ".", "%2e", -1)
				if fixedName != pkgPath[slash+1:] {
					pkgPath = pkgPath[:slash+1] + fixedName
				}
			}
			typename = pkgPath + "." + typename
		}
	}

	return typename, nil
}

func nameOfUnnamedRuntimeType(mds []moduleData, _type *Variable, kind, tflag int64) (string, error) {
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
		elemname, err := fieldToType(mds, _type, "elem")
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("[%d]%s", len, elemname), nil
	case reflect.Chan:
		elemname, err := fieldToType(mds, _type, "elem")
		if err != nil {
			return "", err
		}
		return "chan " + elemname, nil
	case reflect.Func:
		return nameOfFuncRuntimeType(mds, _type, tflag, true)
	case reflect.Interface:
		return nameOfInterfaceRuntimeType(mds, _type, kind, tflag)
	case reflect.Map:
		keyname, err := fieldToType(mds, _type, "key")
		if err != nil {
			return "", err
		}
		elemname, err := fieldToType(mds, _type, "elem")
		if err != nil {
			return "", err
		}
		return "map[" + keyname + "]" + elemname, nil
	case reflect.Ptr:
		elemname, err := fieldToType(mds, _type, "elem")
		if err != nil {
			return "", err
		}
		return "*" + elemname, nil
	case reflect.Slice:
		elemname, err := fieldToType(mds, _type, "elem")
		if err != nil {
			return "", err
		}
		return "[]" + elemname, nil
	case reflect.Struct:
		return nameOfStructRuntimeType(mds, _type, kind, tflag)
	default:
		return nameOfNamedRuntimeType(mds, _type, kind, tflag)
	}
}

// Returns the expression describing an anonymous function type.
// A runtime.functype is followed by a runtime.uncommontype
// (optional) and then by an array of pointers to runtime._type,
// one for each input and output argument.
func nameOfFuncRuntimeType(mds []moduleData, _type *Variable, tflag int64, anonymous bool) (string, error) {
	rtyp, err := _type.bi.findType("runtime._type")
	if err != nil {
		return "", err
	}
	prtyp := pointerTo(rtyp, _type.bi.Arch)

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

	cursortyp := _type.newVariable("", _type.Addr+uint64(uadd), prtyp, _type.mem)
	var buf bytes.Buffer
	if anonymous {
		buf.WriteString("func(")
	} else {
		buf.WriteString("(")
	}

	for i := int64(0); i < inCount; i++ {
		argtype := cursortyp.maybeDereference()
		cursortyp.Addr += uint64(_type.bi.Arch.PtrSize())
		argtypename, _, err := nameOfRuntimeType(mds, argtype)
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
		argtypename, _, err := nameOfRuntimeType(mds, argtype)
		if err != nil {
			return "", err
		}
		buf.WriteString(argtypename)
	default:
		buf.WriteString(" (")
		for i := int64(0); i < outCount; i++ {
			argtype := cursortyp.maybeDereference()
			cursortyp.Addr += uint64(_type.bi.Arch.PtrSize())
			argtypename, _, err := nameOfRuntimeType(mds, argtype)
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

func nameOfInterfaceRuntimeType(mds []moduleData, _type *Variable, kind, tflag int64) (string, error) {
	var buf bytes.Buffer
	buf.WriteString("interface {")

	methods, _ := _type.structMember(interfacetypeFieldMhdr)
	methods.loadArrayValues(0, LoadConfig{false, 1, 0, 4096, -1, 0})
	if methods.Unreadable != nil {
		return "", nil
	}

	if len(methods.Children) == 0 {
		buf.WriteString("}")
		return buf.String(), nil
	}
	buf.WriteString(" ")

	for i, im := range methods.Children {
		var methodname, methodtype string
		for i := range im.Children {
			switch im.Children[i].Name {
			case imethodFieldName:
				nameoff, _ := constant.Int64Val(im.Children[i].Value)
				var err error
				methodname, _, _, err = resolveNameOff(_type.bi, mds, _type.Addr, uint64(nameoff), _type.mem)
				if err != nil {
					return "", err
				}

			case imethodFieldItyp:
				typeoff, _ := constant.Int64Val(im.Children[i].Value)
				typ, err := resolveTypeOff(_type.bi, mds, _type.Addr, uint64(typeoff), _type.mem)
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
				methodtype, err = nameOfFuncRuntimeType(mds, typ, tflag, false)
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

func nameOfStructRuntimeType(mds []moduleData, _type *Variable, kind, tflag int64) (string, error) {
	var buf bytes.Buffer
	buf.WriteString("struct {")

	fields, _ := _type.structMember("fields")
	fields.loadArrayValues(0, LoadConfig{false, 2, 0, 4096, -1, 0})
	if fields.Unreadable != nil {
		return "", fields.Unreadable
	}

	if len(fields.Children) == 0 {
		buf.WriteString("}")
		return buf.String(), nil
	}
	buf.WriteString(" ")

	for i, field := range fields.Children {
		var fieldname, fieldtypename string
		var typeField *Variable
		isembed := false
		for i := range field.Children {
			switch field.Children[i].Name {
			case "name":
				var nameoff int64
				switch field.Children[i].Kind {
				case reflect.Struct:
					nameoff = int64(field.Children[i].fieldVariable("bytes").Children[0].Addr)
				default:
					nameoff, _ = constant.Int64Val(field.Children[i].Value)
				}

				var err error
				fieldname, _, _, err = loadName(_type.bi, uint64(nameoff), _type.mem)
				if err != nil {
					return "", err
				}

			case "typ":
				typeField = field.Children[i].maybeDereference()
				var err error
				fieldtypename, _, err = nameOfRuntimeType(mds, typeField)
				if err != nil {
					return "", err
				}

			case "offsetAnon":
				// The offsetAnon field of runtime.structfield combines the offset of
				// the struct field from the base address of the struct with a flag
				// determining whether the field is anonymous (i.e. an embedded struct).
				//
				//  offsetAnon = (offset<<1) | (anonFlag)
				//
				// Here we are only interested in the anonymous flag.
				offsetAnon, _ := constant.Int64Val(field.Children[i].Value)
				isembed = offsetAnon%2 != 0
			}
		}

		// fieldname will be the empty string for anonymous fields
		if fieldname != "" && !isembed {
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

func fieldToType(mds []moduleData, _type *Variable, fieldName string) (string, error) {
	typeField, err := _type.structMember(fieldName)
	if err != nil {
		return "", err
	}
	typeField = typeField.maybeDereference()
	typename, _, err := nameOfRuntimeType(mds, typeField)
	return typename, err
}

func specificRuntimeType(_type *Variable, kind int64) (*Variable, error) {
	typ, err := typeForKind(kind, _type.bi)
	if err != nil {
		return nil, err
	}
	if typ == nil {
		return _type, nil
	}

	return _type.newVariable(_type.Name, _type.Addr, typ, _type.mem), nil
}

// See reflect.(*rtype).uncommon in $GOROOT/src/reflect/type.go
func uncommon(_type *Variable, tflag int64) *Variable {
	if tflag&tflagUncommon == 0 {
		return nil
	}

	typ, err := _type.bi.findType("runtime.uncommontype")
	if err != nil {
		return nil
	}

	return _type.newVariable(_type.Name, _type.Addr+uint64(_type.RealType.Size()), typ, _type.mem)
}

var kindToRuntimeTypeName = map[reflect.Kind]string{
	reflect.Array:     "runtime.arraytype",
	reflect.Chan:      "runtime.chantype",
	reflect.Func:      "runtime.functype",
	reflect.Interface: "runtime.interfacetype",
	reflect.Map:       "runtime.maptype",
	reflect.Ptr:       "runtime.ptrtype",
	reflect.Slice:     "runtime.slicetype",
	reflect.Struct:    "runtime.structtype",
}

// typeForKind returns a *dwarf.StructType describing the specialization of
// runtime._type for the specified type kind. For example if kind is
// reflect.ArrayType it will return runtime.arraytype
func typeForKind(kind int64, bi *BinaryInfo) (*godwarf.StructType, error) {
	typename, ok := kindToRuntimeTypeName[reflect.Kind(kind&kindMask)]
	if !ok {
		return nil, nil
	}
	typ, err := bi.findType(typename)
	if err != nil {
		return nil, err
	}
	typ = resolveTypedef(typ)
	return typ.(*godwarf.StructType), nil
}

func dwarfToRuntimeType(bi *BinaryInfo, mem MemoryReadWriter, typ godwarf.Type) (typeAddr uint64, typeKind uint64, found bool, err error) {
	so := bi.typeToImage(typ)
	rdr := so.DwarfReader()
	rdr.Seek(typ.Common().Offset)
	e, err := rdr.Next()
	if err != nil {
		return 0, 0, false, err
	}
	off, ok := e.Val(godwarf.AttrGoRuntimeType).(uint64)
	if !ok {
		return 0, 0, false, nil
	}

	mds, err := loadModuleData(bi, mem)
	if err != nil {
		return 0, 0, false, err
	}

	md := bi.imageToModuleData(so, mds)
	if md == nil {
		if so.index > 0 {
			return 0, 0, false, fmt.Errorf("could not find module data for type %s (shared object: %q)", typ, so.Path)
		} else {
			return 0, 0, false, fmt.Errorf("could not find module data for type %s", typ)
		}
	}

	typeAddr = uint64(md.types) + off

	rtyp, err := bi.findType("runtime._type")
	if err != nil {
		return 0, 0, false, err
	}
	_type := newVariable("", typeAddr, rtyp, bi, mem)
	kindv := _type.loadFieldNamed("kind")
	if kindv.Unreadable != nil || kindv.Kind != reflect.Uint {
		return 0, 0, false, fmt.Errorf("unreadable interface type: %v", kindv.Unreadable)
	}
	typeKind, _ = constant.Uint64Val(kindv.Value)
	return typeAddr, typeKind, true, nil
}
