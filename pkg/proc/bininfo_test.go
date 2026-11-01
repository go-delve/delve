package proc

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestParseModuleDataBounds(t *testing.T) {
	for _, tc := range []struct {
		name string
		size int
	}{
		{"near section end", 0x40},
		{"full window", 0x300},
		{"larger section", 0x400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const offset = 16
			const tableAddr = 0x12345678
			data := bytes.Repeat([]byte{0xff}, offset+tc.size)
			binary.LittleEndian.PutUint64(data[offset:], tableAddr)
			got, err := parseModuleData(data, tableAddr)
			assertNoError(err, t, "parseModuleData")
			want := data[offset : offset+min(tc.size, 0x300)]
			if !bytes.Equal(got, want) {
				t.Fatalf("moduledata: got %d bytes, want %d", len(got), len(want))
			}
		})
	}
}
