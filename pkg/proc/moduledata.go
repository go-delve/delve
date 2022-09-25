package proc

// delve counterpart to runtime.moduledata
type moduleData struct {
	text, etext   uint64
	types, etypes uint64
	typemapVar    *Variable
}

func loadModuleData(bi *BinaryInfo, mem MemoryReadWriter) ([]moduleData, error) {
	// +rtype -var firstmoduledata moduledata
	// +rtype -field moduledata.text uintptr
	// +rtype -field moduledata.types uintptr

	scope := globalScope(nil, bi, bi.Images[0], mem)
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
