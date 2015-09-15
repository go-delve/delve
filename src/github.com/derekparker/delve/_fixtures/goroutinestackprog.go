package main

import "runtime"

const N = 10

func agoroutine(done chan<- struct{}, i int) {
	done <- struct{}{}
}

func stacktraceme() {
	return
}

func main() {
	done := make(chan struct{})
	for i := 0; i < N; i++ {
		go agoroutine(done, i)
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
