package main

import (
	"os"
	"sync"
)

var wg sync.WaitGroup

func f(from string) {
	defer wg.Done()
	return
}

func main() {
	wg.Add(1)
	go f("goroutine")
	wg.Wait()
	os.Exit(2)
}
