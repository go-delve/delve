// Package frame contains data structures and
// related functions for parsing and searching
// through Dwarf .debug_frame data.
package frame

import (
	"bytes"
	"encoding/binary"
	"unicode/utf8"
)

type parsefunc func(*parseContext) (parsefunc, *parseContext)

type parseContext struct {
	Buf     *bytes.Buffer
	Entries CommonEntries
	Common  *CommonInformationEntry
	Frame   *FrameDescriptorEntry
	Length  uint32
}

type CommonEntries []*CommonInformationEntry

// Represents a Common Information Entry in
// the Dwarf .debug_frame section.
type CommonInformationEntry struct {
	Length                 uint32
	CIE_id                 uint32
	Version                uint8
	Augmentation           string
	CodeAlignmentFactor    uint64
	DataAlignmentFactor    uint64
	ReturnAddressRegister  byte
	InitialInstructions    []byte
	FrameDescriptorEntries []*FrameDescriptorEntry
}

// Represents a Frame Descriptor Entry in the
// Dwarf .debug_frame section.
type FrameDescriptorEntry struct {
	Length          uint32
	CIE_pointer     *CommonInformationEntry
	InitialLocation uint64
	AddressRange    uint64
	Instructions    []byte
}

const (
	DW_CFA_advance_loc        = (0x1 << 6) // High 2 bits: 0x1, low 6: delta
	DW_CFA_offset             = (0x2 << 6) // High 2 bits: 0x2, low 6: register
	DW_CFA_restore            = (0x3 << 6) // High 2 bits: 0x3, low 6: register
	DW_CFA_nop                = 0x0        // No ops
	DW_CFA_set_loc            = 0x1        // op1: address
	DW_CFA_advance_loc1       = iota       // op1: 1-bytes delta
	DW_CFA_advance_loc2                    // op1: 2-byte delta
	DW_CFA_advance_loc4                    // op1: 4-byte delta
	DW_CFA_offset_extended                 // op1: ULEB128 register, op2: ULEB128 offset
	DW_CFA_restore_extended                // op1: ULEB128 register
	DW_CFA_undefined                       // op1: ULEB128 register
	DW_CFA_same_value                      // op1: ULEB128 register
	DW_CFA_register                        // op1: ULEB128 register, op2: ULEB128 register
	DW_CFA_remember_state                  // No ops
	DW_CFA_restore_state                   // No ops
	DW_CFA_def_cfa                         // op1: ULEB128 register, op2: ULEB128 offset
	DW_CFA_def_cfa_register                // op1: ULEB128 register
	DW_CFA_def_cfa_offset                  // op1: ULEB128 offset
	DW_CFA_def_cfa_expression              // op1: BLOCK
	DW_CFA_expression                      // op1: ULEB128 register, op2: BLOCK
	DW_CFA_offset_extended_sf              // op1: ULEB128 register, op2: SLEB128 offset
	DW_CFA_def_cfa_sf                      // op1: ULEB128 register, op2: SLEB128 offset
	DW_CFA_def_cfa_offset_sf               // op1: SLEB128 offset
	DW_CFA_val_offset                      // op1: ULEB128, op2: ULEB128
	DW_CFA_val_offset_sf                   // op1: ULEB128, op2: SLEB128
	DW_CFA_val_expression                  // op1: ULEB128, op2: BLOCK
	DW_CFA_lo_user            = 0x1c       // op1: BLOCK
	DW_CFA_hi_user            = 0x3f       // op1: ULEB128 register, op2: BLOCK
)

// Parse takes in data (a byte slice) and returns a slice of
// CommonInformationEntry structures. Each CommonInformationEntry
// has a slice of FrameDescriptorEntry structures.
func Parse(data []byte) CommonEntries {
	var (
		length  uint32
		entries CommonEntries
		buf     = bytes.NewBuffer(data)
		pctx    = &parseContext{Buf: buf, Entries: entries, Length: length}
	)

	for fn := parseLength; buf.Len() != 0; {
		fn, pctx = fn(pctx)
	}

	return pctx.Entries
}

// decodeULEB128 decodes a Little Endian Base 128
// represented number.
func decodeULEB128(buf *bytes.Buffer) (uint64, uint32) {
	var (
		result uint64
		shift  uint64
		length uint32
	)

	for {
		b, err := buf.ReadByte()
		if err != nil {
			panic("Could not parse LEB128 value")
		}
		length++

		result |= uint64((uint(b) & 0x7f) << shift)

		// If high order bit is 1.
		if b&0x80 == 0 {
			break
		}

		shift += 7
	}

	return result, length
}

