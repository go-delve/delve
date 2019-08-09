package main

import (
	"fmt"
)

var g int = 0

func compromised(n int64)

//go:nosplit
func skipped() {
	g++
}

func main() {
	compromised(1)
	fmt.Printf("%d\n", g)
}
