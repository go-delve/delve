// This package implements the parsing and usage of the moduledata
// structs from a binary.
// This code is mostly taken and adapted from https://github.com/goretk/gore.
package moduledata

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"errors"
)

// GetGoFuncValue is used to acquire the value of the
// "go:func.*" symbol in stripped binaries.
// TODO(derekparker) support Mach-O, PE.
func GetGoFuncValue(f *elf.File, pclntabAddr uint64) (uint64, error) {
	s := f.Section(".noptrdata")
	if s == nil {
		return 0, errors.New("unable to find .noptrdata section")
	}
	data, err := s.Data()
	if err != nil {
		return 0, err
	}
	buf := new(bytes.Buffer)
	err = binary.Write(buf, binary.LittleEndian, &pclntabAddr)
	if err != nil {
		return 0, err
	}
	off := bytes.Index(data, buf.Bytes()[:8])

	var md moduledata2064
	err = binary.Read(bytes.NewReader(data[off:off+0x340]), f.ByteOrder, &md)
	if err != nil {
		return 0, err
	}
	return md.GoFunc, nil
}

type moduledata2064 struct {
	PcHeader                                    uint64
	Funcnametab, Funcnametablen, Funcnametabcap uint64
	Cutab, Cutablen, Cutabcap                   uint64
	Filetab, Filetablen, Filetabcap             uint64
	Pctab, Pctablen, Pctabcap                   uint64
	Pclntable, Pclntablelen, Pclntablecap       uint64
	Ftab, Ftablen, Ftabcap                      uint64
	Findfunctab                                 uint64
	Minpc, Maxpc                                uint64

	Text, Etext           uint64
	Noptrdata, Enoptrdata uint64
	Data, Edata           uint64
	Bss, Ebss             uint64
	Noptrbss, Enoptrbss   uint64
	Covctrs, Ecovctrs     uint64
	End, Gcdata, Gcbss    uint64
	Types, Etypes         uint64
	RData                 uint64
	GoFunc                uint64

	Textsectmap, Textsectmaplen, Textsectmapcap uint64
	Typelinks, Typelinkslen, Typelinkscap       uint64 // offsets from types
	Itablinks, Itablinkslen, Itablinkscap       uint64

	Ptab, Ptablen, Ptabcap uint64

	Pluginpath, Pluginpathlen             uint64
	Pkghashes, Pkghasheslen, Pkghashescap uint64

	Modulename, Modulenamelen                      uint64
	Modulehashes, Modulehasheslen, Modulehashescap uint64

	/*	These fields we are not planning to use so skipping the parsing of them.

		hasmain uint8 // 1 if module contains the main function, 0 otherwise

		gcdatamask, gcbssmask bitvector

		typemap map[typeOff]*_type // offset to *_rtype in previous module

		bad bool // module failed to load and should be ignored

		next *moduledata
	*/
}
