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

func (dbp *BinaryInfo) loadModuleData(thread *Thread) (err error) {
	dbp.loadModuleDataOnce.Do(func() {
		scope, _ := thread.Scope()
		var md *Variable
		md, err = scope.packageVarAddr("runtime.firstmoduledata")
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

			dbp.moduleData = append(dbp.moduleData, moduleData{uintptr(types), uintptr(etypes), typemapVar})

			md = nextVar.maybeDereference()
			if md.Unreadable != nil {
				err = md.Unreadable
				return
			}
		}
	})

	return
}

func (dbp *BinaryInfo) resolveTypeOff(typeAddr uintptr, off uintptr, thread *Thread) (*Variable, error) {
	var mem memoryReadWriter = thread
	// See runtime.(*_type).typeOff in $GOROOT/src/runtime/type.go
	if err := dbp.loadModuleData(thread); err != nil {
		return nil, err
	}

	var md *moduleData
	for i := range dbp.moduleData {
		if typeAddr >= dbp.moduleData[i].types && typeAddr < dbp.moduleData[i].etypes {
			md = &dbp.moduleData[i]
		}
	}

	rtyp, err := dbp.findType("runtime._type")
	if err != nil {
		return nil, err
	}

	if md == nil {
		v, err := dbp.reflectOffsMapAccess(off, thread)
		if err != nil {
			return nil, err
		}
		v.loadValue(LoadConfig{false, 1, 0, 0, -1})
		addr, _ := constant.Int64Val(v.Value)
		return v.newVariable(v.Name, uintptr(addr), rtyp), nil
	}

	if t, _ := md.typemapVar.mapAccess(newConstant(constant.MakeUint64(uint64(off)), mem)); t != nil {
		return t, nil
	}

	res := md.types + uintptr(off)

	return newVariable("", res, rtyp, thread.dbp, thread), nil
}

func (dbp *BinaryInfo) resolveNameOff(typeAddr uintptr, off uintptr, thread *Thread) (name, tag string, pkgpathoff int32, err error) {
	var mem memoryReadWriter = thread
	// See runtime.resolveNameOff in $GOROOT/src/runtime/type.go
	if err = dbp.loadModuleData(thread); err != nil {
		return "", "", 0, err
	}

	for _, md := range dbp.moduleData {
		if typeAddr >= md.types && typeAddr < md.etypes {
			return dbp.loadName(md.types+off, mem)
		}
	}

	v, err := dbp.reflectOffsMapAccess(off, thread)
	if err != nil {
		return "", "", 0, err
	}

	resv := v.maybeDereference()
	if resv.Unreadable != nil {
		return "", "", 0, resv.Unreadable
	}

	return dbp.loadName(resv.Addr, mem)
}

func (dbp *BinaryInfo) reflectOffsMapAccess(off uintptr, thread *Thread) (*Variable, error) {
	scope, _ := thread.Scope()
	reflectOffs, err := scope.packageVarAddr("runtime.reflectOffs")
	if err != nil {
		return nil, err
	}

	reflectOffsm, err := reflectOffs.structMember("m")
	if err != nil {
		return nil, err
	}

	return reflectOffsm.mapAccess(newConstant(constant.MakeUint64(uint64(off)), thread))
}

const (
	// flags for the name struct (see 'type name struct' in $GOROOT/src/reflect/type.go)
	nameflagExported = 1 << 0
	nameflagHasTag   = 1 << 1
	nameflagHasPkg   = 1 << 2
)

func (dbp *BinaryInfo) loadName(addr uintptr, mem memoryReadWriter) (name, tag string, pkgpathoff int32, err error) {
	off := addr
	namedata, err := mem.readMemory(off, 3)
	off += 3
	if err != nil {
		return "", "", 0, err
	}

	namelen := uint16(namedata[1]<<8) | uint16(namedata[2])

	rawstr, err := mem.readMemory(off, int(namelen))
	off += uintptr(namelen)
	if err != nil {
		return "", "", 0, err
	}

	name = string(rawstr)

	if namedata[0]&nameflagHasTag != 0 {
		taglendata, err := mem.readMemory(off, 2)
		off += 2
		if err != nil {
			return "", "", 0, err
		}
		taglen := uint16(taglendata[0]<<8) | uint16(taglendata[1])

		rawstr, err := mem.readMemory(off, int(taglen))
		off += uintptr(taglen)
		if err != nil {
			return "", "", 0, err
		}

		tag = string(rawstr)
	}

	if namedata[0]&nameflagHasPkg != 0 {
		pkgdata, err := mem.readMemory(off, 4)
		if err != nil {
			return "", "", 0, err
		}

		// see func pkgPath in $GOROOT/src/reflect/type.go
		copy((*[4]byte)(unsafe.Pointer(&pkgpathoff))[:], pkgdata)
	}

	return name, tag, pkgpathoff, nil
}
