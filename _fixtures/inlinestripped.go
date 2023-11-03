package main

import "fmt"

func callme(i int) int {
	return i * i
}

func main() {
	j := 0
	j += callme(2)
	fmt.Println(j)
	fmt.Println(j + 1)
	fmt.Println(j + 2)
}
