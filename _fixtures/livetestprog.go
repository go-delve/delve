package main

import (
	"fmt"
	"os"
	"runtime"
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

	for {
		printPid(pid)
		sayhi()
	}
}
