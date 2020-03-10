package util

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

// The Little Endian Base 128 format is defined in the DWARF v4 standard,
// section 7.6, page 161 and following.

// DecodeULEB128 decodes an unsigned Little Endian Base 128
// represented number.
func DecodeULEB128(buf *bytes.Buffer) (uint64, uint32) {
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
func DecodeSLEB128(buf *bytes.Buffer) (int64, uint32) {
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
