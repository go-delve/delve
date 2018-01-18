package main

import (
	"fmt"
	"os"
)

var args []string

func printArgs(){
	fmt.Printf("Args2: %#v\n", args)
}

func main() {
	args = os.Args[1:]
	printArgs()
}
