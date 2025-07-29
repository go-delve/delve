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
	runtime.Breakpoint()
}
