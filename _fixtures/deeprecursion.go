package main

import "runtime"

func main() {
	recursiveFunc(1000000)
}

//go:noinline
func recursiveFunc(n int) {
	if n <= 0 {
		runtime.Breakpoint()
		return
	}
	recursiveFunc(n - 1)
}
