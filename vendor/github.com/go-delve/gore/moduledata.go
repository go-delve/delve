// This file is part of GoRE.
//
// Copyright (C) 2019-2022 GoRE Authors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <http://www.gnu.org/licenses/>.

package gore

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"reflect"
)

// Moduledata extracts the file's moduledata.
func (f *GoFile) Moduledata() (Moduledata, error) {
	// We need the Go version to be defined to find the module data.
	ver, err := f.GetCompilerVersion()
	if err != nil {
		return nil, fmt.Errorf("could not get the Go version for the moduledata extraction: %w", err)
	}
	f.FileInfo.goversion = ver

	md, err := parseModuledata(f.FileInfo, f.fh)
	if err != nil {
		return nil, fmt.Errorf("error when parsing the moduledata: %w", err)
	}

	return md, nil
}

// Moduledata holds information about the layout of the executable image in memory.
type Moduledata interface {
	// Text returns the text secion.
	Text() ModuleDataSection
	// NoPtrData returns the noptrdata section.
	NoPtrData() ModuleDataSection
	// Data returns the data section.
	Data() ModuleDataSection
	// Bss returns the bss section.
	Bss() ModuleDataSection
	// NoPtrBss returns the noptrbss section.
	NoPtrBss() ModuleDataSection
	// Types returns the types section.
	Types() ModuleDataSection
	// PCLNTab returns the pclntab section.
	PCLNTab() ModuleDataSection
	// FuncTab returns the functab section.
	FuncTab() ModuleDataSection
	// ITabLinks returns the itablinks section.
	ITabLinks() ModuleDataSection
	// TypeLink returns the typelink section.
	TypeLink() ([]int32, error)
	// GoFuncValue returns the value of the 'go:func.*' symbol.
	GoFuncValue() uint64
}

type moduledata struct {
	TextAddr, TextLen           uint64
	NoPtrDataAddr, NoPtrDataLen uint64
	DataAddr, DataLen           uint64
	BssAddr, BssLen             uint64
	NoPtrBssAddr, NoPtrBssLen   uint64

	TypesAddr, TypesLen       uint64
	TypelinkAddr, TypelinkLen uint64
	ITabLinkAddr, ITabLinkLen uint64
	FuncTabAddr, FuncTabLen   uint64
	PCLNTabAddr, PCLNTabLen   uint64

	GoFuncVal uint64

	fh fileHandler
}

// Text returns the text secion.
func (m moduledata) Text() ModuleDataSection {
	return ModuleDataSection{
		Address: m.TextAddr,
		Length:  m.TextLen,
		fh:      m.fh,
	}
}

// NoPtrData returns the noptrdata section.
func (m moduledata) NoPtrData() ModuleDataSection {
	return ModuleDataSection{
		Address: m.NoPtrDataAddr,
		Length:  m.NoPtrDataLen,
		fh:      m.fh,
	}
}

// Data returns the data section.
func (m moduledata) Data() ModuleDataSection {
	return ModuleDataSection{
		Address: m.DataAddr,
		Length:  m.DataLen,
		fh:      m.fh,
	}
}

// Bss returns the bss section.
func (m moduledata) Bss() ModuleDataSection {
	return ModuleDataSection{
		Address: m.BssAddr,
		Length:  m.BssLen,
		fh:      m.fh,
	}
}

// NoPtrBss returns the noptrbss section.
func (m moduledata) NoPtrBss() ModuleDataSection {
	return ModuleDataSection{
		Address: m.NoPtrBssAddr,
		Length:  m.NoPtrBssLen,
		fh:      m.fh,
	}
}

// Types returns the types section.
func (m moduledata) Types() ModuleDataSection {
	return ModuleDataSection{
		Address: m.TypesAddr,
		Length:  m.TypesLen,
		fh:      m.fh,
	}
}

// PCLNTab returns the pclntab section.
func (m moduledata) PCLNTab() ModuleDataSection {
	return ModuleDataSection{
		Address: m.PCLNTabAddr,
		Length:  m.PCLNTabLen,
		fh:      m.fh,
	}
}

