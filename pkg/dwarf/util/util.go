package util

import (
	"bytes"
	"debug/dwarf"
	"encoding/binary"
	"fmt"
	"io"
)

// ByteReaderWithLen is a io.ByteReader with a Len method. This interface is
// satisified by both bytes.Buffer and bytes.Reader.
type ByteReaderWithLen interface {
	io.ByteReader
	io.Reader
	Len() int
}

// The Little Endian Base 128 format is defined in the DWARF v4 standard,
// section 7.6, page 161 and following.

// DecodeULEB128 decodes an unsigned Little Endian Base 128
// represented number.
func DecodeULEB128(buf ByteReaderWithLen) (uint64, uint32) {
	var (
		result uint64
		shift  uint64
		length uint32
	)

	if buf.Len() == 0 {
		return 0, 0
	}

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

// DecodeSLEB128 decodes a signed Little Endian Base 128
// represented number.
func DecodeSLEB128(buf ByteReaderWithLen) (int64, uint32) {
	var (
		b      byte
		err    error
		result int64
		shift  uint64
		length uint32
	)

	if buf.Len() == 0 {
		return 0, 0
	}

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

// EncodeULEB128 encodes x to the unsigned Little Endian Base 128 format
// into out.
func EncodeULEB128(out io.ByteWriter, x uint64) {
	for {
		b := byte(x & 0x7f)
		x = x >> 7
		if x != 0 {
			b = b | 0x80
		}
		out.WriteByte(b)
		if x == 0 {
			break
		}
	}
}

// EncodeSLEB128 encodes x to the signed Little Endian Base 128 format
// into out.
func EncodeSLEB128(out io.ByteWriter, x int64) {
	for {
		b := byte(x & 0x7f)
		x >>= 7

		signb := b & 0x40

		last := false
		if (x == 0 && signb == 0) || (x == -1 && signb != 0) {
			last = true
		} else {
			b = b | 0x80
		}
		out.WriteByte(b)

		if last {
			break
		}
	}
}

// ParseString reads a null-terminated string from data.
func ParseString(data *bytes.Buffer) (string, uint32) {
	str, err := data.ReadString(0x0)
	if err != nil {
		panic("Could not parse string")
	}

	return str[:len(str)-1], uint32(len(str))
}

// ReadUintRaw reads an integer of ptrSize bytes, with the specified byte order, from reader.
func ReadUintRaw(reader io.Reader, order binary.ByteOrder, ptrSize int) (uint64, error) {
	switch ptrSize {
	case 4:
		var n uint32
		if err := binary.Read(reader, order, &n); err != nil {
			return 0, err
		}
		return uint64(n), nil
	case 8:
		var n uint64
		if err := binary.Read(reader, order, &n); err != nil {
			return 0, err
		}
		return n, nil
	}
	return 0, fmt.Errorf("not supprted ptr size %d", ptrSize)
}

// WriteUint writes an integer of ptrSize bytes to writer, in the specified byte order.
func WriteUint(writer io.Writer, order binary.ByteOrder, ptrSize int, data uint64) error {
	switch ptrSize {
	case 4:
		return binary.Write(writer, order, uint32(data))
	case 8:
		return binary.Write(writer, order, data)
	}
	return fmt.Errorf("not support prt size %d", ptrSize)
}

// ReadDwarfLength reads a DWARF length field followed by a version field
func ReadDwarfLengthVersion(data []byte) (length uint64, dwarf64 bool, version uint8, byteOrder binary.ByteOrder) {
	if len(data) < 4 {
		return 0, false, 0, binary.LittleEndian
	}

	lengthfield := binary.LittleEndian.Uint32(data)
	voff := 4
	if lengthfield == ^uint32(0) {
		dwarf64 = true
		voff = 12
	}

	if voff+1 >= len(data) {
		return 0, false, 0, binary.LittleEndian
	}

	byteOrder = binary.LittleEndian
	x, y := data[voff], data[voff+1]
	switch {
	default:
		fallthrough
	case x == 0 && y == 0:
		version = 0
		byteOrder = binary.LittleEndian
	case x == 0:
		version = y
		byteOrder = binary.BigEndian
	case y == 0:
		version = x
		byteOrder = binary.LittleEndian
	}

	if dwarf64 {
		length = byteOrder.Uint64(data[4:])
	} else {
		length = uint64(byteOrder.Uint32(data))
	}

	return length, dwarf64, version, byteOrder
}

const (
	_DW_UT_compile = 0x1 + iota
	_DW_UT_type
	_DW_UT_partial
	_DW_UT_skeleton
	_DW_UT_split_compile
	_DW_UT_split_type
)

// ReadUnitVersions reads the DWARF version of each unit in a debug_info section and returns them as a map.
func ReadUnitVersions(data []byte) map[dwarf.Offset]uint8 {
	r := make(map[dwarf.Offset]uint8)
	off := dwarf.Offset(0)
	for len(data) > 0 {
		length, dwarf64, version, _ := ReadDwarfLengthVersion(data)

		data = data[4:]
		off += 4
		secoffsz := 4
		if dwarf64 {
			off += 8
			secoffsz = 8
			data = data[8:]
		}

		var headerSize int

		switch version {
		case 2, 3, 4:
			headerSize = 3 + secoffsz
		default: // 5 and later?
			unitType := data[2]

			switch unitType {
			case _DW_UT_compile, _DW_UT_partial:
				headerSize = 5 + secoffsz

			case _DW_UT_skeleton, _DW_UT_split_compile:
				headerSize = 4 + secoffsz + 8

			case _DW_UT_type, _DW_UT_split_type:
				headerSize = 4 + secoffsz + 8 + secoffsz
			}
		}

		r[off+dwarf.Offset(headerSize)] = version

		data = data[length:] // skip contents
		off += dwarf.Offset(length)
	}
	return r
}
