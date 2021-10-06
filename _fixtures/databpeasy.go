package main

import (
	"fmt"
	"runtime"
	"sync"
	"time"
)

var globalvar1 = 0
var globalvar2 = 0

func main() { // Position 0
	runtime.LockOSThread()
	globalvar2 = 1
	fmt.Printf("%d %d\n", globalvar1, globalvar2)
	globalvar2 = globalvar1 + 1
	globalvar1 = globalvar2 + 1
	fmt.Printf("%d %d\n", globalvar1, globalvar2) // Position 1

	globalvar2 = globalvar2 + 1          // Position 2
	globalvar2 = globalvar1 + globalvar2 // Position 3
	fmt.Printf("%d %d\n", globalvar1, globalvar2)
	globalvar1 = globalvar2 + 1
	fmt.Printf("%d %d\n", globalvar1, globalvar2)

	done := make(chan struct{}) // Position 4
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go waitfunc(i, &wg)
	}
	wg.Wait()
	go f(done)
	<-done
}

func f(done chan struct{}) {
	runtime.LockOSThread()
	globalvar1 = globalvar2 + 2
	close(done) // Position 5
}

func waitfunc(i int, wg *sync.WaitGroup) {
	runtime.LockOSThread()
	wg.Done()
	time.Sleep(50 * time.Second)
}
