package dwarfbuilder

import (
	"bytes"
	"debug/dwarf"
	"encoding/binary"

	"github.com/derekparker/delve/pkg/dwarf/godwarf"
	"github.com/derekparker/delve/pkg/dwarf/util"
)

// Form represents a DWARF form kind (see Figure 20, page 160 and following,
// DWARF v4)
type Form uint16

const (
	DW_FORM_addr         Form = 0x01 // address
	DW_FORM_block2       Form = 0x03 // block
	DW_FORM_block4       Form = 0x04 // block
	DW_FORM_data2        Form = 0x05 // constant
	DW_FORM_data4        Form = 0x06 // constant
	DW_FORM_data8        Form = 0x07 // constant
	DW_FORM_string       Form = 0x08 // string
	DW_FORM_block        Form = 0x09 // block
	DW_FORM_block1       Form = 0x0a // block
	DW_FORM_data1        Form = 0x0b // constant
	DW_FORM_flag         Form = 0x0c // flag
	DW_FORM_sdata        Form = 0x0d // constant
	DW_FORM_strp         Form = 0x0e // string
	DW_FORM_udata        Form = 0x0f // constant
	DW_FORM_ref_addr     Form = 0x10 // reference
	DW_FORM_ref1         Form = 0x11 // reference
	DW_FORM_ref2         Form = 0x12 // reference
	DW_FORM_ref4         Form = 0x13 // reference
	DW_FORM_ref8         Form = 0x14 // reference
	DW_FORM_ref_udata    Form = 0x15 // reference
	DW_FORM_indirect     Form = 0x16 // (see Section 7.5.3)
	DW_FORM_sec_offset   Form = 0x17 // lineptr, loclistptr, macptr, rangelistptr
	DW_FORM_exprloc      Form = 0x18 // exprloc
	DW_FORM_flag_present Form = 0x19 // flag
	DW_FORM_ref_sig8     Form = 0x20 // reference
)

// Encoding represents a DWARF base type encoding (see section 7.8, page 168
// and following, DWARF v4).
type Encoding uint16

const (
	DW_ATE_address         Encoding = 0x01
	DW_ATE_boolean         Encoding = 0x02
	DW_ATE_complex_float   Encoding = 0x03
	DW_ATE_float           Encoding = 0x04
	DW_ATE_signed          Encoding = 0x05
	DW_ATE_signed_char     Encoding = 0x06
	DW_ATE_unsigned        Encoding = 0x07
	DW_ATE_unsigned_char   Encoding = 0x08
	DW_ATE_imaginary_float Encoding = 0x09
	DW_ATE_packed_decimal  Encoding = 0x0a
	DW_ATE_numeric_string  Encoding = 0x0b
	DW_ATE_edited          Encoding = 0x0c
	DW_ATE_signed_fixed    Encoding = 0x0d
	DW_ATE_unsigned_fixed  Encoding = 0x0e
	DW_ATE_decimal_float   Encoding = 0x0f
	DW_ATE_UTF             Encoding = 0x10
	DW_ATE_lo_user         Encoding = 0x80
	DW_ATE_hi_user         Encoding = 0xff
)

// Address represents a machine address.
type Address uint64

type tagDescr struct {
	tag dwarf.Tag

	attr     []dwarf.Attr
	form     []Form
	children bool
}

type tagState struct {
	off dwarf.Offset
	tagDescr
}

// TagOpen starts a new DIE, call TagClose after adding all attributes and
// children elements.
func (b *Builder) TagOpen(tag dwarf.Tag, name string) dwarf.Offset {
	if len(b.tagStack) > 0 {
		b.tagStack[len(b.tagStack)-1].children = true
	}
	ts := &tagState{off: dwarf.Offset(b.info.Len())}
	ts.tag = tag
	b.info.WriteByte(0)
	b.tagStack = append(b.tagStack, ts)
	b.Attr(dwarf.AttrName, name)

	return ts.off
}

// SetHasChildren sets the current DIE as having children (even if none are added).
func (b *Builder) SetHasChildren() {
	if len(b.tagStack) <= 0 {
		panic("NoChildren with no open tags")
	}
	b.tagStack[len(b.tagStack)-1].children = true
}

// TagClose closes the current DIE.
func (b *Builder) TagClose() {
	if len(b.tagStack) <= 0 {
		panic("TagClose with no open tags")
	}
	tag := b.tagStack[len(b.tagStack)-1]
	abbrev := b.abbrevFor(tag.tagDescr)
	b.info.Bytes()[tag.off] = abbrev
	if tag.children {
		b.info.WriteByte(0)
	}
	b.tagStack = b.tagStack[:len(b.tagStack)-1]
	return
}

