package main

// A debugger test.
//   dlv debug
//   b main.foo
//   c
//   s
//   s
// Expect to be stopped in fmt.Printf or runtime.duffzero
import "fmt"

var v int = 99

func foo(x, y int) (z int) { // c goes here
	fmt.Printf("x=%d, y=%d, z=%d\n", x, y, z) // s #1 goes here
	z = x + y
	return
}

func main() {
	x := v
	y := x * x
	z := foo(x, y)
	fmt.Printf("z=%d\n", z)
}
