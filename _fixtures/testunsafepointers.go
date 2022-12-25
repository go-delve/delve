package main

import (
	"runtime"
	"unsafe"
)

func main() {
	// We're going to produce a pointer with a bad address.
	badAddr := uintptr(0x42)
	unsafeDanglingPtrPtr := unsafe.Pointer(badAddr)
	// We produce a **int, instead of more simply a *int, in order for the test
	// program to test more complex Delve behavior.
	danglingPtrPtr := (**int)(unsafeDanglingPtrPtr)
	_ = danglingPtrPtr
	runtime.Breakpoint()
}
