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
	"go/token"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"github.com/derekparker/delve/pkg/dwarf/godwarf"
	"github.com/derekparker/delve/pkg/dwarf/line"
	"github.com/derekparker/delve/pkg/dwarf/op"
	"github.com/derekparker/delve/pkg/dwarf/reader"
	"github.com/derekparker/delve/pkg/goversion"
	"github.com/derekparker/delve/pkg/logflags"
	"github.com/sirupsen/logrus"
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

// Do not call this function directly it isn't able to deal correctly with package paths
func (bi *BinaryInfo) findType(name string) (godwarf.Type, error) {
	off, found := bi.types[name]
	if !found {
		return nil, reader.TypeNotFoundErr
	}
	return godwarf.ReadType(bi.dwarf, off, bi.typeCache)
}

func pointerTo(typ godwarf.Type, arch Arch) godwarf.Type {
	return &godwarf.PtrType{godwarf.CommonType{int64(arch.PtrSize()), "*" + typ.Common().Name, reflect.Ptr, 0}, typ}
}

func (bi *BinaryInfo) findTypeExpr(expr ast.Expr) (godwarf.Type, error) {
	bi.loadPackageMap()
	if lit, islit := expr.(*ast.BasicLit); islit && lit.Kind == token.STRING {
		// Allow users to specify type names verbatim as quoted
		// string. Useful as a catch-all workaround for cases where we don't
		// parse/serialize types correctly or can not resolve package paths.
		typn, _ := strconv.Unquote(lit.Value)
		return bi.findType(typn)
	}
	bi.expandPackagesInType(expr)
	if snode, ok := expr.(*ast.StarExpr); ok {
		// Pointer types only appear in the dwarf informations when
		// a pointer to the type is used in the target program, here
		// we create a pointer type on the fly so that the user can
		// specify a pointer to any variable used in the target program
		ptyp, err := bi.findTypeExpr(snode.X)
		if err != nil {
			return nil, err
		}
		return pointerTo(ptyp, bi.Arch), nil
	}
	if anode, ok := expr.(*ast.ArrayType); ok {
		// Byte array types (i.e. [N]byte) are only present in DWARF if they are
		// used by the program, but it's convenient to make all of them available
		// to the user so that they can be used to read arbitrary memory, byte by
		// byte.

		alen, litlen := anode.Len.(*ast.BasicLit)
		if litlen && alen.Kind == token.INT {
			n, _ := strconv.Atoi(alen.Value)
			switch exprToString(anode.Elt) {
			case "byte", "uint8":
				btyp, err := bi.findType("uint8")
				if err != nil {
					return nil, err
				}
				return &godwarf.ArrayType{
					CommonType: godwarf.CommonType{
						ReflectKind: reflect.Array,
						ByteSize:    int64(n),
						Name:        fmt.Sprintf("[%d]uint8", n)},
					Type:          btyp,
					StrideBitSize: 8,
					Count:         int64(n)}, nil
			}
		}
	}
	return bi.findType(exprToString(expr))
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

func (bi *BinaryInfo) loadPackageMap() error {
	if bi.packageMap != nil {
		return nil
	}
	bi.packageMap = map[string]string{}
	reader := bi.DwarfReader()
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
		bi.packageMap[name] = path
	}
	return nil
}

type functionsDebugInfoByEntry []Function

func (v functionsDebugInfoByEntry) Len() int           { return len(v) }
func (v functionsDebugInfoByEntry) Less(i, j int) bool { return v[i].Entry < v[j].Entry }
func (v functionsDebugInfoByEntry) Swap(i, j int)      { v[i], v[j] = v[j], v[i] }

type compileUnitsByLowpc []*compileUnit

func (v compileUnitsByLowpc) Len() int               { return len(v) }
func (v compileUnitsByLowpc) Less(i int, j int) bool { return v[i].LowPC < v[j].LowPC }
func (v compileUnitsByLowpc) Swap(i int, j int)      { v[i], v[j] = v[j], v[i] }

type packageVarsByAddr []packageVar

