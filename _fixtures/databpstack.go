package main

import (
	"fmt"
	"runtime"
)

func f() {
	w := 0
	runtime.Breakpoint()
	g(1000, &w) // Position 0
}

func g(cnt int, p *int) {
	if cnt == 0 {
		*p = 10
		return // Position 1
	}
	g(cnt-1, p)
}

func main() {
	f()
	fmt.Printf("done\n") // Position 2
}
