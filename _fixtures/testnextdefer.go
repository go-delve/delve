package main

import "fmt"

func A() {
	defer func() {
		fmt.Println("hi")
	}()
	fmt.Println("bye")
}

func main() {
	A()
}
