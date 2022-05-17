package main

import (
	"fmt"
	"os"
)

func g() {
}

func main() {
	g()
	a := os.Args[1] == "1"
	if a {
		fmt.Printf("true branch %v\n", a)
	} else {
		fmt.Printf("false branch %v\n", a)
	}
}
