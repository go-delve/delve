// This file is part of GoRE.
//
// Copyright (C) 2019-2023 GoRE Authors
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

/*

	Data structures

*/

func newTypeParser(typesData []byte, baseAddres uint64, fi *FileInfo) *typeParser {
	goversion := fi.goversion.Name

	p := &typeParser{
		goversion: goversion,
		base:      baseAddres,
		order:     fi.ByteOrder,
		wordsize:  fi.WordSize,
		cache:     make(map[uint64]*GoType),
		typesData: typesData,
		r:         bytes.NewReader(typesData),
	}

	if fi.WordSize == 8 {
		p.parseArrayType = arrayTypeParseFunc64
		p.parseChanType = chanTypeParseFunc64
		p.parseFuncType = funcTypeParseFunc64
		p.parseIMethod = imethodTypeParseFunc64
		p.parseInterface = interfaceTypeParseFunc64
		p.parseRtype = rtypeParseFunc64
		p.parseStructFieldType = structFieldTypeParseFunc64
		p.parseStructType = structTypeParseFunc64
		p.parseUint = readUintFunc64

		// Use the correct map parser based on Go version.
		if GoVersionCompare(goversion, "go1.11beta1") < 0 {
			p.parseMap = mapTypeParseFunc1764
		} else if GoVersionCompare(goversion, "go1.12beta1") < 0 {
			p.parseMap = mapTypeParseFunc1164
		} else if GoVersionCompare(goversion, "go1.14beta1") < 0 {
			p.parseMap = mapTypeParseFunc1264
		} else {
			p.parseMap = mapTypeParseFunc64
		}
	} else {
		p.parseArrayType = arrayTypeParseFunc32
		p.parseChanType = chanTypeParseFunc32
		p.parseFuncType = funcTypeParseFunc32
		p.parseIMethod = imethodTypeParseFunc64
		p.parseInterface = interfaceTypeParseFunc32
		p.parseRtype = rtypeParseFunc32
		p.parseStructFieldType = structFieldTypeParseFunc32
		p.parseStructType = structTypeParseFunc32
		p.parseUint = readUintFunc32

		// Use the correct map parser based on Go version.
		if GoVersionCompare(goversion, "go1.11beta1") < 0 {
			p.parseMap = mapTypeParseFunc1732
		} else if GoVersionCompare(goversion, "go1.12beta1") < 0 {
			p.parseMap = mapTypeParseFunc1132
		} else if GoVersionCompare(goversion, "go1.14beta1") < 0 {
			p.parseMap = mapTypeParseFunc1232
		} else {
			p.parseMap = mapTypeParseFunc32
		}
	}

	p.parseMethod = methodParseFunc64
	if fi.goversion.Name == "go1.7beta1" {
		p.parseUncommon = uncommonTypeParseFunc17Beta1
	} else {
		p.parseUncommon = uncommonTypeParseFunc64
	}

	if GoVersionCompare(fi.goversion.Name, "go1.17beta1") < 0 {
		// before go1.17, the length of tag used fixed 2-byte encoding.
		p.parseNameLen = nameLenParseFuncTwoByteFixed
	} else {
		// See https://golang.org/cl/318249.
		// Go1.17 switch to using varint encoding.
		p.parseNameLen = nameLenParseFuncVarint
	}

	return p
}

// typeParser can parse the Go type structures for binaries compiled with the
// Go compiler version 1.7 and newer. The entry point for parsing types is the
// method "parseType". All parsed types are stored internally and can be returned
// by calling the method "parsedTypes".
type typeParser struct {
	// r is a reader that can read from the beginning of the types data to the
	// end of the section that the types data is located in.
	r *bytes.Reader
	// base is the starting address of the types data.
	base uint64
	// order holds the byte order for the binary.
	order    binary.ByteOrder
	wordsize int
	// cache is used to track types that has already been parsed.
	cache map[uint64]*GoType

	// typesData is the byte slice of the types data.
	// located.
	typesData []byte

	goversion string

	// Parse functions

	parseArrayType       arrayTypeParseFunc
	parseChanType        chanTypeParseFunc
	parseFuncType        funcTypeParseFunc
	parseIMethod         imethodTypeParseFunc
	parseInterface       interfaceTypeParseFunc
	parseMap             mapTypeParseFunc
	parseMethod          methodParseFunc
	parseRtype           rtypeParseFunc
	parseStructFieldType structFieldTypeParseFunc
	parseStructType      structTypeParseFunc
	parseUint            readUintFunc
	parseUncommon        uncommonTypeParseFunc
	parseNameLen         nameLenParseFunc
}

