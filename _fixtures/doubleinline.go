package main

import (
	"fmt"
	"runtime"
	"strconv"
)

type Rectangle struct{}

func (r *Rectangle) Height() int {
	h, _ := strconv.ParseInt("7", 10, 0)
	return int(h)
}

func (r *Rectangle) Width() int {
	return 6
}

func (r *Rectangle) Area() int { return r.Height() * r.Width() }

func main() {
	var r Rectangle
	runtime.Breakpoint()
	fmt.Println(r.Area())
}
