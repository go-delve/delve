package main

import "fmt"

//go:noinline
func tracedPointer(p *int, u *uint64) {
	fmt.Println(p, u)
}

//go:noinline
func tracedSlice(s []byte) {
	fmt.Println(s)
}

//go:noinline
func tracedSmallInts(i8 int8, u16 uint16, i32 int32) {
	fmt.Println(i8, u16, i32)
}

func main() {
	x := 42
	y := uint64(100)
	tracedPointer(&x, &y)
	tracedSlice([]byte{1, 2, 3})
	tracedSmallInts(7, 1000, -42)
}
