// Package frame contains data structures and
// related functions for parsing and searching
// through Dwarf .debug_frame data.
package frame

import (
	"bytes"
	"encoding/binary"
	"unicode/utf8"
)

type parsefunc func(*bytes.Buffer, CommonEntries, uint32) (parsefunc, CommonEntries, uint32)

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
		reader  = bytes.NewBuffer(data)
	)

	for fn := parseLength; reader.Len() != 0; {
		fn, entries, length = fn(reader, entries, length)
	}

	return entries
}

// DecodeLEB128 decodes a Little Endian Base 128
// represented number.
func DecodeLEB128(reader *bytes.Buffer) (uint64, uint32) {
	var (
		result uint64
		shift  uint64
		length uint32
	)

	for {
		b, err := reader.ReadByte()
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

func parseLength(reader *bytes.Buffer, entries CommonEntries, length uint32) (parsefunc, CommonEntries, uint32) {
	binary.Read(reader, binary.LittleEndian, &length)

	if cieEntry(reader.Bytes()[0:4]) {
		return parseCIEID, append(entries, &CommonInformationEntry{Length: length}), length
	}

	entry := entries[len(entries)-1]
	entry.FrameDescriptorEntries = append(entry.FrameDescriptorEntries, &FrameDescriptorEntry{Length: length, CIE_pointer: entry})

	return parseInitialLocation, entries, length
}

func parseInitialLocation(reader *bytes.Buffer, entries CommonEntries, length uint32) (parsefunc, CommonEntries, uint32) {
	var (
		frameEntries = entries[len(entries)-1].FrameDescriptorEntries
		frame        = frameEntries[len(frameEntries)-1]
	)

	binary.Read(reader, binary.LittleEndian, &frame.InitialLocation)

	return parseAddressRange, entries, length - 4
}

func parseAddressRange(reader *bytes.Buffer, entries CommonEntries, length uint32) (parsefunc, CommonEntries, uint32) {
	var (
		frameEntries = entries[len(entries)-1].FrameDescriptorEntries
		frame        = frameEntries[len(frameEntries)-1]
	)

	binary.Read(reader, binary.LittleEndian, &frame.AddressRange)

	return parseFrameInstructions, entries, length - 4
}

func parseFrameInstructions(reader *bytes.Buffer, entries CommonEntries, length uint32) (parsefunc, CommonEntries, uint32) {
	var (
		// The rest of this entry consists of the instructions
		// so we can just grab all of the data from the buffer
		// cursor to length.
		buf          = make([]byte, length)
		frameEntries = entries[len(entries)-1].FrameDescriptorEntries
		frame        = frameEntries[len(frameEntries)-1]
	)

	binary.Read(reader, binary.LittleEndian, &buf)
	frame.Instructions = buf

	return parseLength, entries, 0
}

func parseCIEID(reader *bytes.Buffer, entries CommonEntries, length uint32) (parsefunc, CommonEntries, uint32) {
	var entry = entries[len(entries)-1]

	binary.Read(reader, binary.LittleEndian, &entry.CIE_id)

	return parseVersion, entries, length - 4
}

func parseVersion(reader *bytes.Buffer, entries CommonEntries, length uint32) (parsefunc, CommonEntries, uint32) {
	entry := entries[len(entries)-1]

	binary.Read(reader, binary.LittleEndian, &entry.Version)

	return parseAugmentation, entries, length - 1
}

func parseAugmentation(reader *bytes.Buffer, entries CommonEntries, length uint32) (parsefunc, CommonEntries, uint32) {
	var (
		entry  = entries[len(entries)-1]
		str, c = parseString(reader)
	)

	entry.Augmentation = str
	return parseCodeAlignmentFactor, entries, length - c
}

func parseCodeAlignmentFactor(reader *bytes.Buffer, entries CommonEntries, length uint32) (parsefunc, CommonEntries, uint32) {
	var (
		entry  = entries[len(entries)-1]
		caf, c = DecodeLEB128(reader)
	)

	entry.CodeAlignmentFactor = caf

	return parseDataAlignmentFactor, entries, length - c
}

func parseDataAlignmentFactor(reader *bytes.Buffer, entries CommonEntries, length uint32) (parsefunc, CommonEntries, uint32) {
	var (
		entry  = entries[len(entries)-1]
		daf, c = DecodeLEB128(reader)
	)

	entry.DataAlignmentFactor = daf

	return parseReturnAddressRegister, entries, length - c
}

func parseReturnAddressRegister(reader *bytes.Buffer, entries CommonEntries, length uint32) (parsefunc, CommonEntries, uint32) {
	entry := entries[len(entries)-1]

	binary.Read(reader, binary.LittleEndian, &entry.ReturnAddressRegister)

	return parseInitialInstructions, entries, length - 1
}

func parseInitialInstructions(reader *bytes.Buffer, entries CommonEntries, length uint32) (parsefunc, CommonEntries, uint32) {
	var (
		// The rest of this entry consists of the instructions
		// so we can just grab all of the data from the buffer
		// cursor to length.
		buf   = make([]byte, length)
		entry = entries[len(entries)-1]
	)

	binary.Read(reader, binary.LittleEndian, &buf)
	entry.InitialInstructions = buf

	return parseLength, entries, 0
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
