package main

import "runtime"

func f1() {
}

func f2(a int8, b int32) {
}

func f3() {
}

func call1() {
	defer f2(1, -1)
	defer f1()
	call2()
}

func call2() {
	defer f3()
	defer f2(42, 61)
	call3()
}

func call3() {
	runtime.Breakpoint()
}

func main() {
	call1()
}
