package main

import "runtime"

func double(i int) int {
	return i * 2
}

func main() {
	runtime.Breakpoint()
}
