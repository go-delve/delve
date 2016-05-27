package util

import "bytes"

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

func ParseString(data *bytes.Buffer) (string, uint32) {
	str, err := data.ReadString(0x0)
	if err != nil {
		panic("Could not parse string")
	}

	return str[:len(str)-1], uint32(len(str))
}
