package main

import (
	"fmt"
	"time"
)

func sleepytime() {
	time.Sleep(time.Millisecond)
}

func helloworld() {
	fmt.Println("Hello, World!")
}

func main() {
	for {
		sleepytime()
		helloworld()
	}
}
