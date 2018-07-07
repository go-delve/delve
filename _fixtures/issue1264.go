package main

import "fmt"

func main() {
	for i := 0; i < 4; i++ {
		equalsTwo := i == 2
		fmt.Printf("i: %d -> equalsTwo: %t \n", i, equalsTwo) // :8
	}
}
