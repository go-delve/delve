package main

import (
	"fmt"
	"sync"
)

func callme(i int, s string) int {
	fmt.Println(s)
	return i * i
}

func dostuff(wg *sync.WaitGroup, lbl string) {
	defer wg.Done()
	var j int
	for i := 0; i < 10; i++ {
		j += callme(i, lbl)
	}
	println(lbl, j)
}

func main() {
	var wg sync.WaitGroup

	for _, lbl := range []string{"one", "two", "three", "four", "five"} {
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go dostuff(&wg, lbl)
		}
	}
	wg.Wait()
}