// FuncTab returns the functab section.
func (m moduledata) FuncTab() ModuleDataSection {
	return ModuleDataSection{
		Address: m.FuncTabAddr,
		Length:  m.FuncTabLen,
		fh:      m.fh,
	}
}

// ITabLinks returns the itablinks section.
func (m moduledata) ITabLinks() ModuleDataSection {
	return ModuleDataSection{
		Address: m.ITabLinkAddr,
		Length:  m.ITabLinkLen,
		fh:      m.fh,
	}
}

// TypeLink returns the typelink section.
func (m moduledata) TypeLink() ([]int32, error) {
	base, data, err := m.fh.getSectionDataFromOffset(m.TypelinkAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to get the typelink data section: %w", err)
	}

	r := bytes.NewReader(data[m.TypelinkAddr-base:])
	a := make([]int32, 0, m.TypelinkLen)
	bo := m.fh.getFileInfo().ByteOrder
	for i := uint64(0); i < m.TypelinkLen; i++ {
		// Type offsets are always int32
		var off int32
		err = binary.Read(r, bo, &off)
		if err != nil {
			return nil, fmt.Errorf("failed to read typelink item %d: %w", i, err)
		}
		a = append(a, off)
	}

	return a, nil
}

// GoFuncValue returns the value of the "go:func.*" symbol.
func (m moduledata) GoFuncValue() uint64 {
	return m.GoFuncVal
}

// ModuleDataSection is a section defined in the Moduledata structure.
type ModuleDataSection struct {
	// Address is the virtual address where the section start.
	Address uint64
	// Length is the byte length for the data in this section.
	Length uint64
	fh     fileHandler
}

// Data returns the data in the section.
func (m ModuleDataSection) Data() ([]byte, error) {
	// If we don't have any data, return an empty slice.
	if m.Length == 0 {
		return []byte{}, nil
	}
	base, data, err := m.fh.getSectionDataFromOffset(m.Address)
	if err != nil {
		return nil, fmt.Errorf("getting module data section failed: %w", err)
	}
	start := m.Address - base
	if uint64(len(data)) < start+m.Length {
		return nil, fmt.Errorf("the length of module data section is to big: address 0x%x, base 0x%x, length 0x%x", m.Address, base, m.Length)
	}
	buf := make([]byte, m.Length)
	copy(buf, data[start:start+m.Length])
	return buf, nil
}

func findModuledata(f fileHandler) ([]byte, error) {
	_, secData, err := f.getSectionData(f.moduledataSection())
	if err != nil {
		return nil, err
	}
	tabAddr, _, err := f.getPCLNTABData()
	if err != nil {
		return nil, err
	}

	// Search for moduledata
	buf := new(bytes.Buffer)
	err = binary.Write(buf, binary.LittleEndian, &tabAddr)
	if err != nil {
		return nil, err
	}
	off := bytes.Index(secData, buf.Bytes()[:intSize32])
	if off == -1 {
		return nil, errors.New("could not find moduledata")
	}
	// TODO: Verify that hit is correct.

	return secData[off : off+0x300], nil
}

func parseModuledata(fileInfo *FileInfo, f fileHandler) (moduledata, error) {
	data, err := findModuledata(f)
	if err != nil {
		return moduledata{}, err
	}

	// buf will hold the struct type that represents the data in the file we are processing.
	var buf interface{}
	is32bit := fileInfo.WordSize == intSize32

	// Determine what kind of struct we need to extract the module data from the file.

	if GoVersionCompare("go1.20rc1", fileInfo.goversion.Name) <= 0 {
		if is32bit {
			buf = &moduledata2032{}
		} else {
			buf = &moduledata2064{}
		}
	} else if GoVersionCompare("go1.18beta1", fileInfo.goversion.Name) <= 0 {
		if is32bit {
			buf = &moduledata1832{}
		} else {
			buf = &moduledata1864{}
		}
	} else if GoVersionCompare("go1.16beta1", fileInfo.goversion.Name) <= 0 {
		if is32bit {
			buf = &moduledata1632{}
		} else {
			buf = &moduledata1664{}
		}
	} else if GoVersionCompare("go1.8beta1", fileInfo.goversion.Name) <= 0 {
		if is32bit {
			buf = &moduledata832{}
		} else {
			buf = &moduledata864{}
		}
	} else if GoVersionCompare("go1.7beta1", fileInfo.goversion.Name) <= 0 {
		if is32bit {
			buf = &moduledata732{}
		} else {
			buf = &moduledata764{}
		}
	} else {
		if is32bit {
			buf = &moduledata532{}
		} else {
			buf = &moduledata564{}
		}
	}

	// Read the module struct from the file.
	r := bytes.NewReader(data)
	err = readStruct(r, fileInfo.ByteOrder, buf)
	if err != nil {
		return moduledata{}, fmt.Errorf("error when reading module data from file: %w", err)
	}

	// Convert the read struct to the type we return to the caller.
	md, err := processModuledata(buf)
	if err != nil {
		return md, fmt.Errorf("error when processing module data: %w", err)
	}

	// Add the file handler.
	md.fh = f

	return md, nil
}

