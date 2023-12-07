package main

import "fmt"

func A(i int, n int) int {
	if n == 1 {
		return i
	} else {
		n--
		return (i * A(i-1, n))
	}
}
func main() {
	j := 0
	j += A(5, 5)
	fmt.Println(j)
}
