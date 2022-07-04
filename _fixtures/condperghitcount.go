package main

import (
	"sync"
	"time"
)

func main() {
	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			j := 0
			for {
				j++
				if j > 10 {
					break
				}
			}
			wg.Done()
		}()
		time.Sleep(time.Second)
	}
	wg.Wait()
}
