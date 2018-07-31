package main

import (
	"fmt"
	"runtime"
	"strings"
)

func callstacktrace() (stacktrace string) {
	for skip := 0; ; skip++ {
		pc, file, line, ok := runtime.Caller(skip)
		if !ok {
			break
		}
		fn := runtime.FuncForPC(pc)
		stacktrace += fmt.Sprintf("in %s at %s:%d\n", fn.Name(), file, line)
	}
	return stacktrace
}

func call1(a, b int) int {
	fmt.Printf("first: %d second: %d\n", a, b)
	return a + b
}

func callpanic() {
	fmt.Printf("about to panic\n")
	panic("callpanic panicked")
}

func stringsJoin(v []string, sep string) string {
	// This is needed because strings.Join is in an optimized package and
	// because of a bug in the compiler arguments of optimized functions don't
	// have a location.
	return strings.Join(v, sep)
}

type astruct struct {
	X int
}

func (a astruct) VRcvr(x int) string {
	return fmt.Sprintf("%d + %d = %d", x, a.X, x+a.X)
}

func (pa *astruct) PRcvr(x int) string {
	return fmt.Sprintf("%d - %d = %d", x, pa.X, x-pa.X)
}

type PRcvrable interface {
	PRcvr(int) string
}

type VRcvrable interface {
	VRcvr(int) string
}

var zero = 0

func main() {
	one, two := 1, 2
	intslice := []int{1, 2, 3}
	stringslice := []string{"one", "two", "three"}
	comma := ","
	a := astruct{X: 3}
	pa := &astruct{X: 6}

	var vable_a VRcvrable = a
	var vable_pa VRcvrable = pa
	var pable_pa PRcvrable = pa

	runtime.Breakpoint()
	call1(one, two)
	fmt.Println(one, two, zero, callpanic, callstacktrace, stringsJoin, intslice, stringslice, comma, a.VRcvr, a.PRcvr, pa, vable_a, vable_pa, pable_pa)
}
