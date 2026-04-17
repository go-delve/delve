package main

import "runtime"

func deepCall(n int) {
	if n == 0 {
		runtime.Breakpoint()
		return
	}
	deepCall(n - 1)
}

func main() {
	deepCall(10000)
}
