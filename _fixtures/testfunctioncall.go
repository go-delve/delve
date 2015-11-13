package main

import (
	"fmt"
	"runtime"
)

func double(i int) int {
	fmt.Println("i is:", i)
	i = i * 2
	fmt.Println("i is now:", i)
	return i
}

func main() {
	runtime.Breakpoint()
	fmt.Println(double(4))
}