func (p *typeParser) hasTag(off uint64) bool {
	return p.typesData[off]&(1<<1) != 0
}

func (p *typeParser) resolveName(ptr uint64, flags uint8) (string, int) {
	i, l := p.parseNameLen(p, ptr+1)
	name := string(p.typesData[ptr+1+uint64(l) : ptr+1+uint64(l)+i])
	nl := int(i)
	if nl == 0 {
		return "", 0
	}
	if flags&tflagExtraStar != 0 {
		// typ.Name = strData[1:]
		return name[1:], nl - 1
	}
	return name, nl
}

func (p *typeParser) resolveTag(o uint64) string {
	if !p.hasTag(o) {
		return ""
	}
	nl, nll := p.parseNameLen(p, o+1)
	tl, tll := p.parseNameLen(p, o+1+nl+uint64(nll))
	if tl == 0 {
		return ""
	}
	o += 1 + nl + uint64(nll+tll)
	return string(p.typesData[o : o+tl])
}

func (p *typeParser) readType(obj interface{}) (int, error) {
	err := binary.Read(p.r, p.order, obj)
	if err != nil {
		return 0, fmt.Errorf("read of type failed: %w", err)
	}
	return binary.Size(obj), nil
}

func (p *typeParser) seekFromStart(off uint64) error {
	_, err := p.r.Seek(int64(off), io.SeekStart)
	return err
}

// parsedTypes returns all the parsed types for the file. This method
// should be called after parseType has parsed all the types.
func (p *typeParser) parsedTypes() map[uint64]*GoType {
	return p.cache
}

