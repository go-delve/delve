package main

import (
	"fmt"
	"runtime"
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

var zero = 0

func main() {
	one, two := 1, 2
	runtime.Breakpoint()
	call1(one, two)
	fmt.Println(one, two, zero, callpanic, callstacktrace)
}
