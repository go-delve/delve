package native

import (
	"errors"
	"unsafe"

	"github.com/go-delve/delve/pkg/elfwriter"
	"github.com/go-delve/delve/pkg/proc"
)

/*
#include <sys/types.h>
#include <sys/user.h>
#include <libutil.h>
#include <stdlib.h>
*/
import "C"

func (p *nativeProcess) MemoryMap() ([]proc.MemoryMapEntry, error) {
	var cnt C.int
	vmentries := C.kinfo_getvmmap(C.int(p.pid), &cnt)
	if vmentries == nil {
		return nil, errors.New("kinfo_getvmmap call failed")
	}
	defer C.free(unsafe.Pointer(vmentries))
	r := make([]proc.MemoryMapEntry, 0, int(cnt))
	base := uintptr(unsafe.Pointer(vmentries))
	sz := unsafe.Sizeof(C.struct_kinfo_vmentry{})
	for i := 0; i < int(cnt); i++ {
		vmentry := (*C.struct_kinfo_vmentry)(unsafe.Pointer(base + sz*uintptr(i)))
		switch vmentry.kve_type {
		case C.KVME_TYPE_DEFAULT, C.KVME_TYPE_VNODE, C.KVME_TYPE_SWAP, C.KVME_TYPE_PHYS:
			r = append(r, proc.MemoryMapEntry{
				Addr: uint64(vmentry.kve_start),
				Size: uint64(vmentry.kve_end - vmentry.kve_start),

				Read:  vmentry.kve_protection&C.KVME_PROT_READ != 0,
				Write: vmentry.kve_protection&C.KVME_PROT_WRITE != 0,
				Exec:  vmentry.kve_protection&C.KVME_PROT_EXEC != 0,
			})
		}
	}
	return r, nil
}

func (p *nativeProcess) DumpProcessNotes(notes []elfwriter.Note, threadDone func()) (threadsDone bool, notesout []elfwriter.Note, err error) {
	return false, notes, nil
}
