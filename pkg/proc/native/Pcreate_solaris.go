package native

import (
	"errors"
	"unsafe"
)

// #cgo LDFLAGS: -lproc
// #include <libproc.h>
import "C"

func makeArgvSlice(argv []string) []*C.char {
	s := make([]*C.char, len(argv)+1)
	for i := 0; i < len(argv); i++ {
		s[i] = C.CString(argv[i])
	}
	return s
}

func freeArgvSlice(s []*C.char) {
	for i := 0; i < len(s); i++ {
		C.free(unsafe.Pointer(s[i]))
	}
}

// Pcreate_helper creates a victim process. It returns a process handle along
// with the full path to the file, which may have been found by looking in PATH.
func Pcreate_helper(file string, argv []string) (*C.struct_ps_prochandle, string, error) {
	cFile := C.CString(file)
	defer C.free(unsafe.Pointer(cFile))
	cArgv := makeArgvSlice(argv)
	defer freeArgvSlice(cArgv)
	var cCode C.int
	cPath := make([]C.char, C.MAXPATHLEN)
	P := C.Pcreate(
		cFile,
		(**C.char)(unsafe.Pointer(&cArgv[0])),
		&cCode,
		(*C.char)(unsafe.Pointer(&cPath[0])),
		C.MAXPATHLEN)
	if P == nil {
		return nil, "", errors.New(C.GoString(C.Pcreate_error(cCode)))
	}
	return P, C.GoString((*C.char)(unsafe.Pointer(&cPath[0]))), nil
}
