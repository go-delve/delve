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
	i := 1

	for {
		i += i * (i ^ 3) / 100

		sleepytime()
		helloworld()

		i--
	}
}
