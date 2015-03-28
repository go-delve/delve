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
	for {
		sleepytime()
		helloworld()
	}
}

func init() {
	runtime.LockOSThread()
}
