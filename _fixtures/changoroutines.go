package main

import (
	"runtime"
	"time"
)

func main() {
	blockingchan1 := make(chan int)
	blockingchan2 := make(chan int)

	go sendToChan("one", blockingchan1)
	go sendToChan("two", blockingchan1)
	go recvFromChan(blockingchan2)
	time.Sleep(time.Second)

	runtime.Breakpoint()
}

func sendToChan(name string, ch chan<- int) {
	ch <- 1
}

func recvFromChan(ch <-chan int) {
	<-ch
}
