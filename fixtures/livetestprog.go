package main

import (
	"fmt"
	"os"
	"time"
)

func main() {
	pid := os.Getpid()
	fmt.Println(pid)

	time.Sleep(3 * time.Second)
	for {
		fmt.Println(pid)
	}
}
