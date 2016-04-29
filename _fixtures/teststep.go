package main

import (
	"fmt"
	"runtime"
)

func callme() {
	fmt.Println("hi")
}

func main() {
	runtime.Breakpoint()
	callme()
}
