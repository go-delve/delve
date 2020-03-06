package api

import (
	"fmt"
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
	res := strings.Split(strings.TrimSpace(PrettyExamineMemory(addr, memArea, format)), "\n")

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
