package main

import "runtime"

const N = 10

func agoroutine(started chan<- struct{}, done chan<- struct{}, i int) {
	started <- struct{}{}
	done <- struct{}{}
}

func main() {
	done := make(chan struct{})
	started := make(chan struct{})
	for i := 0; i < N; i++ {
		runtime.Breakpoint()
		go agoroutine(started, done, i)
	}
	for i := 0; i < N; i++ {
		<-started
	}
	runtime.Gosched()
	for i := 0; i < N; i++ {
		<-done
	}
}
