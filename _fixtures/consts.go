package main

import (
	"fmt"
	"runtime"
)

type ConstType uint8

const (
	constZero ConstType = iota
	constOne
	constTwo
	constThree
)

type BitFieldType uint8

const (
	bitZero BitFieldType = 1 << iota
	bitOne
	bitTwo
	bitThree
	bitFour
)

func main() {
	a := constTwo
	b := constThree
	c := bitZero | bitOne
	d := BitFieldType(33)
	e := ConstType(10)
	f := BitFieldType(0)
	runtime.Breakpoint()
	fmt.Println(a, b, c, d, e, f)
}
