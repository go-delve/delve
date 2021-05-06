package main

import (
	"fmt"
	"runtime"
)

var globalvar1 = 0
var globalvar2 = 0

func main() { // Position 0
	runtime.LockOSThread()
	globalvar2 = 1
	fmt.Printf("%d %d\n", globalvar1, globalvar2)
	globalvar2 = globalvar1 + 1
	globalvar1 = globalvar2 + 1
	fmt.Printf("%d %d\n", globalvar1, globalvar2) // Position 1
	runtime.Breakpoint()
	globalvar2 = globalvar2 + 1          // Position 2
	globalvar2 = globalvar1 + globalvar2 // Position 3
	fmt.Printf("%d %d\n", globalvar1, globalvar2)
	globalvar1 = globalvar2 + 1
	fmt.Printf("%d %d\n", globalvar1, globalvar2)
	runtime.Breakpoint()
	done := make(chan struct{}) // Position 4
	go f(done)
	<-done
}

func f(done chan struct{}) {
	runtime.LockOSThread()
	globalvar1 = globalvar2 + 1
	close(done) // Position 5
}
