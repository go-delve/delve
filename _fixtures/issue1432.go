package main

import "runtime"

type s struct {
	i int64
}

func main() {
	i := 1
	p := &i
	s := s{i: 1}
	_ = s
	runtime.Breakpoint()
	println(i, p)
}
