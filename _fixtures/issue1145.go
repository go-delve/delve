package main

import "time"

func f() {
	for {
		time.Sleep(10 * time.Millisecond)
	}
}

func main() {
	f()
}
