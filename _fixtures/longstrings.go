package main

import (
	"runtime"
)

func buildString(length int) string {
	s := ""
	for i := 0; i < length; i++ {
		s = s + "x"
	}
	return s
}

func main() {
	s513 := buildString(513)
	s1025 := buildString(1025)
	s4097 := buildString(4097)
	nested := map[int]string{513: s513, 1025: s1025, 4097: s4097}
	runtime.Breakpoint()
	_ = nested
}
