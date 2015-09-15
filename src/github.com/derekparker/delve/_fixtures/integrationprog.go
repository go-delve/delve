package main

import (
	"fmt"
	"time"
)

func sayhi() {
	fmt.Println("hi")
}

func main() {
	time.Sleep(1 * time.Second)
	for i := 0; i < 3; i++ {
		sayhi()
		time.Sleep(1 * time.Second)
	}
}
