// This file is part of GoRE.
//
// Copyright (C) 2019-2021 GoRE Authors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <http://www.gnu.org/licenses/>.

package gore

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"reflect"
)

const (
	intSize32            = 4
	intSize64            = intSize32 * 2
	kindMask             = (1 << 5) - 1
	tflagExtraStar uint8 = 1 << 1
	tflagUncommon  uint8 = 1 << 0
)

type _typeField uint8

const (
	_typeFieldSize = iota
	_typeFieldKind
	_typeFieldStr
	_typeFieldFlag
	_typeFieldEnd
)

// ChanDir is a channel direction.
type ChanDir int

const (
	// ChanRecv is a receive only chan (<-chan)
	ChanRecv ChanDir = 1 << iota
	// ChanSend is a send only chan (chan<-)
	ChanSend
	// ChanBoth is a send and receive chan (chan)
	ChanBoth = ChanRecv | ChanSend
)

func getTypes(fileInfo *FileInfo, f fileHandler) (map[uint64]*GoType, error) {
	if GoVersionCompare(fileInfo.goversion.Name, "go1.7beta1") < 0 {
		return getLegacyTypes(fileInfo, f)
	}

	md, err := parseModuledata(fileInfo, f)
	if err != nil {
		return nil, fmt.Errorf("failed to parse the module data: %w", err)
	}

	types, err := md.Types().Data()
	if err != nil {
		return nil, fmt.Errorf("failed to get types data section: %w", err)
	}

	typeLink, err := md.TypeLink()
	if err != nil {
		return nil, fmt.Errorf("failed to get type link data: %w", err)
	}

	// New parser
	parser := newTypeParser(types, md.Types().Address, fileInfo)
	for _, off := range typeLink {
		typ, err := parser.parseType(uint64(off) + parser.base)
		if err != nil || typ == nil {
			return nil, fmt.Errorf("failed to parse type at offset 0x%x: %w", off, err)
		}
	}
	return parser.parsedTypes(), nil
}

func getLegacyTypes(fileInfo *FileInfo, f fileHandler) (map[uint64]*GoType, error) {
	md, err := parseModuledata(fileInfo, f)
	if err != nil {
		return nil, err
	}
	typelinkAddr, typelinkData, err := f.getSectionDataFromOffset(md.TypelinkAddr)
	if err != nil {
		return nil, fmt.Errorf("no typelink section found: %w", err)
	}
	r := bytes.NewReader(typelinkData)
	_, err = r.Seek(int64(md.TypelinkAddr)-int64(typelinkAddr), io.SeekStart)
	if err != nil {
		return nil, err
	}

	goTypes := make(map[uint64]*GoType)
	for i := uint64(0); i < md.TypelinkLen; i++ {
		// Type offsets are always *_type
		off, err := readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
		if err != nil {
			return nil, err
		}
		baseAddr, baseData, err := f.getSectionDataFromOffset(off)
		if err != nil {
			continue
		}
		typ := typeParse(goTypes, fileInfo, off-baseAddr, baseData, baseAddr)
		if typ == nil {
			continue
		}
	}
	return goTypes, nil
}

// GoType is a representation of all types in Go.
type GoType struct {
	// Kind indicates the specific kind of type the GoType
	Kind reflect.Kind
	// Name is the name of the type.
	Name string
	// Addr is the virtual address to where the type struct is defined.
	Addr uint64
	// PtrResolvAddr is the address to where the resolved structure is located
	// if the GoType is of pointer kind.
	PtrResolvAddr uint64
	// PackagePath is the name of the package import path for the GoType.
	PackagePath string
	// Fields is a slice of the struct fields if the GoType is of kind struct.
	Fields []*GoType
	// FieldName is the name of the field if the GoType is a struct field.
	FieldName string
	// FieldTag holds the extracted tag for the field.
	FieldTag string
	// FieldAnon is true if the field does not have a name and is an embedded type.
	FieldAnon bool
	// Element is the element type for arrays, slices channels or the resolved type for
	// a pointer type. For example int if the slice is a []int.
	Element *GoType
	// Length is the array or slice length.
	Length int
	// ChanDir is the channel direction
	ChanDir ChanDir
	// Key is the key type for a map.
	Key *GoType
	// FuncArgs holds the argument types for the function if the type is a function kind.
	FuncArgs []*GoType
	// FuncReturnVals holds the return types for the function if the type is a function kind.
	FuncReturnVals []*GoType
	// IsVariadic is true if the last argument type is variadic. For example "func(s string, n ...int)"
	IsVariadic bool
	// Methods holds information of the types methods.
	Methods []*TypeMethod
	flag    uint8
}

