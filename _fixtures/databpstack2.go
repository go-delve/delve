package main

import (
	"fmt"
)

func f(n int) {
	w := 0
	g(n, &w) // Position 0
}

func g(cnt int, p *int) int {
	if cnt == 0 {
		*p = 10
		return *p // Position 1
	}
	return g(cnt-1, p)
}

func main() {
	f(1000)
	fmt.Printf("done\n") // Position 2
}
