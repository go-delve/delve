package main

import "fmt"

func main() {
	defer func() {
		fmt.Println("hi")
	}()
	fmt.Println("bye")
}
