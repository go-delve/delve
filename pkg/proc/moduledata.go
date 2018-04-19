package proc

import (
	"go/constant"
	"unsafe"
)

// delve counterpart to runtime.moduledata
type moduleData struct {
	types, etypes uintptr
	typemapVar    *Variable
}

func loadModuleData(bi *BinaryInfo, mem MemoryReadWriter) (err error) {
	bi.loadModuleDataOnce.Do(func() {
		scope := globalScope(bi, mem)
		var md *Variable
		md, err = scope.findGlobal("runtime.firstmoduledata")
		if err != nil {
			return
		}

		for md.Addr != 0 {
			var typesVar, etypesVar, nextVar, typemapVar *Variable
			var types, etypes uint64

			if typesVar, err = md.structMember("types"); err != nil {
				return
			}
			if etypesVar, err = md.structMember("etypes"); err != nil {
				return
			}
			if nextVar, err = md.structMember("next"); err != nil {
				return
			}
			if typemapVar, err = md.structMember("typemap"); err != nil {
				return
			}
			if types, err = typesVar.asUint(); err != nil {
				return
			}
			if etypes, err = etypesVar.asUint(); err != nil {
				return
			}

			bi.moduleData = append(bi.moduleData, moduleData{uintptr(types), uintptr(etypes), typemapVar})

			md = nextVar.maybeDereference()
			if md.Unreadable != nil {
				err = md.Unreadable
				return
			}
		}
	})

	return
}

func resolveTypeOff(bi *BinaryInfo, typeAddr uintptr, off uintptr, mem MemoryReadWriter) (*Variable, error) {
	// See runtime.(*_type).typeOff in $GOROOT/src/runtime/type.go
	if err := loadModuleData(bi, mem); err != nil {
		return nil, err
	}

	var md *moduleData
	for i := range bi.moduleData {
		if typeAddr >= bi.moduleData[i].types && typeAddr < bi.moduleData[i].etypes {
			md = &bi.moduleData[i]
		}
	}

	rtyp, err := bi.findType("runtime._type")
	if err != nil {
		return nil, err
	}

	if md == nil {
		v, err := reflectOffsMapAccess(bi, off, mem)
		if err != nil {
			return nil, err
		}
		v.loadValue(LoadConfig{false, 1, 0, 0, -1})
		addr, _ := constant.Int64Val(v.Value)
		return v.newVariable(v.Name, uintptr(addr), rtyp, mem), nil
	}

	if t, _ := md.typemapVar.mapAccess(newConstant(constant.MakeUint64(uint64(off)), mem)); t != nil {
		return t, nil
	}

	res := md.types + uintptr(off)

	return newVariable("", res, rtyp, bi, mem), nil
}

func resolveNameOff(bi *BinaryInfo, typeAddr uintptr, off uintptr, mem MemoryReadWriter) (name, tag string, pkgpathoff int32, err error) {
	// See runtime.resolveNameOff in $GOROOT/src/runtime/type.go
	if err = loadModuleData(bi, mem); err != nil {
		return "", "", 0, err
	}

	for _, md := range bi.moduleData {
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

func reflectOffsMapAccess(bi *BinaryInfo, off uintptr, mem MemoryReadWriter) (*Variable, error) {
	scope := globalScope(bi, mem)
	reflectOffs, err := scope.findGlobal("runtime.reflectOffs")
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

func loadName(bi *BinaryInfo, addr uintptr, mem MemoryReadWriter) (name, tag string, pkgpathoff int32, err error) {
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
	off += uintptr(namelen)
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
		off += uintptr(taglen)
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
