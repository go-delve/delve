package op

import (
	"testing"
	"unsafe"
)

func ptrSizeByRuntimeArch() int {
	return int(unsafe.Sizeof(uintptr(0)))
}

func TestExecuteStackProgram(t *testing.T) {
	var (
		instructions = []byte{byte(DW_OP_consts), 0x1c, byte(DW_OP_consts), 0x1c, byte(DW_OP_plus)}
		expected     = int64(56)
	)
	actual, _, err := ExecuteStackProgram(DwarfRegisters{}, instructions, ptrSizeByRuntimeArch())
	if err != nil {
		t.Fatal(err)
	}

	if actual != expected {
		t.Fatalf("actual %d != expected %d", actual, expected)
	}
}