func (v packageVarsByAddr) Len() int               { return len(v) }
func (v packageVarsByAddr) Less(i int, j int) bool { return v[i].addr < v[j].addr }
func (v packageVarsByAddr) Swap(i int, j int)      { v[i], v[j] = v[j], v[i] }

func (bi *BinaryInfo) loadDebugInfoMaps(debugLineBytes []byte, wg *sync.WaitGroup, cont func()) {
	if wg != nil {
		defer wg.Done()
	}
	bi.types = make(map[string]dwarf.Offset)
	bi.packageVars = []packageVar{}
	bi.Functions = []Function{}
	bi.compileUnits = []*compileUnit{}
	bi.consts = make(map[dwarf.Offset]*constantType)
	bi.runtimeTypeToDIE = make(map[uint64]runtimeTypeDIE)
	reader := bi.DwarfReader()
	var cu *compileUnit = nil
	var pu *partialUnit = nil
	var partialUnits = make(map[dwarf.Offset]*partialUnit)
	for entry, err := reader.Next(); entry != nil; entry, err = reader.Next() {
		if err != nil {
			break
		}
		switch entry.Tag {
		case dwarf.TagCompileUnit:
			if pu != nil {
				partialUnits[pu.entry.Offset] = pu
				pu = nil
			}
			cu = &compileUnit{}
			cu.entry = entry
			if lang, _ := entry.Val(dwarf.AttrLanguage).(int64); lang == dwarfGoLanguage {
				cu.isgo = true
			}
			cu.Name, _ = entry.Val(dwarf.AttrName).(string)
			compdir, _ := entry.Val(dwarf.AttrCompDir).(string)
			if compdir != "" {
				cu.Name = filepath.Join(compdir, cu.Name)
			}
			if ranges, _ := bi.dwarf.Ranges(entry); len(ranges) == 1 {
				cu.LowPC = ranges[0][0]
				cu.HighPC = ranges[0][1]
			}
			lineInfoOffset, _ := entry.Val(dwarf.AttrStmtList).(int64)
			if lineInfoOffset >= 0 && lineInfoOffset < int64(len(debugLineBytes)) {
				var logfn func(string, ...interface{})
				if logflags.DebugLineErrors() {
					logger := logrus.New().WithFields(logrus.Fields{"layer": "dwarf-line"})
					logger.Level = logrus.DebugLevel
					logfn = func(fmt string, args ...interface{}) {
						logger.Printf(fmt, args)
					}
				}
				cu.lineInfo = line.Parse(compdir, bytes.NewBuffer(debugLineBytes[lineInfoOffset:]), logfn)
			}
			cu.producer, _ = entry.Val(dwarf.AttrProducer).(string)
			if cu.isgo && cu.producer != "" {
				semicolon := strings.Index(cu.producer, ";")
				if semicolon < 0 {
					cu.optimized = goversion.ProducerAfterOrEqual(cu.producer, 1, 10)
				} else {
					cu.optimized = !strings.Contains(cu.producer[semicolon:], "-N") || !strings.Contains(cu.producer[semicolon:], "-l")
					cu.producer = cu.producer[:semicolon]
				}
			}
			bi.compileUnits = append(bi.compileUnits, cu)

		case dwarf.TagPartialUnit:
			if pu != nil {
				partialUnits[pu.entry.Offset] = pu
				pu = nil
			}
			pu = &partialUnit{}
			pu.entry = entry
			pu.types = make(map[string]dwarf.Offset)

		case dwarf.TagImportedUnit:
			if unit, exists := partialUnits[entry.Val(dwarf.AttrImport).(dwarf.Offset)]; exists {
				for name, offset := range unit.types {
					if pu != nil {
						pu.types[name] = offset
					} else {
						if !cu.isgo {
							name = "C." + name
						}
						bi.types[name] = offset
					}
				}

				for _, pVar := range unit.variables {
					if pu != nil {
						pu.variables = append(pu.variables, pVar)
					} else {
						if !cu.isgo {
							pVar.name = "C." + pVar.name
						}
						bi.packageVars = append(bi.packageVars, pVar)
					}
				}

				for _, pCt := range unit.constants {
					if pu != nil {
						pu.constants = append(pu.constants, pCt)
					} else {
						if !cu.isgo {
							pCt.name = "C." + pCt.name
						}
						ct := bi.consts[pCt.typ]
						if ct == nil {
							ct = &constantType{}
							bi.consts[pCt.typ] = ct
						}
						ct.values = append(ct.values, constantValue{name: pCt.name, fullName: pCt.name, value: pCt.value})
					}
				}

				for _, pFunc := range unit.functions {
					if pu != nil {
						pu.functions = append(pu.functions, pFunc)
					} else {
						if !cu.isgo {
							pFunc.Name = "C." + pFunc.Name
						}
						pFunc.cu = cu
						bi.Functions = append(bi.Functions, pFunc)
					}
				}
			}

		case dwarf.TagArrayType, dwarf.TagBaseType, dwarf.TagClassType, dwarf.TagStructType, dwarf.TagUnionType, dwarf.TagConstType, dwarf.TagVolatileType, dwarf.TagRestrictType, dwarf.TagEnumerationType, dwarf.TagPointerType, dwarf.TagSubroutineType, dwarf.TagTypedef, dwarf.TagUnspecifiedType:
			if name, ok := entry.Val(dwarf.AttrName).(string); ok {
				if pu != nil {
					if _, exists := pu.types[name]; !exists {
						pu.types[name] = entry.Offset
					}
				} else {
					if !cu.isgo {
						name = "C." + name
					}
					if _, exists := bi.types[name]; !exists {
						bi.types[name] = entry.Offset
					}
				}
			}
			if off, ok := entry.Val(godwarf.AttrGoRuntimeType).(uint64); ok {
				bi.runtimeTypeToDIE[off] = runtimeTypeDIE{entry.Offset, -1}
			}
			reader.SkipChildren()

		case dwarf.TagVariable:
			if n, ok := entry.Val(dwarf.AttrName).(string); ok {
				var addr uint64
				if loc, ok := entry.Val(dwarf.AttrLocation).([]byte); ok {
					if len(loc) == bi.Arch.PtrSize()+1 && op.Opcode(loc[0]) == op.DW_OP_addr {
						addr = binary.LittleEndian.Uint64(loc[1:])
					}
				}
				if pu != nil {
					pu.variables = append(pu.variables, packageVar{n, entry.Offset, addr})
				} else {
					if !cu.isgo {
						n = "C." + n
					}
					bi.packageVars = append(bi.packageVars, packageVar{n, entry.Offset, addr})
				}
			}

		case dwarf.TagConstant:
			name, okName := entry.Val(dwarf.AttrName).(string)
			typ, okType := entry.Val(dwarf.AttrType).(dwarf.Offset)
			val, okVal := entry.Val(dwarf.AttrConstValue).(int64)
			if okName && okType && okVal {
				if pu != nil {
					pu.constants = append(pu.constants, partialUnitConstant{name: name, typ: typ, value: val})
				} else {
					if !cu.isgo {
						name = "C." + name
					}
					ct := bi.consts[typ]
					if ct == nil {
						ct = &constantType{}
						bi.consts[typ] = ct
					}
					ct.values = append(ct.values, constantValue{name: name, fullName: name, value: val})
				}
			}

		case dwarf.TagSubprogram:
			ok1 := false
			var lowpc, highpc uint64
			if ranges, _ := bi.dwarf.Ranges(entry); len(ranges) == 1 {
				ok1 = true
				lowpc = ranges[0][0]
				highpc = ranges[0][1]
			}
			name, ok2 := entry.Val(dwarf.AttrName).(string)
			if ok1 && ok2 {
				if pu != nil {
					pu.functions = append(pu.functions, Function{
						Name:  name,
						Entry: lowpc, End: highpc,
						offset: entry.Offset,
						cu:     &compileUnit{},
					})
				} else {
					if !cu.isgo {
						name = "C." + name
					}
					bi.Functions = append(bi.Functions, Function{
						Name:  name,
						Entry: lowpc, End: highpc,
						offset: entry.Offset,
						cu:     cu,
					})
				}
			}
			reader.SkipChildren()

		}
	}
	sort.Sort(compileUnitsByLowpc(bi.compileUnits))
	sort.Sort(functionsDebugInfoByEntry(bi.Functions))
	sort.Sort(packageVarsByAddr(bi.packageVars))

	bi.LookupFunc = make(map[string]*Function)
	for i := range bi.Functions {
		bi.LookupFunc[bi.Functions[i].Name] = &bi.Functions[i]
	}

	bi.Sources = []string{}
	for _, cu := range bi.compileUnits {
		if cu.lineInfo != nil {
			for _, fileEntry := range cu.lineInfo.FileNames {
				bi.Sources = append(bi.Sources, fileEntry.Path)
			}
		}
	}
	sort.Strings(bi.Sources)
	bi.Sources = uniq(bi.Sources)

	if cont != nil {
		cont()
	}
}

