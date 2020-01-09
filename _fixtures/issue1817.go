package main

import (
	"fmt"
	"runtime"
	"unsafe"
)

func main() {
	l := int(51)
	bs := make([]byte, l)
	for i := 0; i < l; i++ {
		bs[i] = byte(i + int(10))
	}

	p := uintptr(unsafe.Pointer(&bs))

	fmt.Println(p)

	bs[0] = 254

	runtime.KeepAlive(bs)
}
