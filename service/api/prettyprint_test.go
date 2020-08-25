package api

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestPrettyExamineMemory(t *testing.T) {
	// Test whether always use the last addr's len to format when the lens of two adjacent address are different
	addr := uintptr(0xffff)
	memArea := []byte("abcdefghijklmnopqrstuvwxyz")
	format := byte('o')

	display := []string{
		"0x0ffff:   0141   0142   0143   0144   0145   0146   0147   0150   ",
		"0x10007:   0151   0152   0153   0154   0155   0156   0157   0160   ",
		"0x1000f:   0161   0162   0163   0164   0165   0166   0167   0170   ",
		"0x10017:   0171   0172"}
	res := strings.Split(strings.TrimSpace(PrettyExamineMemory(addr, memArea, true, format, 1)), "\n")

	if len(display) != len(res) {
		t.Fatalf("wrong lines return, expected %d but got %d", len(display), len(res))
	}

	for i := 0; i < len(display); i++ {
		if display[i] != res[i] {
			errInfo := fmt.Sprintf("wrong display return at line %d\n", i+1)
			errInfo += fmt.Sprintf("expected:\n   %q\n", display[i])
			errInfo += fmt.Sprintf("but got:\n   %q\n", res[i])
			t.Fatal(errInfo)
		}
	}
}

func Test_byteArrayToUInt64(t *testing.T) {
	tests := []struct {
		name string
		args []byte
		want uint64
	}{
		{"case-nil", nil, 0},
		{"case-empty", []byte{}, 0},
		{"case-1", []byte{0x1}, 1},
		{"case-2", []byte{0x12}, 18},
		{"case-3", []byte{0x1, 0x2}, 513},
		{"case-4", []byte{0x1, 0x1, 0x1, 0x1, 0x1, 0x1, 0x1, 0x2}, 144397766876004609},
		{"case-5", []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, math.MaxUint64},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := byteArrayToUInt64(tt.args, true)
			if got != tt.want {
				t.Errorf("byteArrayToUInt64() got = %v, want %v", got, tt.want)
			}
		})
	}
}
