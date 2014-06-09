package main

import (
	"fmt"
	"time"
)

func sleepytime() {
	time.Sleep(time.Millisecond)
}

func sayhi() {
	fmt.Println("Hello, World!")
}

func main() {
	for {
		sleepytime()
		sayhi()
	}
}
