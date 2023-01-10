package leb128

import (
	"bytes"
	"testing"
)

func TestEncodeUnsigned(t *testing.T) {
	tc := []uint64{0x00, 0x7f, 0x80, 0x8f, 0xffff, 0xfffffff7}
	for i := range tc {
		var buf bytes.Buffer
		EncodeUnsigned(&buf, tc[i])
		enc := append([]byte{}, buf.Bytes()...)
		buf.Write([]byte{0x1, 0x2, 0x3})
		out, c := DecodeUnsigned(&buf)
		t.Logf("input %x output %x encoded %x", tc[i], out, enc)
		if c != uint32(len(enc)) {
			t.Errorf("wrong encode")
		}
		if out != tc[i] {
			t.Errorf("wrong encode")
		}
	}
}

func TestEncodeSigned(t *testing.T) {
	tc := []int64{2, -2, 127, -127, 128, -128, 129, -129}
	for i := range tc {
		var buf bytes.Buffer
		EncodeSigned(&buf, tc[i])
		enc := append([]byte{}, buf.Bytes()...)
		buf.Write([]byte{0x1, 0x2, 0x3})
		out, c := DecodeSigned(&buf)
		t.Logf("input %x output %x encoded %x", tc[i], out, enc)
		if c != uint32(len(enc)) {
			t.Errorf("wrong encode")
		}
		if out != tc[i] {
			t.Errorf("wrong encode")
		}
	}
}