// parseType parses the type at the given offset. This method does return
// the parsed type, but this should not be used to get all types. This
// functionality is used internally because the method is called recursively
// to parse child types. All parsed types should be accessed via the
// "parsedTypes" method.
func (p *typeParser) parseType(address uint64) (*GoType, error) {
	// First check the cache.
	if t, ok := p.cache[address]; ok {
		return t, nil
	}

	/*
		Parsing of the rtype structure.
	*/

	// We seek to the beginning of the rtype structure and parse it from start
	// to finish.
	err := p.seekFromStart(address - p.base)
	if err != nil {
		return nil, err
	}

	// Count is used to track how many bytes have been read from the offset of
	// the type.
	count := 0

	rtype, c, err := p.parseRtype(p)
	if err != nil {
		return nil, err
	}
	count += c

	// Create a new type and store it in the cache.
	typ := &GoType{
		Kind: reflect.Kind(rtype.Kind & kindMask),
		flag: rtype.Tflag,
		Addr: uint64(address),
	}
	p.cache[address] = typ

	// Resolve name of the type.
	typ.Name, _ = p.resolveName(uint64(rtype.Str), typ.flag)

	/*
		Parsing of "kind" fields.
	*/

	// These fields are appended right after the rtype structure. We parse them
	// now since the reader is at the beginning of these fields.

	// These are used to track sub-types that needs to be processed later. We
	// don't parse for example struct fields and function arguments at this
	// time. We only record how many and where the data starts. This is because
	// we don't want to seek to a different location with the reader.
	var child uint64
	var key uint64

	switch typ.Kind {

	case reflect.Array:
		a, c, err := p.parseArrayType(p)
		if err != nil {
			return nil, fmt.Errorf("failed to parse fields for array located at 0x%x: %w", address, err)
		}
		count += c

		typ.Length = int(a.Len)
		child = a.Eem

	case reflect.Chan:
		ch, c, err := p.parseChanType(p)
		if err != nil {
			return nil, fmt.Errorf("failed to parse fields for channel type located at 0x%x: %w", address, err)
		}
		count += c

		typ.ChanDir = ChanDir(int(ch.Dir))
		child = ch.Elem

	case reflect.Func:
		ftype, c, err := p.parseFuncType(p)
		if err != nil {
			return nil, fmt.Errorf("failed to parse fields for function type located at 0x%x: %w", address, err)
		}
		count += c

		typ.FuncArgs = make([]*GoType, int(ftype.InCount))
		typ.IsVariadic = ftype.OutCount&(1<<15) != 0

		out := ftype.OutCount & (1<<15 - 1)
		typ.FuncReturnVals = make([]*GoType, out)

	case reflect.Interface:
		iface, c, err := p.parseInterface(p)
		if err != nil {
			return nil, fmt.Errorf("failed to parse fields for interface type located at 0x%x: %w", address, err)
		}
		count += c

		if iface.PkgPath != 0 {
			typ.PackagePath, _ = p.resolveName(iface.PkgPath-p.base, 0)
		}

		if iface.MethodsLen > 0 {
			child = iface.Methods
			typ.Methods = make([]*TypeMethod, int(iface.MethodsLen), int(iface.MethodsCap))
		}

	case reflect.Map:
		maptyp, c, err := p.parseMap(p)
		if err != nil {
			return nil, fmt.Errorf("failed to parse fields for map type located at 0x%x: %w", address, err)
		}
		count += c

		child = maptyp.Elem
		key = maptyp.Key

	case reflect.Ptr, reflect.Slice:
		ptr, c, err := p.parseUint(p)
		if err != nil {
			return nil, fmt.Errorf("failed to parse pointer to type for type located at 0x%x: %w", address, err)
		}
		count += c

		child = ptr

		if typ.Kind == reflect.Ptr {
			typ.PtrResolvAddr = ptr
		}

	case reflect.Struct:
		s, c, err := p.parseStructType(p)
		if err != nil {
			return nil, fmt.Errorf("failed to parse struct type's fields located at 0x%x: %w", address, err)
		}
		count += c

		child = s.FieldsData
		typ.Fields = make([]*GoType, int(s.FieldsLen), int(s.FieldsCap))

		// Resolve package path.
		if s.PkgPath > uint64(p.base) {
			typ.PackagePath, _ = p.resolveName(s.PkgPath-uint64(p.base), 0)
		}
	}

	/*
		Uncommon type
	*/

	// Some types have methods. If so, it's an uncommon type according to Go's
	// source code.  Uncommon types have an extra data structure located right
	// after the kind data. This data structure holds information about the
	// methods. We parse the structure but resolve the method data later since
	// we want to seek around.

	// This is used to store where the method data is stored.
	var methodStart uint64

	if typ.flag&tflagUncommon != 0 {
		startOfData := address + uint64(count)

		uc, c, err := p.parseUncommon(p)
		if err != nil {
			return nil, fmt.Errorf("failed to parse type's (0x%x) uncommon field data: %w", address, err)
		}
		count += c

		if uc.Mcount != 0 {
			// We have some methods that needs to be parsed. From source code
			// comments the Moff attribute is the offset from the beginning of
			// the uncommon data structure to where the array of methods start.
			// Calculate and store this value.
			methodStart = startOfData + uint64(uc.Moff) - p.base

			// Create a slice to store the methods so we later will know how
			// many methods we need to parse.
			typ.Methods = make([]*TypeMethod, uc.Mcount)
		}
	}

	/*
		Function arguments and return values.
	*/

	// For functions a *rtype for each in and out parameter is stored in an
	// array that directly follows the funcType (and possibly its
	// uncommonType). So a function type with one method, one input, and one
	// output is:
	//
	//  struct {
	//      funcType
	//      uncommonType
	//      [2]*rtype    // [0] is in, [1] is out
	//  }
	if typ.Kind == reflect.Func {
		// The current location of the reader is at the start for the array
		// of function arguments and return values. Saving this location so
		// they can be parsed later. We know how many arguments and return
		// values the function has since the slices in the typ object has
		// been created with the correct size.
		child = uint64(address) + uint64(count)
	}

	// For the rest we don't read linear anymore. Instead we seek around to
	// parse all extra data structures.

	/*
		Parse methods
	*/

	if methodStart != 0 {
		// Used to track how much has been read since the beginning of the
		// method data.
		n := uint64(0)

		// Extract methods.
		for i := 0; i < len(typ.Methods); i++ {
			err = p.seekFromStart(methodStart + n)
			if err != nil {
				return nil, fmt.Errorf("failed to seek to type's methods: %w", err)
			}

			m, c, err := p.parseMethod(p)
			if err != nil {
				return nil, fmt.Errorf("failed to parse methods for type at 0x%x: %w", address, err)
			}
			n += uint64(c)

			if m.Name == 0 || int(m.Name) > len(p.typesData) {
				return nil, fmt.Errorf("method name for type at 0x%x has an invalid address (0x%x)", address, m.Name)
			}

			nm, _ := p.resolveName(uint64(m.Name), 0)

			// With the release of Go 1.16 (commit: https://github.com/golang/go/commit/0ab72ed020d0c320b5007987abdf40677db34cfc)
			// a sentinel value of -1 is used for unreachable code. This code has been removed by the compiler because it has
			// been identified as dead code. Previously it had a value of 0x00 which meant it pointed to the beginning of the
			// text section. This location could have valid code which is why it was changed. We don't have to worry about this.
			// Externally, GoRE has communicated unreachable code by the zero value so this code changes it to 0x00 if the code
			// has been optimized out.
			if m.Mtyp == int32(-1) {
				m.Mtyp = 0
			}
			if m.Ifn == int32(-1) {
				m.Ifn = 0
			}
			if m.Tfn == int32(-1) {
				m.Tfn = 0
			}

			var t *GoType
			if m.Mtyp != 0 {
				// Not all methods have this field. It looks to be mainly
				// available for exported methods.
				t, err = p.parseType(uint64(m.Mtyp) + p.base)
				if err != nil {
					return nil, fmt.Errorf("failed to parse method type: %w", err)
				}
			}

			typ.Methods[i] = &TypeMethod{
				Name:            nm,
				Type:            t,
				IfaceCallOffset: uint64(m.Ifn),
				FuncCallOffset:  uint64(m.Tfn),
			}
		}
	}

	// Handle child types.
	if child != 0 {
		// Used to keep the track how much has been read from the start of the
		// "child" location.
		n := uint64(0)

		switch typ.Kind {

		case reflect.Array,
			reflect.Chan,
			reflect.Ptr,
			reflect.Slice:

			t, err := p.parseType(child)
			if err != nil {
				return nil, fmt.Errorf("failed to parse resolved type for 0x%x: %w", address, err)
			}
			typ.Element = t

		case reflect.Func:
			// The read address.
			var ptr uint64

			// First process the function arguments.
			for i := 0; i < len(typ.FuncArgs)+len(typ.FuncReturnVals); i++ {
				err = p.seekFromStart(child - p.base + n)
				if err != nil {
					return nil, fmt.Errorf("failed to seek to function argument/return type pointer at 0x%x: %w", child+uint64(n), err)
				}

				ptr, c, err = p.parseUint(p)
				if err != nil {
					return nil, fmt.Errorf("failed to read function argument/return type pointer at 0x%x: %w", child+uint64(n), err)
				}
				n += uint64(c)

				// Parse the type for the function argument.
				t, err := p.parseType(ptr)
				if err != nil {
					return nil, fmt.Errorf("failed to parse type for function argument/return type at 0x%x: %w", child+uint64(n), err)
				}

				// Save the type to the right slice.
				if i < len(typ.FuncArgs) {
					typ.FuncArgs[i] = t
				} else {
					typ.FuncReturnVals[i-len(typ.FuncArgs)] = t
				}
			}

		case reflect.Interface:
			for i := 0; i < len(typ.Methods); i++ {
				err = p.seekFromStart(child - p.base + n)
				if err != nil {
					return nil, fmt.Errorf("failed to seek to the interface's method data for type at 0x%x: %w", address, err)
				}

				meth, c, err := p.parseIMethod(p)
				if err != nil {
					return nil, fmt.Errorf("failed to parse imethod %d for type located at 0x%x: %w", i+1, address, err)
				}
				n += uint64(c)

				t, err := p.parseType(uint64(meth.Typ) + p.base)
				if err != nil {
					return nil, fmt.Errorf("failed to parse imethod type %d for type located at 0x%x: %w", i+1, address, err)
				}

				name, _ := p.resolveName(uint64(meth.Name), 0)

				typ.Methods[i] = &TypeMethod{
					Name: name,
					Type: t,
				}
			}

		case reflect.Map:
			el, err := p.parseType(child)
			if err != nil {
				return nil, fmt.Errorf("failed to parse type for map element type at 0x%x: %w", child+uint64(n), err)
			}
			typ.Element = el

			k, err := p.parseType(key)
			if err != nil {
				return nil, fmt.Errorf("failed to parse type for map key type at 0x%x: %w", child+uint64(n), err)
			}
			typ.Key = k

		case reflect.Struct:
			// Parse the data for each struct field.
			for i := 0; i < len(typ.Fields); i++ {
				err = p.seekFromStart(child - p.base + n)
				if err != nil {
					return nil, fmt.Errorf("failed to seek to the structure's field data for type at 0x%x: %w", address, err)
				}

				sf, c, err := p.parseStructFieldType(p)
				if err != nil {
					return nil, fmt.Errorf("failed to parse field %d for type located at 0x%x: %w", i+1, address, err)
				}
				n += uint64(c)

				gt, err := p.parseType(sf.Typ)
				if err != nil {
					return nil, fmt.Errorf("failed to parse field type %d for type located at 0x%x: %w", i+1, address, err)
				}

				// The parseType function returns a pointer to the type.
				// We make a copy of the data that it points to and uses that for our field.
				// If we don't do this, we use a "global" GoType and end up overwriting the content
				// over and over again.
				field := *gt

				name, nl := p.resolveName(sf.Name-p.base, 0)
				field.FieldName = name

				if nl != 0 {
					field.FieldTag = p.resolveTag(sf.Name - p.base)
				}

				// In the commit https://github.com/golang/go/commit/e1e66a03a6bb3210034b640923fa253d7def1a26 the encoding for
				// embedded struct field was moved from the offset field to the name field. This changed was first part of the
				// 1.19rc1 release.s
				if GoVersionCompare(p.goversion, "go1.19rc1") >= 0 {
					field.FieldAnon = p.typesData[sf.Name-p.base]&(1<<3) != 0
				} else {
					field.FieldAnon = name == "" || sf.OffsetEmbed&1 != 0
				}

				typ.Fields[i] = &field
			}
		}
	}

	return typ, nil
}

