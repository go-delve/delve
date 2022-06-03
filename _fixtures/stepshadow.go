package main

import (
	"fmt"
	"sync"
)

func main() {
	var wg sync.WaitGroup
	wg.Add(1)
	go goroutineA(&wg)
	f := stacktraceme1
	for i := 0; i < 100; i++ {
		fmt.Printf("main %d\n", i)
		f()
	}
	wg.Wait()
}

func goroutineA(wg *sync.WaitGroup) {
	defer wg.Done()
	for i := 0; i < 100; i++ {
		stacktraceme2()
	}
}

func stacktraceme1() {
}

func stacktraceme2() {
}
