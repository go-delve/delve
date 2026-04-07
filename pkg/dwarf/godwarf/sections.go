package godwarf

import (
	"bytes"
	"compress/zlib"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"encoding/binary"
	"fmt"
	"io"
)

// GetDebugSectionElf returns the data contents of the specified debug
// section, decompressing it if it is compressed.
// For example GetDebugSectionElf("line") will return the contents of
// .debug_line, if .debug_line doesn't exist it will try to return the
// decompressed contents of .zdebug_line.
func GetDebugSectionElf(f *elf.File, name string) ([]byte, error) {
	sec := f.Section(".debug_" + name)
	if sec != nil {
		return sec.Data()
	}
	sec = f.Section(".zdebug_" + name)
	if sec == nil {
		return nil, fmt.Errorf("could not find .debug_%s section", name)
	}
	b, err := sec.Data()
	if err != nil {
		return nil, err
	}
	return decompressMaybe(b)
}

// GetDebugSectionPE returns the data contents of the specified debug
// section, decompressing it if it is compressed.
// For example GetDebugSectionPE("line") will return the contents of
// .debug_line, if .debug_line doesn't exist it will try to return the
// decompressed contents of .zdebug_line.
func GetDebugSectionPE(f *pe.File, name string) ([]byte, error) {
	sec := f.Section(".debug_" + name)
	if sec != nil {
		return peSectionData(sec)
	}
	sec = f.Section(".zdebug_" + name)
	if sec == nil {
		return nil, fmt.Errorf("could not find .debug_%s section", name)
	}
	b, err := peSectionData(sec)
	if err != nil {
		return nil, err
	}
	return decompressMaybe(b)
}

func peSectionData(sec *pe.Section) ([]byte, error) {
	b, err := sec.Data()
	if err != nil {
		return nil, err
	}
	if 0 < sec.VirtualSize && sec.VirtualSize < sec.Size {
		b = b[:sec.VirtualSize]
	}
	return b, nil
}

// GetDebugSectionMacho returns the data contents of the specified debug
// section, decompressing it if it is compressed.
// For example GetDebugSectionMacho("line") will return the contents of
// __debug_line, if __debug_line doesn't exist it will try to return the
// decompressed contents of __zdebug_line.
func GetDebugSectionMacho(f *macho.File, name string) ([]byte, error) {
	sec := getDebugSectionMachoHelper(f, "__debug_"+name)
	if sec != nil {
		return sec.Data()
	}
	sec = getDebugSectionMachoHelper(f, "__zdebug_"+name)
	if sec == nil {
		return nil, fmt.Errorf("could not find .debug_%s section", name)
	}
	b, err := sec.Data()
	if err != nil {
		return nil, err
	}
	return decompressMaybe(b)
}

// getDebugSectionMachoHelper does the same thing as
// debug/macho.(*File).Section but works around the Mach-O section name
// length limitation: the first section matching the first 16 characters of
// name will be returned.
// Because of what the valid names of DWARFv5 sections are (appendix B)
// there are no risks of collision.
func getDebugSectionMachoHelper(f *macho.File, name string) *macho.Section {
	if len(name) > 16 {
		name = name[:16]
	}
	return f.Section(name)
}

func decompressMaybe(b []byte) ([]byte, error) {
	if len(b) < 12 || string(b[:4]) != "ZLIB" {
		// not compressed
		return b, nil
	}

	dlen := binary.BigEndian.Uint64(b[4:12])
	dbuf := make([]byte, dlen)
	r, err := zlib.NewReader(bytes.NewBuffer(b[12:]))
	if err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(r, dbuf); err != nil {
		return nil, err
	}
	if err := r.Close(); err != nil {
		return nil, err
	}
	return dbuf, nil
}
