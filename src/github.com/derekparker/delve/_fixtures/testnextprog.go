package main

import (
	"fmt"
	"runtime"
	"time"
)

func sleepytime() {
	time.Sleep(5 * time.Millisecond)
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

		sleepytime()
	}

	helloworld()
}

func main() {
	d := make(chan int)
	testnext()
	go testgoroutine(9, d)
	<-d
	fmt.Println("done")
}

// fix line
func testgoroutine(foo int, d chan int) {
	d <- foo
}

func init() {
	runtime.LockOSThread()
	runtime.GOMAXPROCS(4)
}
