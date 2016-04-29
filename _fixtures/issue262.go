package main

import "fmt"

func typicalFunction() (res int) {
	defer func() {
		res = 2
		return
	}()
	res = 10
	return // setup breakpoint here
}

func main() {
	fmt.Println(typicalFunction())
}
