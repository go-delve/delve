package main

import (
	"fmt"
	"runtime"
)

type A struct {
}

func (a *A) Model() string {
	return "A"
}

type B struct {
	*A
	Model string
}

func main() {
	v := &B{Model: "B", A: &A{}}
	fmt.Printf("%s\n", v.Model)
	fmt.Printf("%s\n", v.A.Model())

	runtime.Breakpoint() // breakpoint here
}