/*
	Parse functions
*/

// The following functions are used to parse architecture specific data
// structures in the binary.

// array

type arrayTypeParseFunc func(p *typeParser) (arrayType64, int, error)

var arrayTypeParseFunc64 = func(p *typeParser) (arrayType64, int, error) {
	var typ arrayType64
	c, err := p.readType(&typ)
	return typ, c, err
}

var arrayTypeParseFunc32 = func(p *typeParser) (arrayType64, int, error) {
	var typ arrayType32
	c, err := p.readType(&typ)
	if err != nil {
		return arrayType64{}, c, err
	}

	return arrayType64{
		Eem:   uint64(typ.Eem),
		Slice: uint64(typ.Slice),
		Len:   uint64(typ.Len),
	}, c, err
}

// channel

type chanTypeParseFunc func(p *typeParser) (chanType, int, error)

var chanTypeParseFunc64 = func(p *typeParser) (chanType, int, error) {
	var typ chanType
	c, err := p.readType(&typ)
	return typ, c, err
}

var chanTypeParseFunc32 = func(p *typeParser) (chanType, int, error) {
	var typ chanType32
	c, err := p.readType(&typ)
	if err != nil {
		return chanType{}, c, err
	}

	return chanType{
		Elem: uint64(typ.Elem),
		Dir:  uint64(typ.Dir),
	}, c, err
}

