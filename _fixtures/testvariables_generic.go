package main

import (
	"fmt"
	"runtime"
)

type astruct struct {
	x, y int
}

func testfn[T any, K comparable](arg1 T, arg2 K) {
	m := make(map[K]T)
	m[arg2] = arg1
	runtime.Breakpoint()
	fmt.Println(arg1, arg2, m)
}

func main() {
	testfn[int, float32](3, 2.1)
	testfn(&astruct{0, 1}, astruct{2, 3})
}
