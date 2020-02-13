package main

import (
	"fmt"
	"unsafe"
)

func main() {
	l := int(51)
	bs := make([]byte, l)
	for i := 0; i < l; i++ {
		bs[i] = byte(i + int(10))
	}

	bsp := (*byte)(unsafe.Pointer(&bs[0]))

	bspUintptr := uintptr(unsafe.Pointer(bsp))

	fmt.Printf("%#x\n", bspUintptr)
	_ = *bsp

	bs[0] = 255

	_ = *bsp
}
