package main

import (
	"fmt"
	"math/rand"
	"sync"
	"time"
)

func demo(id int, wait *sync.WaitGroup) {
	for i := 0; i < 100; i++ {
		sleep := rand.Intn(10) + 1
		fmt.Printf("id: %d step: %d sleeping %d\n", id, i, sleep)
		time.Sleep(time.Duration(sleep) * time.Millisecond)
	}

	wait.Done()
}

func main() {
	wait := new(sync.WaitGroup)
	wait.Add(2)
	go demo(1, wait)
	go demo(2, wait)

	wait.Wait()
}
