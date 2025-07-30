package main

import (
	"fmt"
	"runtime"
)

func Hello(name string) string {
	msg := "Hello, " + name
	fmt.Println(msg)
	return msg
}

func f(i int) func() {
	return func() {
		fmt.Println("Function f called with:", i)
	}
}

func main() {
	fmt.Println("Program started")
	fmt.Println("Ready for Delve call")
	runtime.Breakpoint()

	type m struct {
		Hello string
	}
	main := m{
		Hello: "World",
	}
	fmt.Println(main.Hello)
	fn := f(42)
	runtime.Breakpoint()
	fn()
}