// func

type funcTypeParseFunc func(p *typeParser) (funcType, int, error)

var funcTypeParseFunc64 = func(p *typeParser) (funcType, int, error) {
	var typ funcType64
	c, err := p.readType(&typ)
	if err != nil {
		return funcType{}, c, err
	}

	return funcType{
		InCount:  uint64(typ.InCount),
		OutCount: uint64(typ.OutCount),
	}, c, err
}

var funcTypeParseFunc32 = func(p *typeParser) (funcType, int, error) {
	var typ funcType32
	c, err := p.readType(&typ)
	if err != nil {
		return funcType{}, c, err
	}

	return funcType{
		InCount:  uint64(typ.InCount),
		OutCount: uint64(typ.OutCount),
	}, c, err
}

// imethod

type imethodTypeParseFunc func(p *typeParser) (imethod, int, error)

var imethodTypeParseFunc64 = func(p *typeParser) (imethod, int, error) {
	var typ imethod
	c, err := p.readType(&typ)
	return typ, c, err
}

// interface

type interfaceTypeParseFunc func(p *typeParser) (interfaceType, int, error)

var interfaceTypeParseFunc64 = func(p *typeParser) (interfaceType, int, error) {
	var typ interfaceType
	c, err := p.readType(&typ)
	return typ, c, err
}

var interfaceTypeParseFunc32 = func(p *typeParser) (interfaceType, int, error) {
	var typ interfaceType32
	c, err := p.readType(&typ)
	if err != nil {
		return interfaceType{}, c, err
	}

	return interfaceType{
		PkgPath:    uint64(typ.PkgPath),
		Methods:    uint64(typ.Methods),
		MethodsLen: uint64(typ.MethodsLen),
		MethodsCap: uint64(typ.MethodsCap),
	}, c, err
}

// map

type mapTypeParseFunc func(p *typeParser) (mapType, int, error)

var mapTypeParseFunc64 = func(p *typeParser) (mapType, int, error) {
	var typ mapType
	c, err := p.readType(&typ)
	return typ, c, err
}