func cieEntry(data []byte) bool {
	return bytes.Equal(data, []byte{0xff, 0xff, 0xff, 0xff})
}

func parseLength(ctx *parseContext) (parsefunc, *parseContext) {
	binary.Read(ctx.Buf, binary.LittleEndian, &ctx.Length)

	if cieEntry(ctx.Buf.Bytes()[0:4]) {
		ctx.Common = &CommonInformationEntry{Length: ctx.Length}
		ctx.Entries = append(ctx.Entries, ctx.Common)
		return parseCIEID, ctx
	}

	ctx.Frame = &FrameDescriptorEntry{Length: ctx.Length, CIE_pointer: ctx.Common}
	ctx.Common.FrameDescriptorEntries = append(ctx.Common.FrameDescriptorEntries, ctx.Frame)

	// We aren't reading the CIE pointer from this section so just move the cursor past it.
	ctx.Buf.Next(4)

	return parseInitialLocation, ctx
}

func parseInitialLocation(ctx *parseContext) (parsefunc, *parseContext) {
	binary.Read(ctx.Buf, binary.LittleEndian, &ctx.Frame.InitialLocation)
	ctx.Length -= 4

	return parseAddressRange, ctx
}

func parseAddressRange(ctx *parseContext) (parsefunc, *parseContext) {
	binary.Read(ctx.Buf, binary.LittleEndian, &ctx.Frame.AddressRange)
	ctx.Length -= 4

	return parseFrameInstructions, ctx
}

func parseFrameInstructions(ctx *parseContext) (parsefunc, *parseContext) {
	// The rest of this entry consists of the instructions
	// so we can just grab all of the data from the buffer
	// cursor to length.
	var buf = make([]byte, ctx.Length)

	binary.Read(ctx.Buf, binary.LittleEndian, &buf)
	ctx.Frame.Instructions = buf
	ctx.Length = 0

	return parseLength, ctx
}

func parseCIEID(ctx *parseContext) (parsefunc, *parseContext) {
	binary.Read(ctx.Buf, binary.LittleEndian, &ctx.Common.CIE_id)
	ctx.Length -= 4

	return parseVersion, ctx
}

func parseVersion(ctx *parseContext) (parsefunc, *parseContext) {
	binary.Read(ctx.Buf, binary.LittleEndian, &ctx.Common.Version)
	ctx.Length -= 1

	return parseAugmentation, ctx
}

func parseAugmentation(ctx *parseContext) (parsefunc, *parseContext) {
	var str, c = parseString(ctx.Buf)

	ctx.Common.Augmentation = str
	ctx.Length -= c

	return parseCodeAlignmentFactor, ctx
}

func parseCodeAlignmentFactor(ctx *parseContext) (parsefunc, *parseContext) {
	var caf, c = decodeULEB128(ctx.Buf)

	ctx.Common.CodeAlignmentFactor = caf
	ctx.Length -= c

	return parseDataAlignmentFactor, ctx
}

func parseDataAlignmentFactor(ctx *parseContext) (parsefunc, *parseContext) {
	var daf, c = decodeULEB128(ctx.Buf)

	ctx.Common.DataAlignmentFactor = daf
	ctx.Length -= c

	return parseReturnAddressRegister, ctx
}

func parseReturnAddressRegister(ctx *parseContext) (parsefunc, *parseContext) {
	binary.Read(ctx.Buf, binary.LittleEndian, &ctx.Common.ReturnAddressRegister)
	ctx.Length -= 1

	return parseInitialInstructions, ctx
}

func parseInitialInstructions(ctx *parseContext) (parsefunc, *parseContext) {
	// The rest of this entry consists of the instructions
	// so we can just grab all of the data from the buffer
	// cursor to length.
	var buf = make([]byte, ctx.Length)

	binary.Read(ctx.Buf, binary.LittleEndian, &buf)
	ctx.Common.InitialInstructions = buf
	ctx.Length = 0

	return parseLength, ctx
}

func parseString(data *bytes.Buffer) (string, uint32) {
	var (
		size uint32
		str  []rune
		strb []byte
	)

	for {
		b, err := data.ReadByte()
		if err != nil {
			panic("parseString(): Could not read byte")
		}
		size++

		if b == 0x0 {
			if size == 1 {
				return "", size
			}

			break
		}

		strb = append(strb, b)

		if utf8.FullRune(strb) {
			r, _ := utf8.DecodeRune(strb)
			str = append(str, r)
			size++
			strb = strb[0:0]
		}
	}

	return string(str), size
}
