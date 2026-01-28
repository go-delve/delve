package main

import "fmt"

//go:noinline
func allTypes(i int64, u uint64, b bool, s string) int {
	fmt.Printf("i=%d u=%d b=%v s=%s\n", i, u, b, s)
	return len(s)
}

func main() {
	result := allTypes(42, 100, true, "hello")
	fmt.Printf("result=%d\n", result)
}
