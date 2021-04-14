package main

import (
	"context"
	"runtime"
	"runtime/pprof"
	"sync"
	"time"
)

func sleepyfunc(wg *sync.WaitGroup, lbl string) {
	defer wg.Done()
	pprof.SetGoroutineLabels(pprof.WithLabels(context.Background(), pprof.Labels("name", lbl)))
	time.Sleep(10 * 60 * time.Second)
}

func gopoint1(wg *sync.WaitGroup, lbl string, f func(*sync.WaitGroup, string)) {
	go f(wg, lbl)
}

func gopoint2(wg *sync.WaitGroup, lbl string, f func(*sync.WaitGroup, string)) {
	go f(wg, lbl)
}

func gopoint3(wg *sync.WaitGroup, lbl string, f func(*sync.WaitGroup, string)) {
	go f(wg, lbl)
}

func gopoint4(wg *sync.WaitGroup, lbl string, f func(*sync.WaitGroup, string)) {
	go f(wg, lbl)
}

func gopoint5(wg *sync.WaitGroup, lbl string, f func(*sync.WaitGroup, string)) {
	go f(wg, lbl)
}

func startpoint1(wg *sync.WaitGroup, lbl string) {
	sleepyfunc(wg, lbl)
}

func startpoint2(wg *sync.WaitGroup, lbl string) {
	sleepyfunc(wg, lbl)
}

func startpoint3(wg *sync.WaitGroup, lbl string) {
	sleepyfunc(wg, lbl)
}

func startpoint4(wg *sync.WaitGroup, lbl string) {
	sleepyfunc(wg, lbl)
}

func startpoint5(wg *sync.WaitGroup, lbl string) {
	sleepyfunc(wg, lbl)
}

func main() {
	var wg sync.WaitGroup

	for _, lbl := range []string{"one", "two", "three", "four", "five"} {
		for _, f := range []func(*sync.WaitGroup, string){startpoint1, startpoint2, startpoint3, startpoint4, startpoint5} {
			for i := 0; i < 1000; i++ {
				wg.Add(5)
				gopoint1(&wg, lbl, f)
				gopoint2(&wg, lbl, f)
				gopoint3(&wg, lbl, f)
				gopoint4(&wg, lbl, f)
				gopoint5(&wg, lbl, f)
			}
		}
	}
	time.Sleep(2 * time.Second)
	runtime.Breakpoint()
	wg.Wait()
}
