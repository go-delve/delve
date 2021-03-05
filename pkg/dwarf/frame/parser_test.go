package frame

import (
	"bytes"
	"encoding/binary"
	"io/ioutil"
	"os"
	"testing"
)

func TestParseCIE(t *testing.T) {
	ctx := &parseContext{
		buf:    bytes.NewBuffer([]byte{3, 0, 1, 124, 16, 12, 7, 8, 5, 16, 2, 0, 36, 0, 0, 0, 0, 0, 0, 0, 0, 16, 64, 0, 0, 0, 0, 0}),
		common: &CommonInformationEntry{Length: 12},
		length: 12,
	}
	ctx.totalLen = ctx.buf.Len()
	_ = parseCIE(ctx)

	common := ctx.common

	if common.Version != 3 {
		t.Fatalf("Expected Version 3, but get %d", common.Version)
	}
	if common.Augmentation != "" {
		t.Fatalf("Expected Augmentation \"\", but get %s", common.Augmentation)
	}
	if common.CodeAlignmentFactor != 1 {
		t.Fatalf("Expected CodeAlignmentFactor 1, but get %d", common.CodeAlignmentFactor)
	}
	if common.DataAlignmentFactor != -4 {
		t.Fatalf("Expected DataAlignmentFactor -4, but get %d", common.DataAlignmentFactor)
	}
	if common.ReturnAddressRegister != 16 {
		t.Fatalf("Expected ReturnAddressRegister 16, but get %d", common.ReturnAddressRegister)
	}
	initialInstructions := []byte{12, 7, 8, 5, 16, 2, 0}
	if !bytes.Equal(common.InitialInstructions, initialInstructions) {
		t.Fatalf("Expected InitialInstructions %v, but get %v", initialInstructions, common.InitialInstructions)
	}
}

func BenchmarkParse(b *testing.B) {
	f, err := os.Open("testdata/frame")
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()

	data, err := ioutil.ReadAll(f)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Parse(data, binary.BigEndian, 0, ptrSizeByRuntimeArch(), 0)
	}
}
