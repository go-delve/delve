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

func testnext() {
	j := 1

	for i := 0; i <= 1; i++ {
		j += j * (j ^ 3) / 100

		helloworld()
	}

	helloworld()
}

func main() {
	for i := 0; i <= 100; i++ {
		sleepytime()
		testnext()
	}
}
