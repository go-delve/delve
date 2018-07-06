package main

import "runtime"

func f1() {
}

func f2() {
}

func f3() {
}

func call1() {
	defer f2()
	defer f1()
	call2()
}

func call2() {
	defer f3()
	defer f2()
	call3()
}

func call3() {
	runtime.Breakpoint()
}

func main() {
	call1()
}
