package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("hello world!")
	fmt.Fprintf(os.Stdout, "hello world!")
	fmt.Fprintf(os.Stderr, "hello world!\n")
	fmt.Fprintf(os.Stderr, "hello world! error!")
}
