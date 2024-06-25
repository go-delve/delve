package proc

// ModuleData counterpart to runtime.moduleData
type ModuleData struct {
	text, etext   uint64
	types, etypes uint64
	typemapVar    *Variable
}

func LoadModuleData(bi *BinaryInfo, mem MemoryReadWriter) ([]ModuleData, error) {
	// +rtype -var firstmoduledata moduledata
	// +rtype -field moduledata.text uintptr
	// +rtype -field moduledata.types uintptr

	scope := globalScope(nil, bi, bi.Images[0], mem)
	var md *Variable
	md, err := scope.findGlobal("runtime", "firstmoduledata")
	if err != nil {
		return nil, err
	}

	r := []ModuleData{}

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

		r = append(r, ModuleData{
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

func findModuleDataForType(bi *BinaryInfo, mds []ModuleData, typeAddr uint64, mem MemoryReadWriter) *ModuleData {
	for i := range mds {
		if typeAddr >= mds[i].types && typeAddr < mds[i].etypes {
			return &mds[i]
		}
	}
	return nil
}
