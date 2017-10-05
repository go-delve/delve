package op

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/derekparker/delve/pkg/dwarf/util"
)

const (
	DW_OP_addr           = 0x3
	DW_OP_call_frame_cfa = 0x9c
	DW_OP_plus           = 0x22
	DW_OP_consts         = 0x11
	DW_OP_plus_uconsts   = 0x23
)

type stackfn func(byte, *context) error

type context struct {
	buf   *bytes.Buffer
	stack []int64

	DwarfRegisters
}

var oplut = map[byte]stackfn{
	DW_OP_call_frame_cfa: callframecfa,
	DW_OP_plus:           plus,
	DW_OP_consts:         consts,
	DW_OP_addr:           addr,
	DW_OP_plus_uconsts:   plusuconsts,
}

func ExecuteStackProgram(regs DwarfRegisters, instructions []byte) (int64, error) {
	ctxt := &context{
		buf:            bytes.NewBuffer(instructions),
		stack:          make([]int64, 0, 3),
		DwarfRegisters: regs,
	}

	for {
		opcode, err := ctxt.buf.ReadByte()
		if err != nil {
			break
		}
		fn, ok := oplut[opcode]
		if !ok {
			return 0, fmt.Errorf("invalid instruction %#v", opcode)
		}

		err = fn(opcode, ctxt)
		if err != nil {
			return 0, err
		}
	}

	if len(ctxt.stack) == 0 {
		return 0, errors.New("empty OP stack")
	}

	return ctxt.stack[len(ctxt.stack)-1], nil
}

func callframecfa(opcode byte, ctxt *context) error {
	if ctxt.CFA == 0 {
		return fmt.Errorf("Could not retrieve call frame CFA for current PC")
	}
	ctxt.stack = append(ctxt.stack, int64(ctxt.CFA))
	return nil
}

func addr(opcode byte, ctxt *context) error {
	ctxt.stack = append(ctxt.stack, int64(binary.LittleEndian.Uint64(ctxt.buf.Next(8))))
	return nil
}

func plus(opcode byte, ctxt *context) error {
	var (
		slen   = len(ctxt.stack)
		digits = ctxt.stack[slen-2 : slen]
		st     = ctxt.stack[:slen-2]
	)

	ctxt.stack = append(st, digits[0]+digits[1])
	return nil
}

func plusuconsts(opcode byte, ctxt *context) error {
	slen := len(ctxt.stack)
	num, _ := util.DecodeULEB128(ctxt.buf)
	ctxt.stack[slen-1] = ctxt.stack[slen-1] + int64(num)
	return nil
}

func consts(opcode byte, ctxt *context) error {
	num, _ := util.DecodeSLEB128(ctxt.buf)
	ctxt.stack = append(ctxt.stack, num)
	return nil
}

func framebase(opcode byte, ctxt *context) error {
	num, _ := util.DecodeSLEB128(ctxt.buf)
	ctxt.stack = append(ctxt.stack, ctxt.FrameBase+num)
	return nil
}
