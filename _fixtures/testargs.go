package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("Hello, args world!")
	if os.Args[1] != "test" {
		panic("os.args[1] is not test!")
	}
	if os.Args[2] != "-passFlag" {
		panic("os.args[2] is not test!")
	}
}
