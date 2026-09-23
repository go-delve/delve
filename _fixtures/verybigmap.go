package main

import (
	"fmt"
	"runtime"
	"strconv"
)

func main() {
	m := make(map[string]int)
	for i := range 1_000_000 {
		m[strconv.Itoa(i)] = i
	}
	m["thing"] = -1
	runtime.Breakpoint()
	fmt.Println(m)
}
