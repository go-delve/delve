package proc

import (
	"testing"
)

func TestAlignAddr(t *testing.T) {
	c := func(align, in, tgt int64) {
		out := alignAddr(in, align)
		if out != tgt {
			t.Errorf("alignAddr(%x, %x) = %x, expected %x", in, align, out, tgt)
		}
	}

	for i := int64(0); i <= 0xf; i++ {
		c(1, i, i)
		c(1, i+0x10000, i+0x10000)
	}

	for _, example := range []struct{ align, in, tgt int64 }{
		{2, 0, 0},
		{2, 1, 2},
		{2, 2, 2},
		{2, 3, 4},
		{2, 4, 4},
		{2, 5, 6},
		{2, 6, 6},
		{2, 7, 8},
		{2, 8, 8},
		{2, 9, 0xa},
		{2, 0xa, 0xa},
		{2, 0xb, 0xc},
		{2, 0xc, 0xc},
		{2, 0xd, 0xe},
		{2, 0xe, 0xe},
		{2, 0xf, 0x10},

		{4, 0, 0},
		{4, 1, 4},
		{4, 2, 4},
		{4, 3, 4},
		{4, 4, 4},
		{4, 5, 8},
		{4, 6, 8},
		{4, 7, 8},
		{4, 8, 8},
		{4, 9, 0xc},
		{4, 0xa, 0xc},
		{4, 0xb, 0xc},
		{4, 0xc, 0xc},
		{4, 0xd, 0x10},
		{4, 0xe, 0x10},
		{4, 0xf, 0x10},

		{8, 0, 0},
		{8, 1, 8},
		{8, 2, 8},
		{8, 3, 8},
		{8, 4, 8},
		{8, 5, 8},
		{8, 6, 8},
		{8, 7, 8},
		{8, 8, 8},
		{8, 9, 0x10},
		{8, 0xa, 0x10},
		{8, 0xb, 0x10},
		{8, 0xc, 0x10},
		{8, 0xd, 0x10},
		{8, 0xe, 0x10},
		{8, 0xf, 0x10},
	} {
		c(example.align, example.in, example.tgt)
		c(example.align, example.in+0x10000, example.tgt+0x10000)
	}
}
