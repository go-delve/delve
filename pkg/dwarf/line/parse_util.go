package line

import (
	"bytes"
	"encoding/binary"
	"errors"

	"github.com/go-delve/delve/pkg/dwarf/util"
)

const (
	_DW_FORM_block      = 0x09
	_DW_FORM_block1     = 0x0a
	_DW_FORM_block2     = 0x03
	_DW_FORM_block4     = 0x04
	_DW_FORM_data1      = 0x0b
	_DW_FORM_data2      = 0x05
	_DW_FORM_data4      = 0x06
	_DW_FORM_data8      = 0x07
	_DW_FORM_data16     = 0x1e
	_DW_FORM_flag       = 0x0c
	_DW_FORM_line_strp  = 0x1f
	_DW_FORM_sdata      = 0x0d
	_DW_FORM_sec_offset = 0x17
	_DW_FORM_string     = 0x08
	_DW_FORM_strp       = 0x0e
	_DW_FORM_strx       = 0x1a
	_DW_FORM_strx1      = 0x25
	_DW_FORM_strx2      = 0x26
	_DW_FORM_strx3      = 0x27
	_DW_FORM_strx4      = 0x28
	_DW_FORM_udata      = 0x0f
)

const (
	_DW_LNCT_path = 0x1 + iota
	_DW_LNCT_directory_index
	_DW_LNCT_timestamp
	_DW_LNCT_size
	_DW_LNCT_MD5
)

var ErrBufferUnderflow = errors.New("buffer underflow")

type formReader struct {
	logf         func(string, ...interface{})
	contentTypes []uint64
	formCodes    []uint64

	contentType uint64
	formCode    uint64

	block []byte
	u64   uint64
	i64   int64
	str   string
	err   error

	nexti int
}

func readEntryFormat(buf *bytes.Buffer, logf func(string, ...interface{})) *formReader {
	if buf.Len() < 1 {
		return nil
	}
	count := buf.Next(1)[0]
	r := &formReader{
		logf:         logf,
		contentTypes: make([]uint64, count),
		formCodes:    make([]uint64, count),
	}
	for i := range r.contentTypes {
		r.contentTypes[i], _ = util.DecodeULEB128(buf)
		r.formCodes[i], _ = util.DecodeULEB128(buf)
	}
	return r
}

func (rdr *formReader) reset() {
	rdr.err = nil
	rdr.nexti = 0
}

func (rdr *formReader) next(buf *bytes.Buffer) bool {
	if rdr.err != nil {
		return false
	}
	if rdr.nexti >= len(rdr.contentTypes) {
		return false
	}

	rdr.contentType = rdr.contentTypes[rdr.nexti]
	rdr.formCode = rdr.formCodes[rdr.nexti]

	switch rdr.formCode {
	case _DW_FORM_block:
		n, _ := util.DecodeULEB128(buf)
		rdr.readBlock(buf, n)

	case _DW_FORM_block1:
		if buf.Len() < 1 {
			rdr.err = ErrBufferUnderflow
			return false
		}
		rdr.readBlock(buf, uint64(buf.Next(1)[0]))

	case _DW_FORM_block2:
		if buf.Len() < 2 {
			rdr.err = ErrBufferUnderflow
			return false
		}
		rdr.readBlock(buf, uint64(binary.LittleEndian.Uint16(buf.Next(2))))

	case _DW_FORM_block4:
		if buf.Len() < 4 {
			rdr.err = ErrBufferUnderflow
			return false
		}
		rdr.readBlock(buf, uint64(binary.LittleEndian.Uint32(buf.Next(4))))

	case _DW_FORM_data1, _DW_FORM_flag, _DW_FORM_strx1:
		if buf.Len() < 1 {
			rdr.err = ErrBufferUnderflow
			return false
		}
		rdr.u64 = uint64(buf.Next(1)[0])

	case _DW_FORM_data2, _DW_FORM_strx2:
		if buf.Len() < 2 {
			rdr.err = ErrBufferUnderflow
			return false
		}
		rdr.u64 = uint64(binary.LittleEndian.Uint16(buf.Next(2)))

	case _DW_FORM_data4, _DW_FORM_line_strp, _DW_FORM_sec_offset, _DW_FORM_strp, _DW_FORM_strx4:
		if buf.Len() < 4 {
			rdr.err = ErrBufferUnderflow
			return false
		}
		rdr.u64 = uint64(binary.LittleEndian.Uint32(buf.Next(4)))

	case _DW_FORM_data8:
		if buf.Len() < 8 {
			rdr.err = ErrBufferUnderflow
			return false
		}
		rdr.u64 = binary.LittleEndian.Uint64(buf.Next(8))

	case _DW_FORM_data16:
		rdr.readBlock(buf, 16)

	case _DW_FORM_sdata:
		rdr.i64, _ = util.DecodeSLEB128(buf)

	case _DW_FORM_udata, _DW_FORM_strx:
		rdr.u64, _ = util.DecodeULEB128(buf)

	case _DW_FORM_string:
		rdr.str, _ = util.ParseString(buf)

	case _DW_FORM_strx3:
		if buf.Len() < 3 {
			rdr.err = ErrBufferUnderflow
			return false
		}
		rdr.u64 = uint64(binary.LittleEndian.Uint32(append(buf.Next(3), 0x0)))

	default:
		if rdr.logf != nil {
			rdr.logf("unknown form code %#x", rdr.formCode)
		}
		rdr.formCodes[rdr.nexti] = ^uint64(0) // only print error once
	case ^uint64(0):
		// do nothing
	}

	rdr.nexti++
	return true
}

func (rdr *formReader) readBlock(buf *bytes.Buffer, n uint64) {
	if uint64(buf.Len()) < n {
		rdr.err = ErrBufferUnderflow
		return
	}
	if cap(rdr.block) < int(n) {
		rdr.block = make([]byte, 0, n)
	}
	rdr.block = rdr.block[:n]
	buf.Read(rdr.block)
}
