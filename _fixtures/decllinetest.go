package main

import (
	"fmt"
	"runtime"
)

func main() {
	a := 0
	runtime.Breakpoint()
	a++
	b := 0
	runtime.Breakpoint()
	fmt.Println(a, b)
}