func uniq(s []string) []string {
	if len(s) <= 0 {
		return s
	}
	src, dst := 1, 1
	for src < len(s) {
		if s[src] != s[dst-1] {
			s[dst] = s[src]
			dst++
		}
		src++
	}
	return s[:dst]
}

func (bi *BinaryInfo) expandPackagesInType(expr ast.Expr) {
	switch e := expr.(type) {
	case *ast.ArrayType:
		bi.expandPackagesInType(e.Elt)
	case *ast.ChanType:
		bi.expandPackagesInType(e.Value)
	case *ast.FuncType:
		for i := range e.Params.List {
			bi.expandPackagesInType(e.Params.List[i].Type)
		}
		if e.Results != nil {
			for i := range e.Results.List {
				bi.expandPackagesInType(e.Results.List[i].Type)
			}
		}
	case *ast.MapType:
		bi.expandPackagesInType(e.Key)
		bi.expandPackagesInType(e.Value)
	case *ast.ParenExpr:
		bi.expandPackagesInType(e.X)
	case *ast.SelectorExpr:
		switch x := e.X.(type) {
		case *ast.Ident:
			if path, ok := bi.packageMap[x.Name]; ok {
				x.Name = path
			}
		default:
			bi.expandPackagesInType(e.X)
		}
	case *ast.StarExpr:
		bi.expandPackagesInType(e.X)
	default:
		// nothing to do
	}
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
func runtimeTypeToDIE(_type *Variable, dataAddr uintptr) (typ godwarf.Type, kind int64, err error) {
	var go17 bool
	var typestring *Variable

	bi := _type.bi

	// Determine if we are in go1.7 or later
	typestring, err = _type.structMember("_string")
	if err == nil {
		typestring = typestring.maybeDereference()
	} else {
		err = nil
		go17 = true
	}

	if !go17 {
		// pre-go1.7 compatibility
		if typestring == nil || typestring.Addr == 0 || typestring.Kind != reflect.String {
			return nil, 0, fmt.Errorf("invalid interface type")
		}
		typestring.loadValue(LoadConfig{false, 0, 512, 0, 0})
		if typestring.Unreadable != nil {
			return nil, 0, fmt.Errorf("invalid interface type: %v", typestring.Unreadable)
		}

		typename := constant.StringVal(typestring.Value)

		t, err := parser.ParseExpr(typename)
		if err != nil {
			return nil, 0, fmt.Errorf("invalid interface type, unparsable data type: %v", err)
		}

		typ, err := bi.findTypeExpr(t)
		if err != nil {
			return nil, 0, fmt.Errorf("interface type %q not found for %#x: %v", typename, dataAddr, err)
		}

		return typ, 0, nil
	}

	_type = _type.maybeDereference()

	// go 1.11 implementation: use extended attribute in debug_info

	md, err := findModuleDataForType(bi, _type.Addr, _type.mem)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading module data: %v", err)
	}
	if md != nil {
		if rtdie, ok := bi.runtimeTypeToDIE[uint64(_type.Addr-md.types)]; ok {
			typ, err := godwarf.ReadType(bi.dwarf, rtdie.offset, bi.typeCache)
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

	typename, kind, err := nameOfRuntimeType(_type)
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
func nameOfRuntimeType(_type *Variable) (typename string, kind int64, err error) {
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
		typename, err = nameOfNamedRuntimeType(_type, kind, tflag)
		return typename, kind, err
	} else {
		typename, err = nameOfUnnamedRuntimeType(_type, kind, tflag)
		return typename, kind, err
	}

	_type.bi.nameOfRuntimeType[_type.Addr] = nameOfRuntimeTypeEntry{typename, kind}

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

	typename, _, _, err = resolveNameOff(_type.bi, _type.Addr, uintptr(strOff), _type.mem)
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
			pkgPath, _, _, err := resolveNameOff(_type.bi, _type.Addr, uintptr(pkgPathOff), _type.mem)
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

	cursortyp := _type.newVariable("", _type.Addr+uintptr(uadd), prtyp, _type.mem)
	var buf bytes.Buffer
	if anonymous {
		buf.WriteString("func(")
	} else {
		buf.WriteString("(")
	}

	for i := int64(0); i < inCount; i++ {
		argtype := cursortyp.maybeDereference()
		cursortyp.Addr += uintptr(_type.bi.Arch.PtrSize())
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
			cursortyp.Addr += uintptr(_type.bi.Arch.PtrSize())
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

	methods, _ := _type.structMember(interfacetypeFieldMhdr)
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
			case imethodFieldName:
				nameoff, _ := constant.Int64Val(im.Children[i].Value)
				var err error
				methodname, _, _, err = resolveNameOff(_type.bi, _type.Addr, uintptr(nameoff), _type.mem)
				if err != nil {
					return "", err
				}

			case imethodFieldItyp:
				typeoff, _ := constant.Int64Val(im.Children[i].Value)
				typ, err := resolveTypeOff(_type.bi, _type.Addr, uintptr(typeoff), _type.mem)
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
	fields.loadArrayValues(0, LoadConfig{false, 2, 0, 4096, -1})
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
				fieldname, _, _, err = loadName(_type.bi, uintptr(nameoff), _type.mem)
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

	return _type.newVariable(_type.Name, _type.Addr+uintptr(_type.RealType.Size()), typ, _type.mem)
}

// typeForKind returns a *dwarf.StructType describing the specialization of
// runtime._type for the specified type kind. For example if kind is
// reflect.ArrayType it will return runtime.arraytype
func typeForKind(kind int64, bi *BinaryInfo) (*godwarf.StructType, error) {
	var typ godwarf.Type
	switch reflect.Kind(kind & kindMask) {
	case reflect.Array:
		typ, _ = bi.findType("runtime.arraytype")
	case reflect.Chan:
		//
		typ, _ = bi.findType("runtime.chantype")
	case reflect.Func:
		typ, _ = bi.findType("runtime.functype")
	case reflect.Interface:
		typ, _ = bi.findType("runtime.interfacetype")
	case reflect.Map:
		typ, _ = bi.findType("runtime.maptype")
	case reflect.Ptr:
		typ, _ = bi.findType("runtime.ptrtype")
	case reflect.Slice:
		typ, _ = bi.findType("runtime.slicetype")
	case reflect.Struct:
		typ, _ = bi.findType("runtime.structtype")
	default:
		return nil, nil
	}
	if typ != nil {
		typ = resolveTypedef(typ)
		return typ.(*godwarf.StructType), nil
	}
	return constructTypeForKind(kind, bi)
}

// constructTypeForKind synthesizes a *dwarf.StructType for the specified kind.
// This is necessary because on go1.8 and previous the specialized types of
// runtime._type were not exported.
func constructTypeForKind(kind int64, bi *BinaryInfo) (*godwarf.StructType, error) {
	rtyp, err := bi.findType("runtime._type")
	if err != nil {
		return nil, err
	}
	prtyp := pointerTo(rtyp, bi.Arch)

	uintptrtyp, err := bi.findType("uintptr")
	if err != nil {
		return nil, err
	}

	uint32typ := &godwarf.UintType{godwarf.BasicType{CommonType: godwarf.CommonType{ByteSize: 4, Name: "uint32"}}}
	uint16typ := &godwarf.UintType{godwarf.BasicType{CommonType: godwarf.CommonType{ByteSize: 2, Name: "uint16"}}}

	newStructType := func(name string, sz uintptr) *godwarf.StructType {
		return &godwarf.StructType{godwarf.CommonType{Name: name, ByteSize: int64(sz)}, name, "struct", nil, false}
	}

	appendField := func(typ *godwarf.StructType, name string, fieldtype godwarf.Type, off uintptr) {
		typ.Field = append(typ.Field, &godwarf.StructField{Name: name, ByteOffset: int64(off), Type: fieldtype})
	}

	newSliceType := func(elemtype godwarf.Type) *godwarf.SliceType {
		r := newStructType("[]"+elemtype.Common().Name, uintptr(3*uintptrtyp.Size()))
		appendField(r, "array", pointerTo(elemtype, bi.Arch), 0)
		appendField(r, "len", uintptrtyp, uintptr(uintptrtyp.Size()))
		appendField(r, "cap", uintptrtyp, uintptr(2*uintptrtyp.Size()))
		return &godwarf.SliceType{StructType: *r, ElemType: elemtype}
	}

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
		typ := newStructType("runtime.arraytype", unsafe.Sizeof(a))
		appendField(typ, "elem", prtyp, unsafe.Offsetof(a.elem))
		appendField(typ, "len", uintptrtyp, unsafe.Offsetof(a.len))
		return typ, nil
	case reflect.Chan:
		// runtime.chantype
		var a struct {
			rtype
			elem *rtype  // channel element type
			dir  uintptr // channel direction (ChanDir)
		}
		typ := newStructType("runtime.chantype", unsafe.Sizeof(a))
		appendField(typ, "elem", prtyp, unsafe.Offsetof(a.elem))
		return typ, nil
	case reflect.Func:
		// runtime.functype
		var a struct {
			rtype    `reflect:"func"`
			inCount  uint16
			outCount uint16 // top bit is set if last input parameter is ...
		}
		typ := newStructType("runtime.functype", unsafe.Sizeof(a))
		appendField(typ, "inCount", uint16typ, unsafe.Offsetof(a.inCount))
		appendField(typ, "outCount", uint16typ, unsafe.Offsetof(a.outCount))
		return typ, nil
	case reflect.Interface:
		// runtime.imethod
		type imethod struct {
			name uint32 // name of method
			ityp uint32 // .(*FuncType) underneath
		}

		var im imethod

		// runtime.interfacetype
		var a struct {
			rtype   `reflect:"interface"`
			pkgPath *byte     // import path
			mhdr    []imethod // sorted by hash
		}

		imethodtype := newStructType("runtime.imethod", unsafe.Sizeof(im))
		appendField(imethodtype, imethodFieldName, uint32typ, unsafe.Offsetof(im.name))
		appendField(imethodtype, imethodFieldItyp, uint32typ, unsafe.Offsetof(im.ityp))
		typ := newStructType("runtime.interfacetype", unsafe.Sizeof(a))
		appendField(typ, interfacetypeFieldMhdr, newSliceType(imethodtype), unsafe.Offsetof(a.mhdr))
		return typ, nil
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
		typ := newStructType("runtime.maptype", unsafe.Sizeof(a))
		appendField(typ, "key", prtyp, unsafe.Offsetof(a.key))
		appendField(typ, "elem", prtyp, unsafe.Offsetof(a.elem))
		return typ, nil
	case reflect.Ptr:
		// runtime.ptrtype
		var a struct {
			rtype `reflect:"ptr"`
			elem  *rtype // pointer element (pointed at) type
		}
		typ := newStructType("runtime.ptrtype", unsafe.Sizeof(a))
		appendField(typ, "elem", prtyp, unsafe.Offsetof(a.elem))
		return typ, nil
	case reflect.Slice:
		// runtime.slicetype
		var a struct {
			rtype `reflect:"slice"`
			elem  *rtype // slice element type
		}

		typ := newStructType("runtime.slicetype", unsafe.Sizeof(a))
		appendField(typ, "elem", prtyp, unsafe.Offsetof(a.elem))
		return typ, nil
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
		typ := newStructType("runtime.structtype", unsafe.Sizeof(a))
		appendField(typ, "fields", newSliceType(fieldtype), unsafe.Offsetof(a.fields))
		return typ, nil
	default:
		return nil, nil
	}
}