// String implements the fmt.Stringer interface.
func (t *GoType) String() string {
	switch t.Kind {
	case reflect.Slice:
		return fmt.Sprintf("[]%s", t.Element)
	case reflect.Array:
		return fmt.Sprintf("[%d]%s", t.Length, t.Element)
	case reflect.Map:
		return fmt.Sprintf("map[%s]%s", t.Key, t.Element)
	case reflect.Struct:
		// Handle empty struct type
		if t.Name == "" {
			return "struct{}"
		}
		return t.Name
	case reflect.Ptr:
		return fmt.Sprintf("*%s", t.Element)
	case reflect.Chan:
		if t.ChanDir == ChanRecv {
			return fmt.Sprintf("<-chan %s", t.Element)
		}
		if t.ChanDir == ChanSend {
			return fmt.Sprintf("chan<- %s", t.Element)
		}
		return fmt.Sprintf("chan %s", t.Element)
	case reflect.Func:
		buf := "func("
		for i, a := range t.FuncArgs {
			if i != 0 {
				buf += ", "
			}
			if a.Kind == reflect.Func && a.Name == t.Name {
				buf += a.Name
			} else {
				buf += a.String()
			}
		}
		if len(t.FuncReturnVals) > 1 {
			buf += ") ("
		} else if len(t.FuncReturnVals) == 1 {
			buf += ") "
		} else {
			buf += ")"
		}
		for i, r := range t.FuncReturnVals {
			if i != 0 {
				buf += ", "
			}
			if r.Kind == reflect.Func && r.Name == t.Name {
				buf += r.Name
			} else {
				buf += r.String()
			}
		}
		if len(t.FuncReturnVals) > 1 {
			buf += ")"
		}
		return buf
	case reflect.Interface:
		// Handle empty interface
		if t.Name == "" {
			return "interface{}"
		}
		return t.Name
	case reflect.Invalid:
		return t.Name
	default:
		return t.Kind.String()
	}
}

// StructDef reconstructs the type definition code for the struct.
// If the type is not a struct, an empty string is returned.
func StructDef(typ *GoType) string {
	if typ.Kind != reflect.Struct {
		return ""
	}
	buf := fmt.Sprintf("type %s struct{", typ.Name)
	for _, f := range typ.Fields {
		if f.FieldAnon {
			buf += fmt.Sprintf("\n\t%s", f)
		} else {
			buf += fmt.Sprintf("\n\t%s %s", f.FieldName, f)
		}
		if f.FieldTag != "" {
			buf += "\t`" + f.FieldTag + "`"
		}
	}
	if len(typ.Fields) > 0 {
		buf += "\n"
	}
	return buf + "}"
}

// InterfaceDef reconstructs the type definition code for the interface.
// If the type is not an interface, an empty string is returned.
func InterfaceDef(typ *GoType) string {
	if typ.Kind != reflect.Interface {
		return ""
	}
	// Handle interface with no methods defined.
	if len(typ.Methods) == 0 {
		return "type " + typ.Name + " interface{}"
	}
	// Remove package from name.
	buf := fmt.Sprintf("type %s interface {", typ.Name)
	for _, m := range typ.Methods {
		buf += fmt.Sprintf("\n\t%s%s", m.Name, m.Type.String()[4:])
	}
	return buf + "\n}"
}

