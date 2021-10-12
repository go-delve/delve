package main

import (
	"fmt"
)

func g(cnt int) int {
	if cnt == 0 {
		return 10
	}
	return g(cnt - 1)
}

func main() {
	fmt.Println(g(1000))
	fmt.Printf("done\n") // Position 2
}
