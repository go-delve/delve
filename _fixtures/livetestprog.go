package main

import (
	"fmt"
	"os"
	"time"
)

func printPid(pid int) {
	fmt.Println(pid)
}

func sayhi() {
	fmt.Println("hi")
}

func main() {
	pid := os.Getpid()
	printPid(pid)

	time.Sleep(3 * time.Second)
	for {
		printPid(pid)
		time.Sleep(3 * time.Millisecond)
		sayhi()
	}
}