// MethodDef constructs a string summary of all methods for the type.
// If type information exists for the methods, it is used to determine function parameters.
// If the type does not have any methods, an empty string is returned.
func MethodDef(typ *GoType) string {
	if len(typ.Methods) == 0 {
		return ""
	}
	var buf string
	for i, m := range typ.Methods {
		if i > 0 {
			buf += "\n"
		}
		if m.Type != nil {
			buf += fmt.Sprintf("func (%s) %s%s", typ.Name, m.Name, m.Type.String()[4:])
		} else {
			buf += fmt.Sprintf("func (%s) %s()", typ.Name, m.Name)
		}
	}
	return buf
}

// TypeMethod is description of a method owned by the GoType.
type TypeMethod struct {
	// Name is the string name for the method.
	Name string
	// Type is the specific function type for the method.
	// This can be nil. If it is nil, the method is not part of an
	// implementation of a interface or it is not exported.
	Type *GoType
	// IfaceCallOffset is the offset from the beginning of the .text section
	// where the function code starts. According to code comments in the
	// standard library, it is used for interface calls.
	// Can be 0 if the code is not called in the binary and was optimized out
	// by the compiler or linker.
	IfaceCallOffset uint64
	// FuncCallOffset is the offset from the beginning of the .text section
	// where the function code starts. According to code comments in the
	// standard library, it is used for normal method calls.
	// Can be 0 if the code is not called in the binary and was optimized out
	// by the compiler or linker.
	FuncCallOffset uint64
}

/*
Size: 32 or 48
type _type struct {
	size       uintptr 4 or 8
	ptrdata    uintptr 4 or 8
	hash       uint32 4
	tflag      tflag 1
	align      uint8 1
	fieldalign uint8 1
	kind       uint8 1
	alg        *typeAlg 4 or 8
	gcdata    *byte 4 or 8
	str       nameOff 4
	ptrToThis typeOff 4
}
*/

