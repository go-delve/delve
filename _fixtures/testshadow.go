package main

import (
	"fmt"
	"runtime"
)

func main() {
	a := 0
	{
		a := 1
		runtime.Breakpoint()
		fmt.Println(a)
	}
	fmt.Println(a)
}
