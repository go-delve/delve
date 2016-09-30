package main

// A debugger test.
//   dlv debug
//   b main.foo
//   c
//   s
//   s
// Expect to be stopped in fmt.Printf or runtime.duffzero
// In bug, s #2 runs to the process exit because the call
// to duffzero enters duffzero well after the nominal entry
// and skips the internal breakpoint placed by Step().
import "fmt"

var v int = 99

func foo(x, y int) (z int) { // c stops here
	fmt.Printf("x=%d, y=%d, z=%d\n", x, y, z) // s #1 stops here; s #2 is supposed to stop in Printf or duffzero.
	z = x + y
	return
}

func main() {
	x := v
	y := x * x
	z := foo(x, y)
	fmt.Printf("z=%d\n", z)
}
