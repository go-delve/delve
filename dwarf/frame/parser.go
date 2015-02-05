// Package frame contains data structures and
// related functions for parsing and searching
// through Dwarf .debug_frame data.
package frame

import (
	"bytes"
	"encoding/binary"

	"github.com/derekparker/delve/dwarf/util"
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
		pctx = &parseContext{Buf: buf, Entries: NewFrameIndex()}
	)

	for fn := parseLength; buf.Len() != 0; {
		fn = fn(pctx)
	}

	return pctx.Entries
}

func cieEntry(data []byte) bool {
	return bytes.Equal(data, []byte{0xff, 0xff, 0xff, 0xff})
}

func parseLength(ctx *parseContext) parsefunc {
	var data = ctx.Buf.Next(8)

	ctx.Length = binary.LittleEndian.Uint32(data[:4]) - 4 // take off the length of the CIE id / CIE pointer.

	if cieEntry(data[4:]) {
		ctx.Common = &CommonInformationEntry{Length: ctx.Length}
		return parseCIE
	}

	ctx.Frame = &FrameDescriptionEntry{Length: ctx.Length, CIE: ctx.Common}
	return parseFDE
}

func parseFDE(ctx *parseContext) parsefunc {
	r := ctx.Buf.Next(int(ctx.Length))

	ctx.Frame.begin = binary.LittleEndian.Uint64(r[:8])
	ctx.Frame.end = binary.LittleEndian.Uint64(r[8:16])

	// Insert into the tree after setting address range begin
	// otherwise compares won't work.
	ctx.Entries = append(ctx.Entries, ctx.Frame)

	// The rest of this entry consists of the instructions
	// so we can just grab all of the data from the buffer
	// cursor to length.
	ctx.Frame.Instructions = r[16:]
	ctx.Length = 0

	return parseLength
}

func parseCIE(ctx *parseContext) parsefunc {
	data := ctx.Buf.Next(int(ctx.Length))
	buf := bytes.NewBuffer(data)
	// parse version
	ctx.Common.Version = data[0]

	// parse augmentation
	ctx.Common.Augmentation, _ = util.ParseString(buf)

	// parse code alignment factor
	ctx.Common.CodeAlignmentFactor, _ = util.DecodeULEB128(buf)

	// parse data alignment factor
	ctx.Common.DataAlignmentFactor, _ = util.DecodeSLEB128(buf)

	// parse return address register
	ctx.Common.ReturnAddressRegister, _ = util.DecodeULEB128(buf)

	// parse initial instructions
	// The rest of this entry consists of the instructions
	// so we can just grab all of the data from the buffer
	// cursor to length.
	ctx.Common.InitialInstructions = buf.Bytes() //ctx.Buf.Next(int(ctx.Length))
	ctx.Length = 0

	return parseLength
}