var mapTypeParseFunc32 = func(p *typeParser) (mapType, int, error) {
	var typ mapType32
	c, err := p.readType(&typ)
	if err != nil {
		return mapType{}, c, err
	}

	return mapType{
		Key:        uint64(typ.Key),
		Elem:       uint64(typ.Elem),
		Bucket:     uint64(typ.Bucket),
		Hasher:     uint64(typ.Hasher),
		Keysize:    typ.Keysize,
		Valuesize:  typ.Valuesize,
		Bucketsize: typ.Bucketsize,
		Flags:      typ.Flags,
	}, c, err
}

// Map parser for Go 1.7 to 1.10 (64 bit)
var mapTypeParseFunc1764 = func(p *typeParser) (mapType, int, error) {
	var typ mapTypeGo1764
	c, err := p.readType(&typ)
	if err != nil {
		return mapType{}, c, err
	}

	return mapType{
		Key:        typ.Key,
		Elem:       typ.Elem,
		Bucket:     typ.Bucket,
		Keysize:    typ.Keysize,
		Valuesize:  typ.Valuesize,
		Bucketsize: typ.Bucketsize,
	}, c, err
}

// Map parser for Go 1.7 to 1.10 (32 bit)
var mapTypeParseFunc1732 = func(p *typeParser) (mapType, int, error) {
	var typ mapTypeGo1732
	c, err := p.readType(&typ)
	if err != nil {
		return mapType{}, c, err
	}

	return mapType{
		Key:        uint64(typ.Key),
		Elem:       uint64(typ.Elem),
		Bucket:     uint64(typ.Bucket),
		Keysize:    typ.Keysize,
		Valuesize:  typ.Valuesize,
		Bucketsize: typ.Bucketsize,
	}, c, err
}

// Map parser for Go 1.11 (64 bit)
var mapTypeParseFunc1164 = func(p *typeParser) (mapType, int, error) {
	var typ mapTypeGo1164
	c, err := p.readType(&typ)
	if err != nil {
		return mapType{}, c, err
	}

	return mapType{
		Key:        typ.Key,
		Elem:       typ.Elem,
		Bucket:     typ.Bucket,
		Keysize:    typ.Keysize,
		Valuesize:  typ.Valuesize,
		Bucketsize: typ.Bucketsize,
	}, c, err
}

// Map parser for Go 1.11 (32 bit)
var mapTypeParseFunc1132 = func(p *typeParser) (mapType, int, error) {
	var typ mapTypeGo1132
	c, err := p.readType(&typ)
	if err != nil {
		return mapType{}, c, err
	}

	return mapType{
		Key:        uint64(typ.Key),
		Elem:       uint64(typ.Elem),
		Bucket:     uint64(typ.Bucket),
		Keysize:    typ.Keysize,
		Valuesize:  typ.Valuesize,
		Bucketsize: typ.Bucketsize,
	}, c, err
}

// Map parser for Go 1.12 and 1.13 (64 bit)
var mapTypeParseFunc1264 = func(p *typeParser) (mapType, int, error) {
	var typ mapTypeGo1264
	c, err := p.readType(&typ)
	if err != nil {
		return mapType{}, c, err
	}

	return mapType{
		Key:        typ.Key,
		Elem:       typ.Elem,
		Bucket:     typ.Bucket,
		Keysize:    typ.Keysize,
		Valuesize:  typ.Valuesize,
		Bucketsize: typ.Bucketsize,
	}, c, err
}

// Map parser for Go 1.12 and 1.13 (32 bit)
var mapTypeParseFunc1232 = func(p *typeParser) (mapType, int, error) {
	var typ mapTypeGo1232
	c, err := p.readType(&typ)
	if err != nil {
		return mapType{}, c, err
	}

	return mapType{
		Key:        uint64(typ.Key),
		Elem:       uint64(typ.Elem),
		Bucket:     uint64(typ.Bucket),
		Keysize:    typ.Keysize,
		Valuesize:  typ.Valuesize,
		Bucketsize: typ.Bucketsize,
	}, c, err
}

// method

type methodParseFunc func(p *typeParser) (method, int, error)

var methodParseFunc64 = func(p *typeParser) (method, int, error) {
	var typ method
	c, err := p.readType(&typ)
	if err != nil {
		return method{}, c, err
	}

	return typ, c, err
}

// rtype

type rtypeParseFunc func(p *typeParser) (rtypeGo64, int, error)

