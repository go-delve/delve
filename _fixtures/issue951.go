package main

import (
	"fmt"
	"runtime"
)

func main() {
	shadow(42)
}

func shadow(i int) {
	for i := 0; i < 5; i++ {
		for i := 20; i < 25; i++ {
			runtime.Breakpoint()
			fmt.Println("another shadow", i)
		}
		fmt.Println("shadow", i)
	}
	fmt.Println("arg", i)
}
