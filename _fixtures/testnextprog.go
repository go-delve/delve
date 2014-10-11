package main

import (
	"fmt"
	"runtime"
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

	for i := 0; i <= 5; i++ {
		j += j * (j ^ 3) / 100

		if i == f {
			fmt.Println("foo")
			break
		}

		helloworld()
	}

	helloworld()
}

func main() {
	runtime.LockOSThread()
	for {
		helloworld()
		testnext()
	}
}
