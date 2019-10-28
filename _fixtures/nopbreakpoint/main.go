package main

import (
	"fmt"
)

var g int = 0

func compromised()

func skipped() {
	g++
}

func main() {
	compromised()
	fmt.Printf("%d\n", g)
}
