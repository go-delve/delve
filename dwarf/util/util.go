package util

import (
	"bytes"
	"unicode/utf8"
)

// DecodeULEB128 decodes an unsigned Little Endian Base 128
// represented number.
func DecodeULEB128(buf *bytes.Buffer) (uint64, uint32) {
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

// DecodeSLEB128 decodes an signed Little Endian Base 128
// represented number.
func DecodeSLEB128(buf *bytes.Buffer) (int64, uint32) {
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

func ParseString(data *bytes.Buffer) (string, uint32) {
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
