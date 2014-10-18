package main

import (
	"fmt"
	"sync"
)

func anotherthread(wg *sync.WaitGroup) {
	i := 1 * 5 / 39020
	fmt.Println(i)
	wg.Done()
}

func main() {
	var wg sync.WaitGroup
	for i := 0; i < 100000; i++ {
		wg.Add(1)
		go anotherthread(&wg)
	}
	wg.Wait()
}
