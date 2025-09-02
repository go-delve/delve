package main

import "fmt"

func fncall1() int {
	return 1
}

func fncall2() int {
	return 2
}

func main() {
	fmt.Println(fncall1())
	fmt.Println(fncall2())
}