// Attr adds an attribute to the current DIE.
func (b *Builder) Attr(attr dwarf.Attr, val interface{}) {
	if len(b.tagStack) < 0 {
		panic("Attr with no open tags")
	}
	tag := b.tagStack[len(b.tagStack)-1]
	if tag.children {
		panic("Can't add attributes after adding children")
	}

	tag.attr = append(tag.attr, attr)

	switch x := val.(type) {
	case string:
		tag.form = append(tag.form, DW_FORM_string)
		b.info.Write([]byte(x))
		b.info.WriteByte(0)
	case uint8:
		tag.form = append(tag.form, DW_FORM_data1)
		binary.Write(&b.info, binary.LittleEndian, x)
	case uint16:
		tag.form = append(tag.form, DW_FORM_data2)
		binary.Write(&b.info, binary.LittleEndian, x)
	case Address:
		tag.form = append(tag.form, DW_FORM_addr)
		binary.Write(&b.info, binary.LittleEndian, x)
	case dwarf.Offset:
		tag.form = append(tag.form, DW_FORM_ref_addr)
		binary.Write(&b.info, binary.LittleEndian, x)
	case []byte:
		tag.form = append(tag.form, DW_FORM_block4)
		binary.Write(&b.info, binary.LittleEndian, uint32(len(x)))
		b.info.Write(x)
	case []LocEntry:
		tag.form = append(tag.form, DW_FORM_sec_offset)
		binary.Write(&b.info, binary.LittleEndian, uint32(b.loc.Len()))

		// base address
		binary.Write(&b.loc, binary.LittleEndian, ^uint64(0))
		binary.Write(&b.loc, binary.LittleEndian, uint64(0))

		for _, locentry := range x {
			binary.Write(&b.loc, binary.LittleEndian, uint64(locentry.Lowpc))
			binary.Write(&b.loc, binary.LittleEndian, uint64(locentry.Highpc))
			binary.Write(&b.loc, binary.LittleEndian, uint16(len(locentry.Loc)))
			b.loc.Write(locentry.Loc)
		}

		// end of loclist
		binary.Write(&b.loc, binary.LittleEndian, uint64(0))
		binary.Write(&b.loc, binary.LittleEndian, uint64(0))
	default:
		panic("unknown value type")
	}
}

func sameTagDescr(a, b tagDescr) bool {
	if a.tag != b.tag {
		return false
	}
	if len(a.attr) != len(b.attr) {
		return false
	}
	if a.children != b.children {
		return false
	}
	for i := range a.attr {
		if a.attr[i] != b.attr[i] {
			return false
		}
		if a.form[i] != b.form[i] {
			return false
		}
	}
	return true
}

// abbrevFor returns an abbrev for the given entry description. If no abbrev
// for tag already exist a new one is created.
func (b *Builder) abbrevFor(tag tagDescr) byte {
	for abbrev, descr := range b.abbrevs {
		if sameTagDescr(descr, tag) {
			return byte(abbrev + 1)
		}
	}

	b.abbrevs = append(b.abbrevs, tag)
	return byte(len(b.abbrevs))
}

func (b *Builder) makeAbbrevTable() []byte {
	var abbrev bytes.Buffer

	for i := range b.abbrevs {
		util.EncodeULEB128(&abbrev, uint64(i+1))
		util.EncodeULEB128(&abbrev, uint64(b.abbrevs[i].tag))
		if b.abbrevs[i].children {
			abbrev.WriteByte(0x01)
		} else {
			abbrev.WriteByte(0x00)
		}
		for j := range b.abbrevs[i].attr {
			util.EncodeULEB128(&abbrev, uint64(b.abbrevs[i].attr[j]))
			util.EncodeULEB128(&abbrev, uint64(b.abbrevs[i].form[j]))
		}
		util.EncodeULEB128(&abbrev, 0)
		util.EncodeULEB128(&abbrev, 0)
	}

	return abbrev.Bytes()
}

// AddSubprogram adds a subprogram declaration to debug_info, must call
// TagClose after adding all local variables and parameters.
// Will write an abbrev corresponding to a DW_TAG_subprogram, followed by a
// DW_AT_lowpc and a DW_AT_highpc.
func (b *Builder) AddSubprogram(fnname string, lowpc, highpc uint64) dwarf.Offset {
	r := b.TagOpen(dwarf.TagSubprogram, fnname)
	b.Attr(dwarf.AttrLowpc, Address(lowpc))
	b.Attr(dwarf.AttrHighpc, Address(highpc))
	return r
}

// AddVariable adds a new variable entry to debug_info.
// Will write a DW_TAG_variable, followed by a DW_AT_type and a
// DW_AT_location.
func (b *Builder) AddVariable(varname string, typ dwarf.Offset, loc interface{}) dwarf.Offset {
	r := b.TagOpen(dwarf.TagVariable, varname)
	b.Attr(dwarf.AttrType, typ)
	b.Attr(dwarf.AttrLocation, loc)
	b.TagClose()
	return r
}

// AddBaseType adds a new base type entry to debug_info.
// Will write a DW_TAG_base_type, followed by a DW_AT_encoding and a
// DW_AT_byte_size.
func (b *Builder) AddBaseType(typename string, encoding Encoding, byteSz uint16) dwarf.Offset {
	r := b.TagOpen(dwarf.TagBaseType, typename)
	b.Attr(dwarf.AttrEncoding, uint16(encoding))
	b.Attr(dwarf.AttrByteSize, byteSz)
	b.TagClose()
	return r
}

// AddStructType adds a new structure type to debug_info. Call TagClose to
// finish adding fields.
// Will write a DW_TAG_struct_type, followed by a DW_AT_byte_size.
func (b *Builder) AddStructType(typename string, byteSz uint16) dwarf.Offset {
	r := b.TagOpen(dwarf.TagStructType, typename)
	b.Attr(dwarf.AttrByteSize, byteSz)
	return r
}

// AddMember adds a new member entry to debug_info.
// Writes a DW_TAG_member followed by DW_AT_type and DW_AT_data_member_loc.
func (b *Builder) AddMember(fieldname string, typ dwarf.Offset, memberLoc []byte) dwarf.Offset {
	r := b.TagOpen(dwarf.TagMember, fieldname)
	b.Attr(dwarf.AttrType, typ)
	b.Attr(dwarf.AttrDataMemberLoc, memberLoc)
	b.TagClose()
	return r
}

// AddPointerType adds a new pointer type to debug_info.
func (b *Builder) AddPointerType(typename string, typ dwarf.Offset) dwarf.Offset {
	r := b.TagOpen(dwarf.TagPointerType, typename)
	b.Attr(dwarf.AttrType, typ)
	b.Attr(godwarf.AttrGoKind, uint8(22))
	b.TagClose()
	return r
}
