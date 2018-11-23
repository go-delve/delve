package main

import (
	"os"
	"time"
)

func f() {
	for {
		time.Sleep(10 * time.Second)
		if len(os.Args) > 1 {
			break
		}
	}
}

func main() {
	f()
}
