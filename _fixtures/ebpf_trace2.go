package main

import (
	"fmt"
	"time"
)

func main() {
	i := int64(0)
	for i = 0; i < 5; i++ {
		tracedFunction(i)
	}
	for i = 5; i < 10; i++ {
		tracedFunction(i)
		time.Sleep(time.Second)
	}
}

//go:noinline
func tracedFunction(x int64) {
	fmt.Println(x)
}
