package main

import (
	"fmt"
	"os"
	"runtime"
	"time"
)

func printPid(pid int) {
	fmt.Println(pid)
}

func sayhi() {
	fmt.Println("hi")
}

func main() {
	runtime.LockOSThread()
	pid := os.Getpid()
	printPid(pid)
	time.Sleep(10 * time.Second)

	for {
		printPid(pid)
		sayhi()
	}
}