var rtypeParseFunc64 = func(p *typeParser) (rtypeGo64, int, error) {
	var typ rtypeGo64
	c, err := p.readType(&typ)
	return typ, c, err
}

var rtypeParseFunc32 = func(p *typeParser) (rtypeGo64, int, error) {
	var typ rtypeGo32
	c, err := p.readType(&typ)
	if err != nil {
		return rtypeGo64{}, c, err
	}

	return rtypeGo64{
		Size:       uint64(typ.Size),
		Ptrdata:    uint64(typ.Ptrdata),
		Hash:       typ.Hash,
		Tflag:      typ.Tflag,
		Align:      typ.Align,
		FieldAlign: typ.FieldAlign,
		Kind:       typ.Kind,
		Equal:      uint64(typ.Equal),
		Gcdata:     uint64(typ.Gcdata),
		Str:        typ.Str,
		PtrToThis:  typ.PtrToThis,
	}, c, err
}

// struct

type structTypeParseFunc func(p *typeParser) (structType64, int, error)

var structTypeParseFunc64 = func(p *typeParser) (structType64, int, error) {
	var typ structType64
	c, err := p.readType(&typ)
	return typ, c, err
}

var structTypeParseFunc32 = func(p *typeParser) (structType64, int, error) {
	var typ structType32
	c, err := p.readType(&typ)
	if err != nil {
		return structType64{}, c, err
	}

	return structType64{
		PkgPath:    uint64(typ.PkgPath),
		FieldsData: uint64(typ.FieldsData),
		FieldsLen:  uint64(typ.FieldsLen),
		FieldsCap:  uint64(typ.FieldsCap),
	}, c, nil
}

// struct field

type structFieldTypeParseFunc func(p *typeParser) (structField, int, error)

var structFieldTypeParseFunc64 = func(p *typeParser) (structField, int, error) {
	var typ structField
	c, err := p.readType(&typ)
	return typ, c, err
}

var structFieldTypeParseFunc32 = func(p *typeParser) (structField, int, error) {
	var typ structField32
	c, err := p.readType(&typ)
	if err != nil {
		return structField{}, c, err
	}

	return structField{
		Name:        uint64(typ.Name),
		Typ:         uint64(typ.Typ),
		OffsetEmbed: uint64(typ.OffsetEmbed),
	}, c, nil
}

// uintptr

type readUintFunc func(p *typeParser) (uint64, int, error)

var readUintFunc64 = func(p *typeParser) (uint64, int, error) {
	ptr, err := readUIntTo64(p.r, p.order, false)
	count := binary.Size(ptr)
	return ptr, count, err
}

var readUintFunc32 = func(p *typeParser) (uint64, int, error) {
	var ptr uint32
	err := binary.Read(p.r, p.order, &ptr)
	count := binary.Size(ptr)
	return uint64(ptr), count, err
}

// uncommon

type uncommonTypeParseFunc func(p *typeParser) (uncommonType, int, error)

var uncommonTypeParseFunc64 = func(p *typeParser) (uncommonType, int, error) {
	var typ uncommonType
	c, err := p.readType(&typ)
	return typ, c, err
}

var uncommonTypeParseFunc17Beta1 = func(p *typeParser) (uncommonType, int, error) {
	var typ uncommonTypeGo1_7beta1
	c, err := p.readType(&typ)
	return uncommonType{
		PkgPath: typ.PkgPath,
		Mcount:  typ.Mcount,
		Moff:    uint32(typ.Moff),
	}, c, err
}

type nameLenParseFunc func(p *typeParser, offset uint64) (uint64, int)

var nameLenParseFuncTwoByteFixed = func(p *typeParser, offset uint64) (uint64, int) {
	return uint64(uint16(p.typesData[offset])<<8 | uint16(p.typesData[offset+1])), 2
}

var nameLenParseFuncVarint = func(p *typeParser, offset uint64) (uint64, int) {
	return binary.Uvarint(p.typesData[offset:])
}

/*
	Data types
*/

// The following structs are derived from internal types in the reflect package.

type rtypeGo64 struct {
	Size       uint64
	Ptrdata    uint64
	Hash       uint32
	Tflag      uint8
	Align      uint8
	FieldAlign uint8
	Kind       uint8
	Equal      uint64 //func(uint64, uint64) bool
	Gcdata     uint64
	Str        int32
	PtrToThis  int32
}

type rtypeGo32 struct {
	Size       uint32
	Ptrdata    uint32
	Hash       uint32
	Tflag      uint8
	Align      uint8
	FieldAlign uint8
	Kind       uint8
	Equal      uint32 //func(uint32, uint32) bool
	Gcdata     uint32
	Str        int32
	PtrToThis  int32
}

