// Package frame contains data structures and
// related functions for parsing and searching
// through Dwarf .debug_frame data.
package frame

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/go-delve/delve/pkg/dwarf/util"
)

type parsefunc func(*parseContext) parsefunc

type parseContext struct {
	staticBase uint64

	buf         *bytes.Buffer
	totalLen    int
	entries     FrameDescriptionEntries
	ciemap      map[int]*CommonInformationEntry
	common      *CommonInformationEntry
	frame       *FrameDescriptionEntry
	length      uint32
	ptrSize     int
	ehFrameAddr uint64
	err         error
}

// Parse takes in data (a byte slice) and returns FrameDescriptionEntries,
// which is a slice of FrameDescriptionEntry. Each FrameDescriptionEntry
// has a pointer to CommonInformationEntry.
// If ehFrameAddr is not zero the .eh_frame format will be used, a minor variant of DWARF described at https://www.airs.com/blog/archives/460.
// The value of ehFrameAddr will be used as the address at which eh_frame will be mapped into memory
func Parse(data []byte, order binary.ByteOrder, staticBase uint64, ptrSize int, ehFrameAddr uint64) (FrameDescriptionEntries, error) {
	var (
		buf  = bytes.NewBuffer(data)
		pctx = &parseContext{buf: buf, totalLen: len(data), entries: newFrameIndex(), staticBase: staticBase, ptrSize: ptrSize, ehFrameAddr: ehFrameAddr, ciemap: map[int]*CommonInformationEntry{}}
	)

	for fn := parselength; buf.Len() != 0; {
		fn = fn(pctx)
		if pctx.err != nil {
			return nil, pctx.err
		}
	}

	for i := range pctx.entries {
		pctx.entries[i].order = order
	}

	return pctx.entries, nil
}

func (ctx *parseContext) parsingEHFrame() bool {
	return ctx.ehFrameAddr > 0
}

func (ctx *parseContext) cieEntry(cieid uint32) bool {
	if ctx.parsingEHFrame() {
		return cieid == 0x00
	}
	return cieid == 0xffffffff
}

func (ctx *parseContext) offset() int {
	return ctx.totalLen - ctx.buf.Len()
}

func parselength(ctx *parseContext) parsefunc {
	start := ctx.offset()
	binary.Read(ctx.buf, binary.LittleEndian, &ctx.length) //TODO(aarzilli): this does not support 64bit DWARF

	if ctx.length == 0 {
		// ZERO terminator
		return parselength
	}

	var cieid uint32
	binary.Read(ctx.buf, binary.LittleEndian, &cieid)

	ctx.length -= 4 // take off the length of the CIE id / CIE pointer.

	if ctx.cieEntry(cieid) {
		ctx.common = &CommonInformationEntry{Length: ctx.length, staticBase: ctx.staticBase}
		ctx.ciemap[start] = ctx.common
		return parseCIE
	}

	if ctx.ehFrameAddr > 0 {
		cieid = uint32(start - int(cieid) + 4)
	}

	common := ctx.ciemap[int(cieid)]

	if common == nil {
		ctx.err = fmt.Errorf("unknown CIE_id %#x at %#x", cieid, start)
	}

	ctx.frame = &FrameDescriptionEntry{Length: ctx.length, CIE: common}
	return parseFDE
}

func parseFDE(ctx *parseContext) parsefunc {
	startOff := ctx.offset()
	r := ctx.buf.Next(int(ctx.length))

	reader := bytes.NewReader(r)
	num := ctx.readEncodedPtr(addrSum(ctx.ehFrameAddr+uint64(startOff), reader), reader, ctx.frame.CIE.ptrEncAddr)
	ctx.frame.begin = num + ctx.staticBase

	// For the size field in .eh_frame only the size encoding portion of the
	// address pointer encoding is considered.
	// See decode_frame_entry_1 in gdb/dwarf2-frame.c.
	// For .debug_frame ptrEncAddr is always ptrEncAbs and never has flags.
	sizePtrEnc := ctx.frame.CIE.ptrEncAddr & 0x0f
	ctx.frame.size = ctx.readEncodedPtr(0, reader, sizePtrEnc)

	// Insert into the tree after setting address range begin
	// otherwise compares won't work.
	ctx.entries = append(ctx.entries, ctx.frame)

	if ctx.parsingEHFrame() && len(ctx.frame.CIE.Augmentation) > 0 {
		// If we are parsing a .eh_frame and we saw an agumentation string then we
		// need to read the augmentation data, which are encoded as a ULEB128
		// size followed by 'size' bytes.
		n, _ := util.DecodeULEB128(reader)
		reader.Seek(int64(n), io.SeekCurrent)
	}

	// The rest of this entry consists of the instructions
	// so we can just grab all of the data from the buffer
	// cursor to length.

	off, _ := reader.Seek(0, io.SeekCurrent)
	ctx.frame.Instructions = r[off:]
	ctx.length = 0

	return parselength
}

func addrSum(base uint64, buf *bytes.Reader) uint64 {
	n, _ := buf.Seek(0, io.SeekCurrent)
	return base + uint64(n)
}

