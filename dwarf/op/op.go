package op

import (
	"bytes"
	"fmt"

	"github.com/derekparker/dbg/dwarf/util"
)

const (
	DW_OP_call_frame_cfa = 0x9c
	DW_OP_plus           = 0x22
	DW_OP_consts         = 0x11
)

type stackfn func(*bytes.Buffer, []int64, int64) ([]int64, error)

var oplut = map[byte]stackfn{
	DW_OP_call_frame_cfa: callframecfa,
	DW_OP_plus:           plus,
	DW_OP_consts:         consts,
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

	return stack[len(stack)-1], nil
}

func callframecfa(buf *bytes.Buffer, stack []int64, cfa int64) ([]int64, error) {
	return append(stack, int64(cfa)), nil
}

func plus(buf *bytes.Buffer, stack []int64, cfa int64) ([]int64, error) {
	var (
		slen   = len(stack)
		digits = stack[slen-2 : slen]
		st     = stack[:slen-2]
	)

	return append(st, digits[0]+digits[1]), nil
}

func consts(buf *bytes.Buffer, stack []int64, cfa int64) ([]int64, error) {
	num, _ := util.DecodeSLEB128(buf)
	return append(stack, num), nil
}
