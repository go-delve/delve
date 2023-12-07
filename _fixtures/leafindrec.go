package main

import "fmt"

func B(i int) int {
	if i > 0 {
		return A(i - 1)
	} else {
		return 0
	}
}
func A(n int) int {
	if n <= 1 {
		return n
	} else {
		return B(n - 3)
	}
}
func main() {
	j := 0
	j += B(12)
	fmt.Println(j)
}
