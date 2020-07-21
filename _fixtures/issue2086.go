package main

import (
	"runtime"
)

var i int

type T struct{}

func (t T) m() { stop() }

func stop() {
	runtime.Breakpoint()
	i++
}

func main() { T{}.m() }
