package main

import "sync"

func doSomething(wg *sync.WaitGroup) {
	wg.Done()
}

func main() {
	var wg sync.WaitGroup
	wg.Add(2)
	go doSomething(&wg)
	go doSomething(&wg)
	wg.Wait()
}
