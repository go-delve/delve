package main

import (
	"fmt"
	"sync"
)

func sayhi(n int, wg *sync.WaitGroup) {
	fmt.Println("hi", n)
	fmt.Println("hi", n)
	wg.Done()
}

func main() {
	var wg sync.WaitGroup
	wg.Add(10)
	for i := 0; i < 10; i++ {
		go sayhi(i, &wg)
	}
	wg.Wait()
}
