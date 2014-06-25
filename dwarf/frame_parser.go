// Package frame contains data structures and
// related functions for parsing and searching
// through Dwarf .debug_frame data.
package frame

import (
	"bytes"
	"encoding/binary"
	"io"
	"unicode/utf8"
)

type parsefunc func(*parseContext) parsefunc

type parseContext struct {
	Buf     *bytes.Buffer
	Entries FrameDescriptionEntries
	Common  *CommonInformationEntry
	Frame   *FrameDescriptionEntry
	Length  uint32
}

// Parse takes in data (a byte slice) and returns a slice of
// CommonInformationEntry structures. Each CommonInformationEntry
// has a slice of FrameDescriptionEntry structures.
func Parse(data []byte) FrameDescriptionEntries {
	var (
		buf  = bytes.NewBuffer(data)
		pctx = &parseContext{Buf: buf}
	)

	for fn := parseLength; buf.Len() != 0; {
		fn = fn(pctx)
	}

	return pctx.Entries
}

// decodeULEB128 decodes an unsigned Little Endian Base 128
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
			panic("Could not parse ULEB128 value")
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

// decodeSLEB128 decodes an signed Little Endian Base 128
// represented number.
func decodeSLEB128(buf *bytes.Buffer) (int64, uint32) {
	var (
		b      byte
		err    error
		result int64
		shift  uint64
		length uint32
	)

	for {
		b, err = buf.ReadByte()
		if err != nil {
			panic("Could not parse SLEB128 value")
		}
		length++

		result |= int64((int64(b) & 0x7f) << shift)
		shift += 7
		if b&0x80 == 0 {
			break
		}
	}

	if (shift < 8*uint64(length)) && (b&0x40 > 0) {
		result |= -(1 << shift)
	}

	return result, length
}

func cieEntry(data []byte) bool {
	return bytes.Equal(data, []byte{0xff, 0xff, 0xff, 0xff})
}

func parseLength(ctx *parseContext) parsefunc {
	var fn parsefunc

	binary.Read(ctx.Buf, binary.LittleEndian, &ctx.Length)
	cieid := ctx.Buf.Next(4)

	if cieEntry(cieid) {
		ctx.Common = &CommonInformationEntry{Length: ctx.Length}
		fn = parseVersion
	} else {
		ctx.Frame = &FrameDescriptionEntry{Length: ctx.Length, CIE: ctx.Common, AddressRange: &addrange{}}
		ctx.Entries = append(ctx.Entries, ctx.Frame)
		fn = parseInitialLocation
	}

	// Take off the length of the CIE id / CIE pointer.
	ctx.Length -= 4

	return fn
}

func parseInitialLocation(ctx *parseContext) parsefunc {
	binary.Read(ctx.Buf, binary.LittleEndian, &ctx.Frame.AddressRange.begin)

	ctx.Length -= 8

	return parseAddressRange
}

func parseAddressRange(ctx *parseContext) parsefunc {
	binary.Read(ctx.Buf, binary.LittleEndian, &ctx.Frame.AddressRange.end)

	ctx.Length -= 8

	return parseFrameInstructions
}

func parseFrameInstructions(ctx *parseContext) parsefunc {
	// The rest of this entry consists of the instructions
	// so we can just grab all of the data from the buffer
	// cursor to length.
	var buf = make([]byte, ctx.Length)

	io.ReadFull(ctx.Buf, buf)
	ctx.Frame.Instructions = buf
	ctx.Length = 0

	return parseLength
}

func parseVersion(ctx *parseContext) parsefunc {
	binary.Read(ctx.Buf, binary.LittleEndian, &ctx.Common.Version)
	ctx.Length -= 1

	return parseAugmentation
}

func parseAugmentation(ctx *parseContext) parsefunc {
	var str, c = parseString(ctx.Buf)

	ctx.Common.Augmentation = str
	ctx.Length -= c

	return parseCodeAlignmentFactor
}

func parseCodeAlignmentFactor(ctx *parseContext) parsefunc {
	var caf, c = decodeULEB128(ctx.Buf)

	ctx.Common.CodeAlignmentFactor = caf
	ctx.Length -= c

	return parseDataAlignmentFactor
}

func parseDataAlignmentFactor(ctx *parseContext) parsefunc {
	var daf, c = decodeSLEB128(ctx.Buf)

	ctx.Common.DataAlignmentFactor = daf
	ctx.Length -= c

	return parseReturnAddressRegister
}

func parseReturnAddressRegister(ctx *parseContext) parsefunc {
	reg, c := decodeULEB128(ctx.Buf)
	ctx.Common.ReturnAddressRegister = uint8(reg)
	ctx.Length -= c

	return parseInitialInstructions
}

func parseInitialInstructions(ctx *parseContext) parsefunc {
	// The rest of this entry consists of the instructions
	// so we can just grab all of the data from the buffer
	// cursor to length.
	var buf = make([]byte, ctx.Length)

	binary.Read(ctx.Buf, binary.LittleEndian, &buf)
	ctx.Common.InitialInstructions = buf
	ctx.Length = 0

	return parseLength
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
