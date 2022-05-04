package main

import (
	"fmt"
	"github.com/go-delve/liner"
)

func main() {
	line := liner.NewLiner()
	line.Close()
	fmt.Printf("test\n")
}
