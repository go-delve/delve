package main

import "runtime"
import "fmt"

func f() {
	runtime.Breakpoint()
}

func g() int {
	runtime.Breakpoint()
	return 3
}

func main() {
	f()
	n := g() + 1
	fmt.Println(n)
}
