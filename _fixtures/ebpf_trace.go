package main

import "fmt"

func callme(i int) int {
	if i == 0 {
		return 100
	}
	return callme(i - 1)
}

func main() {
	fmt.Println(callme(10))
}
