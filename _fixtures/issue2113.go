package main

import (
	"fmt"
	"runtime"
	"sync"
)

func coroutine(i int, start, finish *sync.WaitGroup) {
	defer finish.Done()

	j := i * 2

	if i == 99 {
		runtime.Breakpoint()
		start.Done()
	} else {
		start.Wait()
	}

	fmt.Println("hello ", i, j)
	fmt.Println("goodbye", i, j)
}

func main() {
	i := 0
	var start, finish sync.WaitGroup
	start.Add(1)
	for ; i < 100; i++ {
		finish.Add(1)
		go coroutine(i, &start, &finish)
	}
	finish.Wait()
	println(i)
}
