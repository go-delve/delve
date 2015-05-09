package op

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/derekparker/delve/dwarf/util"
)

const (
	DW_OP_addr           = 0x3
	DW_OP_call_frame_cfa = 0x9c
	DW_OP_plus           = 0x22
	DW_OP_consts         = 0x11
	DW_OP_plus_uconsts   = 0x23
)

type stackfn func(*bytes.Buffer, []int64, int64) ([]int64, error)

var oplut = map[byte]stackfn{
	DW_OP_call_frame_cfa: callframecfa,
	DW_OP_plus:           plus,
	DW_OP_consts:         consts,
	DW_OP_addr:           addr,
	DW_OP_plus_uconsts:   plusuconsts,
}

func ExecuteStackProgram(cfa int64, instructions []byte) (int64, error) {
	stack := make([]int64, 0, 3)
	buf := bytes.NewBuffer(instructions)

	for ocfaode, err := buf.ReadByte(); err == nil; ocfaode, err = buf.ReadByte() {
		fn, ok := oplut[ocfaode]
		if !ok {
			return 0, fmt.Errorf("invalid instruction %#v", ocfaode)
		}

		stack, err = fn(buf, stack, cfa)
		if err != nil {
			return 0, err
		}
	}

	if len(stack) == 0 {
		return 0, errors.New("empty OP stack")
	}

	return stack[len(stack)-1], nil
}

func callframecfa(buf *bytes.Buffer, stack []int64, cfa int64) ([]int64, error) {
	return append(stack, int64(cfa)), nil
}

func addr(buf *bytes.Buffer, stack []int64, cfa int64) ([]int64, error) {
	return append(stack, int64(binary.LittleEndian.Uint64(buf.Next(8)))), nil
}

func plus(buf *bytes.Buffer, stack []int64, cfa int64) ([]int64, error) {
	var (
		slen   = len(stack)
		digits = stack[slen-2 : slen]
		st     = stack[:slen-2]
	)

	return append(st, digits[0]+digits[1]), nil
}

func plusuconsts(buf *bytes.Buffer, stack []int64, cfa int64) ([]int64, error) {
	slen := len(stack)
	num, _ := util.DecodeULEB128(buf)
	stack[slen-1] = stack[slen-1] + int64(num)
	return stack, nil
}

func consts(buf *bytes.Buffer, stack []int64, cfa int64) ([]int64, error) {
	num, _ := util.DecodeSLEB128(buf)
	return append(stack, num), nil
}
