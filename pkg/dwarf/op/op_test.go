package op

import "testing"

func TestExecuteStackProgram(t *testing.T) {
	var (
		instructions = []byte{DW_OP_consts, 0x1c, DW_OP_consts, 0x1c, DW_OP_plus}
		expected     = int64(56)
	)
	actual, err := ExecuteStackProgram(DwarfRegisters{}, instructions)
	if err != nil {
		t.Fatal(err)
	}

	if actual != expected {
		t.Fatalf("actual %d != expected %d", actual, expected)
	}
}
