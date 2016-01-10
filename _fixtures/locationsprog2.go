package main

import (
	"fmt"
	"runtime"
)

func afunction(s string) {
	fmt.Println(s)
}

type someStruct struct {
	s string
}

func (o *someStruct) structfunc(s2 string) {
	fmt.Println(o.s, s2)
}

func main() {
	fn1 := afunction
	var o someStruct
	fn2 := o.structfunc
	fn3 := func(s string) {
		fmt.Println("inline", s)
	}
	runtime.Breakpoint()
	fmt.Println(fn1, fn2, fn3, o)
}
