package proc

import (
	"testing"
)

func TestIssue554(t *testing.T) {
	// unsigned integer overflow in proc.(*memCache).contains was
	// causing it to always return true for address 0xffffffffffffffff
	mem := memCache{true, 0x20, make([]byte, 100), nil}
	if mem.contains(0xffffffffffffffff, 40) {
		t.Fatalf("should be false")
	}
}
