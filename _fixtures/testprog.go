package main

import (
	"fmt"
	"runtime"
	"time"
)

func helloworld() {
	fmt.Println("Hello, World!")
}

func sleepytime() {
	time.Sleep(time.Millisecond)
}

func main() {
	for i := 0; i < 500; i++ {
		sleepytime()
		helloworld()
	}
}

func init() {
	runtime.LockOSThread()
}
