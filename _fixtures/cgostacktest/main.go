package main

// #include <hello.h>
import "C"

import (
	"fmt"
	"runtime"
)

func main() {
	runtime.Breakpoint()
	C.helloworld(2)
}

//export helloWorld
func helloWorld(x C.int) {
	helloWorldS(x)
}

func helloWorldS(x C.int) {
	runtime.Breakpoint()
	C.helloworld_pt3(x - 1)
}

//export helloWorld2
func helloWorld2(x C.int) {
	runtime.Breakpoint()
	fmt.Printf("hello world %d\n", x)
}
