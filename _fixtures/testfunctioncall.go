package main

import (
	"fmt"
	"runtime"
)

func double(i int) int {
	return i * 2
}

func main() {
	runtime.Breakpoint()
	fmt.Println(double(4))
}
