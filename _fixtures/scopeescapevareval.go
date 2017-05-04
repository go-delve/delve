package main

import (
	"fmt"
	"runtime"
)

func main() {
	a := 2
	{
		a := 3
		p := &a
		runtime.Breakpoint()
		fmt.Println(a, p)
	}
	fmt.Println(a)
}
