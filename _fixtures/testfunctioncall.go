package main

import (
	"fmt"
	"runtime"
)

func double(i int) int {
	return i * 2
}

func mult(a, b int) int {
	return a * b
}

func main() {
	runtime.Breakpoint()
	fmt.Println(double(4), mult(2, 4))
}
