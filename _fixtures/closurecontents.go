package main

import (
	"fmt"
	"runtime"
)

func makeAcc(scale int) func(x int) int {
	a := 0
	return func(x int) int {
		a += x * scale
		return a
	}
}

func main() {
	acc := makeAcc(3)
	runtime.Breakpoint()
	fmt.Println(acc(1))
	runtime.Breakpoint()
	fmt.Println(acc(2))
	runtime.Breakpoint()
	fmt.Println(acc(6))
	runtime.Breakpoint()
}