func readUIntTo64(r io.Reader, byteOrder binary.ByteOrder, is32bit bool) (uint64, error) {
	if is32bit {
		var addr uint32
		err := binary.Read(r, byteOrder, &addr)
		return uint64(addr), err
	}
	var addr uint64
	err := binary.Read(r, byteOrder, &addr)
	return addr, err
}

// readStruct performs a binary read from the reader to the data object. Data object must
// be a pointer to the struct.
func readStruct(r io.Reader, byteOrder binary.ByteOrder, data interface{}) error {
	return binary.Read(r, byteOrder, data)
}

func processModuledata(data interface{}) (moduledata, error) {
	md := moduledata{}
	// This will panic if the data passed in is not a pointer to
	// a struct. But this function should not be called outside
	// of this file, we can ensure this is always the case.
	val := reflect.ValueOf(data).Elem()

	extractModFieldValue(&md, "TextAddr", val, "Text")
	extractModFieldValue(&md, "TextLen", val, "Etext")
	if md.TextLen > md.TextAddr {
		md.TextLen = md.TextLen - md.TextAddr
	}

	extractModFieldValue(&md, "NoPtrDataAddr", val, "Noptrdata")
	extractModFieldValue(&md, "NoPtrDataLen", val, "Enoptrdata")
	if md.NoPtrDataLen > md.NoPtrDataAddr {
		md.NoPtrDataLen = md.NoPtrDataLen - md.NoPtrDataAddr
	}

	extractModFieldValue(&md, "DataAddr", val, "Data")
	extractModFieldValue(&md, "DataLen", val, "Edata")
	if md.DataLen > md.DataAddr {
		md.DataLen = md.DataLen - md.DataAddr
	}

	extractModFieldValue(&md, "BssAddr", val, "Bss")
	extractModFieldValue(&md, "BssLen", val, "Ebss")
	if md.BssLen > md.BssAddr {
		md.BssLen = md.BssLen - md.BssAddr
	}

	extractModFieldValue(&md, "NoPtrBssAddr", val, "Noptrbss")
	extractModFieldValue(&md, "NoPtrBssLen", val, "Enoptrbss")
	if md.NoPtrBssLen > md.NoPtrBssAddr {
		md.NoPtrBssLen = md.NoPtrBssLen - md.NoPtrBssAddr
	}

	extractModFieldValue(&md, "TypelinkAddr", val, "Typelinks")
	extractModFieldValue(&md, "TypelinkLen", val, "Typelinkslen")
	extractModFieldValue(&md, "ITabLinkAddr", val, "Itablinks")
	extractModFieldValue(&md, "ITabLinkLen", val, "Itablinkslen")
	extractModFieldValue(&md, "FuncTabAddr", val, "Ftab")
	extractModFieldValue(&md, "FuncTabLen", val, "Ftablen")
	extractModFieldValue(&md, "PCLNTabAddr", val, "Pclntable")
	extractModFieldValue(&md, "PCLNTabLen", val, "Pclntablelen")

	extractModFieldValue(&md, "TypesAddr", val, "Types")
	extractModFieldValue(&md, "TypesLen", val, "Etypes")
	if md.TypesLen > md.TypesAddr {
		md.TypesLen = md.TypesLen - md.TypesAddr
	}

	extractModFieldValue(&md, "GoFuncVal", val, "GoFunc")

	return md, nil
}