func parseCIE(ctx *parseContext) parsefunc {
	data := ctx.buf.Next(int(ctx.length))
	buf := bytes.NewBuffer(data)
	// parse version
	ctx.common.Version, _ = buf.ReadByte()

	// parse augmentation
	ctx.common.Augmentation, _ = util.ParseString(buf)

	if ctx.parsingEHFrame() {
		if ctx.common.Augmentation == "eh" {
			ctx.err = fmt.Errorf("unsupported 'eh' augmentation at %#x", ctx.offset())
		}
		if len(ctx.common.Augmentation) > 0 && ctx.common.Augmentation[0] != 'z' {
			ctx.err = fmt.Errorf("unsupported augmentation at %#x (does not start with 'z')", ctx.offset())
		}
	}

	// parse code alignment factor
	ctx.common.CodeAlignmentFactor, _ = util.DecodeULEB128(buf)

	// parse data alignment factor
	ctx.common.DataAlignmentFactor, _ = util.DecodeSLEB128(buf)

	// parse return address register
	if ctx.parsingEHFrame() && ctx.common.Version == 1 {
		b, _ := buf.ReadByte()
		ctx.common.ReturnAddressRegister = uint64(b)
	} else {
		ctx.common.ReturnAddressRegister, _ = util.DecodeULEB128(buf)
	}

	ctx.common.ptrEncAddr = ptrEncAbs

	if ctx.parsingEHFrame() && len(ctx.common.Augmentation) > 0 {
		_, _ = util.DecodeULEB128(buf) // augmentation data length
		for i := 1; i < len(ctx.common.Augmentation); i++ {
			switch ctx.common.Augmentation[i] {
			case 'L':
				_, _ = buf.ReadByte() // LSDA pointer encoding, we don't support this.
			case 'R':
				// Pointer encoding, describes how begin and size fields of FDEs are encoded.
				b, _ := buf.ReadByte()
				ctx.common.ptrEncAddr = ptrEnc(b)
				if !ctx.common.ptrEncAddr.Supported() {
					ctx.err = fmt.Errorf("pointer encoding not supported %#x at %#x", ctx.common.ptrEncAddr, ctx.offset())
					return nil
				}
			case 'S':
				// Signal handler invocation frame, we don't support this but there is no associated data to read.
			case 'P':
				// Personality function encoded as a pointer encoding byte followed by
				// the pointer to the personality function encoded as specified by the
				// pointer encoding.
				// We don't support this but have to read it anyway.
				e, _ := buf.ReadByte()
				if !ptrEnc(e).Supported() {
					ctx.err = fmt.Errorf("pointer encoding not supported %#x at %#x", e, ctx.offset())
					return nil
				}
				ctx.readEncodedPtr(0, buf, ptrEnc(e))
			default:
				ctx.err = fmt.Errorf("unsupported augmentation character %c at %#x", ctx.common.Augmentation[i], ctx.offset())
				return nil
			}
		}
	}

	// parse initial instructions
	// The rest of this entry consists of the instructions
	// so we can just grab all of the data from the buffer
	// cursor to length.
	ctx.common.InitialInstructions = buf.Bytes() //ctx.buf.Next(int(ctx.length))
	ctx.length = 0

	return parselength
}

// readEncodedPtr reads a pointer from buf encoded as specified by ptrEnc.
// This function is used to read pointers from a .eh_frame section, when
// used to parse a .debug_frame section ptrEnc will always be ptrEncAbs.
// The parameter addr is the address that the current byte of 'buf' will be
// mapped to when the executable file containing the eh_frame section being
// parse is loaded in memory.
func (ctx *parseContext) readEncodedPtr(addr uint64, buf util.ByteReaderWithLen, ptrEnc ptrEnc) uint64 {
	if ptrEnc == ptrEncOmit {
		return 0
	}

	var ptr uint64

	switch ptrEnc & 0xf {
	case ptrEncAbs, ptrEncSigned:
		ptr, _ = util.ReadUintRaw(buf, binary.LittleEndian, ctx.ptrSize)
	case ptrEncUleb:
		ptr, _ = util.DecodeULEB128(buf)
	case ptrEncUdata2:
		ptr, _ = util.ReadUintRaw(buf, binary.LittleEndian, 2)
	case ptrEncSdata2:
		ptr, _ = util.ReadUintRaw(buf, binary.LittleEndian, 2)
		ptr = uint64(int16(ptr))
	case ptrEncUdata4:
		ptr, _ = util.ReadUintRaw(buf, binary.LittleEndian, 4)
	case ptrEncSdata4:
		ptr, _ = util.ReadUintRaw(buf, binary.LittleEndian, 4)
		ptr = uint64(int32(ptr))
	case ptrEncUdata8, ptrEncSdata8:
		ptr, _ = util.ReadUintRaw(buf, binary.LittleEndian, 8)
	case ptrEncSleb:
		n, _ := util.DecodeSLEB128(buf)
		ptr = uint64(n)
	}

	if ptrEnc&0xf0 == ptrEncPCRel {
		ptr += addr
	}

	return ptr
}

// DwarfEndian determines the endianness of the DWARF by using the version number field in the debug_info section
// Trick borrowed from "debug/dwarf".New()
func DwarfEndian(infoSec []byte) binary.ByteOrder {
	if len(infoSec) < 6 {
		return binary.BigEndian
	}
	x, y := infoSec[4], infoSec[5]
	switch {
	case x == 0 && y == 0:
		return binary.BigEndian
	case x == 0:
		return binary.BigEndian
	case y == 0:
		return binary.LittleEndian
	default:
		return binary.BigEndian
	}
}
