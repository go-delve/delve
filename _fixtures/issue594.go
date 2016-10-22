package main

import (
	"fmt"
	"runtime"
)

func dontsegfault() {
	var p *int
	func() int {
		defer func() {
			recover()
		}()
		return *p
	}()
}

func main() {
	dontsegfault()
	runtime.Breakpoint()
	fmt.Println("should stop here")
}
