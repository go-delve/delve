package main

import (
	"fmt"
	"runtime"
)

func main() {
	a := [3]int{1, 2, 3}
	b := &a
	runtime.Breakpoint()
	fmt.Println(b, *b) // set breakpoint here
}