func typeParse(types map[uint64]*GoType, fileInfo *FileInfo, offset uint64, sectionData []byte, sectionBaseAddr uint64) *GoType {
	typ, ok := types[offset+sectionBaseAddr]
	if ok {
		return typ
	}
	typ = new(GoType)
	// XXX: This is to catch bad parsing. The current parser does not handle
	// uncommon functions correctly. This ensures an out of bounds read does
	// not occur.
	if offset > uint64(len(sectionData)) {
		return nil
	}
	r := bytes.NewReader(sectionData[offset:])

	// Parse size
	off := typeOffset(fileInfo, _typeFieldSize)
	r.Seek(off, io.SeekStart)
	_, err := readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
	if err != nil {
		return nil
	}

	// Parse kind
	off = typeOffset(fileInfo, _typeFieldKind)
	// Defined as uint8
	var k uint8
	r.Seek(off, io.SeekStart)
	binary.Read(r, fileInfo.ByteOrder, &k)
	typ.Kind = reflect.Kind(k & kindMask)

	// Parse flag
	off = typeOffset(fileInfo, _typeFieldFlag)
	// Defined as uint8
	var f uint8
	r.Seek(off, io.SeekStart)
	binary.Read(r, fileInfo.ByteOrder, &f)
	typ.flag = f

	// Parse nameOff
	off = typeOffset(fileInfo, _typeFieldStr)
	r.Seek(off, io.SeekStart)
	ptrN, err := readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
	if err != nil {
		return nil
	}
	if ptrN != 0 {
		typ.Name = parseString(fileInfo, ptrN, sectionBaseAddr, sectionData)
	}

	typ.Addr = offset + sectionBaseAddr
	types[typ.Addr] = typ

	// Legacy types has a field with a pointer to the uncommonType.
	// The flags location is unused, hence 0, so the parsing of the uncommonType
	// is skipped below. So instead, if the binary uses legacy types parse it now.
	// Pointer is right after the string pointer.
	off = typeOffset(fileInfo, _typeFieldStr) + int64(fileInfo.WordSize)
	r.Seek(off, io.SeekStart)
	ptr, err := readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
	if err != nil {
		return nil
	}
	if ptr != 0 {
		// Since we don't know if the struct is located before or after this type,
		// create a new reader.
		ur := bytes.NewReader(sectionData)
		ur.Seek(int64(ptr-sectionBaseAddr), io.SeekStart)
		parseUncommonType(typ, ur, fileInfo, sectionData, sectionBaseAddr, types)
	}

	// Parse extra fields
	off = typeOffset(fileInfo, _typeFieldEnd)
	r.Seek(off, io.SeekStart)
	switch typ.Kind {

	case reflect.Ptr:
		ptr, err := readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
		if err != nil {
			return nil
		}
		typ.PtrResolvAddr = ptr
		if ptr != 0 {
			c := typeParse(types, fileInfo, ptr-sectionBaseAddr, sectionData, sectionBaseAddr)
			typ.Element = c
		}

	case reflect.Struct:

		// Parse struct fields
		fieldptr, err := readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
		if err != nil {
			return nil
		}
		numfield, err := readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
		if err != nil {
			return nil
		}
		// Eat cap
		_, err = readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
		if err != nil {
			return nil
		}

		// Parse methods
		if typ.flag&tflagUncommon != 0 {
			parseUncommonType(typ, r, fileInfo, sectionData, sectionBaseAddr, types)
		}

		// Parse fields
		typ.Fields = make([]*GoType, numfield)
		secR := bytes.NewReader(sectionData)
		for i := 0; i < int(numfield); i++ {
			var fieldName string
			var tag string
			o := int64(fieldptr + uint64(i*5*fileInfo.WordSize) - sectionBaseAddr)
			secR.Seek(o, io.SeekStart)
			nptr, err := readUIntTo64(secR, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
			if err != nil {
				return nil
			}
			ppp, err := readUIntTo64(secR, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
			if err != nil {
				return nil
			}
			if ppp != 0 {
				pps := parseString(fileInfo, ppp, sectionBaseAddr, sectionData)
				if pps != "" {
					typ.PackagePath = pps
				}
			}
			tptr, err := readUIntTo64(secR, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
			if err != nil {
				return nil
			}
			tagptr, err := readUIntTo64(secR, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
			if err != nil {
				return nil
			}
			if tagptr != 0 {
				tag = parseString(fileInfo, tagptr, sectionBaseAddr, sectionData)
			}
			uptr, err := readUIntTo64(secR, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
			if err != nil {
				return nil
			}
			gt := typeParse(types, fileInfo, tptr-sectionBaseAddr, sectionData, sectionBaseAddr)
			// Make a copy
			field := *gt

			fieldName = parseString(fileInfo, nptr, sectionBaseAddr, sectionData)
			field.FieldName = fieldName
			if tag != "" {
				field.FieldTag = tag
			}
			// Older versions has no field name for anonymous fields. New versions
			// uses a bit flag on the offset.
			field.FieldAnon = fieldName == "" || uptr&1 != 0
			typ.Fields[i] = &field
		}
	case reflect.Array:

		elementAddr, err := readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
		if err != nil {
			return nil
		}
		if elementAddr != 0 {
			e := typeParse(types, fileInfo, elementAddr-sectionBaseAddr, sectionData, sectionBaseAddr)
			typ.Element = e
		}

		// Read and skip slice type
		_, err = readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
		if err != nil {
			return nil
		}

		// Read length
		l, err := readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
		if err != nil {
			return nil
		}
		typ.Length = int(l)

	case reflect.Slice:

		elementAddr, err := readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
		if err != nil {
			return nil
		}
		if elementAddr != 0 {
			e := typeParse(types, fileInfo, elementAddr-sectionBaseAddr, sectionData, sectionBaseAddr)
			typ.Element = e
		}

	case reflect.Chan:

		elementAddr, err := readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
		if err != nil {
			return nil
		}
		if elementAddr != 0 {
			e := typeParse(types, fileInfo, elementAddr-sectionBaseAddr, sectionData, sectionBaseAddr)
			typ.Element = e
		}

		// Direction
		d, err := readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
		if err != nil {
			return nil
		}
		typ.ChanDir = ChanDir(int(d))

	case reflect.Map:

		keyAddr, err := readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
		if err != nil {
			return nil
		}
		if keyAddr != 0 {
			k := typeParse(types, fileInfo, keyAddr-sectionBaseAddr, sectionData, sectionBaseAddr)
			typ.Key = k
		}

		elementAddr, err := readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
		if err != nil {
			return nil
		}
		if elementAddr != 0 {
			e := typeParse(types, fileInfo, elementAddr-sectionBaseAddr, sectionData, sectionBaseAddr)
			typ.Element = e
		}

	case reflect.Func:

		// bool plus padding.
		dotdotdot, err := readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
		if err != nil {
			return nil
		}
		typ.IsVariadic = dotdotdot > uint64(0)
		// One for args and one for returns
		rtypes := make([]uint64, 2)
		typelens := make([]uint64, 2)
		for i := 0; i < 2; i++ {
			p, err := readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
			if err != nil {
				continue
			}
			rtypes[i] = p
			l, err := readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
			if err != nil {
				continue
			}
			typelens[i] = l

			// Eat cap
			_, err = readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
			if err != nil {
				println("Error when reading padding:", err)
				return nil
			}
		}
		// Full section reader
		sr := bytes.NewReader(sectionData)
		// Parse the arg types and result types.
		for i := 0; i < 2; i++ {
			if rtypes[i] == 0 {
				continue
			}
			_, err = sr.Seek(int64(rtypes[i]-sectionBaseAddr), io.SeekStart)
			if err != nil {
				continue
			}
			for j := 0; j < int(typelens[i]); j++ {
				p, err := readUIntTo64(sr, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
				if err != nil {
					continue
				}
				if p == 0 {
					continue
				}
				t := typeParse(types, fileInfo, p-sectionBaseAddr, sectionData, sectionBaseAddr)
				if i == 0 {
					typ.FuncArgs = append(typ.FuncArgs, t)
				} else {
					typ.FuncReturnVals = append(typ.FuncReturnVals, t)
				}
			}
		}

	case reflect.Interface:

		ptrMethods, err := readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
		if err != nil {
			return nil
		}
		numMethods, err := readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
		if err != nil {
			return nil
		}
		// Eat cap
		_, err = readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
		if err != nil {
			return nil
		}

		// Parse imethods
		secR := bytes.NewReader(sectionData)
		imethSize := uint64(2 * intSize32)
		int32ptr := true
		if GoVersionCompare(fileInfo.goversion.Name, "go1.7beta1") < 0 {
			imethSize = uint64(3 * fileInfo.WordSize)
			int32ptr = fileInfo.WordSize == intSize32
		}
		for i := 0; i < int(numMethods); i++ {
			meth := new(TypeMethod)
			// All fields has the size of int32
			secR.Seek(int64(ptrMethods+uint64(i)*imethSize-sectionBaseAddr), io.SeekStart)
			nameOff, err := readUIntTo64(secR, fileInfo.ByteOrder, int32ptr)
			if err != nil {
				continue
			}
			if nameOff != 0 {
				meth.Name = parseString(fileInfo, nameOff, sectionBaseAddr, sectionData)
			}

			pkgPathPtr, err := readUIntTo64(secR, fileInfo.ByteOrder, int32ptr)
			if err != nil {
				continue
			}
			if pkgPathPtr != 0 {
				pkgPathStr := parseString(fileInfo, pkgPathPtr, sectionBaseAddr, sectionData)
				if pkgPathStr != "" {
					typ.PackagePath = pkgPathStr
				}
			}

			typeOff, err := readUIntTo64(secR, fileInfo.ByteOrder, int32ptr)
			if err != nil {
				continue
			}
			if typeOff != 0 {
				typeOff = typeOff - sectionBaseAddr
				meth.Type = typeParse(types, fileInfo, typeOff, sectionData, sectionBaseAddr)
			}
			typ.Methods = append(typ.Methods, meth)
		}
	}
	return typ
}

func parseString(fileInfo *FileInfo, off, base uint64, baseData []byte) string {
	if off == 0 {
		return ""
	}
	r := bytes.NewReader(baseData)
	r.Seek(int64(off-base), io.SeekStart)
	h, err := readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
	if err != nil {
		return ""
	}
	l, err := readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
	if err != nil {
		return ""
	}
	if h == 0 || l == 0 {
		return ""
	}
	str := string(baseData[h-base : h-base+l])
	return str
}

func parseUncommonType(typ *GoType, r *bytes.Reader, fileInfo *FileInfo, sectionData []byte, sectionBaseAddr uint64, types map[uint64]*GoType) {
	pname, err := readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
	if err != nil {
		return
	}
	if pname != 0 {
		n := parseString(fileInfo, pname, sectionBaseAddr, sectionData)
		if typ.Name == "" && n != "" {
			typ.Name = n
		}
	}
	ppkg, err := readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
	if err != nil {
		return
	}
	if ppkg != 0 {
		p := parseString(fileInfo, ppkg, sectionBaseAddr, sectionData)
		if typ.PackagePath == "" && p != "" {
			typ.PackagePath = p
		}
	}
	typ.Methods = parseMethods(r, fileInfo, sectionData, sectionBaseAddr, types)
}

// The methods must start at the readers current location.
func parseMethods(r *bytes.Reader, fileInfo *FileInfo, sectionData []byte, sectionBaseAddr uint64, types map[uint64]*GoType) []*TypeMethod {
	pdata, err := readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
	if err != nil {
		return nil
	}
	numMeth, err := readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
	if err != nil {
		return nil
	}
	methods := make([]*TypeMethod, numMeth)
	r.Seek(int64(pdata-sectionBaseAddr), io.SeekStart)
	for i := 0; i < int(numMeth); i++ {
		m := &TypeMethod{}
		p, err := readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
		if err != nil {
			return nil
		}
		m.Name = parseString(fileInfo, p, sectionBaseAddr, sectionData)

		// Eat package path
		_, err = readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
		if err != nil {
			return nil
		}

		// mtyp
		mtype, err := readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
		if err != nil {
			return nil
		}
		if mtype != 0 {
			m.Type = typeParse(types, fileInfo, mtype-sectionBaseAddr, sectionData, sectionBaseAddr)
		}

		// typ
		p, err = readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
		if err != nil {
			return nil
		}
		if p != 0 {
			// Parse it so we capture it in the global type list.
			typeParse(types, fileInfo, p-sectionBaseAddr, sectionData, sectionBaseAddr)
		}

		// ifn
		ifn, err := readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
		if err != nil {
			return nil
		}
		m.IfaceCallOffset = ifn

		// tfn
		tfn, err := readUIntTo64(r, fileInfo.ByteOrder, fileInfo.WordSize == intSize32)
		if err != nil {
			return nil
		}
		m.FuncCallOffset = tfn
		methods[i] = m
	}
	return methods
}

func typeOffset(fileInfo *FileInfo, field _typeField) int64 {
	intSize := intSize64
	if fileInfo.WordSize == intSize32 {
		intSize = intSize32
	}
	if field == _typeFieldSize {
		return int64(0)
	}

	if field == _typeFieldKind {
		return int64(2*intSize + 4 + 3)
	}

	if field == _typeFieldStr {
		return int64(4*intSize + 4 + 4)
	}

	if field == _typeFieldFlag {
		return int64(2*intSize + 4)
	}

	if field == _typeFieldEnd {
		if GoVersionCompare(fileInfo.goversion.Name, "go1.6beta1") < 0 {
			return int64(8*intSize + 8)
		}
		if GoVersionCompare(fileInfo.goversion.Name, "go1.7beta1") < 0 {
			return int64(7*intSize + 8)
		}
		return int64(4*intSize + 16)
	}
	return int64(-1)
}
