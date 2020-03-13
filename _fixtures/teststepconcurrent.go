package main

import (
	"fmt"
	"sync"
	"time"
)

var v int = 99
var wg sync.WaitGroup
var s string

func Foo(x, y int) (z int) {
	//s = fmt.Sprintf("x=%d, y=%d, z=%d\n", x, y, z)
	z = x + y
	return
}

func Threads(fn func(x, y int) int) {
	wg.Add(100)
	for j := 0; j < 100; j++ {
		go func(fn func(x, y int) int, j int) {defer func() {if r := recover(); r != nil {fmt.Printf("panic ? %#v, j %d\n", fn, j);panic(r)}}()
			for k := 0; k < 100; k++ {
				_ = fn(1, 2)
				time.Sleep(10 * time.Millisecond)
			}
			wg.Done()
		}(fn, j)
	}
}

func main() {
	x := v
	y := x * x
	var z int
	Threads(Foo)
	for i := 0; i < 100; i++ {
		z = Foo(x, y)
	}
	fmt.Printf("z=%d\n", z)
	wg.Wait()
}
