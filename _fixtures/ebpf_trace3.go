package main

import (
	"fmt"
	"time"
)

func main() {
	str := "abcdefghijklmnopqrstuvqxyz"
	i := int64(0)
	for i = 0; i < 5; i++ {
		tracedFunction(i, i%5 == 0, str[i])
	}
	for i = 5; i < 10; i++ {
		tracedFunction(i, i%5 == 0, str[i])
		time.Sleep(time.Millisecond)
	}
}

//go:noinline
func tracedFunction(x int64, b bool, r byte) {
	fmt.Println(x, b, r)
}
