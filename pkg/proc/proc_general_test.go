package proc

import (
	"testing"
	"unsafe"
)

func ptrSizeByRuntimeArch() int {
	return int(unsafe.Sizeof(uintptr(0)))
}

func TestIssue554(t *testing.T) {
	// unsigned integer overflow in proc.(*memCache).contains was
	// causing it to always return true for address 0xffffffffffffffff
	mem := memCache{true, 0x20, make([]byte, 100), nil}
	var addr uint64
	switch ptrSizeByRuntimeArch() {
	case 4:
		addr = 0xffffffff
	case 8:
		addr = 0xffffffffffffffff
	}
	if mem.contains(uintptr(addr), 40) {
		t.Fatalf("should be false")
	}
}
