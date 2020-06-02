package main

import (
	"fmt"
	"time"
)

func A() {
	fmt.Printf("hello delve\n")
}

func main() {
	count := 0
	for {
		A()
		time.Sleep(time.Millisecond * time.Duration(100))
		if count >= 30 {
			break
		}
		count++
	}
}
