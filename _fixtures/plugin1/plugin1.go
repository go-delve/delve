package main

import "fmt"

func Fn1() string {
	return "hello"
}

func HelloFn(n int) string {
	n++
	s := fmt.Sprintf("hello%d", n)
	return s
}
