// Copyright 2009 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Buffered reading and decoding of DWARF data streams.

package util

import (
	"debug/dwarf"
	"fmt"
)

// Data buffer being decoded.
type buf struct {
	dwarf  *dwarf.Data
	format dataFormat
	name   string
	off    dwarf.Offset
	data   []byte
	Err    error
}

// Data format, other than byte order.  This affects the handling of
// certain field formats.
type dataFormat interface {
	// DWARF version number.  Zero means unknown.
	version() int

	// 64-bit DWARF format?
	dwarf64() (dwarf64 bool, isKnown bool)

	// Size of an address, in bytes.  Zero means unknown.
	addrsize() int
}

// Some parts of DWARF have no data format, e.g., abbrevs.
type UnknownFormat struct{}

func (u UnknownFormat) version() int {
	return 0
}

func (u UnknownFormat) dwarf64() (bool, bool) {
	return false, false
}

func (u UnknownFormat) addrsize() int {
	return 0
}

func MakeBuf(d *dwarf.Data, format dataFormat, name string, off dwarf.Offset, data []byte) buf {
	return buf{d, format, name, off, data, nil}
}

func (b *buf) slice(length int) buf {
	n := *b
	data := b.data
	b.skip(length) // Will validate length.
	n.data = data[:length]
	return n
}

func (b *buf) Uint8() uint8 {
	if len(b.data) < 1 {
		b.error("underflow")
		return 0
	}
	val := b.data[0]
	b.data = b.data[1:]
	b.off++
	return val
}

func (b *buf) bytes(n int) []byte {
	if len(b.data) < n {
		b.error("underflow")
		return nil
	}
	data := b.data[0:n]
	b.data = b.data[n:]
	b.off += dwarf.Offset(n)
	return data
}

func (b *buf) skip(n int) { b.bytes(n) }

// string returns the NUL-terminated (C-like) string at the start of the buffer.
// The terminal NUL is discarded.
func (b *buf) string() string {
	for i := 0; i < len(b.data); i++ {
		if b.data[i] == 0 {
			s := string(b.data[0:i])
			b.data = b.data[i+1:]
			b.off += dwarf.Offset(i + 1)
			return s
		}
	}
	b.error("underflow")
	return ""
}

// Read a varint, which is 7 bits per byte, little endian.
// the 0x80 bit means read another byte.
func (b *buf) Varint() (c uint64, bits uint) {
	for i := 0; i < len(b.data); i++ {
		byte := b.data[i]
		c |= uint64(byte&0x7F) << bits
		bits += 7
		if byte&0x80 == 0 {
			b.off += dwarf.Offset(i + 1)
			b.data = b.data[i+1:]
			return c, bits
		}
	}
	return 0, 0
}

// Unsigned int is just a varint.
func (b *buf) Uint() uint64 {
	x, _ := b.Varint()
	return x
}

// Signed int is a sign-extended varint.
func (b *buf) Int() int64 {
	ux, bits := b.Varint()
	x := int64(ux)
	if x&(1<<(bits-1)) != 0 {
		x |= -1 << bits
	}
	return x
}

// AssertEmpty checks that everything has been read from b.
func (b *buf) AssertEmpty() {
	if len(b.data) == 0 {
		return
	}
	if len(b.data) > 5 {
		b.error(fmt.Sprintf("unexpected extra data: %x...", b.data[0:5]))
	}
	b.error(fmt.Sprintf("unexpected extra data: %x", b.data))
}

func (b *buf) error(s string) {
	if b.Err == nil {
		b.data = nil
		b.Err = dwarf.DecodeError{Name: b.name, Offset: b.off, Err: s}
	}
}
