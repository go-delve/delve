package main

import "fmt"

// To be set via
//   go build -ldflags '-X main.Hello=World'
var Hello string

func main() {
	if len(Hello) < 1 {
		panic("global main.Hello not set via build flags")
	}
	fmt.Printf("Hello %s!\n", Hello)
}
