package main

import (
	"fmt"
	"runtime"
)

func main() {
	i1 := 1
	i2 := 2
	p1 := &i1
	var amb1 = 1
	runtime.Breakpoint()
	for amb1 := 0; amb1 < 10; amb1++ {
		fmt.Println(amb1)
	}
	fmt.Println(i1, i2, p1, amb1)
}
