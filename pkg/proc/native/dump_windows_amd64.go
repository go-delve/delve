package native

import (
	"errors"
	"fmt"
	"unsafe"

	"github.com/go-delve/delve/pkg/elfwriter"
	"github.com/go-delve/delve/pkg/proc"
)

func (p *nativeProcess) MemoryMap() ([]proc.MemoryMapEntry, error) {
	var memoryMapError error
	r := []proc.MemoryMapEntry{}

	p.execPtraceFunc(func() {
		is64 := true
		if isWow64 := uint32(0); _IsWow64Process(p.os.hProcess, &isWow64) != 0 {
			if isWow64 != 0 {
				is64 = false
			}
		}

		maxaddr := uint64(1 << 48) // windows64 uses only 48 bit addresses
		if !is64 {
			maxaddr = uint64(^uint32(0))
		}

		var meminfo _MEMORY_BASIC_INFORMATION

		for addr := uint64(0); addr < maxaddr; addr += meminfo.RegionSize {
			size := _VirtualQueryEx(p.os.hProcess, uintptr(addr), &meminfo, unsafe.Sizeof(meminfo))
			if size == 0 {
				// size == 0 is an error and the only error returned by VirtualQueryEx
				// is when addr is above the highest address allocated for the
				// application.
				return
			}
			if size != unsafe.Sizeof(meminfo) {
				memoryMapError = fmt.Errorf("bad size returned by _VirtualQueryEx: %d (expected %d)", size, unsafe.Sizeof(meminfo))
				return
			}
			if addr+meminfo.RegionSize <= addr {
				// this shouldn't happen
				memoryMapError = errors.New("VirtualQueryEx wrapped around the address space or stuck")
				return
			}
			if meminfo.State == _MEM_FREE || meminfo.State == _MEM_RESERVE {
				continue
			}
			if meminfo.Protect&_PAGE_GUARD != 0 {
				// reading from this range will result in an error.
				continue
			}

			var mme proc.MemoryMapEntry
			mme.Addr = addr
			mme.Size = meminfo.RegionSize

			switch meminfo.Protect & 0xff {
			case _PAGE_EXECUTE:
				mme.Exec = true
			case _PAGE_EXECUTE_READ:
				mme.Exec = true
				mme.Read = true
			case _PAGE_EXECUTE_READWRITE:
				mme.Exec = true
				mme.Read = true
				mme.Write = true
			case _PAGE_EXECUTE_WRITECOPY:
				mme.Exec = true
				mme.Read = true
			case _PAGE_NOACCESS:
			case _PAGE_READONLY:
				mme.Read = true
			case _PAGE_READWRITE:
				mme.Read = true
				mme.Write = true
			case _PAGE_WRITECOPY:
				mme.Read = true
			}
			r = append(r, mme)
		}
	})

	return r, memoryMapError
}

func (p *nativeProcess) DumpProcessNotes(notes []elfwriter.Note, threadDone func()) (threadsDone bool, out []elfwriter.Note, err error) {
	return false, notes, nil
}
