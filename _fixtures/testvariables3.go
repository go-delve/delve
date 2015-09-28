package main

import (
	"fmt"
	"runtime"
)

func main() {
	i1 := 1
	i2 := 2
	p1 := &i1
	runtime.Breakpoint()
	fmt.Printf("%d %d %v\n", i1, i2, p1)
}
