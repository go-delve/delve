package main

import (
	"fmt"
	"os"
	"time"
)

func main() {
	fmt.Println(os.Getpid())

	time.Sleep(3 * time.Second)
	for {
		fmt.Println("Hello, world!")
	}
}
