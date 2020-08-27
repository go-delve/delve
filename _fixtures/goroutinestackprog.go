package main

import (
	"runtime"
	"time"
)

const N = 10

func agoroutine(started chan<- struct{}, done chan<- struct{}, i int) {
	started <- struct{}{}
	done <- struct{}{}
}

var dummy int

func stacktraceme() {
	dummy++
	return
}

func main() {
	done := make(chan struct{})
	started := make(chan struct{})
	for i := 0; i < N; i++ {
		go agoroutine(started, done, i)
	}
	for i := 0; i < N; i++ {
		<-started
	}
	runtime.Gosched()
	stacktraceme()
	for i := 0; i < N; i++ {
		<-done
	}
	n := 0
	func1(n + 1)
}

func func1(n int) {
	func2(n + 1)
}

func func2(n int) {
	func3(n + 1)
}

func func3(n int) {
	stacktraceme()
}
