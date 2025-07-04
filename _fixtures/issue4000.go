package main

import "fmt"

func main() {
	var test = "a string"
	var f = func() {
		test = "another string"
		fmt.Println("test", test)
	}
	fmt.Println(test)
	f()
	fmt.Println(test)
}