type arrayType64 struct {
	Eem   uint64
	Slice uint64
	Len   uint64
}

type arrayType32 struct {
	Eem   uint32
	Slice uint32
	Len   uint32
}

type chanType struct {
	Elem uint64
	Dir  uint64
}

type chanType32 struct {
	Elem uint32
	Dir  uint32
}

// funcType is a unified type for both the current funcTypes. This type is
// returned by the parse function while the other funcTypes are used to read
// the data from the binary.
type funcType struct {
	// InCount is the number of function arguments.
	InCount uint64
	// OutCount is the number of function returns.
	OutCount uint64
}

type funcType64 struct {
	InCount  uint16
	OutCount uint16
	_        uint32 // padding
}

type funcType32 struct {
	InCount  uint16
	OutCount uint16
}

type imethod struct {
	Name int32
	Typ  int32
}

type interfaceType struct {
	PkgPath    uint64
	Methods    uint64
	MethodsLen uint64
	MethodsCap uint64
}

type interfaceType32 struct {
	PkgPath    uint32
	Methods    uint32
	MethodsLen uint32
	MethodsCap uint32
}

// Map structure used for Go 1.14 to current.
type mapType struct {
	Key        uint64
	Elem       uint64
	Bucket     uint64
	Hasher     uint64
	Keysize    uint8
	Valuesize  uint8
	Bucketsize uint16
	Flags      uint32
}

type mapType32 struct {
	Key        uint32
	Elem       uint32
	Bucket     uint32
	Hasher     uint32
	Keysize    uint8
	Valuesize  uint8
	Bucketsize uint16
	Flags      uint32
}

// Map structure for Go 1.7 to Go 1.10
type mapTypeGo1764 struct {
	Key           uint64
	Elem          uint64
	Bucket        uint64
	Hmap          uint64
	Keysize       uint8
	Indirectkey   uint8
	Valuesize     uint8
	Indirectvalue uint8
	Bucketsize    uint16
	Reflexivekey  bool
	Needkeyupdate bool
}

type mapTypeGo1732 struct {
	Key           uint32
	Elem          uint32
	Bucket        uint32
	Hmap          uint32
	Keysize       uint8
	Indirectkey   uint8
	Valuesize     uint8
	Indirectvalue uint8
	Bucketsize    uint16
	Reflexivekey  bool
	Needkeyupdate bool
}

// Map structure used for Go 1.11
type mapTypeGo1164 struct {
	Key           uint64
	Elem          uint64
	Bucket        uint64
	Keysize       uint8
	Indirectkey   uint8
	Valuesize     uint8
	Indirectvalue uint8
	Bucketsize    uint16
	Reflexivekey  bool
	Needkeyupdate bool
}

type mapTypeGo1132 struct {
	Key           uint32
	Elem          uint32
	Bucket        uint32
	Keysize       uint8
	Indirectkey   uint8
	Valuesize     uint8
	Indirectvalue uint8
	Bucketsize    uint16
	Reflexivekey  bool
	Needkeyupdate bool
}

// Map structure used for Go 1.12 and Go 1.13
type mapTypeGo1264 struct {
	Key        uint64
	Elem       uint64
	Bucket     uint64
	Keysize    uint8
	Valuesize  uint8
	Bucketsize uint16
	Flags      uint32
}

type mapTypeGo1232 struct {
	Key        uint32
	Elem       uint32
	Bucket     uint32
	Keysize    uint8
	Valuesize  uint8
	Bucketsize uint16
	Flags      uint32
}

// Method on non-interface type
type method struct {
	Name int32
	Mtyp int32
	Ifn  int32
	Tfn  int32
}

type structType64 struct {
	PkgPath    uint64
	FieldsData uint64
	FieldsLen  uint64
	FieldsCap  uint64
}

type structType32 struct {
	PkgPath    uint32
	FieldsData uint32
	FieldsLen  uint32
	FieldsCap  uint32
}

type structField32 struct {
	Name        uint32
	Typ         uint32
	OffsetEmbed uint32
}

type structField struct {
	Name        uint64
	Typ         uint64
	OffsetEmbed uint64
}

type uncommonType struct {
	PkgPath int32
	Mcount  uint16
	Xcount  uint16
	Moff    uint32
	_       uint32
}

type uncommonTypeGo1_7beta1 struct {
	PkgPath int32
	Mcount  uint16
	Moff    uint16
}
