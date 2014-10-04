package main

import (
	"fmt"
	"time"
)

func sleepytime() {
	time.Sleep(time.Nanosecond)
}

func helloworld() {
	fmt.Println("Hello, World!")
}

func testnext() {
	var (
		j = 1
		f = 2
	)

	for i := 0; i <= 1; i++ {
		j += j * (j ^ 3) / 100

		helloworld()
	}

	if f == 1 {
		fmt.Println("should never get here")
	}

	helloworld()
}

func main() {
	for i := 0; i <= 1; i++ {
		sleepytime()
		testnext()
	}
}
