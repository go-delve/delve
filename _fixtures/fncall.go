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

var zero = 0

func main() {
	one, two := 1, 2
	intslice := []int{1, 2, 3}
	stringslice := []string{"one", "two", "three"}
	comma := ","
	runtime.Breakpoint()
	call1(one, two)
	fmt.Println(one, two, zero, callpanic, callstacktrace, stringsJoin, intslice, stringslice, comma)
}