func extractModFieldValue(md *moduledata, dst string, val reflect.Value, src string) {
	field := val.FieldByName(src)
	// Not all versions of the module struct has all the fields. If we don't have the
	// field, we skip it.
	if !field.IsValid() {
		return
	}

	// Save 32 to 64 uint casting if needed.
	var num uint64
	switch field.Interface().(type) {
	case uint64:
		num = field.Uint()
	case uint32:
		t := field.Interface().(uint32)
		num = uint64(t)
	}

	// Set the value.
	mdField := reflect.ValueOf(md).Elem().FieldByName(dst)
	mdField.SetUint(num)
}

/*
	Internal module structures from Go's runtime.
*/

// Moduledata structure for Go 1.20 and newer

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

type moduledata2032 struct {
	PcHeader                                    uint32
	Funcnametab, Funcnametablen, Funcnametabcap uint32
	Cutab, Cutablen, Cutabcap                   uint32
	Filetab, Filetablen, Filetabcap             uint32
	Pctab, Pctablen, Pctabcap                   uint32
	Pclntable, Pclntablelen, Pclntablecap       uint32
	Ftab, Ftablen, Ftabcap                      uint32
	Findfunctab                                 uint32
	Minpc, Maxpc                                uint32

	Text, Etext           uint32
	Noptrdata, Enoptrdata uint32
	Data, Edata           uint32
	Bss, Ebss             uint32
	Noptrbss, Enoptrbss   uint32
	Covctrs, Ecovctrs     uint32
	End, Gcdata, Gcbss    uint32
	Types, Etypes         uint32
	RData                 uint32
	GoFunc                uint32

	Textsectmap, Textsectmaplen, Textsectmapcap uint32
	Typelinks, Typelinkslen, Typelinkscap       uint32 // offsets from types
	Itablinks, Itablinkslen, Itablinkscap       uint32

	Ptab, Ptablen, Ptabcap uint32

	Pluginpath, Pluginpathlen             uint32
	Pkghashes, Pkghasheslen, Pkghashescap uint32

	Modulename, Modulenamelen                      uint32
	Modulehashes, Modulehasheslen, Modulehashescap uint32

	/*	These fields we are not planning to use so skipping the parsing of them.

		hasmain uint8 // 1 if module contains the main function, 0 otherwise

		gcdatamask, gcbssmask bitvector

		typemap map[typeOff]*_type // offset to *_rtype in previous module

		bad bool // module failed to load and should be ignored

		next *moduledata
	*/
}

// Moduledata structure for Go 1.18 and Go 1.19

