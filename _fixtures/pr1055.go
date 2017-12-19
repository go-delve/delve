package main

import (
	"fmt"
	"go/ast"
	"os"
)

func main() {
	a := &ast.CompositeLit{}
	fmt.Println("demo", a) // set breakpoint here and use next twice
	os.Exit(2)
}
