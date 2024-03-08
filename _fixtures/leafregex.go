package main

import "fmt"

func callmed(i int) int {
	return i * i * i
}
func callmee(i int) int {

	return i + 20
}
func callme2(i int) int {
	d := callmee(i) + 40
	return d + callmed(i)
}
func callme(i int) int {
	return 10 + callme2(i)
}
func main() {
	j := 0
	j += callme(2)
	fmt.Println(j)
}
