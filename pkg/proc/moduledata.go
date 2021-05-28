package proc

import (
	"go/constant"
	"unsafe"
)

// delve counterpart to runtime.moduledata
type moduleData struct {
	text, etext   uint64
	types, etypes uint64
	typemapVar    *Variable
}

func loadModuleData(bi *BinaryInfo, mem MemoryReadWriter) ([]moduleData, error) {
	scope := globalScope(bi, bi.Images[0], mem)
	var md *Variable
	md, err := scope.findGlobal("runtime", "firstmoduledata")
	if err != nil {
		return nil, err
	}

	r := []moduleData{}

	for md.Addr != 0 {
		const (
			typesField   = "types"
			etypesField  = "etypes"
			textField    = "text"
			etextField   = "etext"
			nextField    = "next"
			typemapField = "typemap"
		)
		vars := map[string]*Variable{}

		for _, fieldName := range []string{typesField, etypesField, textField, etextField, nextField, typemapField} {
			var err error
			vars[fieldName], err = md.structMember(fieldName)
			if err != nil {
				return nil, err
			}

		}

		var err error

		touint := func(name string) (ret uint64) {
			if err == nil {
				var n uint64
				n, err = vars[name].asUint()
				ret = n
			}
			return ret
		}

		r = append(r, moduleData{
			types: touint(typesField), etypes: touint(etypesField),
			text: touint(textField), etext: touint(etextField),
			typemapVar: vars[typemapField],
		})
		if err != nil {
			return nil, err
		}

		md = vars[nextField].maybeDereference()
		if md.Unreadable != nil {
			return nil, md.Unreadable
		}
	}

	return r, nil
}

func findModuleDataForType(bi *BinaryInfo, mds []moduleData, typeAddr uint64, mem MemoryReadWriter) *moduleData {
	for i := range mds {
		if typeAddr >= mds[i].types && typeAddr < mds[i].etypes {
			return &mds[i]
		}
	}
	return nil
}

func resolveTypeOff(bi *BinaryInfo, mds []moduleData, typeAddr, off uint64, mem MemoryReadWriter) (*Variable, error) {
	// See runtime.(*_type).typeOff in $GOROOT/src/runtime/type.go
	md := findModuleDataForType(bi, mds, typeAddr, mem)

	rtyp, err := bi.findType("runtime._type")
	if err != nil {
		return nil, err
	}

	if md == nil {
		v, err := reflectOffsMapAccess(bi, off, mem)
		if err != nil {
			return nil, err
		}
		v.loadValue(LoadConfig{false, 1, 0, 0, -1, 0})
		addr, _ := constant.Int64Val(v.Value)
		return v.newVariable(v.Name, uint64(addr), rtyp, mem), nil
	}

	if t, _ := md.typemapVar.mapAccess(newConstant(constant.MakeUint64(uint64(off)), mem)); t != nil {
		return t, nil
	}

	res := md.types + off

	return newVariable("", uint64(res), rtyp, bi, mem), nil
}

func resolveNameOff(bi *BinaryInfo, mds []moduleData, typeAddr, off uint64, mem MemoryReadWriter) (name, tag string, pkgpathoff int32, err error) {
	// See runtime.resolveNameOff in $GOROOT/src/runtime/type.go
	for _, md := range mds {
		if typeAddr >= md.types && typeAddr < md.etypes {
			return loadName(bi, md.types+off, mem)
		}
	}

	v, err := reflectOffsMapAccess(bi, off, mem)
	if err != nil {
		return "", "", 0, err
	}

	resv := v.maybeDereference()
	if resv.Unreadable != nil {
		return "", "", 0, resv.Unreadable
	}

	return loadName(bi, resv.Addr, mem)
}

func reflectOffsMapAccess(bi *BinaryInfo, off uint64, mem MemoryReadWriter) (*Variable, error) {
	scope := globalScope(bi, bi.Images[0], mem)
	reflectOffs, err := scope.findGlobal("runtime", "reflectOffs")
	if err != nil {
		return nil, err
	}

	reflectOffsm, err := reflectOffs.structMember("m")
	if err != nil {
		return nil, err
	}

	return reflectOffsm.mapAccess(newConstant(constant.MakeUint64(uint64(off)), mem))
}

const (
	// flags for the name struct (see 'type name struct' in $GOROOT/src/reflect/type.go)
	nameflagExported = 1 << 0
	nameflagHasTag   = 1 << 1
	nameflagHasPkg   = 1 << 2
)

func loadName(bi *BinaryInfo, addr uint64, mem MemoryReadWriter) (name, tag string, pkgpathoff int32, err error) {
	off := addr
	namedata := make([]byte, 3)
	_, err = mem.ReadMemory(namedata, off)
	off += 3
	if err != nil {
		return "", "", 0, err
	}

	namelen := uint16(namedata[1])<<8 | uint16(namedata[2])

	rawstr := make([]byte, int(namelen))
	_, err = mem.ReadMemory(rawstr, off)
	off += uint64(namelen)
	if err != nil {
		return "", "", 0, err
	}

	name = string(rawstr)

	if namedata[0]&nameflagHasTag != 0 {
		taglendata := make([]byte, 2)
		_, err = mem.ReadMemory(taglendata, off)
		off += 2
		if err != nil {
			return "", "", 0, err
		}
		taglen := uint16(taglendata[0])<<8 | uint16(taglendata[1])

		rawstr := make([]byte, int(taglen))
		_, err = mem.ReadMemory(rawstr, off)
		off += uint64(taglen)
		if err != nil {
			return "", "", 0, err
		}

		tag = string(rawstr)
	}

	if namedata[0]&nameflagHasPkg != 0 {
		pkgdata := make([]byte, 4)
		_, err = mem.ReadMemory(pkgdata, off)
		if err != nil {
			return "", "", 0, err
		}

		// see func pkgPath in $GOROOT/src/reflect/type.go
		copy((*[4]byte)(unsafe.Pointer(&pkgpathoff))[:], pkgdata)
	}

	return name, tag, pkgpathoff, nil
}
