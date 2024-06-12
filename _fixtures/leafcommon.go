package main

import "fmt"

func D(i int) int {
	return i * i * i
}
func C(i int) int {

	return D(i+10) + 20
}
func B(i int) int {
	return i * D(i)
}
func A(i int) int {
	d := 10 + B(i)
	return d + C(i)
}
func main() {
	j := 0
	j += A(2)
	fmt.Println(j)
}
