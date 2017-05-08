package main

import "fmt"

// Increment Natural number y
func Increment(y uint) uint {
	if y == 0 {
		return 1
	}
	if y%2 == 1 {
		return (2 * Increment(y/2))
	}
	return y + 1
}

func main() {
	fmt.Printf("%d\n", Increment(3))
}
