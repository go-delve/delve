// Package frame contains data structures and
// related functions for parsing and searching
// through Dwarf .debug_frame data.
package frame

import (
	"bytes"
	"encoding/binary"

	"github.com/derekparker/dbg/dwarf/util"
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

func cieEntry(data []byte) bool {
	return bytes.Equal(data, []byte{0xff, 0xff, 0xff, 0xff})
}

func parseLength(ctx *parseContext) parsefunc {
	var fn parsefunc

	ctx.Length = binary.LittleEndian.Uint32(ctx.Buf.Next(4))
	cieid := ctx.Buf.Next(4)

	if cieEntry(cieid) {
		ctx.Common = &CommonInformationEntry{Length: ctx.Length}
		fn = parseVersion
	} else {
		ctx.Frame = &FrameDescriptionEntry{Length: ctx.Length, CIE: ctx.Common, AddressRange: &addrange{}}
		fn = parseInitialLocation
	}

	// Take off the length of the CIE id / CIE pointer.
	ctx.Length -= 4

	return fn
}

func parseInitialLocation(ctx *parseContext) parsefunc {
	ctx.Frame.AddressRange.begin = binary.LittleEndian.Uint64(ctx.Buf.Next(8))

	// Insert into the tree after setting address range begin
	// otherwise compares won't work.
	ctx.Entries.Put(ctx.Frame)

	ctx.Length -= 8

	return parseAddressRange
}

func parseAddressRange(ctx *parseContext) parsefunc {
	ctx.Frame.AddressRange.end = binary.LittleEndian.Uint64(ctx.Buf.Next(8))

	ctx.Length -= 8

	return parseFrameInstructions
}

func parseFrameInstructions(ctx *parseContext) parsefunc {
	// The rest of this entry consists of the instructions
	// so we can just grab all of the data from the buffer
	// cursor to length.
	ctx.Frame.Instructions = ctx.Buf.Next(int(ctx.Length))
	ctx.Length = 0

	return parseLength
}

func parseVersion(ctx *parseContext) parsefunc {
	version, err := ctx.Buf.ReadByte()
	if err != nil {
		panic(err)
	}
	ctx.Common.Version = version
	ctx.Length -= 1

	return parseAugmentation
}

func parseAugmentation(ctx *parseContext) parsefunc {
	var str, c = util.ParseString(ctx.Buf)

	ctx.Common.Augmentation = str
	ctx.Length -= c

	return parseCodeAlignmentFactor
}

func parseCodeAlignmentFactor(ctx *parseContext) parsefunc {
	var caf, c = util.DecodeULEB128(ctx.Buf)

	ctx.Common.CodeAlignmentFactor = caf
	ctx.Length -= c

	return parseDataAlignmentFactor
}

func parseDataAlignmentFactor(ctx *parseContext) parsefunc {
	var daf, c = util.DecodeSLEB128(ctx.Buf)

	ctx.Common.DataAlignmentFactor = daf
	ctx.Length -= c

	return parseReturnAddressRegister
}

func parseReturnAddressRegister(ctx *parseContext) parsefunc {
	reg, c := util.DecodeULEB128(ctx.Buf)
	ctx.Common.ReturnAddressRegister = reg
	ctx.Length -= c

	return parseInitialInstructions
}

func parseInitialInstructions(ctx *parseContext) parsefunc {
	// The rest of this entry consists of the instructions
	// so we can just grab all of the data from the buffer
	// cursor to length.
	ctx.Common.InitialInstructions = ctx.Buf.Next(int(ctx.Length))
	ctx.Length = 0

	return parseLength
}
