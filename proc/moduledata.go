package proc

import (
	"go/constant"
)

// delve counterpart to runtime.moduledata
type moduleData struct {
	types, etypes uintptr
}

func (dbp *Process) loadModuleData() (err error) {
	dbp.loadModuleDataOnce.Do(func() {
		scope := &EvalScope{Thread: dbp.CurrentThread, PC: 0, CFA: 0}
		var md *Variable
		md, err = scope.packageVarAddr("runtime.firstmoduledata")
		if err != nil {
			return
		}

		for md.Addr != 0 {
			var typesVar, etypesVar, nextVar *Variable
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
			if types, err = typesVar.asUint(); err != nil {
				return
			}
			if etypes, err = etypesVar.asUint(); err != nil {
				return
			}

			dbp.moduleData = append(dbp.moduleData, moduleData{uintptr(types), uintptr(etypes)})

			md = nextVar.maybeDereference()
			if md.Unreadable != nil {
				err = md.Unreadable
				return
			}
		}
	})

	return
}

func (dbp *Process) resolveNameOff(typeAddr uintptr, off uintptr) (uintptr, error) {
	// See runtime.resolveNameOff in $GOROOT/src/runtime/type.go
	if err := dbp.loadModuleData(); err != nil {
		return 0, err
	}
	for _, md := range dbp.moduleData {
		if typeAddr >= md.types && typeAddr < md.etypes {
			return md.types + off, nil
		}
	}

	scope := &EvalScope{Thread: dbp.CurrentThread, PC: 0, CFA: 0}
	reflectOffs, err := scope.packageVarAddr("runtime.reflectOffs")
	if err != nil {
		return 0, err
	}

	reflectOffsm, err := reflectOffs.structMember("m")
	if err != nil {
		return 0, err
	}

	v, err := reflectOffsm.mapAccess(newConstant(constant.MakeUint64(uint64(off)), dbp.CurrentThread))
	if err != nil {
		return 0, err
	}

	resv := v.maybeDereference()
	if resv.Unreadable != nil {
		return 0, resv.Unreadable
	}

	return resv.Addr, nil
}
