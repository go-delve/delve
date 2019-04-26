package main

import (
	"fmt"
	"runtime"
)

type W struct {
	x int
	y int
}

func main() {
	testMaps()
}

func testMaps() {

	m := make(map[string]W)

	m["t"] = W{}
	m["s"] = W{}
	m["r"] = W{}
	m["v"] = W{}

	mm := map[string]W{
		"r": {},
		"s": {},
		"t": {},
		"v": {},
	}

	delete(mm, "s")
	delete(m, "t")
	runtime.Breakpoint()
	fmt.Println(m, mm)
}
