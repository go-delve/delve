// Package dwarfbuilder provides a way to build DWARF sections with
// arbitrary contents.
package dwarfbuilder

import (
	"bytes"
	"debug/dwarf"
	"encoding/binary"
	"fmt"
)

// Builder dwarf builder
type Builder struct {
	info     bytes.Buffer
	loc      bytes.Buffer
	abbrevs  []tagDescr
	tagStack []*tagState
}

// New creates a new DWARF builder.
func New() *Builder {
	b := &Builder{}

	b.info.Write([]byte{
		0x0, 0x0, 0x0, 0x0, // length
		0x4, 0x0, // version
		0x0, 0x0, 0x0, 0x0, // debug_abbrev_offset
		0x8, // address_size
	})

	b.TagOpen(dwarf.TagCompileUnit, "go")
	b.Attr(dwarf.AttrLanguage, uint8(22))

	return b
}

// Build closes b and returns all the dwarf sections.
func (b *Builder) Build() (abbrev, aranges, frame, info, line, pubnames, ranges, str, loc []byte, err error) {
	b.TagClose()

	if len(b.tagStack) > 0 {
		err = fmt.Errorf("unbalanced TagOpen/TagClose %d", len(b.tagStack))
		return
	}

	abbrev = b.makeAbbrevTable()
	info = b.info.Bytes()
	binary.LittleEndian.PutUint32(info, uint32(len(info)-4))
	loc = b.loc.Bytes()

	return
}