type moduledata1864 struct {
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

type moduledata1832 struct {
	PcHeader                                    uint32
	Funcnametab, Funcnametablen, Funcnametabcap uint32
	Cutab, Cutablen, Cutabcap                   uint32
	Filetab, Filetablen, Filetabcap             uint32
	Pctab, Pctablen, Pctabcap                   uint32
	Pclntable, Pclntablelen, Pclntablecap       uint32
	Ftab, Ftablen, Ftabcap                      uint32
	Findfunctab                                 uint32
	Minpc, Maxpc                                uint32

	Text, Etext           uint32
	Noptrdata, Enoptrdata uint32
	Data, Edata           uint32
	Bss, Ebss             uint32
	Noptrbss, Enoptrbss   uint32
	End, Gcdata, Gcbss    uint32
	Types, Etypes         uint32
	RData                 uint32
	GoFunc                uint32

	Textsectmap, Textsectmaplen, Textsectmapcap uint32
	Typelinks, Typelinkslen, Typelinkscap       uint32 // offsets from types
	Itablinks, Itablinkslen, Itablinkscap       uint32

	Ptab, Ptablen, Ptabcap uint32

	Pluginpath, Pluginpathlen             uint32
	Pkghashes, Pkghasheslen, Pkghashescap uint32

	Modulename, Modulenamelen                      uint32
	Modulehashes, Modulehasheslen, Modulehashescap uint32

	/*	These fields we are not planning to use so skipping the parsing of them.

		hasmain uint8 // 1 if module contains the main function, 0 otherwise

		gcdatamask, gcbssmask bitvector

		typemap map[typeOff]*_type // offset to *_rtype in previous module

		bad bool // module failed to load and should be ignored

		next *moduledata
	*/
}

// Moduledata structure for Go 1.16 to 1.17

type moduledata1664 struct {
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
	End, Gcdata, Gcbss    uint64
	Types, Etypes         uint64

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

type moduledata1632 struct {
	PcHeader                                    uint32
	Funcnametab, Funcnametablen, Funcnametabcap uint32
	Cutab, Cutablen, Cutabcap                   uint32
	Filetab, Filetablen, Filetabcap             uint32
	Pctab, Pctablen, Pctabcap                   uint32
	Pclntable, Pclntablelen, Pclntablecap       uint32
	Ftab, Ftablen, Ftabcap                      uint32
	Findfunctab                                 uint32
	Minpc, Maxpc                                uint32

	Text, Etext           uint32
	Noptrdata, Enoptrdata uint32
	Data, Edata           uint32
	Bss, Ebss             uint32
	Noptrbss, Enoptrbss   uint32
	End, Gcdata, Gcbss    uint32
	Types, Etypes         uint32

	Textsectmap, Textsectmaplen, Textsectmapcap uint32
	Typelinks, Typelinkslen, Typelinkscap       uint32 // offsets from types
	Itablinks, Itablinkslen, Itablinkscap       uint32

	Ptab, Ptablen, Ptabcap uint32

	Pluginpath, Pluginpathlen             uint32
	Pkghashes, Pkghasheslen, Pkghashescap uint32

	Modulename, Modulenamelen                      uint32
	Modulehashes, Modulehasheslen, Modulehashescap uint32

	/*	These fields we are not planning to use so skipping the parsing of them.

		hasmain uint8 // 1 if module contains the main function, 0 otherwise

		gcdatamask, gcbssmask bitvector

		typemap map[typeOff]*_type // offset to *_rtype in previous module

		bad bool // module failed to load and should be ignored

		next *moduledata
	*/
}

// Moduledata structure for Go 1.8 to 1.15

type moduledata864 struct {
	Pclntable, Pclntablelen, Pclntablecap uint64
	Ftab, Ftablen, Ftabcap                uint64
	Filetab, Filetablen, Filetabcap       uint64
	Findfunctab                           uint64
	Minpc, Maxpc                          uint64

	Text, Etext           uint64
	Noptrdata, Enoptrdata uint64
	Data, Edata           uint64
	Bss, Ebss             uint64
	Noptrbss, Enoptrbss   uint64
	End, Gcdata, Gcbss    uint64
	Types, Etypes         uint64

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

type moduledata832 struct {
	Pclntable, Pclntablelen, Pclntablecap uint32
	Ftab, Ftablen, Ftabcap                uint32
	Filetab, Filetablen, Filetabcap       uint32
	Findfunctab                           uint32
	Minpc, Maxpc                          uint32

	Text, Etext           uint32
	Noptrdata, Enoptrdata uint32
	Data, Edata           uint32
	Bss, Ebss             uint32
	Noptrbss, Enoptrbss   uint32
	End, Gcdata, Gcbss    uint32
	Types, Etypes         uint32

	Textsectmap, Textsectmaplen, Textsectmapcap uint32
	Typelinks, Typelinkslen, Typelinkscap       uint32 // offsets from types
	Itablinks, Itablinkslen, Itablinkscap       uint32

	Ptab, Ptablen, Ptabcap uint32

	Pluginpath, Pluginpathlen             uint32
	Pkghashes, Pkghasheslen, Pkghashescap uint32

	Modulename, Modulenamelen                      uint32
	Modulehashes, Modulehasheslen, Modulehashescap uint32

	/*	These fields we are not planning to use so skipping the parsing of them.

		hasmain uint8 // 1 if module contains the main function, 0 otherwise

		gcdatamask, gcbssmask bitvector

		typemap map[typeOff]*_type // offset to *_rtype in previous module

		bad bool // module failed to load and should be ignored

		next *moduledata
	*/
}

// Moduledata structure for Go 1.7

type moduledata764 struct {
	Pclntable, Pclntablelen, Pclntablecap uint64
	Ftab, Ftablen, Ftabcap                uint64
	Filetab, Filetablen, Filetabcap       uint64
	Findfunctab                           uint64
	Minpc, Maxpc                          uint64

	Text, Etext           uint64
	Noptrdata, Enoptrdata uint64
	Data, Edata           uint64
	Bss, Ebss             uint64
	Noptrbss, Enoptrbss   uint64
	End, Gcdata, Gcbss    uint64
	Types, Etypes         uint64

	Typelinks, Typelinkslen, Typelinkscap uint64 // offsets from types
	Itablinks, Itablinkslen, Itablinkscap uint64

	Modulename, Modulenamelen                      uint64
	Modulehashes, Modulehasheslen, Modulehashescap uint64

	/*	These fields we are not planning to use so skipping the parsing of them.

		gcdatamask, gcbssmask bitvector

		typemap map[typeOff]*_type // offset to *_rtype in previous module

		next *moduledata
	*/
}

type moduledata732 struct {
	Pclntable, Pclntablelen, Pclntablecap uint32
	Ftab, Ftablen, Ftabcap                uint32
	Filetab, Filetablen, Filetabcap       uint32
	Findfunctab                           uint32
	Minpc, Maxpc                          uint32

	Text, Etext           uint32
	Noptrdata, Enoptrdata uint32
	Data, Edata           uint32
	Bss, Ebss             uint32
	Noptrbss, Enoptrbss   uint32
	End, Gcdata, Gcbss    uint32
	Types, Etypes         uint32

	Typelinks, Typelinkslen, Typelinkscap uint32 // offsets from types
	Itablinks, Itablinkslen, Itablinkscap uint32

	Modulename, Modulenamelen                      uint32
	Modulehashes, Modulehasheslen, Modulehashescap uint32

	/*	These fields we are not planning to use so skipping the parsing of them.

		gcdatamask, gcbssmask bitvector

		typemap map[typeOff]*_type // offset to *_rtype in previous module

		next *moduledata
	*/
}

// Moduledata structure for Go 1.5 to 1.6

type moduledata564 struct {
	Pclntable, Pclntablelen, Pclntablecap uint64
	Ftab, Ftablen, Ftabcap                uint64
	Filetab, Filetablen, Filetabcap       uint64
	Findfunctab                           uint64
	Minpc, Maxpc                          uint64

	Text, Etext           uint64
	Noptrdata, Enoptrdata uint64
	Data, Edata           uint64
	Bss, Ebss             uint64
	Noptrbss, Enoptrbss   uint64
	End, Gcdata, Gcbss    uint64

	Typelinks, Typelinkslen, Typelinkscap uint64

	Modulename, Modulenamelen                      uint64
	Modulehashes, Modulehasheslen, Modulehashescap uint64

	/*	These fields we are not planning to use so skipping the parsing of them.

		gcdatamask, gcbssmask bitvector

		typemap map[typeOff]*_type // offset to *_rtype in previous module

		next *moduledata
	*/
}

type moduledata532 struct {
	Pclntable, Pclntablelen, Pclntablecap uint32
	Ftab, Ftablen, Ftabcap                uint32
	Filetab, Filetablen, Filetabcap       uint32
	Findfunctab                           uint32
	Minpc, Maxpc                          uint32

	Text, Etext           uint32
	Noptrdata, Enoptrdata uint32
	Data, Edata           uint32
	Bss, Ebss             uint32
	Noptrbss, Enoptrbss   uint32
	End, Gcdata, Gcbss    uint32

	Typelinks, Typelinkslen, Typelinkscap uint32

	Modulename, Modulenamelen                      uint32
	Modulehashes, Modulehasheslen, Modulehashescap uint32

	/*	These fields we are not planning to use so skipping the parsing of them.

		gcdatamask, gcbssmask bitvector

		typemap map[typeOff]*_type // offset to *_rtype in previous module

		next *moduledata
	*/
}
