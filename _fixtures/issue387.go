package main

import (
	"fmt"
	"sync"
)

func dostuff(id int, wg *sync.WaitGroup) {
	fmt.Println("goroutine:", id)
	fmt.Println("goroutine:", id)
	wg.Done()
}

func main() {
	var wg sync.WaitGroup
	wg.Add(10)
	for i := 0; i < 10; i++ {
		go dostuff(i, &wg)
	}
	wg.Wait()
}
