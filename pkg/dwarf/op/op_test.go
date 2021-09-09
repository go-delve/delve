package op

import (
	"strings"
	"testing"
)

func assertExprResult(t *testing.T, expected int64, instructions []byte) {
	t.Helper()
	actual, _, err := ExecuteStackProgram(DwarfRegisters{}, instructions, 8, nil)
	if err != nil {
		t.Error(err)
	}
	if actual != expected {
		buf := new(strings.Builder)
		PrettyPrint(buf, instructions)
		t.Errorf("actual %d != expected %d (in %s)", actual, expected, buf.String())
	}
}

func TestExecuteStackProgram(t *testing.T) {
	assertExprResult(t, 56, []byte{byte(DW_OP_consts), 0x1c, byte(DW_OP_consts), 0x1c, byte(DW_OP_plus)})
}

func TestSignExtension(t *testing.T) {
	var tgt uint64 = 0xffffffffffffff88
	assertExprResult(t, int64(tgt), []byte{byte(DW_OP_const1s), 0x88})
	tgt = 0xffffffffffff8888
	assertExprResult(t, int64(tgt), []byte{byte(DW_OP_const2s), 0x88, 0x88})
}

func TestStackOps(t *testing.T) {
	assertExprResult(t, 1, []byte{byte(DW_OP_lit1), byte(DW_OP_lit2), byte(DW_OP_drop)})
	assertExprResult(t, 0, []byte{byte(DW_OP_lit1), byte(DW_OP_lit0), byte(DW_OP_pick), 0})
	assertExprResult(t, 1, []byte{byte(DW_OP_lit1), byte(DW_OP_lit0), byte(DW_OP_pick), 1})
}

func TestBra(t *testing.T) {
	assertExprResult(t, 32, []byte{
		byte(DW_OP_lit1),
		byte(DW_OP_lit5),

		byte(DW_OP_dup),
		byte(DW_OP_lit0),
		byte(DW_OP_eq),
		byte(DW_OP_bra), 9, 0x0,

		byte(DW_OP_swap),
		byte(DW_OP_dup),
		byte(DW_OP_plus),
		byte(DW_OP_swap),
		byte(DW_OP_lit1),
		byte(DW_OP_minus),
		byte(DW_OP_skip), 0xf1, 0xff,

		byte(DW_OP_drop),
	})
}
