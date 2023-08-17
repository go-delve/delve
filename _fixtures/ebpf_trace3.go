package main

import (
	"fmt"
	"time"
)

func main() {
	i := int64(0)
	for i = 0; i < 5; i++ {
		tracedFunction(i, i%5 == 0)
	}
	for i = 5; i < 10; i++ {
		tracedFunction(i, i%5 == 0)
		time.Sleep(time.Second)
	}
}

//go:noinline
func tracedFunction(x int64, b bool) {
	fmt.Println(x, b)
}
