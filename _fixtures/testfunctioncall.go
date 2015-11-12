package main

import (
	"fmt"
	"runtime"
)

func double(i int) int {
	fmt.Println("i is:", i)
	return i * 2
}

func main() {
	runtime.Breakpoint()
	fmt.Println(double(4))
}
