package main

import "fmt"

func D(i int) int {
	return i * i * i
}
func C(i int) int {

	return i + 20
}
func B(i int) int {
	d := C(i) + 40
	return d + D(i)
}
func A(i int) int {
	return 10 + B(i)
}
func main() {
	j := 0
	j += A(2)
	fmt.Println(j)
}
