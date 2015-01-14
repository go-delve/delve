package main

import (
	"fmt"
	"runtime"
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

func init() {
	runtime.LockOSThread()
}
